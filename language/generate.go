package language

import (
	"fmt"
	"io/ioutil"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

// generateBundleRules creates the bundle rules for a bundle using the hybrid approach
// In the hybrid approach:
// - Static configuration (coordinates, dependencies) stays in BUILD files
// - Dynamic configuration (version, repos) comes from environment variables
func generateBundleRules(config *MergedConfig, protoTargets []string, rel string, c *config.Config) []*rule.Rule {
	var rules []*rule.Rule
	bundleName := config.BundleName

	// Create aggregated proto_library rule (for reference and compatibility)
	allProtosRule := rule.NewRule("proto_library", fmt.Sprintf("%s_all_protos", bundleName))
	allProtosRule.SetAttr("deps", rule.PlatformStrings{Generic: protoTargets})
	allProtosRule.SetAttr("visibility", []string{"//visibility:public"})
	rules = append(rules, allProtosRule)

	// Collect bundle-specific transitive dependencies for the gRPC libraries
	bundleDir := filepath.Join(c.RepoRoot, rel)
	allProtoTargets := collectBundleTransitiveDependencies(c, bundleDir, protoTargets)

	log.Printf("Bundle %s has %d direct proto targets and %d total with transitive deps",
		bundleName, len(protoTargets), len(allProtoTargets))

	// Generate Java bundle if enabled
	log.Printf("Checking Java bundle generation - Enabled: %v, GroupId: '%s', ArtifactId: '%s'",
		config.JavaConfig.Enabled, config.JavaConfig.GroupId, config.JavaConfig.ArtifactId)
	if config.JavaConfig.Enabled && config.JavaConfig.GroupId != "" && config.JavaConfig.ArtifactId != "" {
		rules = append(rules, generateJavaBundleRules(config, bundleName, allProtoTargets)...)
	} else {
		log.Printf("Skipping Java bundle generation for %s", bundleName)
	}

	// Generate Python bundle if enabled
	log.Printf("Checking Python bundle generation - Enabled: %v, PackageName: '%s'",
		config.PythonConfig.Enabled, config.PythonConfig.PackageName)
	if config.PythonConfig.Enabled && config.PythonConfig.PackageName != "" {
		rules = append(rules, generatePythonBundleRules(config, bundleName, allProtoTargets)...)
	} else {
		log.Printf("Skipping Python bundle generation for %s", bundleName)
	}

	// Generate JavaScript bundle if enabled
	log.Printf("Checking JavaScript bundle generation - Enabled: %v, PackageName: '%s'",
		config.JavaScriptConfig.Enabled, config.JavaScriptConfig.PackageName)
	if config.JavaScriptConfig.Enabled && config.JavaScriptConfig.PackageName != "" {
		rules = append(rules, generateJavaScriptBundleRules(config, bundleName, allProtoTargets)...)
	} else {
		log.Printf("Skipping JavaScript bundle generation for %s", bundleName)
	}

	// Create a build test to verify all bundles
	var testTargets []string
	if config.JavaConfig.Enabled {
		testTargets = append(testTargets, fmt.Sprintf(":%s_java_bundle", bundleName))
	}
	if config.PythonConfig.Enabled {
		testTargets = append(testTargets, fmt.Sprintf(":%s_py_bundle", bundleName))
	}
	if config.JavaScriptConfig.Enabled {
		testTargets = append(testTargets, fmt.Sprintf(":%s_js_bundle", bundleName))
	}

	if len(testTargets) > 0 {
		buildTestRule := rule.NewRule("build_validation", "all")
		buildTestRule.SetAttr("targets", testTargets)
		rules = append(rules, buildTestRule)
	}

	return rules
}

// generateJavaBundleRules creates Java bundle rules with hybrid publishing
func generateJavaBundleRules(config *MergedConfig, bundleName string, allProtoTargets []string) []*rule.Rule {
	var rules []*rule.Rule

	// Java gRPC library (includes both proto messages and gRPC stubs)
	javaGrpcRule := rule.NewRule("java_grpc_library", fmt.Sprintf("%s_java_grpc", bundleName))
	javaGrpcRule.SetAttr("protos", rule.PlatformStrings{Generic: allProtoTargets})
	javaGrpcRule.SetAttr("visibility", []string{"//visibility:public"})
	rules = append(rules, javaGrpcRule)

	// Java bundle rule with STATIC configuration (no version attribute)
	// The unified rule handles environment variables internally
	javaBundleRule := rule.NewRule("java_proto_bundle", fmt.Sprintf("%s_java_bundle", bundleName))
	javaBundleRule.SetAttr("proto_deps", rule.PlatformStrings{Generic: []string{fmt.Sprintf(":%s_all_protos", bundleName)}})
	javaBundleRule.SetAttr("java_deps", rule.PlatformStrings{Generic: []string{fmt.Sprintf(":%s_java_grpc", bundleName)}})
	javaBundleRule.SetAttr("java_grpc_deps", rule.PlatformStrings{Generic: []string{fmt.Sprintf(":%s_java_grpc", bundleName)}})
	// Static coordinates from configuration
	javaBundleRule.SetAttr("group_id", config.JavaConfig.GroupId)
	javaBundleRule.SetAttr("artifact_id", config.JavaConfig.ArtifactId)
	// NOTE: NO version attribute - unified rule reads from environment variable
	javaBundleRule.SetAttr("visibility", []string{"//visibility:public"})
	rules = append(rules, javaBundleRule)

	// Publishing rule with environment variable support
	publishMavenRule := rule.NewRule("genrule", fmt.Sprintf("publish_%s_to_maven", bundleName))
	publishMavenRule.SetAttr("srcs", rule.PlatformStrings{Generic: []string{fmt.Sprintf(":%s_java_bundle", bundleName)}})
	publishMavenRule.SetAttr("outs", rule.PlatformStrings{Generic: []string{"publish_maven.log"}})
	// Command with environment variable expansion - this is the hybrid approach
	publishCmd := fmt.Sprintf("$(location //tools:maven_publisher) "+
		"--jar $(location :%s_java_bundle) "+
		"--group-id %s "+
		"--artifact-id %s "+
		"--version \"${VERSION:-1.0.0}\" "+
		"--repo \"${MAVEN_REPO:-file://~/.m2/repository}\" "+
		"> $@",
		bundleName, config.JavaConfig.GroupId, config.JavaConfig.ArtifactId)
	publishMavenRule.SetAttr("cmd", publishCmd)
	publishMavenRule.SetAttr("tools", rule.PlatformStrings{Generic: []string{"//tools:maven_publisher"}})
	rules = append(rules, publishMavenRule)

	// Convenience alias for publishing
	publishMavenAlias := rule.NewRule("alias", "publish_to_maven")
	publishMavenAlias.SetAttr("actual", fmt.Sprintf(":publish_%s_to_maven", bundleName))
	publishMavenAlias.SetAttr("visibility", []string{"//visibility:public"})
	rules = append(rules, publishMavenAlias)

	return rules
}

// generatePythonBundleRules creates Python bundle rules with hybrid publishing
func generatePythonBundleRules(config *MergedConfig, bundleName string, allProtoTargets []string) []*rule.Rule {
	var rules []*rule.Rule

	// Python gRPC library (includes both proto messages and gRPC stubs)
	pythonGrpcRule := rule.NewRule("python_grpc_library", fmt.Sprintf("%s_python_grpc", bundleName))
	pythonGrpcRule.SetAttr("protos", rule.PlatformStrings{Generic: allProtoTargets})
	pythonGrpcRule.SetAttr("visibility", []string{"//visibility:public"})
	rules = append(rules, pythonGrpcRule)

	// Python bundle rule with STATIC configuration (no version attribute)
	// The unified rule handles environment variables internally
	pyBundleRule := rule.NewRule("py_proto_bundle", fmt.Sprintf("%s_py_bundle", bundleName))
	pyBundleRule.SetAttr("proto_deps", rule.PlatformStrings{Generic: []string{fmt.Sprintf(":%s_all_protos", bundleName)}})
	pyBundleRule.SetAttr("py_deps", rule.PlatformStrings{Generic: []string{fmt.Sprintf(":%s_python_grpc", bundleName)}})
	pyBundleRule.SetAttr("py_grpc_deps", rule.PlatformStrings{Generic: []string{fmt.Sprintf(":%s_python_grpc", bundleName)}})
	// Static package name from configuration
	pyBundleRule.SetAttr("package_name", config.PythonConfig.PackageName)
	// NOTE: NO version attribute - unified rule reads from environment variable
	pyBundleRule.SetAttr("visibility", []string{"//visibility:public"})
	rules = append(rules, pyBundleRule)

	// Publishing rule with environment variable support
	publishPypiRule := rule.NewRule("genrule", fmt.Sprintf("publish_%s_to_pypi", bundleName))
	publishPypiRule.SetAttr("srcs", rule.PlatformStrings{Generic: []string{fmt.Sprintf(":%s_py_bundle", bundleName)}})
	publishPypiRule.SetAttr("outs", rule.PlatformStrings{Generic: []string{"publish_pypi.log"}})
	// Command with environment variable expansion
	publishCmd := fmt.Sprintf("$(location //tools:pypi_publisher) "+
		"--wheel $(location :%s_py_bundle) "+
		"--package-name %s "+
		"--version \"${VERSION:-1.0.0}\" "+
		"--repo \"${PYPI_REPO:-file://~/.pypi}\" "+
		"> $@",
		bundleName, config.PythonConfig.PackageName)
	publishPypiRule.SetAttr("cmd", publishCmd)
	publishPypiRule.SetAttr("tools", rule.PlatformStrings{Generic: []string{"//tools:pypi_publisher"}})
	rules = append(rules, publishPypiRule)

	// Convenience alias for publishing
	publishPypiAlias := rule.NewRule("alias", "publish_to_pypi")
	publishPypiAlias.SetAttr("actual", fmt.Sprintf(":publish_%s_to_pypi", bundleName))
	publishPypiAlias.SetAttr("visibility", []string{"//visibility:public"})
	rules = append(rules, publishPypiAlias)

	return rules
}

// generateJavaScriptBundleRules creates JavaScript bundle rules with hybrid publishing
func generateJavaScriptBundleRules(config *MergedConfig, bundleName string, allProtoTargets []string) []*rule.Rule {
	var rules []*rule.Rule

	// JavaScript gRPC library for Node.js
	jsGrpcNodeRule := rule.NewRule("js_grpc_library", fmt.Sprintf("%s_js_grpc_node", bundleName))
	jsGrpcNodeRule.SetAttr("protos", rule.PlatformStrings{Generic: allProtoTargets})
	jsGrpcNodeRule.SetAttr("visibility", []string{"//visibility:public"})
	rules = append(rules, jsGrpcNodeRule)

	// JavaScript gRPC-Web library for Browser
	jsGrpcWebRule := rule.NewRule("js_grpc_web_library", fmt.Sprintf("%s_js_grpc_web", bundleName))
	jsGrpcWebRule.SetAttr("protos", rule.PlatformStrings{Generic: allProtoTargets})
	jsGrpcWebRule.SetAttr("visibility", []string{"//visibility:public"})
	rules = append(rules, jsGrpcWebRule)

	// JavaScript bundle rule with STATIC configuration (no version attribute)
	// The unified rule handles environment variables internally
	jsBundleRule := rule.NewRule("js_proto_bundle", fmt.Sprintf("%s_js_bundle", bundleName))
	jsBundleRule.SetAttr("proto_deps", rule.PlatformStrings{Generic: []string{fmt.Sprintf(":%s_all_protos", bundleName)}})
	jsBundleRule.SetAttr("js_deps", rule.PlatformStrings{Generic: []string{fmt.Sprintf(":%s_js_grpc_node", bundleName)}})
	jsBundleRule.SetAttr("js_grpc_web_deps", rule.PlatformStrings{Generic: []string{fmt.Sprintf(":%s_js_grpc_web", bundleName)}})
	// Static package name from configuration
	jsBundleRule.SetAttr("package_name", config.JavaScriptConfig.PackageName)
	// NOTE: NO version attribute - unified rule reads from environment variable
	jsBundleRule.SetAttr("visibility", []string{"//visibility:public"})
	rules = append(rules, jsBundleRule)

	// Publishing rule with environment variable support
	publishNpmRule := rule.NewRule("genrule", fmt.Sprintf("publish_%s_to_npm", bundleName))
	publishNpmRule.SetAttr("srcs", rule.PlatformStrings{Generic: []string{fmt.Sprintf(":%s_js_bundle", bundleName)}})
	publishNpmRule.SetAttr("outs", rule.PlatformStrings{Generic: []string{"publish_npm.log"}})
	// Command with environment variable expansion
	publishCmd := fmt.Sprintf("$(location //tools:npm_publisher) "+
		"--package $(location :%s_js_bundle) "+
		"--package-name %s "+
		"--version \"${VERSION:-1.0.0}\" "+
		"--registry \"${NPM_REGISTRY:-file://~/.npm}\" "+
		"> $@",
		bundleName, config.JavaScriptConfig.PackageName)
	publishNpmRule.SetAttr("cmd", publishCmd)
	publishNpmRule.SetAttr("tools", rule.PlatformStrings{Generic: []string{"//tools:npm_publisher"}})
	rules = append(rules, publishNpmRule)

	// Convenience alias for publishing
	publishNpmAlias := rule.NewRule("alias", "publish_to_npm")
	publishNpmAlias.SetAttr("actual", fmt.Sprintf(":publish_%s_to_npm", bundleName))
	publishNpmAlias.SetAttr("visibility", []string{"//visibility:public"})
	rules = append(rules, publishNpmAlias)

	return rules
}

// collectBundleTransitiveDependencies finds transitive dependencies for a specific bundle
// This replaces the overly aggressive global approach with bundle-scoped dependency collection
func collectBundleTransitiveDependencies(c *config.Config, bundleDir string, directTargets []string) []string {
	allDeps := make(map[string]bool)

	// Add direct targets
	for _, target := range directTargets {
		allDeps[target] = true
	}

	// Build global import index (still needed for resolving imports to targets)
	importToTarget := make(map[string]string)
	buildImportIndex(c.RepoRoot, importToTarget)

	// Collect bundle-specific proto files and their imports
	bundleProtoFiles := collectBundleProtoFiles(bundleDir)
	log.Printf("Found %d proto files in bundle at %s", len(bundleProtoFiles), bundleDir)

	// For each proto file in the bundle, find its imports and resolve them
	for _, protoFile := range bundleProtoFiles {
		collectImportsFromProtoFile(protoFile, importToTarget, allDeps)
	}

	// Convert back to slice
	var result []string
	for target := range allDeps {
		result = append(result, target)
	}

	return result
}

// collectBundleProtoFiles finds all proto files within a bundle directory (including subdirectories)
func collectBundleProtoFiles(bundleDir string) []string {
	var protoFiles []string

	filepath.Walk(bundleDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		// Only include .proto files
		if strings.HasSuffix(path, ".proto") {
			// Skip bazel output directories
			if !strings.Contains(path, "bazel-") {
				protoFiles = append(protoFiles, path)
			}
		}

		return nil
	})

	return protoFiles
}

// collectImportsFromProtoFile parses a single proto file and collects its imports
func collectImportsFromProtoFile(protoFile string, importToTarget map[string]string, allDeps map[string]bool) {
	content, err := ioutil.ReadFile(protoFile)
	if err != nil {
		log.Printf("Failed to read proto file %s: %v", protoFile, err)
		return
	}

	// Extract imports
	importPattern := regexp.MustCompile(`import\s+"([^"]+)"`)
	matches := importPattern.FindAllStringSubmatch(string(content), -1)

	for _, match := range matches {
		importPath := match[1]

		// Skip well-known protos
		if strings.HasPrefix(importPath, "google/") {
			continue
		}

		// Look up the target for this import
		if target, ok := importToTarget[importPath]; ok {
			if !allDeps[target] {
				allDeps[target] = true
				log.Printf("Added transitive dependency: %s (from import %s in %s)", target, importPath, protoFile)
				// Note: We don't recursively collect here to avoid the aggressive behavior
				// The bundle should only include direct imports from its own proto files
			}
		} else {
			log.Printf("Warning: Could not resolve import %s from %s", importPath, protoFile)
		}
	}
}

// buildImportIndex creates a mapping from import paths to bazel targets
// This remains largely unchanged but is now only used for resolving specific imports
func buildImportIndex(repoRoot string, index map[string]string) {
	filepath.Walk(repoRoot, func(path string, info os.FileInfo, err error) error {
		if err != nil || !strings.HasSuffix(path, ".proto") {
			return nil
		}

		// Skip bazel output directories
		if strings.Contains(path, "bazel-") {
			return nil
		}

		// Get relative path from repo root
		relPath, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return nil
		}

		// Find BUILD file for this proto
		dir := filepath.Dir(path)
		buildPath := filepath.Join(dir, "BUILD.bazel")
		if _, err := os.Stat(buildPath); os.IsNotExist(err) {
			buildPath = filepath.Join(dir, "BUILD")
			if _, err := os.Stat(buildPath); os.IsNotExist(err) {
				return nil
			}
		}

		// Read BUILD file and find the proto_library rule
		content, err := ioutil.ReadFile(buildPath)
		if err == nil {
			if target := findProtoTarget(string(content), filepath.Base(path)); target != "" {
				pkg, _ := filepath.Rel(repoRoot, dir)
				fullTarget := fmt.Sprintf("//%s:%s", pkg, target)
				index[relPath] = fullTarget
			}
		}
		return nil
	})
}

// findProtoTarget finds the proto_library rule that contains the given proto file
func findProtoTarget(buildContent, protoFile string) string {
	// Simple pattern matching - in production use proper BUILD parser
	pattern := regexp.MustCompile(`proto_library\(\s*name\s*=\s*"([^"]+)"[^)]*srcs\s*=\s*\[[^\]]*"` + regexp.QuoteMeta(protoFile) + `"`)
	if matches := pattern.FindStringSubmatch(buildContent); len(matches) > 1 {
		return matches[1]
	}
	return ""
}

// discoverProtosRecursively finds all proto targets in a directory and its subdirectories
func discoverProtosRecursively(rootDir string, repoRoot string) []string {
	var targets []string

	filepath.Walk(rootDir, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}

		// Look for BUILD files
		if info.Name() == "BUILD.bazel" || info.Name() == "BUILD" {
			content, err := os.ReadFile(path)
			if err != nil {
				return nil
			}

			buildContent := string(content)
			protoLibraryPattern := regexp.MustCompile(`proto_library\s*\(\s*[^)]*name\s*=\s*"([^"]+)"[^)]*\)`)
			matches := protoLibraryPattern.FindAllStringSubmatch(buildContent, -1)

			for _, match := range matches {
				if len(match) > 1 {
					name := match[1]
					dir := filepath.Dir(path)
					pkg, err := filepath.Rel(repoRoot, dir)
					if err == nil {
						if pkg == "." {
							targets = append(targets, ":"+name)
						} else {
							targets = append(targets, "//"+pkg+":"+name)
						}
					}
				}
			}
		}

		return nil
	})

	return targets
}
