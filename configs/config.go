package configs

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/vogo/aimodel"
	"github.com/vogo/vv/traces/costtraces"
	"gopkg.in/yaml.v3"
)

// MemoryConfig holds memory configuration.
type MemoryConfig struct {
	Dir            string `yaml:"dir"`             // default ~/.vv/memory/
	SessionWindow  int    `yaml:"session_window"`  // sliding window size, default 50
	PersistentLoad bool   `yaml:"persistent_load"` // load at startup, default true
	MaxConcurrency int    `yaml:"max_concurrency"` // Deprecated: use orchestrate.max_concurrency
}

// OrchestrateConfig holds orchestration configuration.
type OrchestrateConfig struct {
	MaxConcurrency    int          `yaml:"max_concurrency"`     // DAG concurrency, default 2
	MaxRecursionDepth int          `yaml:"max_recursion_depth"` // max nesting depth, default 2
	SummaryPolicy     string       `yaml:"summary_policy"`      // auto/always/never
	Replan            ReplanConfig `yaml:"replan"`
}

// ReplanConfig holds replanning configuration.
type ReplanConfig struct {
	TriggerOnFailure   bool `yaml:"trigger_on_failure"`
	TriggerOnDeviation bool `yaml:"trigger_on_deviation"` // reserved for future use
	MaxReplans         int  `yaml:"max_replans"`
}

// DefaultDir returns the default vv config directory (~/.vv).
func DefaultDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(".", ".vv")
	}

	return filepath.Join(home, ".vv")
}

// DefaultPath returns the default config file path (~/.vv/vv.yaml).
func DefaultPath() string {
	return filepath.Join(DefaultDir(), "vv.yaml")
}

// Config holds all vv application configuration.
type Config struct {
	LLM          LLMConfig                    `yaml:"llm"`
	Server       ServerConfig                 `yaml:"server"`
	Tools        ToolsConfig                  `yaml:"tools"`
	Agents       AgentsConfig                 `yaml:"agents"`
	Mode         string                       `yaml:"mode"` // "cli" or "http"; default "cli"
	CLI          CLIConfig                    `yaml:"cli"`
	Memory       MemoryConfig                 `yaml:"memory"`
	Orchestrate  OrchestrateConfig            `yaml:"orchestrate"`
	Context      ContextConfig                `yaml:"context"`
	ModelPricing map[string]ModelPricingEntry `yaml:"model_pricing,omitempty"`
	Debug        bool                         `yaml:"debug,omitempty"` // CLI > env (VV_DEBUG) > YAML > false

	// ProjectInstructions holds content loaded from VV.md in the working directory.
	// Runtime-only; not persisted to vv.yaml.
	ProjectInstructions string `yaml:"-"`
}

// PermissionMode defines the tool permission mode for CLI mode.
type PermissionMode string

const (
	PermissionModeDefault     PermissionMode = "default"
	PermissionModeAcceptEdits PermissionMode = "accept-edits"
	PermissionModeAuto        PermissionMode = "auto"
	PermissionModePlan        PermissionMode = "plan"
)

// ValidPermissionModes lists all valid permission mode values.
var ValidPermissionModes = []PermissionMode{
	PermissionModeDefault,
	PermissionModeAcceptEdits,
	PermissionModeAuto,
	PermissionModePlan,
}

// IsValidPermissionMode returns true if the given mode is a recognized permission mode.
func IsValidPermissionMode(m PermissionMode) bool {
	return slices.Contains(ValidPermissionModes, m)
}

// CLIConfig holds CLI-specific configuration.
type CLIConfig struct {
	ConfirmTools   []string       `yaml:"confirm_tools,omitempty"`   // DEPRECATED: use PermissionMode
	PermissionMode PermissionMode `yaml:"permission_mode,omitempty"` // tool permission mode
}

// LLMConfig holds LLM provider configuration.
type LLMConfig struct {
	Provider string `yaml:"provider"` // "openai" or "anthropic"
	Model    string `yaml:"model"`    // e.g. "gpt-4o", "claude-sonnet-4"
	APIKey   string `yaml:"api_key"`  // overridden by VV_LLM_API_KEY; never log this value
	BaseURL  string `yaml:"base_url"` // overridden by VV_LLM_BASE_URL
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
	AskUserTimeout int `yaml:"ask_user_timeout"` // seconds, default 300 (5 minutes)
}

// ModelPricingEntry defines cost rates for a model (USD per million tokens).
type ModelPricingEntry struct {
	InputPerMTokens  float64 `json:"input_per_m_tokens" yaml:"input_per_m_tokens"`
	OutputPerMTokens float64 `json:"output_per_m_tokens" yaml:"output_per_m_tokens"`
	CachePerMTokens  float64 `json:"cache_per_m_tokens,omitempty" yaml:"cache_per_m_tokens,omitempty"`
}

// ContextConfig holds conversation context compression configuration.
type ContextConfig struct {
	ModelMaxContextTokens int      `yaml:"model_max_context_tokens"` // default: 128000
	CompressionThreshold  *float64 `yaml:"compression_threshold"`    // default: 0.8; pointer to distinguish "not set" from 0.0
	ToolOutputMaxTokens   int      `yaml:"tool_output_max_tokens"`   // default: 8000
	ProtectedTurns        int      `yaml:"context_protected_turns"`  // default: 4
}

// EffectiveCompressionThreshold returns the compression threshold,
// falling back to 0.8 if not explicitly set.
func (c ContextConfig) EffectiveCompressionThreshold() float64 {
	if c.CompressionThreshold != nil {
		return *c.CompressionThreshold
	}

	return 0.8
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
	if v := os.Getenv("VV_LLM_API_KEY"); v != "" {
		cfg.LLM.APIKey = v
	}

	if v := os.Getenv("VV_LLM_BASE_URL"); v != "" {
		cfg.LLM.BaseURL = v
	}

	if v := os.Getenv("VV_LLM_MODEL"); v != "" {
		cfg.LLM.Model = v
	}

	if v := os.Getenv("VV_LLM_PROVIDER"); v != "" {
		cfg.LLM.Provider = v
	}

	if v := os.Getenv("VV_SERVER_ADDR"); v != "" {
		cfg.Server.Addr = v
	}

	if v := os.Getenv("VV_MODE"); v != "" {
		cfg.Mode = v
	}

	if v := os.Getenv("VV_PERMISSION_MODE"); v != "" {
		cfg.CLI.PermissionMode = PermissionMode(v)
	}

	if v := os.Getenv("VV_MAX_CONTEXT_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Context.ModelMaxContextTokens = n
		}
	}

	if v := os.Getenv("VV_CONTEXT_COMPRESSION_THRESHOLD"); v != "" {
		if f, err := strconv.ParseFloat(v, 64); err == nil {
			cfg.Context.CompressionThreshold = &f
		}
	}

	if v := os.Getenv("VV_TOOL_OUTPUT_MAX_TOKENS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Context.ToolOutputMaxTokens = n
		}
	}

	if v := os.Getenv("VV_CONTEXT_PROTECTED_TURNS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Context.ProtectedTurns = n
		}
	}

	if v := os.Getenv("VV_DEBUG"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Debug = b
		} else {
			slog.Warn("vv: invalid VV_DEBUG, ignoring", "value", v)
		}
	}

	if v := os.Getenv("VV_MODEL_PRICING"); v != "" {
		var mp map[string]ModelPricingEntry
		if err := json.Unmarshal([]byte(v), &mp); err != nil {
			slog.Warn("vv: invalid VV_MODEL_PRICING JSON, ignoring", "error", err)
		} else {
			if cfg.ModelPricing == nil {
				cfg.ModelPricing = mp
			} else {
				maps.Copy(cfg.ModelPricing, mp)
			}
		}
	}

	applyDefaults(cfg)

	if !IsValidPermissionMode(cfg.CLI.PermissionMode) {
		return nil, fmt.Errorf("invalid permission_mode %q; valid values: default, accept-edits, auto, plan",
			cfg.CLI.PermissionMode)
	}

	if len(cfg.CLI.ConfirmTools) > 0 {
		slog.Warn("vv: confirm_tools is deprecated; use permission_mode instead")
	}

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

	if cfg.CLI.PermissionMode == "" {
		cfg.CLI.PermissionMode = PermissionModeDefault
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

	if cfg.Agents.AskUserTimeout == 0 {
		cfg.Agents.AskUserTimeout = 300
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

	// Migrate MaxConcurrency from memory to orchestrate.
	if cfg.Memory.MaxConcurrency != 0 && cfg.Orchestrate.MaxConcurrency == 0 {
		cfg.Orchestrate.MaxConcurrency = cfg.Memory.MaxConcurrency
	}
	if cfg.Orchestrate.MaxConcurrency == 0 {
		cfg.Orchestrate.MaxConcurrency = 2
	}

	if cfg.Orchestrate.MaxRecursionDepth == 0 {
		cfg.Orchestrate.MaxRecursionDepth = 2
	}

	if cfg.Orchestrate.SummaryPolicy == "" {
		cfg.Orchestrate.SummaryPolicy = "auto"
	}

	if cfg.Orchestrate.Replan.MaxReplans == 0 {
		cfg.Orchestrate.Replan.MaxReplans = 2
	}

	// Context compression defaults.
	if cfg.Context.ModelMaxContextTokens == 0 {
		cfg.Context.ModelMaxContextTokens = 128000
	}

	if cfg.Context.ToolOutputMaxTokens == 0 {
		cfg.Context.ToolOutputMaxTokens = 8000
	}

	if cfg.Context.ProtectedTurns == 0 {
		cfg.Context.ProtectedTurns = 4
	}
	// CompressionThreshold uses a pointer; nil means "use default 0.8" via EffectiveCompressionThreshold().
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

// ConvertPricing converts config pricing entries to costtraces pricing entries.
func ConvertPricing(entries map[string]ModelPricingEntry) map[string]costtraces.Pricing {
	if len(entries) == 0 {
		return nil
	}

	result := make(map[string]costtraces.Pricing, len(entries))

	for k, v := range entries {
		result[k] = costtraces.Pricing{
			InputPerMTokens:  v.InputPerMTokens,
			OutputPerMTokens: v.OutputPerMTokens,
			CachePerMTokens:  v.CachePerMTokens,
		}
	}

	return result
}

// NewLLMClient creates an aimodel.Client from the LLM configuration.
func NewLLMClient(cfg LLMConfig) (*aimodel.Client, error) {
	opts := []aimodel.Option{
		aimodel.WithDefaultModel(cfg.Model),
	}

	// Only set API key if explicitly configured (via YAML or VV_LLM_API_KEY).
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
