package integrations

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vogo/vv/config"
)

func TestIntegration_Config_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
llm:
  provider: "anthropic"
  model: "claude-sonnet-4"
  api_key: "sk-test-integration"
  base_url: "https://custom.example.com/v1"
server:
  addr: ":9999"
tools:
  bash_timeout: 120
  bash_working_dir: "/tmp/test"
agents:
  max_iterations: 25
  run_token_budget: 10000
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path, true)
	if err != nil {
		t.Fatalf("config.Load failed: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"LLM.Provider", cfg.LLM.Provider, "anthropic"},
		{"LLM.Model", cfg.LLM.Model, "claude-sonnet-4"},
		{"LLM.APIKey", cfg.LLM.APIKey, "sk-test-integration"},
		{"LLM.BaseURL", cfg.LLM.BaseURL, "https://custom.example.com/v1"},
		{"Server.Addr", cfg.Server.Addr, ":9999"},
		{"Tools.BashTimeout", cfg.Tools.BashTimeout, 120},
		{"Tools.BashWorkingDir", cfg.Tools.BashWorkingDir, "/tmp/test"},
		{"Agents.MaxIterations", cfg.Agents.MaxIterations, 25},
		{"Agents.RunTokenBudget", cfg.Agents.RunTokenBudget, 10000},
	}

	for _, c := range checks {
		if fmt.Sprintf("%v", c.got) != fmt.Sprintf("%v", c.want) {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

func TestIntegration_Config_EnvVarOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
llm:
  api_key: "yaml-key"
  model: "yaml-model"
  provider: "openai"
  base_url: "https://yaml.example.com"
server:
  addr: ":1111"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("VAGA_LLM_API_KEY", "env-key-override")
	t.Setenv("VAGA_LLM_BASE_URL", "https://env.example.com")
	t.Setenv("VAGA_LLM_MODEL", "env-model-override")
	t.Setenv("VAGA_LLM_PROVIDER", "anthropic")
	t.Setenv("VAGA_SERVER_ADDR", ":2222")

	cfg, err := config.Load(path, true)
	if err != nil {
		t.Fatalf("config.Load failed: %v", err)
	}

	if cfg.LLM.APIKey != "env-key-override" {
		t.Errorf("APIKey = %q, want %q", cfg.LLM.APIKey, "env-key-override")
	}
	if cfg.LLM.BaseURL != "https://env.example.com" {
		t.Errorf("BaseURL = %q, want %q", cfg.LLM.BaseURL, "https://env.example.com")
	}
	if cfg.LLM.Model != "env-model-override" {
		t.Errorf("Model = %q, want %q", cfg.LLM.Model, "env-model-override")
	}
	if cfg.LLM.Provider != "anthropic" {
		t.Errorf("Provider = %q, want %q", cfg.LLM.Provider, "anthropic")
	}
	if cfg.Server.Addr != ":2222" {
		t.Errorf("Addr = %q, want %q", cfg.Server.Addr, ":2222")
	}
}

func TestIntegration_Config_Defaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path, true)
	if err != nil {
		t.Fatalf("config.Load failed: %v", err)
	}

	if cfg.Server.Addr != ":8080" {
		t.Errorf("default Addr = %q, want %q", cfg.Server.Addr, ":8080")
	}
	if cfg.Agents.MaxIterations != 10 {
		t.Errorf("default MaxIterations = %d, want 10", cfg.Agents.MaxIterations)
	}
	if cfg.Tools.BashTimeout != 30 {
		t.Errorf("default BashTimeout = %d, want 30", cfg.Tools.BashTimeout)
	}
	if cfg.LLM.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("default BaseURL = %q, want %q", cfg.LLM.BaseURL, "https://api.openai.com/v1")
	}
}

func TestIntegration_Config_MissingFileDefaultPath(t *testing.T) {
	cfg, err := config.Load("/nonexistent/definitely-not-here/vaga.yaml", false)
	if err != nil {
		t.Fatalf("config.Load should succeed for missing default path: %v", err)
	}

	if cfg.Server.Addr != ":8080" {
		t.Errorf("Addr = %q, want %q", cfg.Server.Addr, ":8080")
	}
	if cfg.Agents.MaxIterations != 10 {
		t.Errorf("MaxIterations = %d, want 10", cfg.Agents.MaxIterations)
	}
	if cfg.Tools.BashTimeout != 30 {
		t.Errorf("BashTimeout = %d, want 30", cfg.Tools.BashTimeout)
	}
}

func TestIntegration_Config_MissingFileExplicitPath(t *testing.T) {
	_, err := config.Load("/nonexistent/definitely-not-here/config.yaml", true)
	if err == nil {
		t.Fatal("expected error when explicit config file is missing")
	}
	if !strings.Contains(err.Error(), "read config file") {
		t.Errorf("error = %q, expected it to contain 'read config file'", err.Error())
	}
}

func TestIntegration_Config_ProviderDefaults(t *testing.T) {
	dir := t.TempDir()

	openaiPath := filepath.Join(dir, "openai.yaml")
	if err := os.WriteFile(openaiPath, []byte("llm:\n  provider: openai\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := config.Load(openaiPath, true)
	if err != nil {
		t.Fatalf("config.Load openai: %v", err)
	}
	if cfg.LLM.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("openai BaseURL = %q, want %q", cfg.LLM.BaseURL, "https://api.openai.com/v1")
	}

	emptyPath := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(emptyPath, []byte("llm:\n  model: gpt-4o\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.Load(emptyPath, true)
	if err != nil {
		t.Fatalf("config.Load empty provider: %v", err)
	}
	if cfg.LLM.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("empty provider BaseURL = %q, want %q", cfg.LLM.BaseURL, "https://api.openai.com/v1")
	}

	anthropicPath := filepath.Join(dir, "anthropic.yaml")
	if err := os.WriteFile(anthropicPath, []byte("llm:\n  provider: anthropic\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err = config.Load(anthropicPath, true)
	if err != nil {
		t.Fatalf("config.Load anthropic: %v", err)
	}
	if cfg.LLM.BaseURL != "" {
		t.Errorf("anthropic BaseURL = %q, want empty", cfg.LLM.BaseURL)
	}
}

func TestIntegration_Config_UnknownProvider(t *testing.T) {
	_, err := config.NewLLMClient(config.LLMConfig{
		Provider: "unknown-provider",
		Model:    "test-model",
		APIKey:   "test-key",
	})
	if err == nil {
		t.Fatal("expected error for unsupported provider")
	}
	if !strings.Contains(err.Error(), "unsupported LLM provider") {
		t.Errorf("error = %q, expected to contain 'unsupported LLM provider'", err.Error())
	}
}

// --- Test 9: Memory configuration defaults are applied correctly ---
// Verifies that MemoryConfig fields get proper defaults when not specified.
func TestIntegration_Config_MemoryDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path, true)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	if cfg.Memory.SessionWindow != 50 {
		t.Errorf("default SessionWindow = %d, want 50", cfg.Memory.SessionWindow)
	}

	if cfg.Memory.MaxConcurrency != 2 {
		t.Errorf("default MaxConcurrency = %d, want 2", cfg.Memory.MaxConcurrency)
	}

	if cfg.Memory.Dir == "" {
		t.Error("default Memory.Dir should not be empty")
	}

	if !strings.HasSuffix(cfg.Memory.Dir, "memory") {
		t.Errorf("default Memory.Dir = %q, expected to end with 'memory'", cfg.Memory.Dir)
	}
}

// --- Test: Memory configuration from YAML ---
// Verifies that explicit memory config values are loaded from YAML.
func TestIntegration_Config_MemoryFromYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
memory:
  dir: "/tmp/test-memory"
  session_window: 100
  persistent_load: true
  max_concurrency: 4
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := config.Load(path, true)
	if err != nil {
		t.Fatalf("config.Load: %v", err)
	}

	if cfg.Memory.Dir != "/tmp/test-memory" {
		t.Errorf("Memory.Dir = %q, want %q", cfg.Memory.Dir, "/tmp/test-memory")
	}

	if cfg.Memory.SessionWindow != 100 {
		t.Errorf("Memory.SessionWindow = %d, want 100", cfg.Memory.SessionWindow)
	}

	if cfg.Memory.MaxConcurrency != 4 {
		t.Errorf("Memory.MaxConcurrency = %d, want 4", cfg.Memory.MaxConcurrency)
	}

	if !cfg.Memory.PersistentLoad {
		t.Error("Memory.PersistentLoad = false, want true")
	}
}
