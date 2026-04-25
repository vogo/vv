package configs

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultPath_ContainsVaga(t *testing.T) {
	p := DefaultPath()
	if !strings.Contains(p, ".vv") {
		t.Errorf("DefaultPath() = %q, want it to contain '.vv'", p)
	}

	if !strings.HasSuffix(p, "vv.yaml") {
		t.Errorf("DefaultPath() = %q, want it to end with 'vv.yaml'", p)
	}
}

func TestLoad_ValidYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
llm:
  provider: "anthropic"
  model: "claude-sonnet-4"
  api_key: "sk-test-123"
  base_url: "https://custom.api.com"
server:
  addr: ":9090"
tools:
  bash_timeout: 60
  bash_working_dir: "/tmp"
agents:
  max_iterations: 20
  run_token_budget: 5000
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.LLM.Provider != "anthropic" {
		t.Errorf("provider = %q, want %q", cfg.LLM.Provider, "anthropic")
	}

	if cfg.LLM.Model != "claude-sonnet-4" {
		t.Errorf("model = %q, want %q", cfg.LLM.Model, "claude-sonnet-4")
	}

	if cfg.LLM.APIKey != "sk-test-123" {
		t.Errorf("api_key = %q, want %q", cfg.LLM.APIKey, "sk-test-123")
	}

	if cfg.LLM.BaseURL != "https://custom.api.com" {
		t.Errorf("base_url = %q, want %q", cfg.LLM.BaseURL, "https://custom.api.com")
	}

	if cfg.Server.Addr != ":9090" {
		t.Errorf("addr = %q, want %q", cfg.Server.Addr, ":9090")
	}

	if cfg.Tools.BashTimeout != 60 {
		t.Errorf("bash_timeout = %d, want 60", cfg.Tools.BashTimeout)
	}

	if cfg.Tools.BashWorkingDir != "/tmp" {
		t.Errorf("bash_working_dir = %q, want %q", cfg.Tools.BashWorkingDir, "/tmp")
	}

	if cfg.Agents.MaxIterations != 20 {
		t.Errorf("max_iterations = %d, want 20", cfg.Agents.MaxIterations)
	}

	if cfg.Agents.RunTokenBudget != 5000 {
		t.Errorf("run_token_budget = %d, want 5000", cfg.Agents.RunTokenBudget)
	}
}

func TestLoad_EnvVarOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
llm:
  api_key: "yaml-key"
  model: "yaml-model"
  provider: "openai"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("VV_LLM_API_KEY", "env-key")
	t.Setenv("VV_LLM_MODEL", "env-model")
	t.Setenv("VV_LLM_PROVIDER", "anthropic")
	t.Setenv("VV_LLM_BASE_URL", "https://env.api.com")
	t.Setenv("VV_SERVER_ADDR", ":7070")

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.LLM.APIKey != "env-key" {
		t.Errorf("api_key = %q, want %q", cfg.LLM.APIKey, "env-key")
	}

	if cfg.LLM.Model != "env-model" {
		t.Errorf("model = %q, want %q", cfg.LLM.Model, "env-model")
	}

	if cfg.LLM.Provider != "anthropic" {
		t.Errorf("provider = %q, want %q", cfg.LLM.Provider, "anthropic")
	}

	if cfg.LLM.BaseURL != "https://env.api.com" {
		t.Errorf("base_url = %q, want %q", cfg.LLM.BaseURL, "https://env.api.com")
	}

	if cfg.Server.Addr != ":7070" {
		t.Errorf("addr = %q, want %q", cfg.Server.Addr, ":7070")
	}
}

func TestLoad_Defaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Server.Addr != ":8080" {
		t.Errorf("addr = %q, want %q", cfg.Server.Addr, ":8080")
	}

	if cfg.Agents.MaxIterations != 10 {
		t.Errorf("max_iterations = %d, want 10", cfg.Agents.MaxIterations)
	}

	if cfg.Tools.BashTimeout != 30 {
		t.Errorf("bash_timeout = %d, want 30", cfg.Tools.BashTimeout)
	}
}

func TestLoad_MissingFileDefaultPath(t *testing.T) {
	cfg, err := Load("/nonexistent/path/vv.yaml", false)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Server.Addr != ":8080" {
		t.Errorf("addr = %q, want %q", cfg.Server.Addr, ":8080")
	}

	if cfg.Agents.MaxIterations != 10 {
		t.Errorf("max_iterations = %d, want 10", cfg.Agents.MaxIterations)
	}
}

func TestLoad_MissingFileExplicitPath(t *testing.T) {
	_, err := Load("/nonexistent/path/config.yaml", true)
	if err == nil {
		t.Fatal("expected error for missing explicit config file")
	}
}

func TestLoad_ProviderDefaultBaseURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
llm:
  provider: "openai"
  model: "gpt-4o"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.LLM.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("base_url = %q, want %q", cfg.LLM.BaseURL, "https://api.openai.com/v1")
	}
}

func TestLoad_EmptyProviderDefaultBaseURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
llm:
  model: "gpt-4o"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.LLM.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("base_url = %q, want %q", cfg.LLM.BaseURL, "https://api.openai.com/v1")
	}
}

func TestLoad_AnthropicProviderNoDefaultBaseURL(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
llm:
  provider: "anthropic"
  model: "claude-sonnet-4"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.LLM.BaseURL != "" {
		t.Errorf("base_url = %q, want empty for anthropic", cfg.LLM.BaseURL)
	}
}

func TestNeedsSetup_MissingAPIKey(t *testing.T) {
	cfg := &Config{}
	if !NeedsSetup(cfg) {
		t.Error("NeedsSetup should return true when API key is missing")
	}
}

func TestNeedsSetup_WithAPIKey(t *testing.T) {
	cfg := &Config{LLM: LLMConfig{APIKey: "sk-test"}}
	if NeedsSetup(cfg) {
		t.Error("NeedsSetup should return false when API key is set")
	}
}

func TestPrompt_AllDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vv.yaml")

	// Simulate user pressing Enter for all prompts except API key.
	input := "\n\nsk-my-key\n\n\n"
	var out bytes.Buffer

	cfg := &Config{}
	if err := Prompt(cfg, path, strings.NewReader(input), &out); err != nil {
		t.Fatalf("Prompt: %v", err)
	}

	if cfg.LLM.Provider != "openai" {
		t.Errorf("provider = %q, want %q", cfg.LLM.Provider, "openai")
	}

	if cfg.LLM.Model != "gpt-4o" {
		t.Errorf("model = %q, want %q", cfg.LLM.Model, "gpt-4o")
	}

	if cfg.LLM.APIKey != "sk-my-key" {
		t.Errorf("api_key = %q, want %q", cfg.LLM.APIKey, "sk-my-key")
	}

	if cfg.LLM.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("base_url = %q, want %q", cfg.LLM.BaseURL, "https://api.openai.com/v1")
	}

	if cfg.Server.Addr != ":8080" {
		t.Errorf("addr = %q, want %q", cfg.Server.Addr, ":8080")
	}

	// Verify file was saved.
	if _, err := os.Stat(path); err != nil {
		t.Errorf("config file not saved: %v", err)
	}
}

func TestPrompt_AnthropicProvider(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vv.yaml")

	// User types "anthropic", accepts default model, enters API key, accepts addr.
	input := "anthropic\n\nsk-ant-key\n\n"
	var out bytes.Buffer

	cfg := &Config{}
	if err := Prompt(cfg, path, strings.NewReader(input), &out); err != nil {
		t.Fatalf("Prompt: %v", err)
	}

	if cfg.LLM.Provider != "anthropic" {
		t.Errorf("provider = %q, want %q", cfg.LLM.Provider, "anthropic")
	}

	if cfg.LLM.Model != "claude-sonnet-4" {
		t.Errorf("model = %q, want %q", cfg.LLM.Model, "claude-sonnet-4")
	}

	if cfg.LLM.APIKey != "sk-ant-key" {
		t.Errorf("api_key = %q, want %q", cfg.LLM.APIKey, "sk-ant-key")
	}

	// Anthropic provider should not prompt for base URL, so no base_url set.
	if cfg.LLM.BaseURL != "" {
		t.Errorf("base_url = %q, want empty for anthropic", cfg.LLM.BaseURL)
	}
}

func TestPrompt_CustomValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vv.yaml")

	input := "openai\ngpt-4o-mini\nsk-custom\nhttps://custom.api.com/v1\n:9090\n"
	var out bytes.Buffer

	cfg := &Config{}
	if err := Prompt(cfg, path, strings.NewReader(input), &out); err != nil {
		t.Fatalf("Prompt: %v", err)
	}

	if cfg.LLM.Provider != "openai" {
		t.Errorf("provider = %q, want %q", cfg.LLM.Provider, "openai")
	}

	if cfg.LLM.Model != "gpt-4o-mini" {
		t.Errorf("model = %q, want %q", cfg.LLM.Model, "gpt-4o-mini")
	}

	if cfg.LLM.APIKey != "sk-custom" {
		t.Errorf("api_key = %q, want %q", cfg.LLM.APIKey, "sk-custom")
	}

	if cfg.LLM.BaseURL != "https://custom.api.com/v1" {
		t.Errorf("base_url = %q, want %q", cfg.LLM.BaseURL, "https://custom.api.com/v1")
	}

	if cfg.Server.Addr != ":9090" {
		t.Errorf("addr = %q, want %q", cfg.Server.Addr, ":9090")
	}
}

func TestSave_CreatesDirectoryAndFile(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "sub", "dir", "vv.yaml")

	cfg := &Config{
		LLM: LLMConfig{Provider: "openai", Model: "gpt-4o", APIKey: "sk-test"},
	}

	if err := Save(cfg, path); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Verify file exists and is readable.
	loaded, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load saved config: %v", err)
	}

	if loaded.LLM.APIKey != "sk-test" {
		t.Errorf("api_key = %q, want %q", loaded.LLM.APIKey, "sk-test")
	}
}

func TestLoad_ModeDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Mode != "cli" {
		t.Errorf("mode = %q, want %q", cfg.Mode, "cli")
	}
}

func TestLoad_ModeFromYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
mode: "http"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Mode != "http" {
		t.Errorf("mode = %q, want %q", cfg.Mode, "http")
	}
}

func TestLoad_ModeEnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
mode: "cli"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("VV_MODE", "http")

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Mode != "http" {
		t.Errorf("mode = %q, want %q", cfg.Mode, "http")
	}
}

func TestLoad_CLIConfig(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
cli:
  confirm_tools:
    - bash
    - write
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.CLI.ConfirmTools) != 2 {
		t.Fatalf("confirm_tools len = %d, want 2", len(cfg.CLI.ConfirmTools))
	}

	if cfg.CLI.ConfirmTools[0] != "bash" {
		t.Errorf("confirm_tools[0] = %q, want %q", cfg.CLI.ConfirmTools[0], "bash")
	}

	if cfg.CLI.ConfirmTools[1] != "write" {
		t.Errorf("confirm_tools[1] = %q, want %q", cfg.CLI.ConfirmTools[1], "write")
	}
}

func TestLoad_CLIConfigEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.CLI.ConfirmTools) != 0 {
		t.Errorf("confirm_tools len = %d, want 0", len(cfg.CLI.ConfirmTools))
	}
}

func TestNewLLMClient_UnsupportedProvider(t *testing.T) {
	_, err := NewLLMClient(LLMConfig{
		Provider: "unsupported",
		Model:    "test-model",
		APIKey:   "test-key",
	})
	if err == nil {
		t.Fatal("expected error for unsupported provider")
	}
}

func TestNewLLMClient_OpenAIWithAPIKey(t *testing.T) {
	client, err := NewLLMClient(LLMConfig{
		Provider: "openai",
		Model:    "gpt-4o",
		APIKey:   "sk-test-key",
		BaseURL:  "https://api.openai.com/v1",
	})
	if err != nil {
		t.Fatalf("NewLLMClient: %v", err)
	}

	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestNewLLMClient_AnthropicWithAPIKey(t *testing.T) {
	client, err := NewLLMClient(LLMConfig{
		Provider: "anthropic",
		Model:    "claude-sonnet-4",
		APIKey:   "sk-test-key",
	})
	if err != nil {
		t.Fatalf("NewLLMClient: %v", err)
	}

	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestNewLLMClient_EmptyProviderDefaultsToOpenAI(t *testing.T) {
	client, err := NewLLMClient(LLMConfig{
		Provider: "",
		Model:    "gpt-4o",
		APIKey:   "sk-test-key",
		BaseURL:  "https://api.openai.com/v1",
	})
	if err != nil {
		t.Fatalf("NewLLMClient: %v", err)
	}

	if client == nil {
		t.Fatal("expected non-nil client")
	}
}

func TestLoad_AskUserTimeout_Default(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Agents.AskUserTimeout != 300 {
		t.Errorf("AskUserTimeout = %d, want 300", cfg.Agents.AskUserTimeout)
	}
}

func TestLoad_AskUserTimeout_Custom(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
agents:
  ask_user_timeout: 600
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Agents.AskUserTimeout != 600 {
		t.Errorf("AskUserTimeout = %d, want 600", cfg.Agents.AskUserTimeout)
	}
}

func TestContextConfig_Defaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Context.ModelMaxContextTokens != 128000 {
		t.Errorf("ModelMaxContextTokens = %d, want 128000", cfg.Context.ModelMaxContextTokens)
	}

	if cfg.Context.ToolOutputMaxTokens != 8000 {
		t.Errorf("ToolOutputMaxTokens = %d, want 8000", cfg.Context.ToolOutputMaxTokens)
	}

	if cfg.Context.ProtectedTurns != 4 {
		t.Errorf("ProtectedTurns = %d, want 4", cfg.Context.ProtectedTurns)
	}

	if cfg.Context.CompressionThreshold != nil {
		t.Errorf("CompressionThreshold = %v, want nil (default)", cfg.Context.CompressionThreshold)
	}

	if cfg.Context.EffectiveCompressionThreshold() != 0.8 {
		t.Errorf("EffectiveCompressionThreshold = %f, want 0.8", cfg.Context.EffectiveCompressionThreshold())
	}
}

func TestContextConfig_ExplicitValues(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
context:
  model_max_context_tokens: 200000
  compression_threshold: 0.5
  tool_output_max_tokens: 4000
  context_protected_turns: 6
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Context.ModelMaxContextTokens != 200000 {
		t.Errorf("ModelMaxContextTokens = %d, want 200000", cfg.Context.ModelMaxContextTokens)
	}

	if cfg.Context.ToolOutputMaxTokens != 4000 {
		t.Errorf("ToolOutputMaxTokens = %d, want 4000", cfg.Context.ToolOutputMaxTokens)
	}

	if cfg.Context.ProtectedTurns != 6 {
		t.Errorf("ProtectedTurns = %d, want 6", cfg.Context.ProtectedTurns)
	}

	if cfg.Context.CompressionThreshold == nil {
		t.Fatal("CompressionThreshold should not be nil")
	}

	if *cfg.Context.CompressionThreshold != 0.5 {
		t.Errorf("CompressionThreshold = %f, want 0.5", *cfg.Context.CompressionThreshold)
	}

	if cfg.Context.EffectiveCompressionThreshold() != 0.5 {
		t.Errorf("EffectiveCompressionThreshold = %f, want 0.5", cfg.Context.EffectiveCompressionThreshold())
	}
}

func TestContextConfig_EnvVarOverrides(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("VV_MAX_CONTEXT_TOKENS", "64000")
	t.Setenv("VV_CONTEXT_COMPRESSION_THRESHOLD", "0.6")
	t.Setenv("VV_TOOL_OUTPUT_MAX_TOKENS", "2000")
	t.Setenv("VV_CONTEXT_PROTECTED_TURNS", "3")

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Context.ModelMaxContextTokens != 64000 {
		t.Errorf("ModelMaxContextTokens = %d, want 64000", cfg.Context.ModelMaxContextTokens)
	}

	if cfg.Context.CompressionThreshold == nil {
		t.Fatal("CompressionThreshold should not be nil after env override")
	}

	if *cfg.Context.CompressionThreshold != 0.6 {
		t.Errorf("CompressionThreshold = %f, want 0.6", *cfg.Context.CompressionThreshold)
	}

	if cfg.Context.ToolOutputMaxTokens != 2000 {
		t.Errorf("ToolOutputMaxTokens = %d, want 2000", cfg.Context.ToolOutputMaxTokens)
	}

	if cfg.Context.ProtectedTurns != 3 {
		t.Errorf("ProtectedTurns = %d, want 3", cfg.Context.ProtectedTurns)
	}
}

func TestContextConfig_EffectiveCompressionThreshold_ZeroValue(t *testing.T) {
	threshold := 0.0
	cfg := ContextConfig{CompressionThreshold: &threshold}
	if cfg.EffectiveCompressionThreshold() != 0.0 {
		t.Errorf("EffectiveCompressionThreshold = %f, want 0.0", cfg.EffectiveCompressionThreshold())
	}
}

func TestLoad_OrchestrateConfig_Default(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
llm:
  provider: "openai"
  model: "gpt-4o"
  api_key: "test-key"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Orchestrate.MaxConcurrency != 2 {
		t.Errorf("Orchestrate.MaxConcurrency = %d, want 2 (default)", cfg.Orchestrate.MaxConcurrency)
	}
}

func TestLoad_OrchestrateConfig_Migration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
llm:
  provider: "openai"
  model: "gpt-4o"
  api_key: "test-key"
memory:
  max_concurrency: 4
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Should migrate from memory.max_concurrency to orchestrate.max_concurrency.
	if cfg.Orchestrate.MaxConcurrency != 4 {
		t.Errorf("Orchestrate.MaxConcurrency = %d, want 4 (migrated from memory)", cfg.Orchestrate.MaxConcurrency)
	}
}

func TestLoad_ModelPricing_FromYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
model_pricing:
  claude-opus-4:
    input_per_m_tokens: 15.0
    output_per_m_tokens: 75.0
    cache_per_m_tokens: 1.5
  my-model:
    input_per_m_tokens: 1.0
    output_per_m_tokens: 5.0
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.ModelPricing) != 2 {
		t.Fatalf("ModelPricing len = %d, want 2", len(cfg.ModelPricing))
	}

	opus := cfg.ModelPricing["claude-opus-4"]
	if opus.InputPerMTokens != 15.0 {
		t.Errorf("claude-opus-4 InputPerMTokens = %f, want 15.0", opus.InputPerMTokens)
	}
	if opus.CachePerMTokens != 1.5 {
		t.Errorf("claude-opus-4 CachePerMTokens = %f, want 1.5", opus.CachePerMTokens)
	}
}

func TestLoad_ModelPricing_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
model_pricing:
  gpt-4o:
    input_per_m_tokens: 2.5
    output_per_m_tokens: 10.0
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("VV_MODEL_PRICING", `{"my-model":{"input_per_m_tokens":1.0,"output_per_m_tokens":5.0},"gpt-4o":{"input_per_m_tokens":99.0,"output_per_m_tokens":99.0}}`)

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Env should merge with YAML; gpt-4o should be overridden.
	if len(cfg.ModelPricing) != 2 {
		t.Fatalf("ModelPricing len = %d, want 2", len(cfg.ModelPricing))
	}

	gpt := cfg.ModelPricing["gpt-4o"]
	if gpt.InputPerMTokens != 99.0 {
		t.Errorf("gpt-4o InputPerMTokens = %f, want 99.0 (env override)", gpt.InputPerMTokens)
	}

	myModel := cfg.ModelPricing["my-model"]
	if myModel.InputPerMTokens != 1.0 {
		t.Errorf("my-model InputPerMTokens = %f, want 1.0", myModel.InputPerMTokens)
	}
}

func TestLoad_ModelPricing_EnvOnly(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("VV_MODEL_PRICING", `{"my-model":{"input_per_m_tokens":1.0,"output_per_m_tokens":5.0}}`)

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(cfg.ModelPricing) != 1 {
		t.Fatalf("ModelPricing len = %d, want 1", len(cfg.ModelPricing))
	}
}

func TestLoad_PermissionMode_Default(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(path, []byte(""), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.CLI.PermissionMode != PermissionModeDefault {
		t.Errorf("PermissionMode = %q, want %q", cfg.CLI.PermissionMode, PermissionModeDefault)
	}
}

func TestLoad_PermissionMode_FromYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
cli:
  permission_mode: "auto"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.CLI.PermissionMode != PermissionModeAuto {
		t.Errorf("PermissionMode = %q, want %q", cfg.CLI.PermissionMode, PermissionModeAuto)
	}
}

func TestLoad_PermissionMode_EnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
cli:
  permission_mode: "default"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("VV_PERMISSION_MODE", "plan")

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.CLI.PermissionMode != PermissionModePlan {
		t.Errorf("PermissionMode = %q, want %q", cfg.CLI.PermissionMode, PermissionModePlan)
	}
}

func TestLoad_PermissionMode_Invalid(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
cli:
  permission_mode: "invalid-mode"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path, true)
	if err == nil {
		t.Fatal("expected error for invalid permission_mode")
	}

	if !strings.Contains(err.Error(), "invalid permission_mode") {
		t.Errorf("error = %q, want it to contain 'invalid permission_mode'", err.Error())
	}
}

func TestLoad_PermissionMode_AcceptEdits(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := `
cli:
  permission_mode: "accept-edits"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.CLI.PermissionMode != PermissionModeAcceptEdits {
		t.Errorf("PermissionMode = %q, want %q", cfg.CLI.PermissionMode, PermissionModeAcceptEdits)
	}
}

func TestLoad_OrchestrateConfig_ExplicitOverridesMigration(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
llm:
  provider: "openai"
  model: "gpt-4o"
  api_key: "test-key"
memory:
  max_concurrency: 4
orchestrate:
  max_concurrency: 8
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	// Explicit orchestrate.max_concurrency should take precedence.
	if cfg.Orchestrate.MaxConcurrency != 8 {
		t.Errorf("Orchestrate.MaxConcurrency = %d, want 8 (explicit)", cfg.Orchestrate.MaxConcurrency)
	}
}

func TestLoad_DebugFromYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	if err := os.WriteFile(path, []byte("debug: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VV_DEBUG", "")
	t.Setenv("VV_LLM_API_KEY", "x")
	cfg, err := Load(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Debug {
		t.Errorf("expected Debug=true from YAML")
	}
}

func TestLoad_DebugEnvOverridesYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	if err := os.WriteFile(path, []byte("debug: false\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VV_DEBUG", "true")
	t.Setenv("VV_LLM_API_KEY", "x")
	cfg, err := Load(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if !cfg.Debug {
		t.Errorf("env VV_DEBUG=true should override YAML")
	}
}

func TestLoad_DebugDefaultFalse(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "c.yaml")
	if err := os.WriteFile(path, []byte(""), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("VV_DEBUG", "")
	t.Setenv("VV_LLM_API_KEY", "x")
	cfg, err := Load(path, true)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.Debug {
		t.Errorf("expected default Debug=false")
	}
}

func TestLoad_BudgetYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
llm:
  api_key: "sk-x"
budget:
  session_hard_tokens: 200000
  session_hard_cost_usd: 5.0
  daily_hard_tokens: 2000000
  daily_hard_cost_usd: 10.0
  warn_percent: 0.7
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Budget.SessionHardTokens != 200000 {
		t.Errorf("session_hard_tokens = %d, want 200000", cfg.Budget.SessionHardTokens)
	}
	if cfg.Budget.SessionHardCostUSD != 5.0 {
		t.Errorf("session_hard_cost_usd = %v, want 5.0", cfg.Budget.SessionHardCostUSD)
	}
	if cfg.Budget.DailyHardTokens != 2000000 {
		t.Errorf("daily_hard_tokens = %d, want 2000000", cfg.Budget.DailyHardTokens)
	}
	if cfg.Budget.DailyHardCostUSD != 10.0 {
		t.Errorf("daily_hard_cost_usd = %v, want 10.0", cfg.Budget.DailyHardCostUSD)
	}
	if cfg.Budget.WarnPercent != 0.7 {
		t.Errorf("warn_percent = %v, want 0.7", cfg.Budget.WarnPercent)
	}
	if !cfg.Budget.IsEnabled() {
		t.Error("Budget.IsEnabled should be true with any limit configured")
	}
}

func TestLoad_BudgetEnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("llm:\n  api_key: sk-x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("VV_BUDGET_SESSION_HARD_TOKENS", "12345")
	t.Setenv("VV_BUDGET_SESSION_HARD_COST_USD", "1.5")
	t.Setenv("VV_BUDGET_DAILY_HARD_TOKENS", "999999")
	t.Setenv("VV_BUDGET_DAILY_HARD_COST_USD", "50")
	t.Setenv("VV_BUDGET_WARN_PERCENT", "0.5")

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Budget.SessionHardTokens != 12345 {
		t.Errorf("session tokens env: want 12345, got %d", cfg.Budget.SessionHardTokens)
	}
	if cfg.Budget.SessionHardCostUSD != 1.5 {
		t.Errorf("session cost env: want 1.5, got %v", cfg.Budget.SessionHardCostUSD)
	}
	if cfg.Budget.DailyHardTokens != 999999 {
		t.Errorf("daily tokens env: want 999999, got %d", cfg.Budget.DailyHardTokens)
	}
	if cfg.Budget.DailyHardCostUSD != 50 {
		t.Errorf("daily cost env: want 50, got %v", cfg.Budget.DailyHardCostUSD)
	}
	if cfg.Budget.WarnPercent != 0.5 {
		t.Errorf("warn env: want 0.5, got %v", cfg.Budget.WarnPercent)
	}
}

func TestLoad_BudgetDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("llm:\n  api_key: sk-x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Budget.IsEnabled() {
		t.Error("unset budget should report disabled")
	}
	// WarnPercent default applied even when limits are unset; the percent is
	// used only once a tracker exists, but the default is written for
	// observability/docs parity.
	if cfg.Budget.WarnPercent != 0.8 {
		t.Errorf("default warn_percent: want 0.8, got %v", cfg.Budget.WarnPercent)
	}
}

func TestEffectiveRouterConfig_Disabled(t *testing.T) {
	cfg := &Config{
		LLM: LLMConfig{Provider: "openai", Model: "gpt-4o", APIKey: "sk-main", BaseURL: "https://api.openai.com/v1"},
	}

	if got, ok := EffectiveRouterConfig(cfg); ok || got.Model != "" {
		t.Errorf("router unset must report disabled, got (%+v, %v)", got, ok)
	}
}

func TestEffectiveRouterConfig_InheritsFromMain(t *testing.T) {
	cfg := &Config{
		LLM: LLMConfig{Provider: "openai", Model: "gpt-4o", APIKey: "sk-main", BaseURL: "https://api.openai.com/v1"},
	}
	cfg.Orchestrate.Router = LLMConfig{Model: "gpt-4o-mini"}

	got, ok := EffectiveRouterConfig(cfg)
	if !ok {
		t.Fatal("router should be enabled when model is set")
	}

	if got.Model != "gpt-4o-mini" {
		t.Errorf("model = %q, want gpt-4o-mini", got.Model)
	}

	if got.Provider != "openai" || got.APIKey != "sk-main" || got.BaseURL != "https://api.openai.com/v1" {
		t.Errorf("missing fields did not inherit from main LLM: %+v", got)
	}
}

func TestEffectiveRouterConfig_OverridesMain(t *testing.T) {
	cfg := &Config{
		LLM: LLMConfig{Provider: "openai", Model: "gpt-4o", APIKey: "sk-main", BaseURL: "https://api.openai.com/v1"},
	}
	cfg.Orchestrate.Router = LLMConfig{
		Provider: "anthropic",
		Model:    "claude-haiku-4-5",
		APIKey:   "sk-router",
		BaseURL:  "https://api.anthropic.com/v1",
	}

	got, ok := EffectiveRouterConfig(cfg)
	if !ok {
		t.Fatal("router should be enabled")
	}

	if got.Provider != "anthropic" || got.Model != "claude-haiku-4-5" ||
		got.APIKey != "sk-router" || got.BaseURL != "https://api.anthropic.com/v1" {
		t.Errorf("explicit router fields should not be clobbered by main: %+v", got)
	}
}

func TestEffectiveRouterConfig_WhitespaceModelIsDisabled(t *testing.T) {
	cfg := &Config{
		LLM: LLMConfig{Model: "gpt-4o"},
	}
	cfg.Orchestrate.Router = LLMConfig{Model: "   "}

	if _, ok := EffectiveRouterConfig(cfg); ok {
		t.Error("whitespace-only router model must be treated as disabled")
	}
}

func TestEffectiveRouterConfig_NilConfig(t *testing.T) {
	if _, ok := EffectiveRouterConfig(nil); ok {
		t.Error("nil config should be treated as disabled")
	}
}

func TestLoad_RouterFromYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
llm:
  provider: "openai"
  model: "gpt-4o"
  api_key: "sk-main"
  base_url: "https://api.openai.com/v1"
orchestrate:
  router:
    model: "gpt-4o-mini"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Orchestrate.Router.Model != "gpt-4o-mini" {
		t.Errorf("router.model = %q, want gpt-4o-mini", cfg.Orchestrate.Router.Model)
	}

	got, ok := EffectiveRouterConfig(cfg)
	if !ok {
		t.Fatal("router should be enabled after YAML load")
	}

	if got.APIKey != "sk-main" {
		t.Errorf("router APIKey should inherit from main, got %q", got.APIKey)
	}
}

func TestLoad_RouterEnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("llm:\n  model: gpt-4o\n  api_key: sk-main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("VV_ROUTER_MODEL", "claude-haiku-4-5")
	t.Setenv("VV_ROUTER_PROVIDER", "anthropic")
	t.Setenv("VV_ROUTER_API_KEY", "sk-env-router")
	t.Setenv("VV_ROUTER_BASE_URL", "https://api.anthropic.com/v1")

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Orchestrate.Router.Model != "claude-haiku-4-5" {
		t.Errorf("env VV_ROUTER_MODEL did not take effect, got %q", cfg.Orchestrate.Router.Model)
	}
	if cfg.Orchestrate.Router.Provider != "anthropic" {
		t.Errorf("env VV_ROUTER_PROVIDER did not take effect, got %q", cfg.Orchestrate.Router.Provider)
	}
	if cfg.Orchestrate.Router.APIKey != "sk-env-router" {
		t.Errorf("env VV_ROUTER_API_KEY did not take effect, got %q", cfg.Orchestrate.Router.APIKey)
	}
	if cfg.Orchestrate.Router.BaseURL != "https://api.anthropic.com/v1" {
		t.Errorf("env VV_ROUTER_BASE_URL did not take effect, got %q", cfg.Orchestrate.Router.BaseURL)
	}
}

func TestValidateOrchestrateMode(t *testing.T) {
	// As of M6 every input normalises to OrchestrateModeUnified; classical
	// and unknown values trigger a slog.Warn but the call still succeeds —
	// the (string, error) signature is retained for source compatibility,
	// and err is always nil.
	cases := []struct {
		in   string
		want string
	}{
		{"", OrchestrateModeUnified},
		{"unified", OrchestrateModeUnified},
		{"UNIFIED", OrchestrateModeUnified},
		{"  unified  ", OrchestrateModeUnified},
		// classical is now an alias that warns and routes to unified.
		{"classical", OrchestrateModeUnified},
		{"  Classical ", OrchestrateModeUnified},
		// Typos warn and fall back to unified instead of failing Load.
		{"unifiec", OrchestrateModeUnified},
		{"primary", OrchestrateModeUnified},
	}

	for _, tc := range cases {
		got, err := ValidateOrchestrateMode(tc.in)
		if err != nil {
			t.Errorf("ValidateOrchestrateMode(%q) returned err=%v, want nil", tc.in, err)
		}

		if got != tc.want {
			t.Errorf("ValidateOrchestrateMode(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestLoad_OrchestrateModeFromYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := `
llm:
  provider: "openai"
  model: "gpt-4o"
  api_key: "sk-main"
orchestrate:
  mode: "unified"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Orchestrate.Mode != OrchestrateModeUnified {
		t.Errorf("mode = %q, want %q", cfg.Orchestrate.Mode, OrchestrateModeUnified)
	}
}

func TestLoad_OrchestrateModeEnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("llm:\n  model: gpt-4o\n  api_key: sk-main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("VV_ORCHESTRATE_MODE", "unified")

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Orchestrate.Mode != OrchestrateModeUnified {
		t.Errorf("env VV_ORCHESTRATE_MODE did not take effect, got %q", cfg.Orchestrate.Mode)
	}
}

// TestLoad_PrimaryAllowBashEnvOverride pins the M6 G1 env contract:
// VV_PRIMARY_ALLOW_BASH=true|false flips the YAML default at Load time
// without requiring a config edit. Invalid values warn and leave the
// YAML setting intact.
func TestLoad_PrimaryAllowBashEnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("llm:\n  model: gpt-4o\n  api_key: sk-main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("VV_PRIMARY_ALLOW_BASH", "true")

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !cfg.Orchestrate.PrimaryAllowBash {
		t.Errorf("env VV_PRIMARY_ALLOW_BASH=true did not take effect, got %v", cfg.Orchestrate.PrimaryAllowBash)
	}
}

// TestLoad_OrchestrateModeUnknownDoesNotReject pins the M6 behaviour: an
// unknown orchestrate.mode value (typo, removed mode, etc.) must NOT abort
// Load — ValidateOrchestrateMode warns and falls back to unified so the
// process keeps starting. This replaces the M5
// TestLoad_OrchestrateModeRejectsUnknown.
func TestLoad_OrchestrateModeUnknownDoesNotReject(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("llm:\n  model: gpt-4o\n  api_key: sk-main\norchestrate:\n  mode: bogus\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load should not fail with unknown orchestrate.mode (M6 behaviour); got %v", err)
	}

	if cfg.Orchestrate.Mode != OrchestrateModeUnified {
		t.Errorf("unknown mode should normalise to unified; got %q", cfg.Orchestrate.Mode)
	}
}

func TestLoad_OrchestrateModeDefaultsToUnified(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("llm:\n  model: gpt-4o\n  api_key: sk-main\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Orchestrate.Mode != OrchestrateModeUnified {
		t.Errorf("mode = %q, want %q (M5 default)", cfg.Orchestrate.Mode, OrchestrateModeUnified)
	}
}

// TestLoad_OrchestrateModeExplicitClassicalRoutesToUnified pins the M6
// behaviour: an explicit `orchestrate.mode: classical` carried over from an
// M5 config must normalise to unified at Load time (with a slog.Warn,
// emitted by ValidateOrchestrateMode) rather than being preserved.
func TestLoad_OrchestrateModeExplicitClassicalRoutesToUnified(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := "llm:\n  model: gpt-4o\n  api_key: sk-main\norchestrate:\n  mode: classical\n"

	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Orchestrate.Mode != OrchestrateModeUnified {
		t.Errorf("mode = %q, want %q — classical must normalise to unified as of M6",
			cfg.Orchestrate.Mode, OrchestrateModeUnified)
	}
}
