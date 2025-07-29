package language

import (
	yaml "gopkg.in/yaml.v3"
	"log"
	"os"
	"path/filepath"
)

// LakeConfig represents the lake.yaml configuration structure
// Based on the LakeConfig message in lake.proto
type LakeConfig struct {
	Config struct {
		LanguageDefaults struct {
			Java struct {
				Enabled       bool   `yaml:"enabled"`
				GroupId       string `yaml:"group_id"`
				SourceVersion string `yaml:"source_version"`
				TargetVersion string `yaml:"target_version"`
			} `yaml:"java"`
			Python struct {
				Enabled     bool   `yaml:"enabled"`
				PackageName string `yaml:"package_name"`
				Version     string `yaml:"python_version"`
			} `yaml:"python"`
			Javascript struct {
				Enabled     bool   `yaml:"enabled"`
				PackageName string `yaml:"package_name"`
			} `yaml:"javascript"`
		} `yaml:"language_defaults"`
		BuildDefaults struct {
			BaseVersion string `yaml:"base_version"`
		} `yaml:"build_defaults"`
	} `yaml:"config"`
}

// BundleConfig represents the bundle.yaml configuration structure
// Based on the new protolake format
type BundleConfig struct {
	// Bundle metadata fields
	Name         string `yaml:"name"`
	DisplayName  string `yaml:"display_name"`
	Description  string `yaml:"description"`
	BundlePrefix string `yaml:"bundle_prefix"`
	Version      string `yaml:"version"`

	// Config section with language-specific settings
	Config struct {
		Languages struct {
			Java struct {
				Enabled    *bool  `yaml:"enabled"` // Use pointer to distinguish between unset and false
				GroupId    string `yaml:"group_id"`
				ArtifactId string `yaml:"artifact_id"`
			} `yaml:"java"`
			Python struct {
				Enabled     *bool  `yaml:"enabled"` // Use pointer to distinguish between unset and false
				PackageName string `yaml:"package_name"`
			} `yaml:"python"`
			Javascript struct {
				Enabled     *bool  `yaml:"enabled"` // Use pointer to distinguish between unset and false
				PackageName string `yaml:"package_name"`
			} `yaml:"javascript"`
		} `yaml:"languages"`
	} `yaml:"config"`
}

// LoadLakeConfig loads lake.yaml configuration from the given directory
// Walks up the directory tree to find lake.yaml
func LoadLakeConfig(startDir string) (*LakeConfig, error) {
	log.Printf("[protolake-gazelle] LoadLakeConfig starting from: %s", startDir)
	dir := startDir
	for {
		lakeFile := filepath.Join(dir, "lake.yaml")
		log.Printf("[protolake-gazelle] Checking for lake.yaml at: %s", lakeFile)
		if _, err := os.Stat(lakeFile); err == nil {
			log.Printf("[protolake-gazelle] Found lake.yaml at: %s", lakeFile)
			data, err := os.ReadFile(lakeFile)
			if err != nil {
				return nil, err
			}

			var config LakeConfig
			if err := yaml.Unmarshal(data, &config); err != nil {
				return nil, err
			}

			return &config, nil
		}

		parent := filepath.Dir(dir)
		if parent == dir {
			// Reached root directory, no lake.yaml found
			break
		}
		dir = parent
	}

	return nil, nil // No lake.yaml found
}

// LoadBundleConfig loads bundle.yaml configuration from the given directory
func LoadBundleConfig(dir string) (*BundleConfig, error) {
	bundleFile := filepath.Join(dir, "bundle.yaml")

	// Check if bundle.yaml exists
	if _, err := os.Stat(bundleFile); os.IsNotExist(err) {
		return nil, nil // No bundle.yaml, not an error
	}

	// Read the file
	data, err := os.ReadFile(bundleFile)
	if err != nil {
		return nil, err
	}

	// Parse YAML
	var config BundleConfig
	if err := yaml.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	// Validate required fields
	if config.Name == "" {
		return nil, nil // Invalid bundle config - no name found
	}

	return &config, nil
}

// MergeConfigurations merges lake defaults with bundle-specific configurations
// Bundle config takes precedence over lake defaults, including explicit disabling
func MergeConfigurations(lakeConfig *LakeConfig, bundleConfig *BundleConfig) *MergedConfig {
	merged := &MergedConfig{
		BundleName:       bundleConfig.Name,
		BundleOwner:      "", // Not used in new format
		ProtoPackage:     "", // Not used in new format
		Description:      bundleConfig.Description,
		Version:          bundleConfig.Version,
		JavaConfig:       JavaConfig{},
		PythonConfig:     PythonConfig{},
		JavaScriptConfig: JavaScriptConfig{},
	}

	// Start with lake defaults
	if lakeConfig != nil {
		merged.JavaConfig = JavaConfig{
			Enabled:    lakeConfig.Config.LanguageDefaults.Java.Enabled,
			GroupId:    lakeConfig.Config.LanguageDefaults.Java.GroupId,
			ArtifactId: "", // Will be set from bundle
		}
		merged.PythonConfig = PythonConfig{
			Enabled:     lakeConfig.Config.LanguageDefaults.Python.Enabled,
			PackageName: lakeConfig.Config.LanguageDefaults.Python.PackageName,
		}
		merged.JavaScriptConfig = JavaScriptConfig{
			Enabled:     lakeConfig.Config.LanguageDefaults.Javascript.Enabled,
			PackageName: lakeConfig.Config.LanguageDefaults.Javascript.PackageName,
		}

		// Log lake defaults for debugging
		log.Printf("Lake defaults - Java enabled: %v, GroupId: %s",
			lakeConfig.Config.LanguageDefaults.Java.Enabled,
			lakeConfig.Config.LanguageDefaults.Java.GroupId)
	} else {
		log.Printf("Warning: No lake configuration found")
	}

	// Override with bundle-specific config - now properly handles explicit enabling/disabling
	// Java configuration
	if bundleConfig.Config.Languages.Java.Enabled != nil {
		// Explicitly set in bundle config (either true or false)
		merged.JavaConfig.Enabled = *bundleConfig.Config.Languages.Java.Enabled
	}
	if bundleConfig.Config.Languages.Java.GroupId != "" {
		merged.JavaConfig.GroupId = bundleConfig.Config.Languages.Java.GroupId
	}
	if bundleConfig.Config.Languages.Java.ArtifactId != "" {
		merged.JavaConfig.ArtifactId = bundleConfig.Config.Languages.Java.ArtifactId
	}

	// Python configuration
	if bundleConfig.Config.Languages.Python.Enabled != nil {
		// Explicitly set in bundle config (either true or false)
		merged.PythonConfig.Enabled = *bundleConfig.Config.Languages.Python.Enabled
	}
	if bundleConfig.Config.Languages.Python.PackageName != "" {
		merged.PythonConfig.PackageName = bundleConfig.Config.Languages.Python.PackageName
	}

	// JavaScript configuration
	if bundleConfig.Config.Languages.Javascript.Enabled != nil {
		// Explicitly set in bundle config (either true or false)
		merged.JavaScriptConfig.Enabled = *bundleConfig.Config.Languages.Javascript.Enabled
	}
	if bundleConfig.Config.Languages.Javascript.PackageName != "" {
		merged.JavaScriptConfig.PackageName = bundleConfig.Config.Languages.Javascript.PackageName
	}

	// Log final merged configuration for debugging
	log.Printf("Merged config for bundle %s - Java enabled: %v, GroupId: %s, ArtifactId: %s",
		merged.BundleName, merged.JavaConfig.Enabled, merged.JavaConfig.GroupId, merged.JavaConfig.ArtifactId)
	log.Printf("Merged config for bundle %s - Python enabled: %v, PackageName: %s",
		merged.BundleName, merged.PythonConfig.Enabled, merged.PythonConfig.PackageName)
	log.Printf("Merged config for bundle %s - JavaScript enabled: %v, PackageName: %s",
		merged.BundleName, merged.JavaScriptConfig.Enabled, merged.JavaScriptConfig.PackageName)

	return merged
}

// MergedConfig represents the final configuration after merging lake and bundle configs
type MergedConfig struct {
	BundleName       string
	BundleOwner      string
	ProtoPackage     string
	Description      string
	Version          string
	JavaConfig       JavaConfig
	PythonConfig     PythonConfig
	JavaScriptConfig JavaScriptConfig
}

type JavaConfig struct {
	Enabled    bool
	GroupId    string
	ArtifactId string
}

type PythonConfig struct {
	Enabled     bool
	PackageName string
}

type JavaScriptConfig struct {
	Enabled     bool
	PackageName string
}
