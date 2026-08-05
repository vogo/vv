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

// TestLoadProjectInstructionsFrom_CandidateOrder locks in the fallback chain
// that was missing: a repository carrying only AGENTS.md (or the CLAUDE.md
// symlink most agent tooling ships) used to load no project instructions at
// all, so its rules never reached any system prompt.
func TestLoadProjectInstructionsFrom_CandidateOrder(t *testing.T) {
	cases := []struct {
		name     string
		files    map[string]string
		wantFile string
		wantBody string
	}{
		{
			name:     "VV.md wins over AGENTS.md",
			files:    map[string]string{"VV.md": "vv rules", "AGENTS.md": "agent rules"},
			wantFile: "VV.md",
			wantBody: "vv rules",
		},
		{
			name:     "AGENTS.md when VV.md absent",
			files:    map[string]string{"AGENTS.md": "agent rules"},
			wantFile: "AGENTS.md",
			wantBody: "agent rules",
		},
		{
			name:     "CLAUDE.md as last resort",
			files:    map[string]string{"CLAUDE.md": "claude rules"},
			wantFile: "CLAUDE.md",
			wantBody: "claude rules",
		},
		{
			name:     "blank candidate does not mask the next",
			files:    map[string]string{"VV.md": "   \n", "AGENTS.md": "agent rules"},
			wantFile: "AGENTS.md",
			wantBody: "agent rules",
		},
		{
			name:     "no candidate present",
			files:    map[string]string{"README.md": "not instructions"},
			wantFile: "",
			wantBody: "",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()

			for name, body := range tc.files {
				if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
					t.Fatal(err)
				}
			}

			body, file := LoadProjectInstructionsFrom(dir, nil)
			if file != tc.wantFile {
				t.Errorf("file = %q, want %q", file, tc.wantFile)
			}

			if body != tc.wantBody {
				t.Errorf("body = %q, want %q", body, tc.wantBody)
			}
		})
	}
}

// TestLoadProjectInstructionsFrom_ExplicitCandidates verifies the
// project_instructions_files override replaces the default list rather than
// extending it.
func TestLoadProjectInstructionsFrom_ExplicitCandidates(t *testing.T) {
	dir := t.TempDir()

	for name, body := range map[string]string{"VV.md": "vv rules", "HOUSE.md": "house rules"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	body, file := LoadProjectInstructionsFrom(dir, []string{"HOUSE.md"})
	if file != "HOUSE.md" || body != "house rules" {
		t.Errorf("LoadProjectInstructionsFrom = (%q, %q), want (\"house rules\", \"HOUSE.md\")", body, file)
	}
}
