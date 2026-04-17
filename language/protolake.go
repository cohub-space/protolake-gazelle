package language

import (
	"flag"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/label"
	"github.com/bazelbuild/bazel-gazelle/language"
	"github.com/bazelbuild/bazel-gazelle/repo"
	"github.com/bazelbuild/bazel-gazelle/resolve"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

const (
	protolakeName  = "protolake"
	buildBazelFile = "BUILD.bazel"
	buildFile      = "BUILD"
	bundleYamlFile = "bundle.yaml"
	lakeYamlFile   = "lake.yaml"
	bazelDirPrefix = "bazel-"
)

var protoLibraryPattern = regexp.MustCompile(`proto_library\s*\(\s*[^)]*name\s*=\s*"([^"]+)"[^)]*\)`)

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
	bundleYamlPath := filepath.Join(args.Dir, bundleYamlFile)
	if _, err := os.Stat(bundleYamlPath); os.IsNotExist(err) {
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

	// Signal deletion of legacy rules replaced by Connect-ES migration
	emptyRules := generateLegacyCleanupRules(mergedConfig)

	return language.GenerateResult{
		Gen:     gen,
		Empty:   emptyRules,
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
	bf := filepath.Join(dir, buildBazelFile)
	if _, err := os.Stat(bf); os.IsNotExist(err) {
		bf = filepath.Join(dir, buildFile)
	}

	if _, err := os.Stat(bf); os.IsNotExist(err) {
		log.Printf("No BUILD file found in %s", dir)
		return targets
	}

	// Read BUILD file and parse proto_library rules
	content, err := os.ReadFile(bf)
	if err != nil {
		log.Printf("Failed to read BUILD file %s: %v", bf, err)
		return targets
	}

	buildContent := string(content)

	// Use regex to find proto_library rules with proper multi-line support
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
		if info.IsDir() && strings.HasPrefix(info.Name(), bazelDirPrefix) {
			return filepath.SkipDir
		}

		// Process BUILD files
		if info.Name() == buildBazelFile || info.Name() == buildFile {
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
				"package_name": true,
				"proto_deps":   true,
				"es_deps":      true,
			},
		},
		"es_proto_compile": {
			NonEmptyAttrs: map[string]bool{
				"protos": true,
			},
			// MergeableAttrs lets Gazelle overwrite these attributes on existing rules
			// during a regenerate pass. Without this, changes to external proto deps
			// (added by detectExternalProtoImports for google/api, buf/validate, etc.)
			// would not propagate into checked-in BUILD.bazel files.
			MergeableAttrs: map[string]bool{
				"protos":     true,
				"visibility": true,
			},
		},
		// java_grpc_library + python_grpc_library live in @rules_proto_grpc_{java,python}
		// but we generate them from this extension and want to merge our attrs into
		// existing checked-in rules. Register KindInfo so Gazelle knows to merge
		// `protos` and `deps` (external proto deps added by detectExternalProtoImports).
		"java_grpc_library": {
			NonEmptyAttrs: map[string]bool{
				"protos": true,
			},
			MergeableAttrs: map[string]bool{
				"protos":     true,
				"deps":       true,
				"visibility": true,
			},
		},
		"python_grpc_library": {
			NonEmptyAttrs: map[string]bool{
				"protos": true,
			},
			MergeableAttrs: map[string]bool{
				"protos":     true,
				"deps":       true,
				"visibility": true,
			},
		},
		"proto_descriptor_set": {
			NonEmptyAttrs: map[string]bool{
				"deps": true,
			},
		},
		"js_proto_loader_bundle": {
			NonEmptyAttrs: map[string]bool{
				"package_name": true,
				"proto_deps":   true,
			},
		},
		"build_validation": {
			NonEmptyAttrs: map[string]bool{
				"targets": true,
			},
		},
		// Legacy rule kinds — kept in KindInfo so Gazelle can delete them
		// during merge when they appear in GenerateResult.Empty.
		// These were replaced by es_proto_compile in the Connect-ES migration.
		"js_grpc_library": {
			NonEmptyAttrs: map[string]bool{
				"protos": true,
			},
		},
		"js_grpc_web_library": {
			NonEmptyAttrs: map[string]bool{
				"protos": true,
			},
		},
	}
}

// Loads returns the load statements required for the rules we generate
func (pe *protolakeExtension) Loads() []rule.LoadInfo {
	return []rule.LoadInfo{
		{
			Name:    "@rules_proto//proto:defs.bzl",
			Symbols: []string{"proto_library"},
		},
		{
			Name:    "@rules_proto_grpc_java//:defs.bzl",
			Symbols: []string{"java_grpc_library"},
		},
		{
			Name:    "@rules_proto_grpc_python//:defs.bzl",
			Symbols: []string{"python_grpc_library"},
		},
		{
			Name:    "//tools:es_proto.bzl",
			Symbols: []string{"es_proto_compile"},
		},
		{
			Name:    "//tools:proto_bundle.bzl",
			Symbols: []string{"build_validation", "java_proto_bundle", "py_proto_bundle", "js_proto_bundle", "proto_descriptor_set", "js_proto_loader_bundle"},
		},
		// Legacy load — kept so Gazelle can remove it when no rules reference these symbols
		{
			Name:    "@rules_proto_grpc_js//:defs.bzl",
			Symbols: []string{"js_grpc_library", "js_grpc_web_library"},
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
