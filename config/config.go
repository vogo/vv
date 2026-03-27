package config

import (
	"bufio"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/vogo/aimodel"
	"gopkg.in/yaml.v3"
)

// MemoryConfig holds memory configuration.
type MemoryConfig struct {
	Dir            string `yaml:"dir"`             // default ~/.vaga/memory/
	SessionWindow  int    `yaml:"session_window"`  // sliding window size, default 50
	PersistentLoad bool   `yaml:"persistent_load"` // load at startup, default true
	MaxConcurrency int    `yaml:"max_concurrency"` // orchestrator DAG concurrency, default 2
	// TODO: MaxConcurrency is orchestration config, not memory config.
	// Move to a dedicated OrchestratorConfig in a future cleanup.
}

// DefaultDir returns the default vaga config directory (~/.vaga).
func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".vaga")
	}

	return filepath.Join(home, ".vaga")
}

// DefaultPath returns the default config file path (~/.vaga/vaga.yaml).
func DefaultPath() string {
	return filepath.Join(DefaultDir(), "vaga.yaml")
}

// Config holds all vaga application configuration.
type Config struct {
	LLM    LLMConfig    `yaml:"llm"`
	Server ServerConfig `yaml:"server"`
	Tools  ToolsConfig  `yaml:"tools"`
	Agents AgentsConfig `yaml:"agents"`
	Mode   string       `yaml:"mode"` // "cli" or "http"; default "cli"
	CLI    CLIConfig    `yaml:"cli"`
	Memory MemoryConfig `yaml:"memory"`
}

// CLIConfig holds CLI-specific configuration.
type CLIConfig struct {
	ConfirmTools []string `yaml:"confirm_tools"` // tool names requiring confirmation
}

// LLMConfig holds LLM provider configuration.
type LLMConfig struct {
	Provider string `yaml:"provider"` // "openai" or "anthropic"
	Model    string `yaml:"model"`    // e.g. "gpt-4o", "claude-sonnet-4"
	APIKey   string `yaml:"api_key"`  // overridden by VAGA_LLM_API_KEY; never log this value
	BaseURL  string `yaml:"base_url"` // overridden by VAGA_LLM_BASE_URL
}

// ServerConfig holds HTTP server configuration.
type ServerConfig struct {
	Addr string `yaml:"addr"` // default ":8080"
}

// ToolsConfig holds tool configuration.
type ToolsConfig struct {
	BashTimeout    int    `yaml:"bash_timeout"`     // seconds, default 30
	BashWorkingDir string `yaml:"bash_working_dir"` // default ""
}

// AgentsConfig holds agent configuration.
type AgentsConfig struct {
	MaxIterations  int `yaml:"max_iterations"`   // default 10
	RunTokenBudget int `yaml:"run_token_budget"` // default 0 (unlimited)
}

// Load loads configuration from a YAML file with environment variable overrides.
// If explicit is true and the file does not exist, an error is returned.
// If explicit is false and the file does not exist, a zero-value config is used.
func Load(path string, explicit bool) (*Config, error) {
	cfg := &Config{}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) && !explicit {
			// Default path missing is fine; proceed with zero-value config.
		} else {
			return nil, fmt.Errorf("read config file %s: %w", path, err)
		}
	} else {
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("parse config file %s: %w", path, err)
		}
	}

	// Apply environment variable overrides.
	if v := os.Getenv("VAGA_LLM_API_KEY"); v != "" {
		cfg.LLM.APIKey = v
	}

	if v := os.Getenv("VAGA_LLM_BASE_URL"); v != "" {
		cfg.LLM.BaseURL = v
	}

	if v := os.Getenv("VAGA_LLM_MODEL"); v != "" {
		cfg.LLM.Model = v
	}

	if v := os.Getenv("VAGA_LLM_PROVIDER"); v != "" {
		cfg.LLM.Provider = v
	}

	if v := os.Getenv("VAGA_SERVER_ADDR"); v != "" {
		cfg.Server.Addr = v
	}

	if v := os.Getenv("VAGA_MODE"); v != "" {
		cfg.Mode = v
	}

	applyDefaults(cfg)

	return cfg, nil
}

// NeedsSetup returns true if required configuration is missing.
func NeedsSetup(cfg *Config) bool {
	return cfg.LLM.APIKey == ""
}

// Prompt interactively asks the user for configuration values, fills in the
// given Config, and saves the result to path.
func Prompt(cfg *Config, path string, r io.Reader, w io.Writer) error {
	scanner := bufio.NewScanner(r)

	cfg.LLM.Provider = prompt(scanner, w,
		"LLM provider (openai/anthropic)",
		cfg.LLM.Provider, "openai")

	defaultModel := "gpt-4o"
	if cfg.LLM.Provider == "anthropic" {
		defaultModel = "claude-sonnet-4"
	}

	cfg.LLM.Model = prompt(scanner, w,
		"LLM model",
		cfg.LLM.Model, defaultModel)

	cfg.LLM.APIKey = prompt(scanner, w,
		"LLM API key",
		cfg.LLM.APIKey, "")

	if cfg.LLM.Provider == "openai" || cfg.LLM.Provider == "" {
		cfg.LLM.BaseURL = prompt(scanner, w,
			"LLM base URL",
			cfg.LLM.BaseURL, "https://api.openai.com/v1")
	} else {
		// Clear any base URL default from a previous provider setting.
		cfg.LLM.BaseURL = ""
	}

	cfg.Server.Addr = prompt(scanner, w,
		"Server listen address",
		cfg.Server.Addr, ":8080")

	applyDefaults(cfg)

	return Save(cfg, path)
}

// Save writes the config to the given path, creating parent directories as needed.
func Save(cfg *Config, path string) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return fmt.Errorf("create config directory: %w", err)
	}

	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("marshal config: %w", err)
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write config file %s: %w", path, err)
	}

	return nil
}

func applyDefaults(cfg *Config) {
	if cfg.Mode == "" {
		cfg.Mode = "cli"
	}

	if cfg.Server.Addr == "" {
		cfg.Server.Addr = ":8080"
	}

	if cfg.Agents.MaxIterations == 0 {
		cfg.Agents.MaxIterations = 10
	}

	if cfg.Tools.BashTimeout == 0 {
		cfg.Tools.BashTimeout = 30
	}

	// Provider-specific defaults.
	if cfg.LLM.Provider == "openai" || cfg.LLM.Provider == "" {
		if cfg.LLM.BaseURL == "" {
			cfg.LLM.BaseURL = "https://api.openai.com/v1"
		}
	}

	// Memory defaults.
	if cfg.Memory.SessionWindow == 0 {
		cfg.Memory.SessionWindow = 50
	}
	if cfg.Memory.Dir == "" {
		cfg.Memory.Dir = filepath.Join(DefaultDir(), "memory")
	}
	if cfg.Memory.MaxConcurrency == 0 {
		cfg.Memory.MaxConcurrency = 2
	}
}

func prompt(scanner *bufio.Scanner, w io.Writer, label, current, defaultVal string) string {
	display := defaultVal
	if current != "" {
		display = current
	}

	if display != "" {
		_, _ = fmt.Fprintf(w, "  %s [%s]: ", label, display)
	} else {
		_, _ = fmt.Fprintf(w, "  %s: ", label)
	}

	if scanner.Scan() {
		if v := strings.TrimSpace(scanner.Text()); v != "" {
			return v
		}
	}

	if current != "" {
		return current
	}

	return defaultVal
}

// NewLLMClient creates an aimodel.Client from the LLM configuration.
func NewLLMClient(cfg LLMConfig) (*aimodel.Client, error) {
	opts := []aimodel.Option{
		aimodel.WithDefaultModel(cfg.Model),
	}

	// Only set API key if explicitly configured (via YAML or VAGA_LLM_API_KEY).
	// Otherwise, let aimodel.NewClient fall back to its own env var reading
	// (AI_API_KEY, OPENAI_API_KEY, ANTHROPIC_API_KEY).
	if cfg.APIKey != "" {
		opts = append(opts, aimodel.WithAPIKey(cfg.APIKey))
	}

	if cfg.BaseURL != "" {
		opts = append(opts, aimodel.WithBaseURL(cfg.BaseURL))
	}

	switch cfg.Provider {
	case "anthropic":
		opts = append(opts, aimodel.WithProtocol(aimodel.ProtocolAnthropic))
	case "openai", "":
		// ProtocolOpenAI is the default.
		// OpenAI protocol requires a base URL. When called from main(),
		// Load already sets this default. This fallback ensures
		// NewLLMClient works correctly when called standalone (e.g., tests).
		if cfg.BaseURL == "" {
			opts = append(opts, aimodel.WithBaseURL("https://api.openai.com/v1"))
		}
	default:
		return nil, fmt.Errorf("unsupported LLM provider: %q (supported: openai, anthropic)", cfg.Provider)
	}

	return aimodel.NewClient(opts...)
}
