package cli_tests

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/vogo/vv/configs"
)

// --- Test: Config mode defaults to "cli" ---
// Verifies that when no mode is specified in YAML or env, the default is "cli".
func TestIntegration_CLI_ConfigModeDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "configs.yaml")

	content := `
llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := configs.Load(path, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}

	if cfg.Mode != "cli" {
		t.Errorf("default mode = %q, want %q", cfg.Mode, "cli")
	}
}

// --- Test: Config mode explicit "http" from YAML ---
// Verifies that mode can be set to "http" via YAML.
func TestIntegration_CLI_ConfigModeHTTP(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "configs.yaml")

	content := `
llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
mode: "http"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := configs.Load(path, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}

	if cfg.Mode != "http" {
		t.Errorf("mode = %q, want %q", cfg.Mode, "http")
	}
}

// --- Test: Config mode explicit "cli" from YAML ---
// Verifies that mode can be explicitly set to "cli" via YAML.
func TestIntegration_CLI_ConfigModeCLI(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "configs.yaml")

	content := `
llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
mode: "cli"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := configs.Load(path, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}

	if cfg.Mode != "cli" {
		t.Errorf("mode = %q, want %q", cfg.Mode, "cli")
	}
}

// --- Test: VV_MODE environment variable override ---
// Verifies that VV_MODE env var overrides YAML mode setting.
func TestIntegration_CLI_ConfigModeEnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "configs.yaml")

	content := `
llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
mode: "cli"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("VV_MODE", "http")

	cfg, err := configs.Load(path, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}

	if cfg.Mode != "http" {
		t.Errorf("mode = %q, want %q after VV_MODE override", cfg.Mode, "http")
	}
}

// --- Test: CLIConfig.ConfirmTools parsed from YAML ---
// Verifies that the confirm_tools list is correctly loaded.
func TestIntegration_CLI_ConfigConfirmTools(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "configs.yaml")

	content := `
llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
cli:
  confirm_tools:
    - bash
    - write
    - edit
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := configs.Load(path, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}

	if len(cfg.CLI.ConfirmTools) != 3 {
		t.Fatalf("confirm_tools len = %d, want 3", len(cfg.CLI.ConfirmTools))
	}

	expected := []string{"bash", "write", "edit"}
	for i, want := range expected {
		if cfg.CLI.ConfirmTools[i] != want {
			t.Errorf("confirm_tools[%d] = %q, want %q", i, cfg.CLI.ConfirmTools[i], want)
		}
	}
}

// --- Test: Empty CLIConfig.ConfirmTools ---
// Verifies that absent confirm_tools results in an empty slice.
func TestIntegration_CLI_ConfigConfirmToolsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "configs.yaml")

	content := `
llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := configs.Load(path, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}

	if len(cfg.CLI.ConfirmTools) != 0 {
		t.Errorf("confirm_tools len = %d, want 0", len(cfg.CLI.ConfirmTools))
	}
}

// --- Test: Full config with all CLI fields ---
// Verifies that a config with all CLI-related fields loads correctly.
func TestIntegration_CLI_FullConfigWithAllFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "configs.yaml")

	content := `
llm:
  provider: "anthropic"
  model: "claude-sonnet-4"
  api_key: "sk-test-full"
server:
  addr: ":9999"
tools:
  bash_timeout: 120
  bash_working_dir: "/tmp/test"
agents:
  max_iterations: 25
  run_token_budget: 10000
mode: "cli"
cli:
  confirm_tools:
    - bash
    - write
    - edit
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := configs.Load(path, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"Mode", cfg.Mode, "cli"},
		{"LLM.Provider", cfg.LLM.Provider, "anthropic"},
		{"LLM.Model", cfg.LLM.Model, "claude-sonnet-4"},
		{"LLM.APIKey", cfg.LLM.APIKey, "sk-test-full"},
		{"Server.Addr", cfg.Server.Addr, ":9999"},
		{"Tools.BashTimeout", cfg.Tools.BashTimeout, 120},
		{"Tools.BashWorkingDir", cfg.Tools.BashWorkingDir, "/tmp/test"},
		{"Agents.MaxIterations", cfg.Agents.MaxIterations, 25},
		{"Agents.RunTokenBudget", cfg.Agents.RunTokenBudget, 10000},
		{"CLI.ConfirmTools len", len(cfg.CLI.ConfirmTools), 3},
	}

	for _, c := range checks {
		if fmt.Sprintf("%v", c.got) != fmt.Sprintf("%v", c.want) {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

// --- Test: Mode selection drives the correct branch in main ---
// Verifies that the config mode field correctly distinguishes between CLI and HTTP paths.
// This is a structural test -- it validates the config plumbing, not the actual TUI/server startup.
func TestIntegration_CLI_ModeSelectionBranching(t *testing.T) {
	tests := []struct {
		name     string
		yaml     string
		envMode  string
		wantMode string
	}{
		{
			name:     "default mode is cli",
			yaml:     "",
			wantMode: "cli",
		},
		{
			name:     "explicit cli mode from YAML",
			yaml:     "mode: cli",
			wantMode: "cli",
		},
		{
			name:     "explicit http mode from YAML",
			yaml:     "mode: http",
			wantMode: "http",
		},
		{
			name:     "VV_MODE overrides YAML",
			yaml:     "mode: cli",
			envMode:  "http",
			wantMode: "http",
		},
		{
			name:     "VV_MODE sets mode when absent from YAML",
			yaml:     "",
			envMode:  "http",
			wantMode: "http",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "configs.yaml")

			if err := os.WriteFile(path, []byte(tt.yaml), 0o644); err != nil {
				t.Fatal(err)
			}

			if tt.envMode != "" {
				t.Setenv("VV_MODE", tt.envMode)
			}

			cfg, err := configs.Load(path, true)
			if err != nil {
				t.Fatalf("configs.Load: %v", err)
			}

			if cfg.Mode != tt.wantMode {
				t.Errorf("mode = %q, want %q", cfg.Mode, tt.wantMode)
			}
		})
	}
}
