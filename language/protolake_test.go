package language

import (
	"github.com/bazelbuild/bazel-gazelle/config"
	"github.com/bazelbuild/bazel-gazelle/label"
	"github.com/bazelbuild/bazel-gazelle/rule"
	"os"
	"path/filepath"
	"testing"
)

// Helper functions for creating bool pointers
func boolPtr(b bool) *bool {
	return &b
}

func TestProtolakeExtension(t *testing.T) {
	ext := NewLanguage()

	if ext.Name() != "protolake" {
		t.Errorf("Expected name 'protolake', got '%s'", ext.Name())
	}
}

func TestProtolakeExtensionBasics(t *testing.T) {
	ext := &protolakeExtension{}

	// Test Name method
	if ext.Name() != "protolake" {
		t.Errorf("Expected name 'protolake', got '%s'", ext.Name())
	}

	// Test KnownDirectives method
	directives := ext.KnownDirectives()
	expectedDirectives := []string{"protolake"}

	if len(directives) != len(expectedDirectives) {
		t.Errorf("Expected %d directives, got %d", len(expectedDirectives), len(directives))
	}

	for i, directive := range directives {
		if directive != expectedDirectives[i] {
			t.Errorf("Expected directive '%s', got '%s'", expectedDirectives[i], directive)
		}
	}
}

func TestKindsAndKindInfo(t *testing.T) {
	ext := &protolakeExtension{}

	// Test Kinds method
	kinds := ext.Kinds()
	expectedKinds := []string{"java_proto_bundle", "py_proto_bundle", "js_proto_bundle", "build_validation"}

	for _, expectedKind := range expectedKinds {
		if _, exists := kinds[expectedKind]; !exists {
			t.Errorf("Expected kind '%s' not found", expectedKind)
		}
	}

	// Test KindInfo method
	kindInfo := ext.KindInfo()
	if len(kindInfo) == 0 {
		t.Error("KindInfo should return non-empty map")
	}

	// Verify java_proto_bundle has required attributes
	if javaInfo, exists := kindInfo["java_proto_bundle"]; exists {
		requiredAttrs := []string{"group_id", "artifact_id", "proto_deps", "java_deps", "java_grpc_deps"}
		for _, attr := range requiredAttrs {
			if !javaInfo.NonEmptyAttrs[attr] {
				t.Errorf("java_proto_bundle should have required attribute '%s'", attr)
			}
		}
	} else {
		t.Error("java_proto_bundle kind info not found")
	}
}

func TestLoads(t *testing.T) {
	ext := &protolakeExtension{}

	// Test Loads method
	loads := ext.Loads()
	expectedLoads := map[string][]string{
		"@rules_proto_grpc_java//:defs.bzl":   {"java_grpc_library"},
		"@rules_proto_grpc_python//:defs.bzl": {"python_grpc_library"},
		"@rules_proto_grpc_js//:defs.bzl":     {"js_grpc_library", "js_grpc_web_library"},
		"//tools:proto_bundle.bzl":            {"build_validation", "java_proto_bundle", "py_proto_bundle", "js_proto_bundle"},
	}

	if len(loads) != len(expectedLoads) {
		t.Errorf("Expected %d load statements, got %d", len(expectedLoads), len(loads))
	}

	for _, load := range loads {
		if expectedSymbols, exists := expectedLoads[load.Name]; exists {
			if len(load.Symbols) != len(expectedSymbols) {
				t.Errorf("Expected %d symbols for %s, got %d", len(expectedSymbols), load.Name, len(load.Symbols))
			}
		} else {
			t.Errorf("Unexpected load statement: %s", load.Name)
		}
	}
}

func TestRegisterFlags(t *testing.T) {
	ext := &protolakeExtension{}
	c := &config.Config{
		Exts: make(map[string]interface{}),
	}

	// Test RegisterFlags
	ext.RegisterFlags(nil, "", c)

	// Check that protolake config was registered
	if _, exists := c.Exts["protolake"]; !exists {
		t.Error("protolake extension should be registered in config")
	}

	// Check that it's enabled by default
	pc := getProtolakeConfig(c)
	if !pc.enabled {
		t.Error("protolake extension should be enabled by default")
	}
}

func TestConfigure(t *testing.T) {
	ext := &protolakeExtension{}
	c := &config.Config{
		Exts: make(map[string]interface{}),
	}
	c.Exts["protolake"] = &protolakeConfig{enabled: true}

	// Test Configure with nil file (should not crash)
	ext.Configure(c, "", nil)

	// Test Configure with file containing directive
	f := &rule.File{
		Directives: []rule.Directive{
			{Key: "protolake", Value: "false"},
		},
	}

	ext.Configure(c, "", f)

	// Check that the directive was processed
	pc := getProtolakeConfig(c)
	if pc.enabled {
		t.Error("protolake extension should be disabled after directive")
	}

	// Test enabling it back
	f.Directives[0].Value = "true"
	ext.Configure(c, "", f)

	pc = getProtolakeConfig(c)
	if !pc.enabled {
		t.Error("protolake extension should be enabled after directive")
	}
}

func TestGetProtolakeConfig(t *testing.T) {
	// Test with empty config
	c := &config.Config{
		Exts: make(map[string]interface{}),
	}

	pc := getProtolakeConfig(c)
	if pc == nil {
		t.Error("getProtolakeConfig should return non-nil config")
	}

	if !pc.enabled {
		t.Error("default protolake config should be enabled")
	}

	// Test with existing config
	existingConfig := &protolakeConfig{enabled: false}
	c.Exts["protolake"] = existingConfig

	pc = getProtolakeConfig(c)
	if pc != existingConfig {
		t.Error("getProtolakeConfig should return existing config")
	}

	if pc.enabled {
		t.Error("existing config should maintain its state")
	}
}

// File I/O Tests for LoadLakeConfig
func TestLoadLakeConfig(t *testing.T) {
	// Test case 1: Valid lake.yaml file
	t.Run("ValidLakeYaml", func(t *testing.T) {
		tmpDir := t.TempDir()

		lakeYaml := `
config:
  language_defaults:
    java:
      enabled: true
      group_id: "com.example.proto"
      source_version: "11"
      target_version: "8"
    python:
      enabled: true
      package_name: "example_proto"
      python_version: ">=3.8"
    javascript:
      enabled: true
      package_name: "@example/proto"
  build_defaults:
    base_version: "1.0.0"
`

		err := os.WriteFile(filepath.Join(tmpDir, "lake.yaml"), []byte(lakeYaml), 0644)
		if err != nil {
			t.Fatalf("Failed to create lake.yaml: %v", err)
		}

		config, err := LoadLakeConfig(tmpDir)
		if err != nil {
			t.Fatalf("Failed to load lake config: %v", err)
		}

		if config == nil {
			t.Fatal("Lake config should not be nil")
		}

		// Verify parsed values
		if config.Config.LanguageDefaults.Java.GroupId != "com.example.proto" {
			t.Errorf("Expected Java group_id 'com.example.proto', got '%s'",
				config.Config.LanguageDefaults.Java.GroupId)
		}

		if config.Config.LanguageDefaults.Java.SourceVersion != "11" {
			t.Errorf("Expected Java source_version '11', got '%s'",
				config.Config.LanguageDefaults.Java.SourceVersion)
		}

		if config.Config.LanguageDefaults.Python.PackageName != "example_proto" {
			t.Errorf("Expected Python package_name 'example_proto', got '%s'",
				config.Config.LanguageDefaults.Python.PackageName)
		}

		if config.Config.LanguageDefaults.Javascript.PackageName != "@example/proto" {
			t.Errorf("Expected JavaScript package_name '@example/proto', got '%s'",
				config.Config.LanguageDefaults.Javascript.PackageName)
		}

		if config.Config.BuildDefaults.BaseVersion != "1.0.0" {
			t.Errorf("Expected base_version '1.0.0', got '%s'",
				config.Config.BuildDefaults.BaseVersion)
		}
	})

	// Test case 2: No lake.yaml file (should return nil, no error)
	t.Run("NoLakeYaml", func(t *testing.T) {
		tmpDir := t.TempDir()

		config, err := LoadLakeConfig(tmpDir)
		if err != nil {
			t.Errorf("Should not error when lake.yaml doesn't exist: %v", err)
		}

		if config != nil {
			t.Error("Should return nil when lake.yaml doesn't exist")
		}
	})

	// Test case 3: Invalid YAML syntax
	t.Run("InvalidYaml", func(t *testing.T) {
		tmpDir := t.TempDir()

		invalidYaml := `
config:
  language_defaults:
    java:
      enabled: true
      group_id: "com.example.proto"
    python:
      enabled: true
      package_name: invalid_yaml_structure: [
`

		err := os.WriteFile(filepath.Join(tmpDir, "lake.yaml"), []byte(invalidYaml), 0644)
		if err != nil {
			t.Fatalf("Failed to create invalid lake.yaml: %v", err)
		}

		config, err := LoadLakeConfig(tmpDir)
		if err == nil {
			t.Error("Should return error for invalid YAML")
		}

		if config != nil {
			t.Error("Should return nil config for invalid YAML")
		}
	})

	// Test case 4: Unreadable file (permission error)
	t.Run("UnreadableFile", func(t *testing.T) {
		tmpDir := t.TempDir()

		lakeYamlPath := filepath.Join(tmpDir, "lake.yaml")
		err := os.WriteFile(lakeYamlPath, []byte("config: {}"), 0644)
		if err != nil {
			t.Fatalf("Failed to create lake.yaml: %v", err)
		}

		// Make file unreadable
		err = os.Chmod(lakeYamlPath, 0000)
		if err != nil {
			t.Fatalf("Failed to change file permissions: %v", err)
		}

		// Restore permissions at end of test
		defer os.Chmod(lakeYamlPath, 0644)

		config, err := LoadLakeConfig(tmpDir)
		if err == nil {
			t.Error("Should return error for unreadable file")
		}

		if config != nil {
			t.Error("Should return nil config for unreadable file")
		}
	})

	// Test case 5: Directory traversal to find lake.yaml
	t.Run("DirectoryTraversal", func(t *testing.T) {
		tmpDir := t.TempDir()

		// Create nested directory structure
		nestedDir := filepath.Join(tmpDir, "sub", "nested", "dir")
		err := os.MkdirAll(nestedDir, 0755)
		if err != nil {
			t.Fatalf("Failed to create nested directories: %v", err)
		}

		// Put lake.yaml in the root
		lakeYaml := `
config:
  language_defaults:
    java:
      enabled: true
      group_id: "com.traversal.proto"
`

		err = os.WriteFile(filepath.Join(tmpDir, "lake.yaml"), []byte(lakeYaml), 0644)
		if err != nil {
			t.Fatalf("Failed to create lake.yaml: %v", err)
		}

		// Search from nested directory
		config, err := LoadLakeConfig(nestedDir)
		if err != nil {
			t.Fatalf("Failed to load lake config from nested dir: %v", err)
		}

		if config == nil {
			t.Fatal("Should find lake.yaml in parent directory")
		}

		if config.Config.LanguageDefaults.Java.GroupId != "com.traversal.proto" {
			t.Errorf("Expected group_id 'com.traversal.proto', got '%s'",
				config.Config.LanguageDefaults.Java.GroupId)
		}
	})
}

// File I/O Tests for LoadBundleConfig
func TestLoadBundleConfig(t *testing.T) {
	// Test case 1: Valid bundle.yaml file
	t.Run("ValidBundleYaml", func(t *testing.T) {
		tmpDir := t.TempDir()

		bundleYaml := `
bundle:
  name: "user-service"
  owner: "platform-team"
  proto_package: "com.example.user"
  description: "User service proto definitions"
  version: "1.0.0"
config:
  languages:
    java:
      enabled: true
      group_id: "com.example.proto"
      artifact_id: "user-service-proto"
    python:
      enabled: true
      package_name: "example_user_proto"
    javascript:
      enabled: true
      package_name: "@example/user-service-proto"
`

		err := os.WriteFile(filepath.Join(tmpDir, "bundle.yaml"), []byte(bundleYaml), 0644)
		if err != nil {
			t.Fatalf("Failed to create bundle.yaml: %v", err)
		}

		config, err := LoadBundleConfig(tmpDir)
		if err != nil {
			t.Fatalf("Failed to load bundle config: %v", err)
		}

		if config == nil {
			t.Fatal("Bundle config should not be nil")
		}

		// Verify parsed values
		if config.Bundle.Name != "user-service" {
			t.Errorf("Expected bundle name 'user-service', got '%s'", config.Bundle.Name)
		}

		if config.Bundle.Owner != "platform-team" {
			t.Errorf("Expected owner 'platform-team', got '%s'", config.Bundle.Owner)
		}

		if config.Bundle.ProtoPackage != "com.example.user" {
			t.Errorf("Expected proto_package 'com.example.user', got '%s'", config.Bundle.ProtoPackage)
		}

		if config.Bundle.Description != "User service proto definitions" {
			t.Errorf("Expected description 'User service proto definitions', got '%s'", config.Bundle.Description)
		}

		if config.Bundle.Version != "1.0.0" {
			t.Errorf("Expected version '1.0.0', got '%s'", config.Bundle.Version)
		}

		if config.Config.Languages.Java.GroupId != "com.example.proto" {
			t.Errorf("Expected Java group_id 'com.example.proto', got '%s'",
				config.Config.Languages.Java.GroupId)
		}

		if config.Config.Languages.Java.ArtifactId != "user-service-proto" {
			t.Errorf("Expected Java artifact_id 'user-service-proto', got '%s'",
				config.Config.Languages.Java.ArtifactId)
		}

		if config.Config.Languages.Python.PackageName != "example_user_proto" {
			t.Errorf("Expected Python package_name 'example_user_proto', got '%s'",
				config.Config.Languages.Python.PackageName)
		}

		if config.Config.Languages.Javascript.PackageName != "@example/user-service-proto" {
			t.Errorf("Expected JavaScript package_name '@example/user-service-proto', got '%s'",
				config.Config.Languages.Javascript.PackageName)
		}
	})

	// Test case 2: No bundle.yaml file (should return nil, no error)
	t.Run("NoBundleYaml", func(t *testing.T) {
		tmpDir := t.TempDir()

		config, err := LoadBundleConfig(tmpDir)
		if err != nil {
			t.Errorf("Should not error when bundle.yaml doesn't exist: %v", err)
		}

		if config != nil {
			t.Error("Should return nil when bundle.yaml doesn't exist")
		}
	})

	// Test case 3: Invalid YAML syntax
	t.Run("InvalidYaml", func(t *testing.T) {
		tmpDir := t.TempDir()

		invalidYaml := `
bundle:
  name: "test-bundle"
  owner: "test-team"
config:
  languages:
    java:
      enabled: true
      invalid_yaml_structure: [
`

		err := os.WriteFile(filepath.Join(tmpDir, "bundle.yaml"), []byte(invalidYaml), 0644)
		if err != nil {
			t.Fatalf("Failed to create invalid bundle.yaml: %v", err)
		}

		config, err := LoadBundleConfig(tmpDir)
		if err == nil {
			t.Error("Should return error for invalid YAML")
		}

		if config != nil {
			t.Error("Should return nil config for invalid YAML")
		}
	})

	// Test case 4: Empty bundle name (should return nil)
	t.Run("EmptyBundleName", func(t *testing.T) {
		tmpDir := t.TempDir()

		bundleYaml := `
bundle:
  name: ""
  owner: "test-team"
config:
  languages:
    java:
      enabled: true
`

		err := os.WriteFile(filepath.Join(tmpDir, "bundle.yaml"), []byte(bundleYaml), 0644)
		if err != nil {
			t.Fatalf("Failed to create bundle.yaml: %v", err)
		}

		config, err := LoadBundleConfig(tmpDir)
		if err != nil {
			t.Errorf("Should not error for empty bundle name: %v", err)
		}

		if config != nil {
			t.Error("Should return nil for empty bundle name")
		}
	})

	// Test case 5: Missing bundle name (should return nil)
	t.Run("MissingBundleName", func(t *testing.T) {
		tmpDir := t.TempDir()

		bundleYaml := `
bundle:
  owner: "test-team"
config:
  languages:
    java:
      enabled: true
`

		err := os.WriteFile(filepath.Join(tmpDir, "bundle.yaml"), []byte(bundleYaml), 0644)
		if err != nil {
			t.Fatalf("Failed to create bundle.yaml: %v", err)
		}

		config, err := LoadBundleConfig(tmpDir)
		if err != nil {
			t.Errorf("Should not error for missing bundle name: %v", err)
		}

		if config != nil {
			t.Error("Should return nil for missing bundle name")
		}
	})

	// Test case 6: Minimal valid configuration
	t.Run("MinimalValid", func(t *testing.T) {
		tmpDir := t.TempDir()

		bundleYaml := `
bundle:
  name: "minimal-bundle"
  owner: "test-team"
`

		err := os.WriteFile(filepath.Join(tmpDir, "bundle.yaml"), []byte(bundleYaml), 0644)
		if err != nil {
			t.Fatalf("Failed to create bundle.yaml: %v", err)
		}

		config, err := LoadBundleConfig(tmpDir)
		if err != nil {
			t.Fatalf("Failed to load minimal bundle config: %v", err)
		}

		if config == nil {
			t.Fatal("Should load minimal valid configuration")
		}

		if config.Bundle.Name != "minimal-bundle" {
			t.Errorf("Expected bundle name 'minimal-bundle', got '%s'", config.Bundle.Name)
		}

		if config.Bundle.Owner != "test-team" {
			t.Errorf("Expected owner 'test-team', got '%s'", config.Bundle.Owner)
		}
	})

	// Test case 7: Unreadable file (permission error)
	t.Run("UnreadableFile", func(t *testing.T) {
		tmpDir := t.TempDir()

		bundleYamlPath := filepath.Join(tmpDir, "bundle.yaml")
		err := os.WriteFile(bundleYamlPath, []byte(`
bundle:
  name: "test-bundle"
  owner: "test-team"
`), 0644)
		if err != nil {
			t.Fatalf("Failed to create bundle.yaml: %v", err)
		}

		// Make file unreadable
		err = os.Chmod(bundleYamlPath, 0000)
		if err != nil {
			t.Fatalf("Failed to change file permissions: %v", err)
		}

		// Restore permissions at end of test
		defer os.Chmod(bundleYamlPath, 0644)

		config, err := LoadBundleConfig(tmpDir)
		if err == nil {
			t.Error("Should return error for unreadable file")
		}

		if config != nil {
			t.Error("Should return nil config for unreadable file")
		}
	})
}

func TestMergeConfigurations(t *testing.T) {
	// Test with nil lake config
	bundleConfig := &BundleConfig{}
	bundleConfig.Bundle.Name = "test-bundle"
	bundleConfig.Bundle.Owner = "test-team"
	bundleConfig.Config.Languages.Java.Enabled = boolPtr(true)
	bundleConfig.Config.Languages.Java.GroupId = "com.test.proto"
	bundleConfig.Config.Languages.Java.ArtifactId = "test-bundle-proto"

	merged := MergeConfigurations(nil, bundleConfig)

	if merged.BundleName != "test-bundle" {
		t.Errorf("Expected bundle name 'test-bundle', got '%s'", merged.BundleName)
	}

	if merged.JavaConfig.GroupId != "com.test.proto" {
		t.Errorf("Expected Java group_id 'com.test.proto', got '%s'", merged.JavaConfig.GroupId)
	}

	// Test with lake config providing defaults
	lakeConfig := &LakeConfig{}
	lakeConfig.Config.LanguageDefaults.Java.Enabled = true
	lakeConfig.Config.LanguageDefaults.Java.GroupId = "com.default.proto"
	lakeConfig.Config.LanguageDefaults.Python.Enabled = true
	lakeConfig.Config.LanguageDefaults.Python.PackageName = "default_proto"

	bundleConfig2 := &BundleConfig{}
	bundleConfig2.Bundle.Name = "test-bundle-2"
	bundleConfig2.Bundle.Owner = "test-team-2"
	bundleConfig2.Config.Languages.Java.Enabled = boolPtr(true)
	bundleConfig2.Config.Languages.Java.GroupId = "com.override.proto" // Override
	bundleConfig2.Config.Languages.Java.ArtifactId = "test-bundle-2-proto"
	bundleConfig2.Config.Languages.Python.Enabled = boolPtr(true)
	bundleConfig2.Config.Languages.Python.PackageName = "override_proto" // Override

	merged2 := MergeConfigurations(lakeConfig, bundleConfig2)

	// Java config should be overridden by bundle
	if merged2.JavaConfig.GroupId != "com.override.proto" {
		t.Errorf("Expected Java group_id 'com.override.proto', got '%s'", merged2.JavaConfig.GroupId)
	}

	if merged2.JavaConfig.ArtifactId != "test-bundle-2-proto" {
		t.Errorf("Expected Java artifact_id 'test-bundle-2-proto', got '%s'", merged2.JavaConfig.ArtifactId)
	}

	// Python config should be overridden by bundle
	if merged2.PythonConfig.PackageName != "override_proto" {
		t.Errorf("Expected Python package_name 'override_proto', got '%s'", merged2.PythonConfig.PackageName)
	}
}

func TestBundleConfigStructure(t *testing.T) {
	// Test that we can create the basic structures without file I/O
	bundleConfig := &BundleConfig{}
	bundleConfig.Bundle.Name = "test-bundle"
	bundleConfig.Bundle.Owner = "test-team"
	bundleConfig.Bundle.ProtoPackage = "com.test.bundle"
	bundleConfig.Bundle.Description = "Test bundle"
	bundleConfig.Bundle.Version = "1.0.0"
	bundleConfig.Config.Languages.Java.Enabled = boolPtr(true)
	bundleConfig.Config.Languages.Java.GroupId = "com.test.proto"
	bundleConfig.Config.Languages.Java.ArtifactId = "test-proto"
	bundleConfig.Config.Languages.Python.Enabled = boolPtr(true)
	bundleConfig.Config.Languages.Python.PackageName = "test_proto"
	bundleConfig.Config.Languages.Javascript.Enabled = boolPtr(true)
	bundleConfig.Config.Languages.Javascript.PackageName = "@test/proto"

	if bundleConfig.Bundle.Name != "test-bundle" {
		t.Errorf("Expected bundle name 'test-bundle', got '%s'", bundleConfig.Bundle.Name)
	}

	if bundleConfig.Bundle.ProtoPackage != "com.test.bundle" {
		t.Errorf("Expected proto_package 'com.test.bundle', got '%s'", bundleConfig.Bundle.ProtoPackage)
	}

	if bundleConfig.Config.Languages.Java.GroupId != "com.test.proto" {
		t.Errorf("Expected Java group_id 'com.test.proto', got '%s'", bundleConfig.Config.Languages.Java.GroupId)
	}
}

func TestLakeConfigStructure(t *testing.T) {
	// Test that we can create the basic structures without file I/O
	lakeConfig := &LakeConfig{}
	lakeConfig.Config.LanguageDefaults.Java.Enabled = true
	lakeConfig.Config.LanguageDefaults.Java.GroupId = "com.lake.proto"
	lakeConfig.Config.LanguageDefaults.Java.SourceVersion = "11"
	lakeConfig.Config.LanguageDefaults.Java.TargetVersion = "8"
	lakeConfig.Config.LanguageDefaults.Python.Enabled = true
	lakeConfig.Config.LanguageDefaults.Python.PackageName = "lake_proto"
	lakeConfig.Config.LanguageDefaults.Python.Version = ">=3.8"
	lakeConfig.Config.LanguageDefaults.Javascript.Enabled = true
	lakeConfig.Config.LanguageDefaults.Javascript.PackageName = "@lake/proto"
	lakeConfig.Config.BuildDefaults.BaseVersion = "1.0.0"

	if lakeConfig.Config.LanguageDefaults.Java.GroupId != "com.lake.proto" {
		t.Errorf("Expected Java group_id 'com.lake.proto', got '%s'", lakeConfig.Config.LanguageDefaults.Java.GroupId)
	}

	if lakeConfig.Config.LanguageDefaults.Java.SourceVersion != "11" {
		t.Errorf("Expected Java source_version '11', got '%s'", lakeConfig.Config.LanguageDefaults.Java.SourceVersion)
	}

	if lakeConfig.Config.LanguageDefaults.Python.PackageName != "lake_proto" {
		t.Errorf("Expected Python package_name 'lake_proto', got '%s'", lakeConfig.Config.LanguageDefaults.Python.PackageName)
	}
}

// Test empty interface methods to ensure they don't panic
func TestEmptyInterfaceMethods(t *testing.T) {
	ext := &protolakeExtension{}
	c := &config.Config{}
	f := &rule.File{}
	r := &rule.Rule{}

	// These should not panic
	ext.Fix(c, f)

	imports := ext.Imports(c, r, f)
	if imports != nil {
		t.Error("Imports should return nil")
	}

	embeds := ext.Embeds(r, label.Label{})
	if embeds != nil {
		t.Error("Embeds should return nil")
	}

	ext.Resolve(c, nil, nil, r, nil, label.Label{})

	err := ext.CheckFlags(nil, c)
	if err != nil {
		t.Errorf("CheckFlags should return nil, got %v", err)
	}
}
