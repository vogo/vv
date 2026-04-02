package configs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"gopkg.in/yaml.v3"
)

func TestLoadProjectInstructions_FileExists(t *testing.T) {
	dir := t.TempDir()
	content := "# My Project\n\nUse Go 1.22. Run tests with `make test`.\n"

	if err := os.WriteFile(filepath.Join(dir, ProjectInstructionsFileName), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	got := LoadProjectInstructions(dir)
	if got != content {
		t.Errorf("LoadProjectInstructions() = %q, want %q", got, content)
	}
}

func TestLoadProjectInstructions_FileNotExists(t *testing.T) {
	dir := t.TempDir()

	got := LoadProjectInstructions(dir)
	if got != "" {
		t.Errorf("LoadProjectInstructions() = %q, want empty string", got)
	}
}

func TestLoadProjectInstructions_EmptyFile(t *testing.T) {
	dir := t.TempDir()

	if err := os.WriteFile(filepath.Join(dir, ProjectInstructionsFileName), []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	got := LoadProjectInstructions(dir)
	if got != "" {
		t.Errorf("LoadProjectInstructions() = %q, want empty string", got)
	}
}

func TestLoadProjectInstructions_YAMLExclusion(t *testing.T) {
	cfg := Config{
		ProjectInstructions: "should not appear in YAML",
		LLM: LLMConfig{
			Provider: "openai",
			Model:    "gpt-4o",
		},
	}

	data, err := yaml.Marshal(&cfg)
	if err != nil {
		t.Fatal(err)
	}

	yamlStr := string(data)
	if strings.Contains(yamlStr, "should not appear in YAML") {
		t.Error("ProjectInstructions should not be serialized to YAML")
	}

	if strings.Contains(yamlStr, "projectinstructions") {
		t.Error("ProjectInstructions field name should not appear in YAML output")
	}

	// Verify round-trip: unmarshaling does not set ProjectInstructions.
	var cfg2 Config
	if err := yaml.Unmarshal(data, &cfg2); err != nil {
		t.Fatal(err)
	}

	if cfg2.ProjectInstructions != "" {
		t.Errorf("ProjectInstructions after unmarshal = %q, want empty", cfg2.ProjectInstructions)
	}
}
