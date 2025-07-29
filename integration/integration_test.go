package integration

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	yaml "gopkg.in/yaml.v3"
)

// TestGazelleIntegration runs actual Gazelle and verifies BUILD file generation
func TestGazelleIntegration(t *testing.T) {
	t.Log("🚀 Starting Gazelle integration test...")

	// Use a temporary directory for testing
	testDir := t.TempDir()
	t.Logf("Test workspace created at: %s", testDir)

	// Set up the workspace
	setupWorkspace(t, testDir)

	// Run Gazelle
	runGazelle(t, testDir)

	// Verify generated BUILD files
	verifyBuildFiles(t, testDir)

	t.Log("🎉 Integration test completed successfully!")
}

// setupWorkspace creates a complete workspace with MODULE.bazel, BUILD.bazel, and proto files
func setupWorkspace(t *testing.T, testDir string) {
	// Create MODULE.bazel for the test workspace
	moduleContent := `module(name = "test_workspace", version = "0.0.1")

bazel_dep(name = "bazel_skylib", version = "1.7.1")
bazel_dep(name = "gazelle", version = "0.44.0", repo_name = "bazel_gazelle")
bazel_dep(name = "rules_go", version = "0.51.0", repo_name = "io_bazel_rules_go")
bazel_dep(name = "protobuf", version = "31.1", repo_name = "com_google_protobuf")
bazel_dep(name = "rules_proto", version = "7.1.0")
bazel_dep(name = "rules_proto_grpc", version = "5.3.1")
bazel_dep(name = "rules_proto_grpc_java", version = "5.3.1")
bazel_dep(name = "rules_proto_grpc_python", version = "5.3.1")
bazel_dep(name = "rules_proto_grpc_js", version = "5.3.1")
`

	err := os.WriteFile(filepath.Join(testDir, "MODULE.bazel"), []byte(moduleContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create MODULE.bazel: %v", err)
	}

	// Create BUILD.bazel for the workspace root
	buildContent := `load("@bazel_gazelle//:def.bzl", "gazelle")

gazelle(name = "gazelle")
`

	err = os.WriteFile(filepath.Join(testDir, "BUILD.bazel"), []byte(buildContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create BUILD.bazel: %v", err)
	}

	// Create tools directory with proto_bundle.bzl
	toolsDir := filepath.Join(testDir, "tools")
	err = os.MkdirAll(toolsDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create tools directory: %v", err)
	}

	// Create proto_bundle.bzl with hybrid approach support
	protoBundleContent := `# Proto bundle rules with hybrid publishing approach
def java_proto_bundle(name, proto_deps=[], java_deps=[], java_grpc_deps=[], group_id="", artifact_id="", **kwargs):
    native.filegroup(
        name = name,
        srcs = java_deps + java_grpc_deps + proto_deps,
        visibility = kwargs.get("visibility", []),
    )

def py_proto_bundle(name, proto_deps=[], py_deps=[], py_grpc_deps=[], package_name="", **kwargs):
    native.filegroup(
        name = name,
        srcs = py_deps + py_grpc_deps + proto_deps,
        visibility = kwargs.get("visibility", []),
    )

def js_proto_bundle(name, proto_deps=[], js_deps=[], js_grpc_web_deps=[], package_name="", **kwargs):
    native.filegroup(
        name = name,
        srcs = js_deps + js_grpc_web_deps + proto_deps,
        visibility = kwargs.get("visibility", []),
    )

def build_validation(name, targets=[], **kwargs):
    native.genrule(
        name = name,
        outs = [name + ".validation"],
        cmd = "echo 'Build validation passed for: %s' > $@" % " ".join(targets),
        **kwargs
    )
`

	err = os.WriteFile(filepath.Join(toolsDir, "proto_bundle.bzl"), []byte(protoBundleContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create proto_bundle.bzl: %v", err)
	}

	// Create tools BUILD.bazel
	toolsBuildContent := `exports_files(["proto_bundle.bzl"])

# Stub publishers for testing
genrule(
    name = "maven_publisher",
    outs = ["maven_publisher.sh"],
    cmd = "echo '#!/bin/bash\\necho Maven publisher: $$@' > $@ && chmod +x $@",
    executable = True,
)

genrule(
    name = "pypi_publisher", 
    outs = ["pypi_publisher.sh"],
    cmd = "echo '#!/bin/bash\\necho PyPI publisher: $$@' > $@ && chmod +x $@",
    executable = True,
)

genrule(
    name = "npm_publisher",
    outs = ["npm_publisher.sh"], 
    cmd = "echo '#!/bin/bash\\necho NPM publisher: $$@' > $@ && chmod +x $@",
    executable = True,
)
`

	err = os.WriteFile(filepath.Join(toolsDir, "BUILD.bazel"), []byte(toolsBuildContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create tools/BUILD.bazel: %v", err)
	}

	// Create lake.yaml
	lakeContent := `config:
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
  build_defaults:
    base_version: "1.0.0"
`

	err = os.WriteFile(filepath.Join(testDir, "lake.yaml"), []byte(lakeContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create lake.yaml: %v", err)
	}

	// Create service directory and bundle.yaml
	serviceDir := filepath.Join(testDir, "com", "testcompany", "user")
	err = os.MkdirAll(serviceDir, 0755)
	if err != nil {
		t.Fatalf("Failed to create service directory: %v", err)
	}

	bundleContent := `bundle:
  name: "user-service"
  owner: "platform-team"
  proto_package: "com.testcompany.user"
  description: "User service proto definitions"
  version: "1.0.0"
config:
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
`

	err = os.WriteFile(filepath.Join(serviceDir, "bundle.yaml"), []byte(bundleContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create bundle.yaml: %v", err)
	}

	// Create proto files in the service directory (same level as bundle.yaml)
	userProto := `syntax = "proto3";

package com.testcompany.user.api.v1;

service UserService {
    rpc GetUser(GetUserRequest) returns (GetUserResponse);
}

message GetUserRequest {
    string user_id = 1;
}

message GetUserResponse {
    string user_id = 1;
    string name = 2;
    string email = 3;
}
`

	err = os.WriteFile(filepath.Join(serviceDir, "user.proto"), []byte(userProto), 0644)
	if err != nil {
		t.Fatalf("Failed to create user.proto: %v", err)
	}

	// Create types proto file
	typesProto := `syntax = "proto3";

package com.testcompany.user.types.v1;

message User {
    string id = 1;
    string name = 2;
    string email = 3;
    int64 created_at = 4;
}

message UserPreferences {
    string language = 1;
    string timezone = 2;
    bool notifications_enabled = 3;
}
`

	err = os.WriteFile(filepath.Join(serviceDir, "types.proto"), []byte(typesProto), 0644)
	if err != nil {
		t.Fatalf("Failed to create types.proto: %v", err)
	}

	// Create BUILD.bazel file with proto_library rules in the service directory
	protoBuildContent := `load("@rules_proto//proto:defs.bzl", "proto_library")

proto_library(
    name = "user_proto",
    srcs = ["user.proto"],
    visibility = ["//visibility:public"],
)

proto_library(
    name = "types_proto",
    srcs = ["types.proto"],
    visibility = ["//visibility:public"],
)
`

	err = os.WriteFile(filepath.Join(serviceDir, "BUILD.bazel"), []byte(protoBuildContent), 0644)
	if err != nil {
		t.Fatalf("Failed to create service BUILD.bazel: %v", err)
	}

	t.Log("✅ Test workspace setup completed")
}

// runGazelle executes the gazelle binary on the test workspace
func runGazelle(t *testing.T, testDir string) {
	t.Log("🛠️ Setting up to run Gazelle...")

	// Find the gazelle binary from the test data
	gazelleBinary := findGazelleBinary(t)
	t.Logf("Using gazelle binary: %s", gazelleBinary)

	// Ensure binary is executable
	if err := os.Chmod(gazelleBinary, 0755); err != nil {
		t.Logf("Warning: Could not set execute permissions: %v", err)
	} else {
		t.Log("✅ Execute permissions set on gazelle binary")
	}

	// Change to test directory
	oldWd, err := os.Getwd()
	if err != nil {
		t.Fatalf("Failed to get current directory: %v", err)
	}
	defer os.Chdir(oldWd)

	t.Logf("Changing to test directory: %s", testDir)
	err = os.Chdir(testDir)
	if err != nil {
		t.Fatalf("Failed to change to test directory: %v", err)
	}

	// Run gazelle binary with protolake language
	t.Log("🚀 Running gazelle with protolake language...")
	cmd := exec.Command(gazelleBinary, "-lang=protolake")

	// Set environment variables for testing
	cmd.Env = append(os.Environ(),
		"VERSION=1.0.0-test",
		"MAVEN_REPO=file://~/.m2/repository",
		"PYPI_REPO=file://~/.pypi",
		"NPM_REGISTRY=file://~/.npm",
	)

	output, err := cmd.CombinedOutput()
	t.Logf("📋 Gazelle output:\n%s", string(output))

	if err != nil {
		t.Fatalf("❌ Gazelle execution failed: %v\nOutput: %s", err, string(output))
	}

	t.Log("✅ Gazelle execution completed successfully")
}

// findGazelleBinary locates the gazelle binary in the runfiles
func findGazelleBinary(t *testing.T) string {
	// Look inside the gazelle_with_protolake_ directory
	gazelleDir := "gazelle_with_protolake_"
	if info, err := os.Stat(gazelleDir); err == nil && info.IsDir() {
		// Try to find the binary inside the directory
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
		"gazelle_with_protolake_.exe", // Windows
		"gazelle_with_protolake.exe",  // Windows
	}

	for _, name := range possibleNames {
		if info, err := os.Stat(name); err == nil && info.Mode().IsRegular() {
			absPath, err := filepath.Abs(name)
			if err == nil {
				return absPath
			}
		}
	}

	t.Fatalf("❌ Could not find gazelle binary in runfiles")
	return ""
}

// verifyBuildFiles checks that BUILD files were generated with correct hybrid approach content
func verifyBuildFiles(t *testing.T, testDir string) {
	// Check that BUILD file was generated in the service directory
	buildFile := filepath.Join(testDir, "com", "testcompany", "user", "BUILD.bazel")

	if _, err := os.Stat(buildFile); os.IsNotExist(err) {
		t.Log("BUILD.bazel not found - checking for BUILD file...")
		buildFile = filepath.Join(testDir, "com", "testcompany", "user", "BUILD")
		if _, err := os.Stat(buildFile); os.IsNotExist(err) {
			t.Errorf("❌ No BUILD file generated in service directory")
			return
		}
	}

	// Read the generated BUILD file
	content, err := os.ReadFile(buildFile)
	if err != nil {
		t.Fatalf("Failed to read generated BUILD file: %v", err)
	}

	buildContent := string(content)

	// Test hybrid approach implementation
	testHybridApproach(t, buildContent)
}

// testHybridApproach verifies the hybrid approach is implemented correctly
func testHybridApproach(t *testing.T, buildContent string) {
	t.Log("🔍 Verifying hybrid approach implementation...")

	// Check for static configuration in BUILD file
	staticChecks := []struct {
		name     string
		pattern  string
		required bool
	}{
		{"Java group_id", `group_id = "com.testcompany.proto"`, true},
		{"Java artifact_id", `artifact_id = "user-service-proto"`, true},
		{"Python package_name", `package_name = "testcompany_user_proto"`, true},
		{"JavaScript package_name", `package_name = "@testcompany/user-service-proto"`, true},
		{"Java bundle rule", "java_proto_bundle", true},
		{"Python bundle rule", "py_proto_bundle", true},
		{"JavaScript bundle rule", "js_proto_bundle", true},
		{"gRPC library", "grpc_library", true},
		{"Aggregated proto rule", "_all_protos", true},
	}

	staticFound := 0
	for _, check := range staticChecks {
		if strings.Contains(buildContent, check.pattern) {
			staticFound++
			t.Logf("✅ Found static config: %s", check.name)
		} else {
			if check.required {
				t.Errorf("❌ Missing required static config: %s", check.name)
			} else {
				t.Logf("⚠️ Missing optional static config: %s", check.name)
			}
		}
	}

	// Check for dynamic configuration (environment variables)
	dynamicChecks := []struct {
		name     string
		pattern  string
		required bool
	}{
		{"Version environment variable", "${VERSION:-", true},
		{"Maven repo environment variable", "${MAVEN_REPO:-", true},
		{"Publishing rule", "publish_", true},
	}

	dynamicFound := 0
	for _, check := range dynamicChecks {
		if strings.Contains(buildContent, check.pattern) {
			dynamicFound++
			t.Logf("✅ Found dynamic config: %s", check.name)
		} else {
			if check.required {
				t.Errorf("❌ Missing required dynamic config: %s", check.name)
			} else {
				t.Logf("⚠️ Missing optional dynamic config: %s", check.name)
			}
		}
	}

	// Check that version is NOT hardcoded in bundle rules (hybrid approach)
	if strings.Contains(buildContent, `version = "1.0.0"`) {
		t.Error("❌ Found hardcoded version in bundle rule - violates hybrid approach")
	} else {
		t.Log("✅ No hardcoded version in bundle rules - hybrid approach implemented correctly")
	}

	// Summary
	t.Logf("📊 Static configuration checks: %d/%d found", staticFound, len(staticChecks))
	t.Logf("📊 Dynamic configuration checks: %d/%d found", dynamicFound, len(dynamicChecks))

	if staticFound < len(staticChecks) {
		t.Error("❌ Missing some static configuration")
	}

	if dynamicFound < len(dynamicChecks) {
		t.Error("❌ Missing some dynamic configuration")
	}

	if staticFound == len(staticChecks) && dynamicFound == len(dynamicChecks) {
		t.Log("🎯 Hybrid approach verification completed successfully!")
	}
}

// TestConfigurationLoading tests the configuration loading with existing testdata
func TestConfigurationLoading(t *testing.T) {
	// Find testdata directory
	testDataDir := findTestDataDir(t)

	// Test lake configuration loading
	testLakeConfig(t, testDataDir)

	// Test bundle configuration loading
	testBundleConfigs(t, testDataDir)
}

// findTestDataDir finds the testdata directory
func findTestDataDir(t *testing.T) string {
	possiblePaths := []string{
		"testdata/basic_lake",
		"../testdata/basic_lake",
		"protolake-gazelle/testdata/basic_lake",
		"external/protolake_gazelle/testdata/basic_lake",
	}

	for _, path := range possiblePaths {
		if _, err := os.Stat(path); err == nil {
			t.Logf("Found testdata directory at: %s", path)
			return path
		}
	}

	t.Skip("testdata directory not found - skipping configuration test")
	return ""
}

// testLakeConfig validates lake.yaml configuration
func testLakeConfig(t *testing.T, testDataDir string) {
	lakeYamlPath := filepath.Join(testDataDir, "lake.yaml")

	data, err := os.ReadFile(lakeYamlPath)
	if err != nil {
		t.Fatalf("Failed to read lake.yaml: %v", err)
	}

	var config map[string]interface{}
	if err := yaml.Unmarshal(data, &config); err != nil {
		t.Fatalf("Invalid YAML in lake.yaml: %v", err)
	}

	if _, ok := config["config"]; !ok {
		t.Errorf("Missing 'config' section in lake.yaml")
	}

	t.Log("✅ lake.yaml is valid")
}

// testBundleConfigs validates bundle.yaml configuration files
func testBundleConfigs(t *testing.T, testDataDir string) {
	userBundlePath := filepath.Join(testDataDir, "com", "testcompany", "user", "bundle.yaml")

	data, err := os.ReadFile(userBundlePath)
	if err != nil {
		t.Fatalf("Failed to read user bundle.yaml: %v", err)
	}

	var userConfig map[string]interface{}
	if err := yaml.Unmarshal(data, &userConfig); err != nil {
		t.Fatalf("Invalid YAML in user bundle.yaml: %v", err)
	}

	if _, ok := userConfig["bundle"]; !ok {
		t.Errorf("Missing 'bundle' section in user bundle.yaml")
	}

	if _, ok := userConfig["config"]; !ok {
		t.Errorf("Missing 'config' section in user bundle.yaml")
	}

	t.Log("✅ bundle.yaml files are valid")
}
