package language

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/rule"
)

var importPattern = regexp.MustCompile(`import\s+"([^"]+)"`)

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

	// Detect external proto imports and derive per-language Bazel targets. Java
	// depends on a pre-compiled umbrella library (googleapis-java); Python and JS
	// have no such umbrella today, so they compile the external proto_library
	// targets directly alongside the bundle's own protos.
	externalDeps := detectExternalProtoImports(bundleDir)

	// Generate Java bundle if enabled
	log.Printf("Checking Java bundle generation - Enabled: %v, GroupId: '%s', ArtifactId: '%s'",
		config.JavaConfig.Enabled, config.JavaConfig.GroupId, config.JavaConfig.ArtifactId)
	if config.JavaConfig.Enabled && config.JavaConfig.GroupId != "" && config.JavaConfig.ArtifactId != "" {
		rules = append(rules, generateJavaBundleRules(config, bundleName, allProtoTargets, externalDeps.Java)...)
	} else {
		log.Printf("Skipping Java bundle generation for %s", bundleName)
	}

	// Generate Python bundle if enabled
	log.Printf("Checking Python bundle generation - Enabled: %v, PackageName: '%s'",
		config.PythonConfig.Enabled, config.PythonConfig.PackageName)
	if config.PythonConfig.Enabled && config.PythonConfig.PackageName != "" {
		rules = append(rules, generatePythonBundleRules(config, bundleName, allProtoTargets, externalDeps.ProtoLibraries)...)
	} else {
		log.Printf("Skipping Python bundle generation for %s", bundleName)
	}

	// Generate JavaScript bundle if enabled
	log.Printf("Checking JavaScript bundle generation - Enabled: %v, PackageName: '%s'",
		config.JavaScriptConfig.Enabled, config.JavaScriptConfig.PackageName)
	if config.JavaScriptConfig.Enabled && config.JavaScriptConfig.PackageName != "" {
		rules = append(rules, generateJavaScriptBundleRules(config, bundleName, allProtoTargets, externalDeps.ProtoLibraries)...)
	} else {
		log.Printf("Skipping JavaScript bundle generation for %s", bundleName)
	}

	// Generate descriptor set if enabled
	if config.GenerateDescriptorSet {
		rules = append(rules, generateDescriptorSetRules(config, bundleName, protoTargets)...)
	}

	// Generate proto-loader bundle if enabled
	if config.JavaScriptConfig.Enabled && config.JavaScriptConfig.ProtoLoader && config.JavaScriptConfig.PackageName != "" {
		rules = append(rules, generateProtoLoaderBundleRules(config, bundleName, allProtoTargets)...)
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
func generateJavaBundleRules(config *MergedConfig, bundleName string, allProtoTargets []string, externalJavaDeps []string) []*rule.Rule {
	var rules []*rule.Rule

	// Java gRPC library (includes both proto messages and gRPC stubs)
	javaGrpcRule := rule.NewRule("java_grpc_library", fmt.Sprintf("%s_java_grpc", bundleName))
	javaGrpcRule.SetAttr("protos", rule.PlatformStrings{Generic: allProtoTargets})
	if len(externalJavaDeps) > 0 {
		javaGrpcRule.SetAttr("deps", externalJavaDeps)
		log.Printf("Added %d external Java deps to %s_java_grpc: %v", len(externalJavaDeps), bundleName, externalJavaDeps)
	}
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
	if config.JavaConfig.FatJar {
		javaBundleRule.SetAttr("fat_jar", true)
	}
	// When the bundle requests a proto descriptor, wire the descriptor target
	// into the JAR so it ships at META-INF/proto-descriptors/<bundle>.pb.
	if config.GenerateDescriptorSet {
		javaBundleRule.SetAttr("descriptor_pb", fmt.Sprintf(":%s_descriptor", bundleName))
		javaBundleRule.SetAttr("bundle_name", bundleName)
	}
	javaBundleRule.SetAttr("visibility", []string{"//visibility:public"})
	rules = append(rules, javaBundleRule)

	// Publishing rule with environment variable support
	publishMavenRule := rule.NewRule("genrule", fmt.Sprintf("publish_%s_to_maven", bundleName))
	publishMavenRule.SetAttr("srcs", rule.PlatformStrings{Generic: []string{fmt.Sprintf(":%s_java_bundle", bundleName)}})
	publishMavenRule.SetAttr("outs", rule.PlatformStrings{Generic: []string{"publish_maven.log"}})
	// Command with environment variable expansion - this is the hybrid approach
	publishCmd := fmt.Sprintf("$(location //tools:publish_to_maven) "+
		"$(location :%s_java_bundle) "+
		"--group-id %s "+
		"--artifact-id %s "+
		"--version \"$${VERSION:-%s}\" "+
		"--repo \"$${MAVEN_REPO:-file://~/.m2/repository}\" "+
		"--protobuf-version \"$${PROTOBUF_JAVA_VERSION:-4.33.5}\" "+
		"--grpc-version \"$${GRPC_VERSION:-1.78.0}\" "+
		"> $@",
		bundleName, config.JavaConfig.GroupId, config.JavaConfig.ArtifactId, config.Version)
	publishMavenRule.SetAttr("cmd", publishCmd)
	publishMavenRule.SetAttr("tools", rule.PlatformStrings{Generic: []string{"//tools:publish_to_maven"}})
	rules = append(rules, publishMavenRule)

	// Convenience alias for publishing
	publishMavenAlias := rule.NewRule("alias", "publish_to_maven")
	publishMavenAlias.SetAttr("actual", fmt.Sprintf(":publish_%s_to_maven", bundleName))
	publishMavenAlias.SetAttr("visibility", []string{"//visibility:public"})
	rules = append(rules, publishMavenAlias)

	return rules
}

// generatePythonBundleRules creates Python bundle rules with hybrid publishing
func generatePythonBundleRules(config *MergedConfig, bundleName string, allProtoTargets []string, externalProtoLibraries []string) []*rule.Rule {
	var rules []*rule.Rule

	// Python gRPC library (includes both proto messages and gRPC stubs).
	// External proto_library targets (e.g. @googleapis//google/api:annotations_proto)
	// are appended to `protos` so rules_proto_grpc_python compiles them alongside
	// the bundle's own protos — without this the published wheel would be missing
	// google/api/*_pb2.py companions.
	pythonGrpcRule := rule.NewRule("python_grpc_library", fmt.Sprintf("%s_python_grpc", bundleName))
	protos := append([]string{}, allProtoTargets...)
	protos = append(protos, externalProtoLibraries...)
	pythonGrpcRule.SetAttr("protos", rule.PlatformStrings{Generic: protos})
	pythonGrpcRule.SetAttr("visibility", []string{"//visibility:public"})
	if len(externalProtoLibraries) > 0 {
		log.Printf("Added %d external proto_library targets to %s_python_grpc: %v",
			len(externalProtoLibraries), bundleName, externalProtoLibraries)
	}
	rules = append(rules, pythonGrpcRule)

	// Python bundle rule with STATIC configuration (no version attribute)
	// The unified rule handles environment variables internally
	pyBundleRule := rule.NewRule("py_proto_bundle", fmt.Sprintf("%s_py_bundle", bundleName))
	pyBundleRule.SetAttr("proto_deps", rule.PlatformStrings{Generic: []string{fmt.Sprintf(":%s_all_protos", bundleName)}})
	// Only set py_grpc_deps since python_grpc_library generates both proto and gRPC files
	pyBundleRule.SetAttr("py_deps", rule.PlatformStrings{Generic: []string{}})
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
	publishCmd := fmt.Sprintf("$(location //tools:publish_to_pypi) "+
		"$(location :%s_py_bundle) "+
		"--package-name %s "+
		"--version \"$${VERSION:-%s}\" "+
		"--repo \"$${PYPI_REPO:-file://~/.pypi}\" "+
		"> $@",
		bundleName, config.PythonConfig.PackageName, config.Version)
	publishPypiRule.SetAttr("cmd", publishCmd)
	publishPypiRule.SetAttr("tools", rule.PlatformStrings{Generic: []string{"//tools:publish_to_pypi"}})
	rules = append(rules, publishPypiRule)

	// Convenience alias for publishing
	publishPypiAlias := rule.NewRule("alias", "publish_to_pypi")
	publishPypiAlias.SetAttr("actual", fmt.Sprintf(":publish_%s_to_pypi", bundleName))
	publishPypiAlias.SetAttr("visibility", []string{"//visibility:public"})
	rules = append(rules, publishPypiAlias)

	return rules
}

// generateJavaScriptBundleRules creates JavaScript bundle rules with Connect-ES and hybrid publishing
func generateJavaScriptBundleRules(config *MergedConfig, bundleName string, allProtoTargets []string, externalProtoLibraries []string) []*rule.Rule {
	var rules []*rule.Rule

	// Connect-ES compilation (protoc-gen-es) — generates _pb.js + _pb.d.ts.
	// External proto_library targets (e.g. @googleapis//google/api:annotations_proto)
	// are appended to `protos` so protoc-gen-es produces _pb.js for them too —
	// without this the generated authz_pb.js would reference missing
	// ../../../google/api/*_pb imports and the consumer's build would fail.
	esProtoRule := rule.NewRule("es_proto_compile", fmt.Sprintf("%s_es_proto", bundleName))
	protos := append([]string{}, allProtoTargets...)
	protos = append(protos, externalProtoLibraries...)
	esProtoRule.SetAttr("protos", rule.PlatformStrings{Generic: protos})
	esProtoRule.SetAttr("visibility", []string{"//visibility:public"})
	if len(externalProtoLibraries) > 0 {
		log.Printf("Added %d external proto_library targets to %s_es_proto: %v",
			len(externalProtoLibraries), bundleName, externalProtoLibraries)
	}
	rules = append(rules, esProtoRule)

	// JavaScript bundle rule with Connect-ES deps
	jsBundleRule := rule.NewRule("js_proto_bundle", fmt.Sprintf("%s_js_bundle", bundleName))
	jsBundleRule.SetAttr("proto_deps", rule.PlatformStrings{Generic: []string{fmt.Sprintf(":%s_all_protos", bundleName)}})
	jsBundleRule.SetAttr("es_deps", rule.PlatformStrings{Generic: []string{fmt.Sprintf(":%s_es_proto", bundleName)}})
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
	publishCmd := fmt.Sprintf("echo '%s@'\"$${VERSION:-%s}\" > %s_coords.txt && "+
		"$(location //tools:publish_to_npm) "+
		"$(location :%s_js_bundle) %s_coords.txt "+
		"> $@",
		config.JavaScriptConfig.PackageName, config.Version, bundleName, bundleName, bundleName)
	publishNpmRule.SetAttr("cmd", publishCmd)
	publishNpmRule.SetAttr("tools", rule.PlatformStrings{Generic: []string{"//tools:publish_to_npm"}})
	rules = append(rules, publishNpmRule)

	// Convenience alias for publishing
	publishNpmAlias := rule.NewRule("alias", "publish_to_npm")
	publishNpmAlias.SetAttr("actual", fmt.Sprintf(":publish_%s_to_npm", bundleName))
	publishNpmAlias.SetAttr("visibility", []string{"//visibility:public"})
	rules = append(rules, publishNpmAlias)

	return rules
}

// generateDescriptorSetRules creates a proto_descriptor_set rule for Envoy/gRPC tools
func generateDescriptorSetRules(config *MergedConfig, bundleName string, protoTargets []string) []*rule.Rule {
	var rules []*rule.Rule

	descriptorRule := rule.NewRule("proto_descriptor_set", fmt.Sprintf("%s_descriptor", bundleName))
	descriptorRule.SetAttr("deps", rule.PlatformStrings{Generic: protoTargets})
	descriptorRule.SetAttr("visibility", []string{"//visibility:public"})
	rules = append(rules, descriptorRule)

	return rules
}

// generateProtoLoaderBundleRules creates proto-loader rules for @grpc/proto-loader packages
func generateProtoLoaderBundleRules(config *MergedConfig, bundleName string, allProtoTargets []string) []*rule.Rule {
	var rules []*rule.Rule

	// Proto-loader bundle rule
	protoLoaderRule := rule.NewRule("js_proto_loader_bundle", fmt.Sprintf("%s_proto_loader_bundle", bundleName))
	protoLoaderRule.SetAttr("proto_deps", rule.PlatformStrings{Generic: []string{fmt.Sprintf(":%s_all_protos", bundleName)}})
	// Use the same package name with a -loader suffix to distinguish from compiled JS package
	loaderPkgName := config.JavaScriptConfig.PackageName + "-loader"
	protoLoaderRule.SetAttr("package_name", loaderPkgName)
	protoLoaderRule.SetAttr("visibility", []string{"//visibility:public"})
	rules = append(rules, protoLoaderRule)

	// Publishing rule for proto-loader package
	publishProtoLoaderRule := rule.NewRule("genrule", fmt.Sprintf("publish_%s_proto_loader_to_npm", bundleName))
	publishProtoLoaderRule.SetAttr("srcs", rule.PlatformStrings{Generic: []string{fmt.Sprintf(":%s_proto_loader_bundle", bundleName)}})
	publishProtoLoaderRule.SetAttr("outs", rule.PlatformStrings{Generic: []string{"publish_proto_loader_npm.log"}})
	publishCmd := fmt.Sprintf("echo '%s@'\"$${VERSION:-%s}\" > %s_proto_loader_coords.txt && "+
		"$(location //tools:publish_proto_loader_to_npm) "+
		"$(location :%s_proto_loader_bundle) %s_proto_loader_coords.txt "+
		"> $@",
		loaderPkgName, config.Version, bundleName, bundleName, bundleName)
	publishProtoLoaderRule.SetAttr("cmd", publishCmd)
	publishProtoLoaderRule.SetAttr("tools", rule.PlatformStrings{Generic: []string{"//tools:publish_proto_loader_to_npm"}})
	rules = append(rules, publishProtoLoaderRule)

	return rules
}

// generateLegacyCleanupRules returns empty rules that signal Gazelle to delete
// legacy grpc-web JS rules from existing BUILD files (replaced by es_proto_compile).
func generateLegacyCleanupRules(config *MergedConfig) []*rule.Rule {
	var empty []*rule.Rule
	bundleName := config.BundleName

	// Delete legacy js_grpc_library / js_grpc_web_library rules
	if config.JavaScriptConfig.Enabled {
		emptyJsGrpc := rule.NewRule("js_grpc_library", fmt.Sprintf("%s_js_grpc_node", bundleName))
		emptyJsGrpcWeb := rule.NewRule("js_grpc_web_library", fmt.Sprintf("%s_js_grpc_web", bundleName))
		empty = append(empty, emptyJsGrpc, emptyJsGrpcWeb)
	}

	// Delete proto_loader rules if no longer enabled
	if config.JavaScriptConfig.Enabled && !config.JavaScriptConfig.ProtoLoader {
		emptyLoader := rule.NewRule("js_proto_loader_bundle", fmt.Sprintf("%s_proto_loader_bundle", bundleName))
		empty = append(empty, emptyLoader)
	}

	return empty
}

// ExternalProtoDeps carries the Bazel targets that need to be wired into per-language
// rules when a bundle's protos import external sources (googleapis, protovalidate).
//
//   - Java uses pre-compiled umbrella library targets (e.g. api_java_proto) — these are
//     passed to `java_grpc_library.deps` so the JAR depends on already-built classes.
//   - Python and JS have no umbrella equivalent in this workspace, so the raw
//     proto_library targets are compiled alongside the bundle's own protos by
//     appending them to the `protos` attribute of `python_grpc_library` and
//     `es_proto_compile`. This produces the matching _pb2.py / _pb.js companions
//     inside the published wheel / npm tarball.
type ExternalProtoDeps struct {
	// Java target list (umbrella libraries, typically one entry per provider).
	Java []string
	// Raw proto_library targets, used by Python and JS which recompile per-bundle.
	ProtoLibraries []string
}

// googleapisJsProtos is the set of google/api protos our consumers transitively need
// when any google/api/*.proto is imported. protoc compiles only the srcs of each
// listed proto_library (not their transitive imports), so we include the whole set
// rather than trying to resolve each import graph precisely. launch_stage is
// pulled in by client.proto; the remaining ones cover the annotations we see
// across cohub bundles today. This is a small set and the extra _pb.js files
// are negligible in the published tarball.
var googleapisJsProtos = []string{
	"@googleapis//google/api:annotations_proto",
	"@googleapis//google/api:client_proto",
	"@googleapis//google/api:field_behavior_proto",
	"@googleapis//google/api:http_proto",
	"@googleapis//google/api:launch_stage_proto",
	"@googleapis//google/api:resource_proto",
}

// detectExternalProtoImports scans a bundle's .proto files and returns the per-language
// Bazel targets required to satisfy external imports (googleapis, longrunning,
// protovalidate).
func detectExternalProtoImports(bundleDir string) ExternalProtoDeps {
	needsGoogleapis := false
	needsLongrunning := false
	needsProtovalidate := false

	protoFiles := collectBundleProtoFiles(bundleDir)
	for _, protoFile := range protoFiles {
		content, err := os.ReadFile(protoFile)
		if err != nil {
			continue
		}
		matches := importPattern.FindAllStringSubmatch(string(content), -1)
		for _, match := range matches {
			importPath := match[1]
			if strings.HasPrefix(importPath, "google/api/") {
				needsGoogleapis = true
			}
			if strings.HasPrefix(importPath, "google/longrunning/") {
				needsLongrunning = true
			}
			if strings.HasPrefix(importPath, "buf/validate/") {
				needsProtovalidate = true
			}
		}
	}

	var out ExternalProtoDeps
	if needsGoogleapis {
		out.Java = append(out.Java, "@googleapis//google/api:api_java_proto")
		out.ProtoLibraries = append(out.ProtoLibraries, googleapisJsProtos...)
	}
	if needsLongrunning {
		// Java umbrella target aggregates longrunning message + gRPC classes.
		out.Java = append(out.Java, "@googleapis//google/longrunning:longrunning_java_proto")
		// Python/JS recompile raw proto_library targets per-bundle — operations.proto
		// is the only file under google/longrunning and carries everything callers need.
		out.ProtoLibraries = append(out.ProtoLibraries, "@googleapis//google/longrunning:operations_proto")
	}
	if needsProtovalidate {
		out.Java = append(out.Java, "//:protovalidate_java_proto")
		// Raw proto_library lives under the module's proto/ subtree — see the lake's
		// `protovalidate_java_proto` target for the canonical path.
		out.ProtoLibraries = append(out.ProtoLibraries, "@protovalidate//proto/protovalidate/buf/validate:validate_proto")
	}
	return out
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
			if !strings.Contains(path, bazelDirPrefix) {
				protoFiles = append(protoFiles, path)
			}
		}

		return nil
	})

	return protoFiles
}

// collectImportsFromProtoFile parses a single proto file and collects its imports
func collectImportsFromProtoFile(protoFile string, importToTarget map[string]string, allDeps map[string]bool) {
	content, err := os.ReadFile(protoFile)
	if err != nil {
		log.Printf("Failed to read proto file %s: %v", protoFile, err)
		return
	}

	// Extract imports
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
		if strings.Contains(path, bazelDirPrefix) {
			return nil
		}

		// Get relative path from repo root
		relPath, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return nil
		}

		// Find BUILD file for this proto
		dir := filepath.Dir(path)
		buildPath := filepath.Join(dir, buildBazelFile)
		if _, err := os.Stat(buildPath); os.IsNotExist(err) {
			buildPath = filepath.Join(dir, buildFile)
			if _, err := os.Stat(buildPath); os.IsNotExist(err) {
				return nil
			}
		}

		// Read BUILD file and find the proto_library rule
		content, err := os.ReadFile(buildPath)
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

