package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestGazelleIntegration runs Gazelle on a 2-bundle workspace and verifies
// BUILD file generation. The workspace has:
//   - user-service bundle (java+python+js enabled, protos in api/v1/ subdirectory)
//   - common-types bundle (java+python enabled, js DISABLED, protos in types/v1/ subdirectory)
//   - Cross-bundle import: user.proto imports common.proto
func TestGazelleIntegration(t *testing.T) {
	testDir := t.TempDir()

	setupWorkspace(t, testDir)
	runGazelle(t, testDir)

	userContent := readBuildFile(t, filepath.Join(testDir, "com", "testcompany", "user"))
	commonContent := readBuildFile(t, filepath.Join(testDir, "com", "testcompany", "common"))

	t.Run("UserBundle", func(t *testing.T) { verifyUserBundle(t, userContent) })
	t.Run("CommonBundle", func(t *testing.T) { verifyCommonBundle(t, commonContent) })
	t.Run("SubdirectoryTargets", func(t *testing.T) {
		verifySubdirectoryTargets(t, userContent, commonContent)
	})
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

	// Empty BUILD at bundle root
	writeFile(t, commonDir, "BUILD.bazel", "")

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
}

// writeFile creates parent directories if needed and writes content to dir/name.
func writeFile(t *testing.T, dir, name, content string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0644); err != nil {
		t.Fatalf("Failed to write %s/%s: %v", dir, name, err)
	}
}

// runGazelle executes the gazelle binary on the test workspace.
func runGazelle(t *testing.T, testDir string) {
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
		"VERSION=1.0.0-test",
		"MAVEN_REPO=file://~/.m2/repository",
		"PYPI_REPO=file://~/.pypi",
		"NPM_REGISTRY=file://~/.npm",
	)

	output, err := cmd.CombinedOutput()
	t.Logf("Gazelle output:\n%s", string(output))
	if err != nil {
		t.Fatalf("Gazelle execution failed: %v\nOutput: %s", err, string(output))
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

	// Hybrid approach: environment variables in publish genrules
	// Version default must come from bundle.yaml (1.0.0), not be hardcoded
	requireContains(t, content, "${VERSION:-1.0.0}", "VERSION default from bundle.yaml (1.0.0)")
	requireContains(t, content, "${MAVEN_REPO:-", "MAVEN_REPO env var in publish cmd")
	requireContains(t, content, "publish_", "publish genrule")

	// build_validation with all 3 language bundle targets
	requireContains(t, content, "build_validation", "build_validation rule")
	requireContains(t, content, "user-service_java_bundle", "java target in build_validation")
	requireContains(t, content, "user-service_py_bundle", "python target in build_validation")
	requireContains(t, content, "user-service_js_bundle", "js target in build_validation")

	// No hardcoded version (hybrid approach)
	if strings.Contains(content, `version = "1.0.0"`) {
		t.Error("Found hardcoded version in bundle rule - violates hybrid approach")
	}

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
// Java and Python enabled; JavaScript explicitly disabled.
func verifyCommonBundle(t *testing.T, content string) {
	// Enabled languages
	requireContains(t, content, "java_proto_bundle", "java_proto_bundle rule")
	requireContains(t, content, `group_id = "com.testcompany.proto"`, "Java group_id")
	requireContains(t, content, `artifact_id = "common-types-proto"`, "Java artifact_id")
	requireContains(t, content, "py_proto_bundle", "py_proto_bundle rule")
	requireContains(t, content, `package_name = "testcompany_common_proto"`, "Python package_name")

	// JavaScript must NOT be generated (explicitly disabled in bundle.yaml)
	requireAbsent(t, content, "js_proto_bundle", "js_proto_bundle (JS disabled)")
	requireAbsent(t, content, "es_proto_compile", "es_proto_compile (JS disabled)")
	requireAbsent(t, content, "publish_common-types_to_npm", "npm publish (JS disabled)")

	// Version default must come from bundle.yaml (2.3.0), not hardcoded 1.0.0
	requireContains(t, content, "${VERSION:-2.3.0}", "VERSION default from bundle.yaml (2.3.0)")
	requireAbsent(t, content, "${VERSION:-1.0.0}", "VERSION must not use 1.0.0 default (common-types is 2.3.0)")

	// Publishing rules for maven and pypi only
	requireContains(t, content, "publish_common-types_to_maven", "maven publish rule")
	requireContains(t, content, "publish_common-types_to_pypi", "pypi publish rule")

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
