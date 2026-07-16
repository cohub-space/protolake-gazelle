package integration

import (
	"io/fs"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGazelleIntegration runs Gazelle on a 3-bundle workspace and verifies
// BUILD file generation. The workspace has:
//   - user-service bundle (java+python+js enabled, protos in api/v1/ subdirectory)
//   - common-types bundle (java+python enabled, js DISABLED, protos in types/v1/
//     subdirectory; BUILD seeded with stale old-form JS rules that must be deleted)
//   - legacy-service bundle (all languages enabled; BUILD seeded pre-PL-bstm —
//     baked version attrs / --version flags — and must be migrated in place)
//   - Cross-bundle import: user.proto imports common.proto
func TestGazelleIntegration(t *testing.T) {
	testDir := t.TempDir()

	setupWorkspace(t, testDir)
	runGazelle(t, testDir)

	userContent := readBuildFile(t, filepath.Join(testDir, "com", "testcompany", "user"))
	commonContent := readBuildFile(t, filepath.Join(testDir, "com", "testcompany", "common"))
	legacyContent := readBuildFile(t, filepath.Join(testDir, "com", "testcompany", "legacy"))

	t.Run("UserBundle", func(t *testing.T) { verifyUserBundle(t, userContent) })
	t.Run("CommonBundle", func(t *testing.T) { verifyCommonBundle(t, commonContent) })
	t.Run("LegacyMigration", func(t *testing.T) { verifyLegacyMigration(t, legacyContent) })
	t.Run("SubdirectoryTargets", func(t *testing.T) {
		verifySubdirectoryTargets(t, userContent, commonContent)
	})

	// Fixed-point idempotency: a second gazelle pass over the same tree must
	// change NOTHING — every BUILD file byte-identical, the legacy MIGRATION
	// fixture included. Migrating an OLD-form BUILD used to be a two-pass
	// convergence (the seeded `_all_protos` block appeared to relocate on
	// pass 2): the discovery regex was re-matching the bundle's own generated
	// `_all_protos` aggregate and wiring it into its own deps — a
	// self-referential proto_library. Since discoverExistingProtoTargets skips
	// the bundle's own aggregate, every bundle — fresh, cleaned, or migrated —
	// reaches its fixed point on the first pass; this pins that.
	pass1 := captureBuildFiles(t, testDir)
	runGazelle(t, testDir)
	pass2 := captureBuildFiles(t, testDir)
	t.Run("SecondPassIdempotency", func(t *testing.T) {
		requireBuildFilesIdentical(t, pass1, pass2)
	})
}

// TestGazelleFailsWithoutLakeYaml: a bundle.yaml with no lake.yaml anywhere up
// the tree is a misconfig, not a valid empty state — with no lake defaults,
// every defaults-reliant language merges disabled and the disabled-language
// cleanup would silently strip every bundle rule (bazel then "succeeds" while
// publishing nothing). The extension must fail fast instead.
func TestGazelleFailsWithoutLakeYaml(t *testing.T) {
	testDir := t.TempDir()

	writeFile(t, testDir, "MODULE.bazel", `module(name = "test_workspace", version = "0.0.1")
`)
	writeFile(t, testDir, "BUILD.bazel", "")

	bundleDir := filepath.Join(testDir, "com", "testcompany", "orphan")
	if err := os.MkdirAll(bundleDir, 0755); err != nil {
		t.Fatalf("Failed to create bundle dir: %v", err)
	}
	writeFile(t, bundleDir, "bundle.yaml", `name: "orphan-service"
display_name: "Orphan Service"
version: "1.0.0"
`)
	writeFile(t, bundleDir, "BUILD.bazel", "")

	runGazelleExpectFatal(t, testDir, "no lake.yaml found")
}

// TestGazelleFailsOnEnabledLanguageWithoutCoordinates: python is enabled via
// lake defaults but no package_name is provided anywhere. Generation needs the
// coordinates while cleanup keys on Enabled alone, so this bundle would get
// neither — stale rules would survive to break the bazel loading phase. The
// extension must fail fast naming the missing field.
func TestGazelleFailsOnEnabledLanguageWithoutCoordinates(t *testing.T) {
	testDir := t.TempDir()

	writeFile(t, testDir, "MODULE.bazel", `module(name = "test_workspace", version = "0.0.1")
`)
	writeFile(t, testDir, "BUILD.bazel", "")
	writeFile(t, testDir, "lake.yaml", `config:
  language_defaults:
    java:
      enabled: false
    python:
      enabled: true
    javascript:
      enabled: false
`)

	bundleDir := filepath.Join(testDir, "com", "testcompany", "half")
	if err := os.MkdirAll(bundleDir, 0755); err != nil {
		t.Fatalf("Failed to create bundle dir: %v", err)
	}
	writeFile(t, bundleDir, "bundle.yaml", `name: "half-configured"
display_name: "Half Configured"
version: "1.0.0"
`)
	writeFile(t, bundleDir, "half.proto", `syntax = "proto3";

package com.testcompany.half;

message Half {
  string id = 1;
}
`)
	// A proto_library must exist: GenerateRules returns early when a bundle has
	// no proto targets, and the coordinates guard sits in generateBundleRules.
	writeFile(t, bundleDir, "BUILD.bazel", `load("@rules_proto//proto:defs.bzl", "proto_library")

proto_library(
    name = "half_proto",
    srcs = ["half.proto"],
    visibility = ["//visibility:public"],
)
`)

	runGazelleExpectFatal(t, testDir, "leaves package_name empty")
}

// readBuildFile reads a BUILD.bazel or BUILD file from the given directory.
func readBuildFile(t *testing.T, dir string) string {
	t.Helper()
	for _, name := range []string{"BUILD.bazel", "BUILD"} {
		path := filepath.Join(dir, name)
		content, err := os.ReadFile(path)
		if err == nil && len(content) > 0 {
			t.Logf("Read %s (%d bytes)", path, len(content))
			return string(content)
		}
	}
	t.Fatalf("No BUILD file with content found in %s", dir)
	return ""
}

// setupWorkspace creates a 2-bundle workspace in a temp directory.
func setupWorkspace(t *testing.T, root string) {
	t.Helper()

	// MODULE.bazel
	writeFile(t, root, "MODULE.bazel", `module(name = "test_workspace", version = "0.0.1")

bazel_dep(name = "bazel_skylib", version = "1.9.0")
bazel_dep(name = "gazelle", version = "0.47.0", repo_name = "bazel_gazelle")
bazel_dep(name = "rules_go", version = "0.60.0", repo_name = "io_bazel_rules_go")
bazel_dep(name = "protobuf", version = "31.1", repo_name = "com_google_protobuf")
bazel_dep(name = "rules_proto", version = "7.1.0")
bazel_dep(name = "rules_proto_grpc", version = "5.8.0")
bazel_dep(name = "rules_proto_grpc_java", version = "5.8.0")
bazel_dep(name = "rules_proto_grpc_python", version = "5.8.0")
`)

	// Root BUILD.bazel
	writeFile(t, root, "BUILD.bazel", `load("@bazel_gazelle//:def.bzl", "gazelle")

gazelle(name = "gazelle")
`)

	// lake.yaml — java, python, javascript all enabled at lake level
	writeFile(t, root, "lake.yaml", `config:
  language_defaults:
    java:
      enabled: true
      group_id: "com.testcompany.proto"
      source_version: "11"
      target_version: "8"
    python:
      enabled: true
      package_name: "testcompany_proto"
      python_version: ">=3.8"
    javascript:
      enabled: true
      package_name: "@testcompany/proto"
`)

	// --- tools/ ---
	toolsDir := filepath.Join(root, "tools")
	os.MkdirAll(toolsDir, 0755)

	writeFile(t, toolsDir, "proto_bundle.bzl", `def java_proto_bundle(name, proto_deps=[], java_deps=[], java_grpc_deps=[], group_id="", artifact_id="", **kwargs):
    native.filegroup(name = name, srcs = java_deps + java_grpc_deps + proto_deps, visibility = kwargs.get("visibility", []))

def py_proto_bundle(name, proto_deps=[], py_deps=[], py_grpc_deps=[], package_name="", **kwargs):
    native.filegroup(name = name, srcs = py_deps + py_grpc_deps + proto_deps, visibility = kwargs.get("visibility", []))

def js_proto_bundle(name, proto_deps=[], es_deps=[], package_name="", **kwargs):
    native.filegroup(name = name, srcs = es_deps + proto_deps, visibility = kwargs.get("visibility", []))

def build_validation(name, targets=[], **kwargs):
    native.genrule(name = name, outs = [name + ".validation"], cmd = "echo 'Build validation passed' > $@", **kwargs)

def proto_descriptor_set(name, deps=[], **kwargs):
    native.filegroup(name = name, srcs = deps, visibility = kwargs.get("visibility", []))
`)

	writeFile(t, toolsDir, "es_proto.bzl", `def es_proto_compile(**kwargs):
    native.filegroup(name = kwargs.get("name"), srcs = kwargs.get("protos", []), visibility = kwargs.get("visibility", []))
`)

	writeFile(t, toolsDir, "BUILD.bazel", `exports_files(["proto_bundle.bzl"])

genrule(name = "maven_publisher", outs = ["maven_publisher.sh"], cmd = "echo '#!/bin/bash' > $@ && chmod +x $@", executable = True)
genrule(name = "pypi_publisher", outs = ["pypi_publisher.sh"], cmd = "echo '#!/bin/bash' > $@ && chmod +x $@", executable = True)
genrule(name = "npm_publisher", outs = ["npm_publisher.sh"], cmd = "echo '#!/bin/bash' > $@ && chmod +x $@", executable = True)
`)

	// ---------- user-service bundle ----------
	userDir := filepath.Join(root, "com", "testcompany", "user")
	userApiDir := filepath.Join(userDir, "api", "v1")
	os.MkdirAll(userApiDir, 0755)

	writeFile(t, userDir, "bundle.yaml", `name: "user-service"
display_name: "User Service"
description: "User service proto definitions"
bundle_prefix: "com.testcompany"
version: "1.0.0"
config:
  generate_descriptor_set: true
  languages:
    java:
      enabled: true
      group_id: "com.testcompany.proto"
      artifact_id: "user-service-proto"
    python:
      enabled: true
      package_name: "testcompany_user_proto"
    javascript:
      enabled: true
      package_name: "@testcompany/user-service-proto"
`)

	// Empty BUILD at bundle root (protos live in subdirectory)
	writeFile(t, userDir, "BUILD.bazel", "")

	// api/v1/user.proto — imports common.proto (cross-bundle dependency)
	// plus google/api/annotations.proto and google/longrunning/operations.proto
	// (external dependencies — exercises the multi-language external-deps wiring).
	writeFile(t, userApiDir, "user.proto", `syntax = "proto3";

package com.testcompany.user.api.v1;

import "com/testcompany/common/types/v1/common.proto";
import "google/api/annotations.proto";
import "google/api/field_behavior.proto";
import "google/longrunning/operations.proto";

service UserService {
  rpc GetUser(GetUserRequest) returns (GetUserResponse);
  rpc CreateUser(CreateUserRequest) returns (CreateUserResponse);
  rpc CreateUserAsync(CreateUserRequest) returns (google.longrunning.Operation);
}

message GetUserRequest {
  string user_id = 1;
}

message GetUserResponse {
  string user_id = 1;
  string name = 2;
  com.testcompany.common.types.v1.Status status = 3;
}

message CreateUserRequest {
  string name = 1;
  string email = 2;
}

message CreateUserResponse {
  string user_id = 1;
  com.testcompany.common.types.v1.Status status = 2;
}
`)

	writeFile(t, userApiDir, "BUILD.bazel", `load("@rules_proto//proto:defs.bzl", "proto_library")

proto_library(
    name = "user_proto",
    srcs = ["user.proto"],
    deps = [
        "//com/testcompany/common/types/v1:common_proto",
        "@googleapis//google/api:annotations_proto",
        "@googleapis//google/api:field_behavior_proto",
        "@googleapis//google/longrunning:operations_proto",
    ],
    visibility = ["//visibility:public"],
)
`)

	// ---------- common-types bundle ----------
	commonDir := filepath.Join(root, "com", "testcompany", "common")
	commonTypesDir := filepath.Join(commonDir, "types", "v1")
	os.MkdirAll(commonTypesDir, 0755)

	// javascript explicitly disabled at bundle level
	writeFile(t, commonDir, "bundle.yaml", `name: "common-types"
display_name: "Common Types"
description: "Common shared types"
bundle_prefix: "com.testcompany"
version: "2.3.0"
config:
  languages:
    java:
      enabled: true
      group_id: "com.testcompany.proto"
      artifact_id: "common-types-proto"
    python:
      enabled: true
      package_name: "testcompany_common_proto"
    javascript:
      enabled: false
`)

	// BUILD at bundle root seeded with STALE old-form JS rules — JS is disabled
	// for this bundle, so the disabled-language cleanup must delete every one of
	// them. The js_proto_bundle carries the old baked `version` attr: left in
	// place it would fail the bazel loading phase under the post-PL-bstm
	// proto_bundle.bzl; the publish py_binary, publish_to_npm alias, and the
	// build_validation js target would dangle on the deleted rules.
	writeFile(t, commonDir, "BUILD.bazel", `load("@rules_python//python:defs.bzl", "py_binary")
load("//tools:es_proto.bzl", "es_proto_compile")
load("//tools:proto_bundle.bzl", "build_validation", "js_proto_bundle")

es_proto_compile(
    name = "common-types_es_proto",
    protos = ["//com/testcompany/common/types/v1:common_proto"],
    visibility = ["//visibility:public"],
)

js_proto_bundle(
    name = "common-types_js_bundle",
    es_deps = [":common-types_es_proto"],
    package_name = "@testcompany/common-types-proto",
    proto_deps = [":common-types_all_protos"],
    version = "9.9.9",
    visibility = ["//visibility:public"],
)

py_binary(
    name = "publish_common-types_to_npm",
    srcs = ["//tools:publish/npm_publisher_generated.py"],
    args = [
        "$(location :common-types_js_bundle)",
        "--package-name=@testcompany/common-types-proto",
        "--version=9.9.9",
    ],
    data = [":common-types_js_bundle"],
    main = "publish/npm_publisher_generated.py",
    visibility = ["//visibility:public"],
)

alias(
    name = "publish_to_npm",
    actual = ":publish_common-types_to_npm",
    visibility = ["//visibility:public"],
)

build_validation(
    name = "all",
    targets = [
        ":common-types_java_bundle",
        ":common-types_py_bundle",
        ":common-types_js_bundle",
    ],
)
`)

	writeFile(t, commonTypesDir, "common.proto", `syntax = "proto3";

package com.testcompany.common.types.v1;

message Status {
  int32 code = 1;
  string message = 2;
  repeated string details = 3;
}

message Timestamp {
  int64 seconds = 1;
  int32 nanos = 2;
}

message Pagination {
  int32 page = 1;
  int32 size = 2;
  int32 total = 3;
}
`)

	writeFile(t, commonTypesDir, "BUILD.bazel", `load("@rules_proto//proto:defs.bzl", "proto_library")

proto_library(
    name = "common_proto",
    srcs = ["common.proto"],
    visibility = ["//visibility:public"],
)
`)

	// ---------- legacy-service bundle (pre-PL-bstm MIGRATION fixture) ----------
	// The seeded BUILD is shaped exactly like a pre-PL-bstm gazelle emission:
	// bundle rules carry baked `version` attrs and no bundle_yaml, the pom
	// genrule bakes `--version` and has no srcs, the py_binary publishers bake
	// `--version=` args, and maven_publish carries stale 9.9.9 coordinates.
	// One gazelle run must migrate all of it in place.
	legacyDir := filepath.Join(root, "com", "testcompany", "legacy")
	legacyApiDir := filepath.Join(legacyDir, "api", "v1")
	os.MkdirAll(legacyApiDir, 0755)

	writeFile(t, legacyDir, "bundle.yaml", `name: "legacy-service"
display_name: "Legacy Service"
description: "Pre-PL-bstm bundle migrated in place"
bundle_prefix: "com.testcompany"
version: "3.1.4"
config:
  languages:
    java:
      enabled: true
      group_id: "com.testcompany.proto"
      artifact_id: "legacy-service-proto"
    python:
      enabled: true
      package_name: "testcompany_legacy_proto"
    javascript:
      enabled: true
      package_name: "@testcompany/legacy-service-proto"
`)

	writeFile(t, legacyDir, "BUILD.bazel", `load("@rules_proto//proto:defs.bzl", "proto_library")
load("@rules_proto_grpc_java//:defs.bzl", "java_grpc_library")
load("@rules_proto_grpc_python//:defs.bzl", "python_grpc_library")
load("@rules_jvm_external//private/rules:maven_publish.bzl", "maven_publish")
load("@rules_python//python:defs.bzl", "py_binary")
load("//tools:es_proto.bzl", "es_proto_compile")
load("//tools:proto_bundle.bzl", "build_validation", "java_proto_bundle", "js_proto_bundle", "py_proto_bundle")

proto_library(
    name = "legacy-service_all_protos",
    visibility = ["//visibility:public"],
    deps = ["//com/testcompany/legacy/api/v1:legacy_proto"],
)

java_grpc_library(
    name = "legacy-service_java_grpc",
    protos = ["//com/testcompany/legacy/api/v1:legacy_proto"],
    visibility = ["//visibility:public"],
)

java_proto_bundle(
    name = "legacy-service_java_bundle",
    artifact_id = "legacy-service-proto",
    group_id = "com.testcompany.proto",
    java_deps = [":legacy-service_java_grpc"],
    java_grpc_deps = [":legacy-service_java_grpc"],
    proto_deps = [":legacy-service_all_protos"],
    version = "9.9.9",
    visibility = ["//visibility:public"],
)

genrule(
    name = "legacy-service_pom",
    outs = ["legacy-service.pom.xml"],
    cmd = "$(location //tools:pom_generator) --group-id com.testcompany.proto --artifact-id legacy-service-proto --version 9.9.9 --protobuf-version $${PROTOBUF_JAVA_VERSION:-4.33.5} --grpc-version $${GRPC_VERSION:-1.78.0} --out $@",
    tools = ["//tools:pom_generator"],
    visibility = ["//visibility:public"],
)

maven_publish(
    name = "publish_legacy-service_to_maven",
    artifact = ":legacy-service_java_bundle",
    coordinates = "com.testcompany.proto:legacy-service-proto:9.9.9",
    pom = ":legacy-service_pom",
    visibility = ["//visibility:public"],
)

alias(
    name = "publish_to_maven",
    actual = ":publish_legacy-service_to_maven",
    visibility = ["//visibility:public"],
)

python_grpc_library(
    name = "legacy-service_python_grpc",
    protos = ["//com/testcompany/legacy/api/v1:legacy_proto"],
    visibility = ["//visibility:public"],
)

py_proto_bundle(
    name = "legacy-service_py_bundle",
    package_name = "testcompany_legacy_proto",
    proto_deps = [":legacy-service_all_protos"],
    py_grpc_deps = [":legacy-service_python_grpc"],
    version = "9.9.9",
    visibility = ["//visibility:public"],
)

py_binary(
    name = "publish_legacy-service_to_pypi",
    srcs = ["//tools:publish/pypi_publisher_generated.py"],
    args = [
        "$(location :legacy-service_py_bundle)",
        "--package-name=testcompany_legacy_proto",
        "--version=9.9.9",
    ],
    data = [":legacy-service_py_bundle"],
    main = "publish/pypi_publisher_generated.py",
    visibility = ["//visibility:public"],
    deps = ["//tools:publisher_utils"],
)

alias(
    name = "publish_to_pypi",
    actual = ":publish_legacy-service_to_pypi",
    visibility = ["//visibility:public"],
)

es_proto_compile(
    name = "legacy-service_es_proto",
    protos = ["//com/testcompany/legacy/api/v1:legacy_proto"],
    visibility = ["//visibility:public"],
)

js_proto_bundle(
    name = "legacy-service_js_bundle",
    es_deps = [":legacy-service_es_proto"],
    package_name = "@testcompany/legacy-service-proto",
    proto_deps = [":legacy-service_all_protos"],
    version = "9.9.9",
    visibility = ["//visibility:public"],
)

py_binary(
    name = "publish_legacy-service_to_npm",
    srcs = ["//tools:publish/npm_publisher_generated.py"],
    args = [
        "$(location :legacy-service_js_bundle)",
        "--package-name=@testcompany/legacy-service-proto",
        "--version=9.9.9",
    ],
    data = [":legacy-service_js_bundle"],
    main = "publish/npm_publisher_generated.py",
    visibility = ["//visibility:public"],
)

alias(
    name = "publish_to_npm",
    actual = ":publish_legacy-service_to_npm",
    visibility = ["//visibility:public"],
)

build_validation(
    name = "all",
    targets = [
        ":legacy-service_java_bundle",
        ":legacy-service_py_bundle",
        ":legacy-service_js_bundle",
    ],
)
`)

	writeFile(t, legacyApiDir, "legacy.proto", `syntax = "proto3";

package com.testcompany.legacy.api.v1;

message LegacyRecord {
  string id = 1;
  string payload = 2;
}

service LegacyService {
  rpc GetRecord(LegacyRecord) returns (LegacyRecord);
}
`)

	writeFile(t, legacyApiDir, "BUILD.bazel", `load("@rules_proto//proto:defs.bzl", "proto_library")

proto_library(
    name = "legacy_proto",
    srcs = ["legacy.proto"],
    visibility = ["//visibility:public"],
)
`)
}

// writeFile creates parent directories if needed and writes content to dir/name.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write %s/%s: %v", dir, name, err)
	}
}

// runGazelleCmd executes the gazelle binary on the test workspace and returns
// its combined output and exit error, letting callers assert success or an
// expected fail-fast.
func runGazelleCmd(t *testing.T, testDir string) (string, error) {
	t.Helper()
	gazelleBinary := findGazelleBinary(t)

	if err := os.Chmod(gazelleBinary, 0755); err != nil {
		t.Logf("Warning: Could not set execute permissions: %v", err)
	}

	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current directory: %v", err)
	}
	defer os.Chdir(oldWd)

	if err := os.Chdir(testDir); err != nil {
		t.Fatalf("Failed to change to test directory: %v", err)
	}

	cmd := exec.Command(gazelleBinary, "-lang=protolake")
	cmd.Env = append(os.Environ(),
		"MAVEN_REPO=file://~/.m2/repository",
		"PYPI_REPO=file://~/.pypi",
		"NPM_REGISTRY=file://~/.npm",
	)

	output, err := cmd.CombinedOutput()
	t.Logf("Gazelle output:\n%s", string(output))
	return string(output), err
}

// runGazelle executes the gazelle binary on the test workspace, failing the
// test if gazelle fails.
func runGazelle(t *testing.T, testDir string) {
	t.Helper()
	if output, err := runGazelleCmd(t, testDir); err != nil {
		t.Fatalf("Gazelle execution failed: %v\nOutput: %s", err, output)
	}
}

// runGazelleExpectFatal runs gazelle expecting the protolake extension to
// fail-fast (log.Fatalf exits non-zero) with wantMsg in its output.
func runGazelleExpectFatal(t *testing.T, testDir, wantMsg string) {
	t.Helper()
	output, err := runGazelleCmd(t, testDir)
	if err == nil {
		t.Fatalf("Gazelle succeeded but a fatal misconfig error containing %q was expected", wantMsg)
	}
	if !strings.Contains(output, wantMsg) {
		t.Errorf("Gazelle failed as expected but output does not contain %q", wantMsg)
	}
}

// findGazelleBinary locates the gazelle binary in the runfiles.
func findGazelleBinary(t *testing.T) string {
	t.Helper()

	// Look inside the gazelle_with_protolake_ directory
	gazelleDir := "gazelle_with_protolake_"
	if info, err := os.Stat(gazelleDir); err == nil && info.IsDir() {
		possibleBinaries := []string{
			filepath.Join(gazelleDir, "gazelle_with_protolake"),
			filepath.Join(gazelleDir, "gazelle_with_protolake_"),
			filepath.Join(gazelleDir, "gazelle_with_protolake.exe"),
			filepath.Join(gazelleDir, "gazelle_with_protolake_.exe"),
		}

		for _, binaryPath := range possibleBinaries {
			if info, err := os.Stat(binaryPath); err == nil && info.Mode().IsRegular() {
				absPath, err := filepath.Abs(binaryPath)
				if err == nil {
					return absPath
				}
			}
		}
	}

	// Fallback: try original approach in case structure is different
	possibleNames := []string{
		"gazelle_with_protolake_",
		"gazelle_with_protolake",
		"gazelle_with_protolake_.exe",
		"gazelle_with_protolake.exe",
	}

	for _, name := range possibleNames {
		if info, err := os.Stat(name); err == nil && info.Mode().IsRegular() {
			absPath, err := filepath.Abs(name)
			if err == nil {
				return absPath
			}
		}
	}

	t.Fatalf("Could not find gazelle binary in runfiles")
	return ""
}

// ---------------------------------------------------------------------------
// Per-bundle verification subtests
// ---------------------------------------------------------------------------

// verifyUserBundle checks the user-service bundle BUILD file.
// All 3 languages are enabled; cross-bundle dependency on common.
func verifyUserBundle(t *testing.T, content string) {
	// Bundle rules
	requireContains(t, content, "java_proto_bundle", "java_proto_bundle rule")
	requireContains(t, content, `group_id = "com.testcompany.proto"`, "Java group_id")
	requireContains(t, content, `artifact_id = "user-service-proto"`, "Java artifact_id")
	requireContains(t, content, "py_proto_bundle", "py_proto_bundle rule")
	requireContains(t, content, `package_name = "testcompany_user_proto"`, "Python package_name")
	requireContains(t, content, "js_proto_bundle", "js_proto_bundle rule")
	requireContains(t, content, `package_name = "@testcompany/user-service-proto"`, "JS package_name")

	// gRPC / codegen rules
	requireContains(t, content, "java_grpc_library", "java_grpc_library")
	requireContains(t, content, "python_grpc_library", "python_grpc_library")
	requireContains(t, content, "es_proto_compile", "es_proto_compile")

	// Aggregated proto rule
	requireContains(t, content, "_all_protos", "aggregated proto rule")

	// Publish rules. The maven_publish coordinates keep the gazelle-baked
	// version literal (the one intentional analysis-time literal — see
	// generateJavaBundleRules); everything else resolves the version from
	// bundle.yaml at build/run time.
	requireContains(t, content, "maven_publish", "maven_publish rule")
	requireContains(t, content, `coordinates = "com.testcompany.proto:user-service-proto:1.0.0"`,
		"Maven coordinates baked at gazelle time")
	requireContains(t, content, "publish_user-service_to_maven", "maven publish target")
	requireContains(t, content, "publish_user-service_to_pypi", "pypi publish target")
	requireContains(t, content, "publish_user-service_to_npm", "npm publish target")
	// Local-publish twin: -local qualifier keeps local installs off the
	// release coordinates.
	requireContains(t, content, "publish_user-service_to_maven_local", "maven local publish target")
	requireContains(t, content, `coordinates = "com.testcompany.proto:user-service-proto:1.0.0-local"`,
		"local Maven coordinates carry the -local qualifier")
	requireAbsent(t, content, "${VERSION:", "no runtime VERSION env-var dance after gazelle bake")

	// Version-from-bundle.yaml wiring (PL-bstm): the pom genrules read
	// bundle.yaml at build time, the local twin appends the -local qualifier,
	// and the py_binary publishers get bundle.yaml via data + --bundle-yaml.
	requireContains(t, content, `--bundle-yaml $(location bundle.yaml)`,
		"pom genrule cmd resolves version from bundle.yaml at build time")
	requireContains(t, content, `srcs = ["bundle.yaml"]`, "pom genrule srcs carry bundle.yaml")
	requireContains(t, content, `--version-suffix=-local`,
		"local pom twin appends the -local qualifier to the bundle.yaml version")
	// Stale-BUILD guard: both pom genrules bake the RAW bundle.yaml version
	// (pre-suffix, also on the -local twin) as --expected-version so a
	// bundle.yaml edit without a gazelle pass fails the pom build instead of
	// publishing an artifact whose GAV disagrees with its POM.
	requireContains(t, content, `--expected-version 1.0.0 `,
		"pom genrules bake the expected version as a stale-BUILD guard")
	requireContains(t, content, `--expected-version 1.0.0 --version-suffix=-local`,
		"local pom twin checks the RAW (pre-suffix) version")
	requireAbsent(t, content, `--expected-version 1.0.0-local`,
		"the -local suffix must never leak into --expected-version")
	requireContains(t, content, `--bundle-yaml=$(location bundle.yaml)`,
		"py_binary publishers resolve version from bundle.yaml at run time")
	requireAbsent(t, content, `--version=`, "no version literal in py_binary args")

	// build_validation with all 3 language bundle targets
	requireContains(t, content, "build_validation", "build_validation rule")
	requireContains(t, content, "user-service_java_bundle", "java target in build_validation")
	requireContains(t, content, "user-service_py_bundle", "python target in build_validation")
	requireContains(t, content, "user-service_js_bundle", "js target in build_validation")

	// java_proto_bundle, py_proto_bundle, js_proto_bundle each carry a
	// `bundle_yaml` label instead of a baked `version` attr — the bundlers
	// (jar_bundler MANIFEST.MF, wheel_builder PKG-INFO, npm_bundler
	// package.json) read the version from bundle.yaml at build time.
	requireContains(t, content, `bundle_yaml = ":bundle.yaml"`, "bundle_yaml attr on bundle rules")
	requireAbsent(t, content, `version = "`, "no baked version attr on bundle rules")

	// Cross-bundle dependency: user.proto imports common.proto, so the
	// generated gRPC rules should reference the common types target.
	requireContains(t, content, "//com/testcompany/common/types/v1:", "cross-bundle proto target reference")

	// Descriptor set is enabled for user-service: expect proto_descriptor_set rule
	// and the java_proto_bundle wired to receive it via descriptor_pb/bundle_name.
	requireContains(t, content, "proto_descriptor_set(", "proto_descriptor_set rule")
	requireContains(t, content, `name = "user-service_descriptor"`, "descriptor target name")
	requireContains(t, content, `descriptor_pb = ":user-service_descriptor"`, "java bundle wires descriptor_pb")
	requireContains(t, content, `bundle_name = "user-service"`, "java bundle sets bundle_name")

	// External deps: user.proto imports google/api/annotations.proto and
	// google/longrunning/operations.proto, so:
	//   - java_grpc_library.deps must include the Java umbrella libraries
	//   - python_grpc_library.protos must include the raw proto_library targets
	//   - es_proto_compile.protos must include the raw proto_library targets
	requireContains(t, content, `"@googleapis//google/api:api_java_proto"`,
		"java_grpc_library deps include googleapis java umbrella")
	requireContains(t, content, `"@googleapis//google/api:annotations_proto"`,
		"python_grpc_library + es_proto_compile protos include annotations_proto")
	requireContains(t, content, `"@googleapis//google/api:field_behavior_proto"`,
		"protos include field_behavior_proto")
	requireContains(t, content, `"@googleapis//google/api:http_proto"`,
		"protos include http_proto (transitive companion)")
	requireContains(t, content, `"@googleapis//google/api:launch_stage_proto"`,
		"protos include launch_stage_proto (pulled in by client.proto)")
	requireContains(t, content, `"@googleapis//google/longrunning:longrunning_java_proto"`,
		"java_grpc_library deps include longrunning java umbrella")
	requireContains(t, content, `"@googleapis//google/longrunning:operations_proto"`,
		"python_grpc_library + es_proto_compile protos include longrunning operations_proto")
}

// verifyCommonBundle checks the common-types bundle BUILD file.
// Java and Python enabled; JavaScript explicitly disabled. The seeded BUILD
// carried stale old-form JS rules (js_proto_bundle with a baked version attr,
// es_proto_compile, publish py_binary, publish_to_npm alias, js target in
// build_validation) — the disabled-language cleanup must delete all of them.
func verifyCommonBundle(t *testing.T, content string) {
	// Enabled languages
	requireContains(t, content, "java_proto_bundle", "java_proto_bundle rule")
	requireContains(t, content, `group_id = "com.testcompany.proto"`, "Java group_id")
	requireContains(t, content, `artifact_id = "common-types-proto"`, "Java artifact_id")
	requireContains(t, content, "py_proto_bundle", "py_proto_bundle rule")
	requireContains(t, content, `package_name = "testcompany_common_proto"`, "Python package_name")

	// JavaScript must NOT be present (explicitly disabled in bundle.yaml).
	// These are real deletions, not just non-generation: the seeded BUILD
	// carried each of these rules in stale old form.
	requireAbsent(t, content, "js_proto_bundle", "js_proto_bundle (JS disabled, stale rule deleted)")
	requireAbsent(t, content, "es_proto_compile", "es_proto_compile (JS disabled, stale rule deleted)")
	requireAbsent(t, content, "publish_common-types_to_npm", "npm publish (JS disabled, stale rule deleted)")
	requireAbsent(t, content, "publish_to_npm", "npm publish alias (JS disabled, stale alias deleted)")
	requireAbsent(t, content, "9.9.9", "stale seeded version literal fully gone")

	// Publish rules — maven coordinates keep the gazelle-baked literal (2.3.0);
	// everything else resolves the version from bundle.yaml at build/run time.
	requireContains(t, content, "maven_publish", "maven_publish rule")
	requireContains(t, content, `coordinates = "com.testcompany.proto:common-types-proto:2.3.0"`,
		"Maven coordinates baked from bundle.yaml")
	requireContains(t, content, "publish_common-types_to_maven", "maven publish target")
	requireContains(t, content, "publish_common-types_to_pypi", "pypi publish target")
	requireContains(t, content, `coordinates = "com.testcompany.proto:common-types-proto:2.3.0-local"`,
		"local Maven coordinates carry the -local qualifier")

	// Version-from-bundle.yaml wiring (PL-bstm).
	requireContains(t, content, `bundle_yaml = ":bundle.yaml"`, "bundle_yaml attr on bundle rules")
	requireContains(t, content, `--bundle-yaml $(location bundle.yaml)`,
		"pom genrule cmd resolves version from bundle.yaml at build time")
	requireContains(t, content, `srcs = ["bundle.yaml"]`, "pom genrule srcs carry bundle.yaml")
	requireContains(t, content, `--expected-version 2.3.0 `,
		"pom genrules bake the expected version as a stale-BUILD guard")
	requireContains(t, content, `--bundle-yaml=$(location bundle.yaml)`,
		"py_binary publishers resolve version from bundle.yaml at run time")
	requireAbsent(t, content, `version = "`, "no baked version attr on bundle rules")

	// Regression check for the version-stuck-at-1.0.0 bug: common-types is 2.3.0
	// and must never appear with the 1.0.0 fallback.
	requireAbsent(t, content, `--version=`, "no version literal in py_binary args")
	requireAbsent(t, content, `:common-types-proto:1.0.0`, "no 1.0.0 maven coord for common-types")

	// build_validation with only java and python
	requireContains(t, content, "build_validation", "build_validation rule")
	requireContains(t, content, "common-types_java_bundle", "java target in build_validation")
	requireContains(t, content, "common-types_py_bundle", "python target in build_validation")
	requireAbsent(t, content, "common-types_js_bundle", "js target in build_validation (JS disabled)")

	// Descriptor set NOT enabled for common-types: rule must be absent and
	// java_proto_bundle must not carry the descriptor attributes.
	requireAbsent(t, content, "proto_descriptor_set(", "proto_descriptor_set rule (not requested)")
	requireAbsent(t, content, "descriptor_pb =", "descriptor_pb attribute (not requested)")
	requireAbsent(t, content, "bundle_name =", "bundle_name attribute (not requested)")

	// common-types doesn't import google/api, so no external deps should appear.
	requireAbsent(t, content, "@googleapis//", "googleapis deps (not imported)")
	requireAbsent(t, content, "api_java_proto", "Java umbrella dep (not imported)")
}

// verifyLegacyMigration checks the legacy-service bundle BUILD file. It was
// seeded pre-PL-bstm shaped (baked version attrs, --version flags, no
// bundle_yaml/srcs, stale 9.9.9 maven coordinates) and one gazelle run must
// migrate it in place to the bundle.yaml-sourced shape (bundle.yaml says 3.1.4).
func verifyLegacyMigration(t *testing.T, content string) {
	// Bundle rules: baked version attrs deleted, bundle_yaml labels added.
	requireAbsent(t, content, `version = "`, "baked version attrs deleted from bundle rules")
	requireContains(t, content, `bundle_yaml = ":bundle.yaml"`, "bundle_yaml attr added to bundle rules")

	// pom genrule: cmd rewritten from baked --version to --bundle-yaml +
	// --expected-version, and the bundle.yaml src merged in.
	requireAbsent(t, content, `--version 9.9.9`, "baked --version gone from pom cmd")
	requireContains(t, content, `--bundle-yaml $(location bundle.yaml)`,
		"pom cmd resolves version from bundle.yaml at build time")
	requireContains(t, content, `--expected-version 3.1.4 `,
		"pom cmd bakes the current bundle.yaml version as the stale-BUILD guard")
	requireContains(t, content, `srcs = ["bundle.yaml"]`, "bundle.yaml src merged into pom genrule")

	// The -local pom twin is generated fresh alongside, checking the RAW
	// (pre-suffix) version.
	requireContains(t, content, `--expected-version 3.1.4 --version-suffix=-local`,
		"local pom twin checks the RAW (pre-suffix) version")
	requireContains(t, content, "publish_legacy-service_to_maven_local", "maven local publish twin generated")

	// py_binary publishers: baked --version= args replaced by --bundle-yaml=.
	requireAbsent(t, content, `--version=`, "baked --version= gone from publisher args")
	requireContains(t, content, `--bundle-yaml=$(location bundle.yaml)`,
		"publishers resolve version from bundle.yaml at run time")

	// maven_publish coordinates refreshed from bundle.yaml (release + -local twin).
	requireContains(t, content, `coordinates = "com.testcompany.proto:legacy-service-proto:3.1.4"`,
		"maven coordinates refreshed to the bundle.yaml version")
	requireContains(t, content, `coordinates = "com.testcompany.proto:legacy-service-proto:3.1.4-local"`,
		"local maven coordinates refreshed with the -local qualifier")

	// Nothing anywhere in the migrated file may still reference the stale version.
	requireAbsent(t, content, "9.9.9", "stale version literal fully gone")

	// The seeded `_all_protos` aggregate must not be re-discovered as a proto
	// target of its own bundle: that wired the aggregate into its own deps (a
	// self-referential proto_library) and into the grpc/es rules' protos.
	requireAbsent(t, content, "//com/testcompany/legacy:legacy-service_all_protos",
		"bundle's own aggregate self-referenced as a proto target")
}

// verifySubdirectoryTargets checks that proto files in subdirectories
// (api/v1/, types/v1/) are discovered and included in bundle targets.
func verifySubdirectoryTargets(t *testing.T, userContent, commonContent string) {
	// User bundle should reference the api/v1 subdirectory proto target
	requireContains(t, userContent, "com/testcompany/user/api/v1", "user api/v1 subdirectory proto target")

	// Common bundle should reference the types/v1 subdirectory proto target
	requireContains(t, commonContent, "com/testcompany/common/types/v1", "common types/v1 subdirectory proto target")
}

// ---------------------------------------------------------------------------
// Test helpers
// ---------------------------------------------------------------------------

const buildBazelName = "BUILD.bazel"

// captureBuildFiles walks root and returns every BUILD.bazel's contents,
// keyed by slash-separated path relative to root.
func captureBuildFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := make(map[string]string)
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if d.IsDir() || d.Name() != buildBazelName {
			return nil
		}
		rel, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		content, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		files[filepath.ToSlash(rel)] = string(content)
		return nil
	})
	if err != nil {
		t.Fatalf("Failed to capture BUILD files under %s: %v", root, err)
	}
	return files
}

// requireBuildFilesIdentical asserts that before and after hold the same
// BUILD file set with byte-identical contents.
func requireBuildFilesIdentical(t *testing.T, before, after map[string]string) {
	t.Helper()
	for rel, prev := range before {
		cur, ok := after[rel]
		if !ok {
			t.Errorf("BUILD file disappeared between passes: %s", rel)
			continue
		}
		if cur != prev {
			t.Errorf("BUILD file not byte-identical between passes: %s\n--- earlier pass ---\n%s\n--- later pass ---\n%s", rel, prev, cur)
		}
	}
	for rel := range after {
		if _, ok := before[rel]; !ok {
			t.Errorf("BUILD file appeared between passes: %s", rel)
		}
	}
}

func requireContains(t *testing.T, content, pattern, description string) {
	t.Helper()
	if !strings.Contains(content, pattern) {
		t.Errorf("Missing required content: %s (pattern: %q)", description, pattern)
	}
}

func requireAbsent(t *testing.T, content, pattern, description string) {
	t.Helper()
	if strings.Contains(content, pattern) {
		t.Errorf("Found unexpected content: %s (pattern: %q)", description, pattern)
	}
}
