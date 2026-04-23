package permission_tests

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vogo/vv/configs"
)

// TestIntegration_Config_PermissionMode_DefaultWhenOmitted verifies that
// when permission_mode is not set in YAML, it defaults to "default".
func TestIntegration_Config_PermissionMode_DefaultWhenOmitted(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

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

	if cfg.CLI.PermissionMode != configs.PermissionModeDefault {
		t.Errorf("PermissionMode = %q, want %q", cfg.CLI.PermissionMode, configs.PermissionModeDefault)
	}
}

// TestIntegration_Config_PermissionMode_LoadedFromYAML verifies that
// permission_mode is correctly loaded from YAML configuration.
func TestIntegration_Config_PermissionMode_LoadedFromYAML(t *testing.T) {
	modes := []configs.PermissionMode{
		configs.PermissionModeDefault,
		configs.PermissionModeAcceptEdits,
		configs.PermissionModeAuto,
		configs.PermissionModePlan,
	}

	for _, mode := range modes {
		t.Run(string(mode), func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "config.yaml")

			content := fmt.Sprintf(`
llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
cli:
  permission_mode: %q
`, mode)
			if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
				t.Fatal(err)
			}

			cfg, err := configs.Load(path, true)
			if err != nil {
				t.Fatalf("configs.Load: %v", err)
			}

			if cfg.CLI.PermissionMode != mode {
				t.Errorf("PermissionMode = %q, want %q", cfg.CLI.PermissionMode, mode)
			}
		})
	}
}

// TestIntegration_Config_PermissionMode_EnvVarOverridesYAML verifies that the
// VV_PERMISSION_MODE environment variable overrides the YAML permission_mode value.
func TestIntegration_Config_PermissionMode_EnvVarOverridesYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
cli:
  permission_mode: "default"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("VV_PERMISSION_MODE", "auto")

	cfg, err := configs.Load(path, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}

	if cfg.CLI.PermissionMode != configs.PermissionModeAuto {
		t.Errorf("PermissionMode = %q, want %q", cfg.CLI.PermissionMode, configs.PermissionModeAuto)
	}
}

// TestIntegration_Config_PermissionMode_InvalidValueReturnsError verifies that
// an invalid permission_mode value causes Load to return an error.
func TestIntegration_Config_PermissionMode_InvalidValueReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
cli:
  permission_mode: "invalid-mode"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := configs.Load(path, true)
	if err == nil {
		t.Fatal("expected error for invalid permission_mode, got nil")
	}

	if !strings.Contains(err.Error(), "invalid permission_mode") {
		t.Errorf("error = %q, want to contain 'invalid permission_mode'", err.Error())
	}
}

// TestIntegration_Config_PermissionMode_InvalidEnvVarReturnsError verifies that
// an invalid VV_PERMISSION_MODE env var causes Load to return an error.
func TestIntegration_Config_PermissionMode_InvalidEnvVarReturnsError(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("VV_PERMISSION_MODE", "bogus")

	_, err := configs.Load(path, true)
	if err == nil {
		t.Fatal("expected error for invalid VV_PERMISSION_MODE, got nil")
	}

	if !strings.Contains(err.Error(), "invalid permission_mode") {
		t.Errorf("error = %q, want to contain 'invalid permission_mode'", err.Error())
	}
}

// TestIntegration_Config_PermissionMode_DeprecatedConfirmToolsStillLoads verifies
// that config with deprecated confirm_tools still loads successfully (with the
// deprecation warning logged, which we don't assert on here).
func TestIntegration_Config_PermissionMode_DeprecatedConfirmToolsStillLoads(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
cli:
  confirm_tools:
    - bash
    - write
  permission_mode: "default"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := configs.Load(path, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}

	if len(cfg.CLI.ConfirmTools) != 2 {
		t.Errorf("ConfirmTools len = %d, want 2", len(cfg.CLI.ConfirmTools))
	}

	if cfg.CLI.PermissionMode != configs.PermissionModeDefault {
		t.Errorf("PermissionMode = %q, want %q", cfg.CLI.PermissionMode, configs.PermissionModeDefault)
	}
}
