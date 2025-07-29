package language

import (
	"flag"
	"log"
	"os"
	"path/filepath"
	"regexp"

	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/label"
	"github.com/bazelbuild/bazel-gazelle/language"
	"github.com/bazelbuild/bazel-gazelle/repo"
	"github.com/bazelbuild/bazel-gazelle/resolve"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

const protolakeName = "protolake"

// protolakeExtension implements the Gazelle language.Language interface
// for generating protolake bundle rules with hybrid publishing support
type protolakeExtension struct{}

func NewLanguage() language.Language {
	log.Printf("[protolake-gazelle] NewLanguage() called - extension initialized")
	return &protolakeExtension{}
}

func (pe *protolakeExtension) Name() string {
	return protolakeName
}

// RegisterFlags registers command-line flags used by the extension.
func (pe *protolakeExtension) RegisterFlags(fs *flag.FlagSet, cmd string, c *config.Config) {
	// Initialize our config in the extensions map
	pc := &protolakeConfig{
		enabled: true,
	}
	c.Exts[protolakeName] = pc
}

// KnownDirectives returns the list of directives recognized by this extension
func (pe *protolakeExtension) KnownDirectives() []string {
	return []string{
		"protolake", // Enable/disable protolake extension (e.g., # gazelle:protolake false)
	}
}

type protolakeConfig struct {
	enabled bool
}

func (pe *protolakeExtension) Configure(c *config.Config, rel string, f *rule.File) {
	if f == nil {
		return
	}

	// Check for directive to enable/disable
	for _, d := range f.Directives {
		switch d.Key {
		case "protolake":
			if pc, ok := c.Exts[protolakeName].(*protolakeConfig); ok {
				pc.enabled = d.Value == "true"
			}
		}
	}
}

func getProtolakeConfig(c *config.Config) *protolakeConfig {
	pc, ok := c.Exts[protolakeName].(*protolakeConfig)
	if !ok {
		pc = &protolakeConfig{enabled: true}
		c.Exts[protolakeName] = pc
	}
	return pc
}

// GenerateRules generates bundle rules for directories containing bundle.yaml
func (pe *protolakeExtension) GenerateRules(args language.GenerateArgs) language.GenerateResult {
	pc := getProtolakeConfig(args.Config)
	if !pc.enabled {
		return language.GenerateResult{}
	}

	// Debug logging
	log.Printf("[protolake-gazelle] GenerateRules called for dir: %s, rel: %s", args.Dir, args.Rel)

	// Check if this directory has a bundle.yaml file
	bundleYamlPath := filepath.Join(args.Dir, "bundle.yaml")
	if _, err := os.Stat(bundleYamlPath); os.IsNotExist(err) {
		// Debug: List files in directory to understand what's there
		if files, err := os.ReadDir(args.Dir); err == nil {
			log.Printf("[protolake-gazelle] Files in %s:", args.Dir)
			for _, f := range files {
				log.Printf("  - %s", f.Name())
			}
		}
		return language.GenerateResult{}
	}

	log.Printf("[protolake-gazelle] Found bundle.yaml at: %s", bundleYamlPath)

	// Load lake configuration (walks up directory tree)
	lakeConfig, err := LoadLakeConfig(args.Dir)
	if err != nil {
		log.Printf("Failed to load lake configuration: %v", err)
		return language.GenerateResult{}
	}

	// Load bundle configuration
	bundleConfig, err := LoadBundleConfig(args.Dir)
	if err != nil {
		log.Printf("Failed to load bundle configuration: %v", err)
		return language.GenerateResult{}
	}

	if bundleConfig == nil {
		log.Printf("No bundle configuration found")
		return language.GenerateResult{}
	}

	// Merge lake and bundle configurations
	mergedConfig := MergeConfigurations(lakeConfig, bundleConfig)

	log.Printf("Processing bundle: %s at %s", mergedConfig.BundleName, args.Rel)

	// Discover existing proto targets from BUILD files (including subdirectories)
	protoTargets := pe.discoverExistingProtoTargets(args)
	if len(protoTargets) == 0 {
		log.Printf("No proto targets found for bundle %s", mergedConfig.BundleName)
		return language.GenerateResult{}
	}

	log.Printf("Found %d proto targets for bundle %s: %v", len(protoTargets), mergedConfig.BundleName, protoTargets)

	// Generate bundle rules using the merged configuration
	rules := generateBundleRules(mergedConfig, protoTargets, args.Rel, args.Config)

	log.Printf("Generated %d rules for bundle %s", len(rules), mergedConfig.BundleName)

	// Convert to GenerateResult
	gen := make([]*rule.Rule, 0, len(rules))
	for _, r := range rules {
		gen = append(gen, r)
	}

	// Create empty imports (we don't track imports for bundle rules)
	imports := make([]interface{}, len(gen))
	for i := range imports {
		imports[i] = nil
	}

	return language.GenerateResult{
		Gen:     gen,
		Imports: imports,
	}
}

// discoverExistingProtoTargets finds proto_library targets in the current directory and subdirectories
// This enhanced version searches recursively to support bundles with protos in subdirectories
func (pe *protolakeExtension) discoverExistingProtoTargets(args language.GenerateArgs) []string {
	var targets []string

	// First, check for protos in the current directory (bundle.yaml directory)
	targets = append(targets, pe.discoverProtoTargetsInDirectory(args.Dir, args.Config.RepoRoot)...)

	// Then recursively search subdirectories for additional proto targets
	subdirTargets := pe.discoverProtoTargetsRecursively(args.Dir, args.Config.RepoRoot)
	targets = append(targets, subdirTargets...)

	log.Printf("Discovered %d total proto targets for bundle: %v", len(targets), targets)
	return targets
}

// discoverProtoTargetsInDirectory finds proto_library targets in a specific directory
func (pe *protolakeExtension) discoverProtoTargetsInDirectory(dir string, repoRoot string) []string {
	var targets []string

	// Look for existing BUILD file
	buildFile := filepath.Join(dir, "BUILD.bazel")
	if _, err := os.Stat(buildFile); os.IsNotExist(err) {
		buildFile = filepath.Join(dir, "BUILD")
	}

	if _, err := os.Stat(buildFile); os.IsNotExist(err) {
		log.Printf("No BUILD file found in %s", dir)
		return targets
	}

	// Read BUILD file and parse proto_library rules
	content, err := os.ReadFile(buildFile)
	if err != nil {
		log.Printf("Failed to read BUILD file %s: %v", buildFile, err)
		return targets
	}

	buildContent := string(content)
	log.Printf("Parsing BUILD file content for proto_library rules in %s", dir)

	// Use regex to find proto_library rules with proper multi-line support
	protoLibraryPattern := regexp.MustCompile(`proto_library\s*\(\s*[^)]*name\s*=\s*"([^"]+)"[^)]*\)`)
	matches := protoLibraryPattern.FindAllStringSubmatch(buildContent, -1)

	for _, match := range matches {
		if len(match) > 1 {
			name := match[1]

			// Determine the correct target format based on directory relationship
			pkg, err := filepath.Rel(repoRoot, dir)
			if err != nil || pkg == "." {
				// Same directory as bundle.yaml - use local reference
				targets = append(targets, ":"+name)
				log.Printf("Found local proto_library target: %s", name)
			} else {
				// Subdirectory - use full package reference
				fullTarget := "//" + pkg + ":" + name
				targets = append(targets, fullTarget)
				log.Printf("Found subdirectory proto_library target: %s", fullTarget)
			}
		}
	}

	return targets
}

// discoverProtoTargetsRecursively finds proto_library targets in all subdirectories
func (pe *protolakeExtension) discoverProtoTargetsRecursively(bundleDir string, repoRoot string) []string {
	var targets []string

	// Walk through all subdirectories
	filepath.Walk(bundleDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		// Skip the bundle directory itself (already processed)
		if path == bundleDir {
			return nil
		}

		// Skip bazel output directories
		if info.IsDir() && (info.Name() == "bazel-bin" || info.Name() == "bazel-out" ||
			info.Name() == "bazel-testlogs" || info.Name() == "bazel-"+filepath.Base(bundleDir)) {
			return filepath.SkipDir
		}

		// Process BUILD files
		if info.Name() == "BUILD.bazel" || info.Name() == "BUILD" {
			dir := filepath.Dir(path)
			dirTargets := pe.discoverProtoTargetsInDirectory(dir, repoRoot)
			targets = append(targets, dirTargets...)
		}

		return nil
	})

	return targets
}

// Kinds returns the kinds of rules this extension generates
func (pe *protolakeExtension) Kinds() map[string]rule.KindInfo {
	return pe.KindInfo()
}

// KindInfo returns information about the rules this extension generates
func (pe *protolakeExtension) KindInfo() map[string]rule.KindInfo {
	return map[string]rule.KindInfo{
		"java_proto_bundle": {
			NonEmptyAttrs: map[string]bool{
				"group_id":       true,
				"artifact_id":    true,
				"proto_deps":     true,
				"java_deps":      true,
				"java_grpc_deps": true,
			},
		},
		"py_proto_bundle": {
			NonEmptyAttrs: map[string]bool{
				"package_name": true,
				"proto_deps":   true,
				"py_deps":      true,
				"py_grpc_deps": true,
			},
		},
		"js_proto_bundle": {
			NonEmptyAttrs: map[string]bool{
				"package_name":     true,
				"proto_deps":       true,
				"js_deps":          true,
				"js_grpc_web_deps": true,
			},
		},
		"build_validation": {
			NonEmptyAttrs: map[string]bool{
				"targets": true,
			},
		},
	}
}

// Loads returns the load statements required for the rules we generate
func (pe *protolakeExtension) Loads() []rule.LoadInfo {
	return []rule.LoadInfo{
		{
			Name:    "@rules_proto_grpc_java//:defs.bzl",
			Symbols: []string{"java_grpc_library"},
		},
		{
			Name:    "@rules_proto_grpc_python//:defs.bzl",
			Symbols: []string{"python_grpc_library"},
		},
		{
			Name:    "@rules_proto_grpc_js//:defs.bzl",
			Symbols: []string{"js_grpc_library", "js_grpc_web_library"},
		},
		{
			Name:    "//tools:proto_bundle.bzl",
			Symbols: []string{"build_validation", "java_proto_bundle", "py_proto_bundle", "js_proto_bundle"},
		},
	}
}

// Required interface methods with empty implementations
func (pe *protolakeExtension) Fix(c *config.Config, f *rule.File) {}

func (pe *protolakeExtension) Imports(c *config.Config, r *rule.Rule, f *rule.File) []resolve.ImportSpec {
	return nil
}

func (pe *protolakeExtension) Embeds(r *rule.Rule, from label.Label) []label.Label {
	return nil
}

func (pe *protolakeExtension) Resolve(c *config.Config, ix *resolve.RuleIndex, rc *repo.RemoteCache, r *rule.Rule, imports interface{}, from label.Label) {
}

func (pe *protolakeExtension) CheckFlags(fs *flag.FlagSet, c *config.Config) error {
	return nil
}
