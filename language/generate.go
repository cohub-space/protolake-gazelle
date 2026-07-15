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

// generateBundleRules creates the bundle rules for a bundle.
// Static configuration (coordinates, dependencies, package names) is baked
// into BUILD files at gazelle time; the version resolves from the bundle's
// bundle.yaml at build time (`bundle_yaml` attr on bundle rules,
// `--bundle-yaml` on the pom genrules and py_binary publishers). The one
// intentional analysis-time version literal is the maven_publish
// `coordinates` string, guarded by `--expected-version` on the pom genrules
// — see generateJavaBundleRules.
func generateBundleRules(config *MergedConfig, protoTargets []string, rel string, c *config.Config) []*rule.Rule {
	var rules []*rule.Rule
	bundleName := config.BundleName

	// Fail fast if version is missing. Defaulting to "1.0.0" silently masked the
	// "bundle.yaml bumped but publish stuck at 1.0.0" failure mode for months —
	// see PL-c4d8 / cohub-protolake#30 incident notes. Better to break the build
	// loudly so the missing version is fixed at its source. Since PL-bstm the
	// version literal is only baked into maven_publish coordinates (everything
	// else resolves from bundle.yaml at build time), but this check stays as
	// gazelle-time validation of bundle.yaml.
	if config.Version == "" {
		log.Fatalf("[protolake-gazelle] bundle %q at %s is missing required field "+
			"`version` in bundle.yaml. Set an explicit version (e.g. `version: \"1.0.0\"`); "+
			"omitting it would publish under a fallback that drifts silently.",
			bundleName, rel)
	}

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
	if config.JavaConfig.Enabled {
		requireCoordinates(bundleName, rel, "java",
			[2]string{"group_id", config.JavaConfig.GroupId},
			[2]string{"artifact_id", config.JavaConfig.ArtifactId})
		rules = append(rules, generateJavaBundleRules(config, bundleName, allProtoTargets, externalDeps.Java)...)
	} else {
		log.Printf("Skipping Java bundle generation for %s (disabled)", bundleName)
	}

	// Generate Python bundle if enabled
	log.Printf("Checking Python bundle generation - Enabled: %v, PackageName: '%s'",
		config.PythonConfig.Enabled, config.PythonConfig.PackageName)
	if config.PythonConfig.Enabled {
		requireCoordinates(bundleName, rel, "python",
			[2]string{"package_name", config.PythonConfig.PackageName})
		rules = append(rules, generatePythonBundleRules(config, bundleName, allProtoTargets, externalDeps.ProtoLibraries)...)
	} else {
		log.Printf("Skipping Python bundle generation for %s (disabled)", bundleName)
	}

	// Generate JavaScript bundle if enabled
	log.Printf("Checking JavaScript bundle generation - Enabled: %v, PackageName: '%s'",
		config.JavaScriptConfig.Enabled, config.JavaScriptConfig.PackageName)
	if config.JavaScriptConfig.Enabled {
		requireCoordinates(bundleName, rel, "javascript",
			[2]string{"package_name", config.JavaScriptConfig.PackageName})
		rules = append(rules, generateJavaScriptBundleRules(config, bundleName, allProtoTargets, externalDeps.ProtoLibraries)...)
	} else {
		log.Printf("Skipping JavaScript bundle generation for %s (disabled)", bundleName)
	}

	// Generate descriptor set if enabled
	if config.GenerateDescriptorSet {
		rules = append(rules, generateDescriptorSetRules(config, bundleName, protoTargets)...)
	}

	// Generate proto-loader bundle if enabled (requireCoordinates above
	// guarantees a non-empty package name whenever JS is enabled)
	if config.JavaScriptConfig.Enabled && config.JavaScriptConfig.ProtoLoader {
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

// requireCoordinates fail-fasts when a language is enabled but a publish
// coordinate (group_id/artifact_id/package_name) is empty in the merged
// lake.yaml+bundle.yaml config. Generation needs the coordinates while the
// disabled-language cleanup keys on Enabled alone, so an enabled-but-
// coordinate-less language would get neither generation nor cleanup — stale
// old-form rules survive and fail the bazel loading phase with a confusing
// error. Mirrors the missing-version fatal in generateBundleRules. Each field
// is a {name, value} pair; order determines the message order.
func requireCoordinates(bundleName, rel, language string, fields ...[2]string) {
	var missing []string
	for _, f := range fields {
		if f[1] == "" {
			missing = append(missing, f[0])
		}
	}
	if len(missing) == 0 {
		return
	}
	log.Fatalf("[protolake-gazelle] bundle %q at %s enables %s but the merged "+
		"lake.yaml/bundle.yaml config leaves %s empty. An enabled language without "+
		"coordinates gets neither generated rules nor cleanup, leaving stale rules "+
		"to break the bazel loading phase. Set the field(s) or disable the language "+
		"explicitly (`enabled: false`).",
		bundleName, rel, language, strings.Join(missing, ", "))
}

// generateJavaBundleRules creates Java bundle rules with maven_publish from rules_jvm_external.
//
// The publish target is no longer a genrule wrapping a python publisher — it's a
// `maven_publish` executable rule. POM XML is generated by a sibling genrule that
// invokes `//tools:pom_generator`. The bundle version resolves from bundle.yaml at
// BUILD time: the bundle rule reads it via the `bundle_yaml` attr and the pom
// genrule via `--bundle-yaml`. The maven_publish `coordinates` string is the one
// intentional analysis-time version literal — see the comment on that rule.
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

	// Version literal from bundle.yaml — used ONLY for the maven_publish
	// coordinates below. generateBundleRules already fail-fasts if
	// config.Version is empty, so no fallback needed here.
	version := config.Version

	// Java bundle rule. Coordinates come from configuration; the JAR's
	// MANIFEST.MF carries the version, resolved at build time from the
	// `bundle_yaml` source-file label (same-package ref, no exports_files
	// needed). The maven coordinate used at publish time comes from the
	// sibling maven_publish rule.
	javaBundleRule := rule.NewRule("java_proto_bundle", fmt.Sprintf("%s_java_bundle", bundleName))
	javaBundleRule.SetAttr("proto_deps", rule.PlatformStrings{Generic: []string{fmt.Sprintf(":%s_all_protos", bundleName)}})
	javaBundleRule.SetAttr("java_deps", rule.PlatformStrings{Generic: []string{fmt.Sprintf(":%s_java_grpc", bundleName)}})
	javaBundleRule.SetAttr("java_grpc_deps", rule.PlatformStrings{Generic: []string{fmt.Sprintf(":%s_java_grpc", bundleName)}})
	javaBundleRule.SetAttr("group_id", config.JavaConfig.GroupId)
	javaBundleRule.SetAttr("artifact_id", config.JavaConfig.ArtifactId)
	javaBundleRule.SetAttr("bundle_yaml", ":bundle.yaml")
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

	// POM generator genrule — pom_generator writes POM XML to its --out path. No
	// upload happens here; that's maven_publish's job. The version is read from
	// bundle.yaml at build time (`--bundle-yaml`), so a version bump takes effect
	// without regenerating this cmd. `--expected-version` bakes the version this
	// gazelle run saw (the same literal as the maven_publish coordinates below):
	// if bundle.yaml is edited without a gazelle pass, pom_generator fails
	// instead of uploading an artifact whose GAV coordinate disagrees with its
	// POM. Space-separated on purpose — versions can't start with '-'.
	pomRule := rule.NewRule("genrule", fmt.Sprintf("%s_pom", bundleName))
	pomRule.SetAttr("srcs", rule.PlatformStrings{Generic: []string{"bundle.yaml"}})
	pomRule.SetAttr("outs", rule.PlatformStrings{Generic: []string{fmt.Sprintf("%s.pom.xml", bundleName)}})
	pomCmd := fmt.Sprintf(
		"$(location //tools:pom_generator) "+
			"--group-id %s "+
			"--artifact-id %s "+
			"--bundle-yaml $(location bundle.yaml) "+
			"--expected-version %s "+
			"--protobuf-version $${PROTOBUF_JAVA_VERSION:-4.33.5} "+
			"--grpc-version $${GRPC_VERSION:-1.78.0} "+
			"--out $@",
		config.JavaConfig.GroupId, config.JavaConfig.ArtifactId, version)
	pomRule.SetAttr("cmd", pomCmd)
	pomRule.SetAttr("tools", rule.PlatformStrings{Generic: []string{"//tools:pom_generator"}})
	pomRule.SetAttr("visibility", []string{"//visibility:public"})
	rules = append(rules, pomRule)

	// maven_publish executable rule. Invoked via `bazel run`. Reads MAVEN_REPO /
	// MAVEN_USER / MAVEN_PASSWORD from the env at run time.
	//
	// `coordinates` keeps the version baked as a gazelle-time literal — the ONE
	// intentional analysis-time literal left after PL-bstm. rules_jvm_external
	// 6.10's maven_publish runs coordinates through ctx.expand_make_variables,
	// so a `$(pom_version)` make variable fed by `--define pom_version=X` would
	// work — but expansion happens at analysis time for EVERY invocation that
	// analyzes the target, so plain wildcard builds (`bazel build //...`) would
	// fail unless a default define is wired into .bazelrc, and a default define
	// reintroduces exactly the silent-fallback-version drift PL-bstm removes.
	// There is no runtime/stamping placeholder in 6.10. Gazelle runs before
	// every protolake build, and `coordinates` is mergeable, so the literal
	// stays in sync with bundle.yaml.
	publishMavenRule := rule.NewRule("maven_publish", fmt.Sprintf("publish_%s_to_maven", bundleName))
	publishMavenRule.SetAttr("coordinates",
		fmt.Sprintf("%s:%s:%s", config.JavaConfig.GroupId, config.JavaConfig.ArtifactId, version))
	publishMavenRule.SetAttr("pom", fmt.Sprintf(":%s_pom", bundleName))
	publishMavenRule.SetAttr("artifact", fmt.Sprintf(":%s_java_bundle", bundleName))
	publishMavenRule.SetAttr("visibility", []string{"//visibility:public"})
	rules = append(rules, publishMavenRule)

	// Convenience alias for publishing
	publishMavenAlias := rule.NewRule("alias", "publish_to_maven")
	publishMavenAlias.SetAttr("actual", fmt.Sprintf(":publish_%s_to_maven", bundleName))
	publishMavenAlias.SetAttr("visibility", []string{"//visibility:public"})
	rules = append(rules, publishMavenAlias)

	// Local-publish twin at a `-local`-qualified version. Maven caches a
	// release version forever, so a local install at the release coordinates
	// silently shadows the eventual CI release on that machine. Keeping local
	// installs at `<version>-local` leaves the release name vacant; consumers
	// under test pin the qualifier explicitly. The orchestrator runs this
	// target instead of the plain one when MAVEN_REPO is not an http(s)
	// registry (protolake BazelBuildRunner).
	localVersion := version + "-local"
	pomLocalRule := rule.NewRule("genrule", fmt.Sprintf("%s_pom_local", bundleName))
	pomLocalRule.SetAttr("srcs", rule.PlatformStrings{Generic: []string{"bundle.yaml"}})
	pomLocalRule.SetAttr("outs", rule.PlatformStrings{Generic: []string{fmt.Sprintf("%s.pom_local.xml", bundleName)}})
	// `--version-suffix=-local` (equals form — argparse rejects a space-separated
	// value starting with `-`) appends the qualifier to the version pom_generator
	// reads from bundle.yaml, keeping the POM's <version> aligned with the
	// -local maven_publish coordinates below. `--expected-version` carries the
	// RAW bundle.yaml version (no -local suffix): pom_generator runs the
	// stale-BUILD check on the pre-suffix version, then applies the suffix.
	pomLocalCmd := fmt.Sprintf(
		"$(location //tools:pom_generator) "+
			"--group-id %s "+
			"--artifact-id %s "+
			"--bundle-yaml $(location bundle.yaml) "+
			"--expected-version %s "+
			"--version-suffix=-local "+
			"--protobuf-version $${PROTOBUF_JAVA_VERSION:-4.33.5} "+
			"--grpc-version $${GRPC_VERSION:-1.78.0} "+
			"--out $@",
		config.JavaConfig.GroupId, config.JavaConfig.ArtifactId, version)
	pomLocalRule.SetAttr("cmd", pomLocalCmd)
	pomLocalRule.SetAttr("tools", rule.PlatformStrings{Generic: []string{"//tools:pom_generator"}})
	pomLocalRule.SetAttr("visibility", []string{"//visibility:public"})
	rules = append(rules, pomLocalRule)

	publishMavenLocalRule := rule.NewRule("maven_publish", fmt.Sprintf("publish_%s_to_maven_local", bundleName))
	publishMavenLocalRule.SetAttr("coordinates",
		fmt.Sprintf("%s:%s:%s", config.JavaConfig.GroupId, config.JavaConfig.ArtifactId, localVersion))
	publishMavenLocalRule.SetAttr("pom", fmt.Sprintf(":%s_pom_local", bundleName))
	publishMavenLocalRule.SetAttr("artifact", fmt.Sprintf(":%s_java_bundle", bundleName))
	publishMavenLocalRule.SetAttr("visibility", []string{"//visibility:public"})
	rules = append(rules, publishMavenLocalRule)

	return rules
}

// generatePythonBundleRules creates Python bundle rules and a per-bundle py_binary
// publish target. External proto_library targets (e.g.
// @googleapis//google/api:annotations_proto) are compiled alongside the bundle's
// own protos by python_grpc_library. The publisher script lives in //tools and
// is referenced via cross-package srcs; the package name is baked at gazelle
// time, while the version resolves from bundle.yaml at build/run time
// (bundle_yaml attr on the bundle rule, --bundle-yaml on the publisher).
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

	// Python bundle rule. Package name comes from configuration; the version is
	// read from bundle.yaml at build time via the bundle_yaml attr.
	pyBundleRule := rule.NewRule("py_proto_bundle", fmt.Sprintf("%s_py_bundle", bundleName))
	pyBundleRule.SetAttr("proto_deps", rule.PlatformStrings{Generic: []string{fmt.Sprintf(":%s_all_protos", bundleName)}})
	// Only set py_grpc_deps since python_grpc_library generates both proto and gRPC files
	pyBundleRule.SetAttr("py_deps", rule.PlatformStrings{Generic: []string{}})
	pyBundleRule.SetAttr("py_grpc_deps", rule.PlatformStrings{Generic: []string{fmt.Sprintf(":%s_python_grpc", bundleName)}})
	pyBundleRule.SetAttr("package_name", config.PythonConfig.PackageName)
	pyBundleRule.SetAttr("bundle_yaml", ":bundle.yaml")
	pyBundleRule.SetAttr("visibility", []string{"//visibility:public"})
	rules = append(rules, pyBundleRule)

	// py_binary publish target. Invoked via `bazel run`; exit code propagates.
	// bundle.yaml rides in `data` and is addressed via the same runfiles-relative
	// $(location) mechanism as the bundle artifact arg.
	publishPypiRule := rule.NewRule("py_binary", fmt.Sprintf("publish_%s_to_pypi", bundleName))
	publishPypiRule.SetAttr("srcs", []string{"//tools:publish/pypi_publisher_generated.py"})
	publishPypiRule.SetAttr("main", "publish/pypi_publisher_generated.py")
	publishPypiRule.SetAttr("data", []string{
		fmt.Sprintf(":%s_py_bundle", bundleName),
		"bundle.yaml",
	})
	publishPypiRule.SetAttr("args", []string{
		fmt.Sprintf("$(location :%s_py_bundle)", bundleName),
		fmt.Sprintf("--package-name=%s", config.PythonConfig.PackageName),
		"--bundle-yaml=$(location bundle.yaml)",
	})
	publishPypiRule.SetAttr("deps", []string{"//tools:publisher_utils"})
	publishPypiRule.SetAttr("visibility", []string{"//visibility:public"})
	rules = append(rules, publishPypiRule)

	// Convenience alias for publishing
	publishPypiAlias := rule.NewRule("alias", "publish_to_pypi")
	publishPypiAlias.SetAttr("actual", fmt.Sprintf(":publish_%s_to_pypi", bundleName))
	publishPypiAlias.SetAttr("visibility", []string{"//visibility:public"})
	rules = append(rules, publishPypiAlias)

	return rules
}

// generateJavaScriptBundleRules creates JavaScript bundle rules with Connect-ES
// and a per-bundle py_binary publish target. External proto_library targets
// (e.g. @googleapis//google/api:annotations_proto) are compiled alongside the
// bundle's own protos by es_proto_compile. NPM publish modes (link, workspace,
// registry, ...) are still selected at runtime via NPM_PUBLISH_MODE; the
// package version resolves from bundle.yaml at build/run time.
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

	// JavaScript bundle rule with Connect-ES deps. Version read from bundle.yaml
	// at build time via the bundle_yaml attr.
	jsBundleRule := rule.NewRule("js_proto_bundle", fmt.Sprintf("%s_js_bundle", bundleName))
	jsBundleRule.SetAttr("proto_deps", rule.PlatformStrings{Generic: []string{fmt.Sprintf(":%s_all_protos", bundleName)}})
	jsBundleRule.SetAttr("es_deps", rule.PlatformStrings{Generic: []string{fmt.Sprintf(":%s_es_proto", bundleName)}})
	jsBundleRule.SetAttr("package_name", config.JavaScriptConfig.PackageName)
	jsBundleRule.SetAttr("bundle_yaml", ":bundle.yaml")
	jsBundleRule.SetAttr("visibility", []string{"//visibility:public"})
	rules = append(rules, jsBundleRule)

	// py_binary publish target. Invoked via `bazel run`.
	publishNpmRule := rule.NewRule("py_binary", fmt.Sprintf("publish_%s_to_npm", bundleName))
	publishNpmRule.SetAttr("srcs", []string{"//tools:publish/npm_publisher_generated.py"})
	publishNpmRule.SetAttr("main", "publish/npm_publisher_generated.py")
	publishNpmRule.SetAttr("data", []string{
		fmt.Sprintf(":%s_js_bundle", bundleName),
		"bundle.yaml",
	})
	publishNpmRule.SetAttr("args", []string{
		fmt.Sprintf("$(location :%s_js_bundle)", bundleName),
		fmt.Sprintf("--package-name=%s", config.JavaScriptConfig.PackageName),
		"--bundle-yaml=$(location bundle.yaml)",
	})
	publishNpmRule.SetAttr("deps", []string{"//tools:publisher_utils", "//tools:pkg_editor"})
	publishNpmRule.SetAttr("visibility", []string{"//visibility:public"})
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

// generateProtoLoaderBundleRules creates proto-loader rules for @grpc/proto-loader packages.
// Like the npm path, the publish target is a per-bundle py_binary; the version
// resolves from bundle.yaml at build/run time.
func generateProtoLoaderBundleRules(config *MergedConfig, bundleName string, allProtoTargets []string) []*rule.Rule {
	var rules []*rule.Rule

	// Proto-loader bundle rule
	protoLoaderRule := rule.NewRule("js_proto_loader_bundle", fmt.Sprintf("%s_proto_loader_bundle", bundleName))
	protoLoaderRule.SetAttr("proto_deps", rule.PlatformStrings{Generic: []string{fmt.Sprintf(":%s_all_protos", bundleName)}})
	// Use the same package name with a -loader suffix to distinguish from compiled JS package
	loaderPkgName := config.JavaScriptConfig.PackageName + "-loader"
	protoLoaderRule.SetAttr("package_name", loaderPkgName)
	protoLoaderRule.SetAttr("bundle_yaml", ":bundle.yaml")
	protoLoaderRule.SetAttr("visibility", []string{"//visibility:public"})
	rules = append(rules, protoLoaderRule)

	// py_binary publish target.
	publishProtoLoaderRule := rule.NewRule("py_binary", fmt.Sprintf("publish_%s_proto_loader_to_npm", bundleName))
	publishProtoLoaderRule.SetAttr("srcs", []string{"//tools:publish/proto_loader_publisher_generated.py"})
	publishProtoLoaderRule.SetAttr("main", "publish/proto_loader_publisher_generated.py")
	publishProtoLoaderRule.SetAttr("data", []string{
		fmt.Sprintf(":%s_proto_loader_bundle", bundleName),
		"bundle.yaml",
	})
	publishProtoLoaderRule.SetAttr("args", []string{
		fmt.Sprintf("$(location :%s_proto_loader_bundle)", bundleName),
		fmt.Sprintf("--package-name=%s", loaderPkgName),
		"--bundle-yaml=$(location bundle.yaml)",
	})
	publishProtoLoaderRule.SetAttr("deps", []string{"//tools:pkg_editor"})
	publishProtoLoaderRule.SetAttr("visibility", []string{"//visibility:public"})
	rules = append(rules, publishProtoLoaderRule)

	return rules
}

// generateLegacyCleanupRules returns empty rules that signal Gazelle to delete
// rules superseded by migrations:
//   - js_grpc_library / js_grpc_web_library — replaced by es_proto_compile
//     (Connect-ES migration)
//   - genrule(publish_<bundle>_to_*) — replaced by maven_publish + py_binary
//     (publisher-execution-model migration). Without these, the new rules
//     would collide with same-named genrules and gazelle's merge silently
//     skips the new emission.
func generateLegacyCleanupRules(config *MergedConfig) []*rule.Rule {
	var empty []*rule.Rule
	bundleName := config.BundleName

	// Delete legacy js_grpc_library / js_grpc_web_library rules
	if config.JavaScriptConfig.Enabled {
		emptyJsGrpc := rule.NewRule("js_grpc_library", fmt.Sprintf("%s_js_grpc_node", bundleName))
		emptyJsGrpcWeb := rule.NewRule("js_grpc_web_library", fmt.Sprintf("%s_js_grpc_web", bundleName))
		empty = append(empty, emptyJsGrpc, emptyJsGrpcWeb)
	}

	// Delete proto_loader rules if no longer enabled — the bundle rule AND its
	// publish py_binary (which references the bundle rule via data/args and
	// would dangle if only the bundle rule were removed).
	if config.JavaScriptConfig.Enabled && !config.JavaScriptConfig.ProtoLoader {
		empty = append(empty,
			rule.NewRule("js_proto_loader_bundle", fmt.Sprintf("%s_proto_loader_bundle", bundleName)),
			rule.NewRule("py_binary", fmt.Sprintf("publish_%s_proto_loader_to_npm", bundleName)))
	}

	// Delete legacy publish genrules. They collide with the new maven_publish /
	// py_binary rules (same names) — gazelle's merge would silently skip the
	// new emission if these aren't explicitly removed first.
	if config.JavaConfig.Enabled {
		empty = append(empty,
			rule.NewRule("genrule", fmt.Sprintf("publish_%s_to_maven", bundleName)))
	}
	if config.PythonConfig.Enabled {
		empty = append(empty,
			rule.NewRule("genrule", fmt.Sprintf("publish_%s_to_pypi", bundleName)))
	}
	if config.JavaScriptConfig.Enabled {
		empty = append(empty,
			rule.NewRule("genrule", fmt.Sprintf("publish_%s_to_npm", bundleName)))
		if config.JavaScriptConfig.ProtoLoader {
			empty = append(empty,
				rule.NewRule("genrule", fmt.Sprintf("publish_%s_proto_loader_to_npm", bundleName)))
		}
	}

	return empty
}

// generateDisabledLanguageCleanupRules returns empty rules for every
// deterministic target name of each language NOT enabled in the merged
// config, so stale pre-existing rules are deleted when a bundle disables a
// language instead of breaking the build:
//   - a lingering old-form bundle rule with `version =` fails at the bazel
//     loading phase under the post-PL-bstm proto_bundle.bzl (no such attr);
//   - a lingering publish rule or `publish_to_*` alias dangles on its deleted
//     bundle target and fails analysis;
//   - with zero languages enabled, a pre-existing build_validation `all`
//     dangles on its just-deleted bundle targets (see below).
//
// Empty rules that match nothing are no-ops, so this is safe on lakes that
// never had the language. Never emitted for enabled languages — those are
// covered by the generated rules merging over the existing ones.
func generateDisabledLanguageCleanupRules(config *MergedConfig) []*rule.Rule {
	var empty []*rule.Rule
	bundleName := config.BundleName

	if !config.JavaConfig.Enabled {
		empty = append(empty,
			rule.NewRule("java_grpc_library", fmt.Sprintf("%s_java_grpc", bundleName)),
			rule.NewRule("java_proto_bundle", fmt.Sprintf("%s_java_bundle", bundleName)),
			rule.NewRule("genrule", fmt.Sprintf("%s_pom", bundleName)),
			rule.NewRule("genrule", fmt.Sprintf("%s_pom_local", bundleName)),
			rule.NewRule("maven_publish", fmt.Sprintf("publish_%s_to_maven", bundleName)),
			rule.NewRule("maven_publish", fmt.Sprintf("publish_%s_to_maven_local", bundleName)),
			rule.NewRule("alias", "publish_to_maven"))
	}

	if !config.PythonConfig.Enabled {
		empty = append(empty,
			rule.NewRule("python_grpc_library", fmt.Sprintf("%s_python_grpc", bundleName)),
			rule.NewRule("py_proto_bundle", fmt.Sprintf("%s_py_bundle", bundleName)),
			rule.NewRule("py_binary", fmt.Sprintf("publish_%s_to_pypi", bundleName)),
			rule.NewRule("alias", "publish_to_pypi"))
	}

	if !config.JavaScriptConfig.Enabled {
		empty = append(empty,
			rule.NewRule("es_proto_compile", fmt.Sprintf("%s_es_proto", bundleName)),
			rule.NewRule("js_proto_bundle", fmt.Sprintf("%s_js_bundle", bundleName)),
			rule.NewRule("py_binary", fmt.Sprintf("publish_%s_to_npm", bundleName)),
			rule.NewRule("alias", "publish_to_npm"),
			// The proto-loader pair is a JS sub-feature — gone with the language.
			rule.NewRule("js_proto_loader_bundle", fmt.Sprintf("%s_proto_loader_bundle", bundleName)),
			rule.NewRule("py_binary", fmt.Sprintf("publish_%s_proto_loader_to_npm", bundleName)))
	}

	// With zero languages enabled, generateBundleRules emits no
	// build_validation at all, so a pre-existing `all` rule would survive and
	// dangle on its just-deleted bundle targets. Empty-delete it explicitly.
	// (With at least one language enabled the generated build_validation
	// merges over the old one — `targets` is mergeable — so the danger only
	// exists here.)
	if !config.JavaConfig.Enabled && !config.PythonConfig.Enabled && !config.JavaScriptConfig.Enabled {
		empty = append(empty, rule.NewRule("build_validation", "all"))
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
