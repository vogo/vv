package configs

import (
	"bufio"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"maps"
	"net"
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
	Backend        string `yaml:"backend"`         // "file" (default) | "sqlite"
}

// Supported memory backend identifiers.
const (
	MemoryBackendFile   = "file"
	MemoryBackendSQLite = "sqlite"
)

// ValidateMemoryBackend normalizes and validates cfg.Memory.Backend. An empty
// value is treated as "file" so existing configs continue to work unchanged.
func ValidateMemoryBackend(backend string) (string, error) {
	b := strings.ToLower(strings.TrimSpace(backend))
	switch b {
	case "", MemoryBackendFile:
		return MemoryBackendFile, nil
	case MemoryBackendSQLite:
		return MemoryBackendSQLite, nil
	default:
		return "", fmt.Errorf(
			"unknown memory backend %q (expected %q or %q)",
			backend, MemoryBackendFile, MemoryBackendSQLite,
		)
	}
}

// OrchestrateConfig holds orchestration configuration.
type OrchestrateConfig struct {
	MaxConcurrency    int `yaml:"max_concurrency"`     // DAG concurrency, default 2
	MaxRecursionDepth int `yaml:"max_recursion_depth"` // max nesting depth, default 2

	// Router optionally points the dispatcher's routing/classification LLM
	// calls at a cheaper/smaller model. Any empty field inherits from
	// Config.LLM; an empty Router.Model leaves the feature disabled.
	Router LLMConfig `yaml:"router,omitempty"`

	// Mode is kept as a YAML field only for backwards compatibility with
	// M4–M6 configs. As of M7 only the unified Primary Assistant pipeline
	// exists and the field is silently normalised to "unified" by
	// ValidateOrchestrateMode; any other value (including the long-removed
	// "classical") emits a slog.Warn but does not abort startup. Other
	// stale orchestrate.* keys (legacy_phase_events, fast_path,
	// unified_intent, summary_policy, replan) trigger one slog.Warn per
	// key during Load via the raw-YAML stale-key sweep.
	Mode string `yaml:"mode,omitempty"`

	// PrimaryAllowBash, when true, mounts the bash tool on the Primary
	// Assistant so single-line shell tasks finish inline without
	// delegate_to_coder. Off by default. Env override:
	// VV_PRIMARY_ALLOW_BASH. The fallback (depth-exceeded) Primary
	// always stays tool-free regardless.
	PrimaryAllowBash bool `yaml:"primary_allow_bash,omitempty"`
}

// OrchestrateModeUnified is the only supported orchestrate pipeline as of
// M7. Empty `orchestrate.mode` normalises to this value.
const OrchestrateModeUnified = "unified"

// staleOrchestrateKeys lists YAML keys under `orchestrate:` that were
// removed in M7 along with the classical pipeline. They are silently
// dropped on unmarshal (the struct fields no longer exist), but Load
// surfaces a slog.Warn per occurrence so existing vv.yaml files surface
// the deprecation rather than vanish silently.
var staleOrchestrateKeys = []string{
	"summary_policy",
	"replan",
	"fast_path",
	"unified_intent",
	"legacy_phase_events",
}

// warnStaleOrchestrateKeys re-parses raw YAML so it can detect `orchestrate.*`
// keys that no longer have a struct field to land on. Anything other than a
// well-formed YAML mapping is treated as "no warnings" — the strict
// unmarshal in Load already surfaced syntax errors.
func warnStaleOrchestrateKeys(data []byte) {
	var raw struct {
		Orchestrate map[string]any `yaml:"orchestrate"`
	}

	if err := yaml.Unmarshal(data, &raw); err != nil {
		return
	}

	for _, key := range staleOrchestrateKeys {
		if _, present := raw.Orchestrate[key]; present {
			slog.Warn(
				"vv: orchestrate."+key+" is deprecated as of M7; the key is silently ignored",
				"key", key,
			)
		}
	}
}

// ValidateOrchestrateMode normalises mode strings. As of M7 the unified
// pipeline is the sole supported path; "" / "unified" pass through, any
// other value (including the long-removed "classical") is normalised to
// OrchestrateModeUnified with a slog.Warn so misconfiguration is surfaced
// without aborting startup.
//
// The error return is retained so the (string, error) signature stays
// source-compatible with M5 callers; it is always nil.
func ValidateOrchestrateMode(mode string) (string, error) {
	m := strings.ToLower(strings.TrimSpace(mode))
	switch m {
	case "", OrchestrateModeUnified:
		return OrchestrateModeUnified, nil
	default:
		slog.Warn(
			"vv: orchestrate.mode is deprecated as of M7; the field is ignored and the unified pipeline is always used",
			"received", mode,
		)

		return OrchestrateModeUnified, nil
	}
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
	Mode         string                       `yaml:"mode"` // "cli", "http", or "mcp"; default "cli"
	CLI          CLIConfig                    `yaml:"cli"`
	Memory       MemoryConfig                 `yaml:"memory"`
	Orchestrate  OrchestrateConfig            `yaml:"orchestrate"`
	Context      ContextConfig                `yaml:"context"`
	Security     SecurityConfig               `yaml:"security,omitempty"`
	MCP          MCPConfig                    `yaml:"mcp,omitempty"`
	Eval         EvalConfig                   `yaml:"eval,omitempty"`
	ModelPricing map[string]ModelPricingEntry `yaml:"model_pricing,omitempty"`
	Budget       BudgetConfig                 `yaml:"budget,omitempty"`
	Trace        TraceConfig                  `yaml:"trace,omitempty"`
	Debug        bool                         `yaml:"debug,omitempty"` // CLI > env (VV_DEBUG) > YAML > false

	// ProjectInstructions holds content loaded from VV.md in the working directory.
	// Runtime-only; not persisted to vv.yaml.
	ProjectInstructions string `yaml:"-"`
}

// MCPConfig groups MCP-related configuration. Currently only the `server`
// subsection is defined; MCP client settings live under `security.mcp_credential_filter`.
type MCPConfig struct {
	Server MCPServerConfig `yaml:"server,omitempty"`
}

// MCPServerConfig configures the MCP server exposed by `vv --mode mcp`.
//
// Transport defaults to "stdio" (process-isolated, no auth). When Transport
// is "http", the server uses Streamable HTTP; non-loopback binds require
// AuthToken to be set.
type MCPServerConfig struct {
	Transport        string   `yaml:"transport,omitempty"`         // "stdio" (default) | "http"
	Addr             string   `yaml:"addr,omitempty"`              // default "127.0.0.1:7801" when Transport="http"
	AuthToken        string   `yaml:"auth_token,omitempty"`        // required for non-loopback binds
	Agents           []string `yaml:"agents,omitempty"`            // empty = all dispatchable
	ExposeDispatcher bool     `yaml:"expose_dispatcher,omitempty"` // default false
	SessionTimeout   int      `yaml:"session_timeout,omitempty"`   // seconds; 0 = no timeout; http only
}

// SecurityConfig groups security subsystems (tool-result injection scanning,
// MCP credential filter, etc.).
type SecurityConfig struct {
	ToolResultInjection ToolResultInjectionConfig `yaml:"tool_result_injection,omitempty"`
	MCPCredentialFilter MCPCredentialFilterConfig `yaml:"mcp_credential_filter,omitempty"`
}

// MCPCredentialFilterConfig controls the MCP credential filter middleware.
// It scans tool arguments (outbound) and tool results (inbound) on the MCP
// client, and handler inputs/outputs on the MCP server, to prevent
// credentials (AWS keys, JWTs, PEM private keys, etc.) from leaking to or
// from third-party MCP servers.
//
// Default posture: enabled=true, action=redact, max_scan_bytes=256 KiB.
type MCPCredentialFilterConfig struct {
	// Enabled gates the feature. nil means "use the default" (true).
	Enabled *bool `yaml:"enabled,omitempty"`

	// Action is taken on any hit: "log" | "redact" | "block". Default "redact".
	Action string `yaml:"action,omitempty"`

	// MaxScanBytes caps scanned text length. 0 = default 256*1024.
	MaxScanBytes int `yaml:"max_scan_bytes,omitempty"`

	// ExtraPatterns are additional user-supplied Go regex patterns. Each
	// invalid pattern is logged and skipped; it does not fail config loading.
	// Each pattern scans without keyword gating and flags the match as
	// type "custom".
	ExtraPatterns []string `yaml:"extra_patterns,omitempty"`

	// Allowlist extends the default allowlist with user regexes. A match
	// whose entire text matches any allowlist regex is dropped.
	Allowlist []string `yaml:"allowlist,omitempty"`
}

// IsEnabled returns true unless the user explicitly set `enabled: false`.
func (m MCPCredentialFilterConfig) IsEnabled() bool {
	return m.Enabled == nil || *m.Enabled
}

// ToolResultInjectionConfig controls the tool-result injection scanning guard.
// Default posture: enabled=true, action=log, block_on_severity=high — low/medium
// hits are recorded only; high-severity structural attacks are blocked.
type ToolResultInjectionConfig struct {
	// Enabled gates the feature. nil means "use the default" (true).
	Enabled *bool `yaml:"enabled,omitempty"`

	// Action is taken on any low/medium hit: "log" | "rewrite" | "block".
	Action string `yaml:"action,omitempty"`

	// BlockOnSeverity escalates to block when a hit meets this severity:
	// "" (disabled), "low", "medium", "high". Default "high".
	BlockOnSeverity string `yaml:"block_on_severity,omitempty"`

	// MaxScanBytes caps the scanned text length. Default 256*1024.
	MaxScanBytes int `yaml:"max_scan_bytes,omitempty"`
}

// IsEnabled returns true unless the user explicitly set `enabled: false`.
func (t ToolResultInjectionConfig) IsEnabled() bool {
	return t.Enabled == nil || *t.Enabled
}

// EvalConfig controls the evaluation subsystem (CLI `-eval` + HTTP
// `POST /v1/eval/run`). Defaults: disabled for HTTP surface, concurrency 1,
// 60s per-case timeout, latency+cost evaluators (no extra LLM calls).
type EvalConfig struct {
	// Enabled gates the HTTP `/v1/eval/run` endpoint. The CLI `-eval` flag
	// does not consult this field — it always works when the user passes it.
	Enabled bool `yaml:"enabled,omitempty"`

	// Concurrency is the number of cases evaluated in parallel. <=0 → 1.
	Concurrency int `yaml:"concurrency,omitempty"`

	// TimeoutMs caps each case's wall-clock time. 0 → 60000.
	TimeoutMs int `yaml:"timeout_ms,omitempty"`

	// Evaluators selects which evaluators run. Valid values:
	// "latency", "cost", "contains", "llm_judge". Empty → ["latency","cost"].
	Evaluators []string `yaml:"evaluators,omitempty"`

	// LatencyThresholdMs is the pass threshold for the latency evaluator.
	// 0 → 60000.
	LatencyThresholdMs int64 `yaml:"latency_threshold_ms,omitempty"`

	// CostBudgetTokens is the token budget for the cost evaluator. 0 → 10000.
	CostBudgetTokens int `yaml:"cost_budget_tokens,omitempty"`

	// ContainsKeywords is required when "contains" is in Evaluators.
	ContainsKeywords []string `yaml:"contains_keywords,omitempty"`

	// LLMJudgeModel overrides the main LLM model for the LLM judge evaluator.
	// Empty → use cfg.LLM.Model.
	LLMJudgeModel string `yaml:"llm_judge_model,omitempty"`
}

// ValidateEval validates eval configuration. Called from Load after
// applyDefaults. Returns an error for unknown evaluator names so the user
// hears about typos at startup rather than at HTTP/CLI invocation time.
func ValidateEval(c *EvalConfig) error {
	if c.TimeoutMs < 0 {
		return fmt.Errorf("eval.timeout_ms must be >= 0, got %d", c.TimeoutMs)
	}

	for _, name := range c.Evaluators {
		switch strings.ToLower(strings.TrimSpace(name)) {
		case "latency", "cost", "contains", "llm_judge":
		default:
			return fmt.Errorf("eval.evaluators: unknown evaluator %q; valid: latency, cost, contains, llm_judge", name)
		}
	}

	return nil
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
	BashTimeout    int             `yaml:"bash_timeout"`           // seconds, default 30
	BashWorkingDir string          `yaml:"bash_working_dir"`       // default ""
	AllowedDirs    *[]string       `yaml:"allowed_dirs,omitempty"` // workspace allow-list; nil = auto-populate defaults, empty = startup error
	BashRules      BashRulesConfig `yaml:"bash_rules,omitempty"`   // dangerous-command classification
}

// BashRulesConfig controls the pre-execution bash command classifier.
type BashRulesConfig struct {
	// Enabled gates the classifier. A nil pointer means "use the default" (true);
	// explicit `false` disables the feature entirely so behaviour matches the pre-feature baseline.
	Enabled *bool `yaml:"enabled,omitempty"`

	// UserBlocked, UserDangerous, UserSafe extend the built-in rule library.
	// Each entry is a regular expression matched against every sub-command.
	// Invalid entries are logged and skipped; they do not fail config loading.
	UserBlocked   []string `yaml:"user_blocked,omitempty"`
	UserDangerous []string `yaml:"user_dangerous,omitempty"`
	UserSafe      []string `yaml:"user_safe,omitempty"`
}

// IsEnabled returns true unless the user explicitly set `enabled: false`.
func (b BashRulesConfig) IsEnabled() bool {
	return b.Enabled == nil || *b.Enabled
}

// AgentsConfig holds agent configuration.
type AgentsConfig struct {
	MaxIterations  int `yaml:"max_iterations"`   // default 10
	RunTokenBudget int `yaml:"run_token_budget"` // default 0 (unlimited)
	AskUserTimeout int `yaml:"ask_user_timeout"` // seconds, default 300 (5 minutes)
	// MaxParallelToolCalls caps concurrent tool dispatch within a single
	// assistant message. 0 uses the framework default (4); <=1 serializes.
	MaxParallelToolCalls int `yaml:"max_parallel_tool_calls"`
	// PromptCaching, nil-default-on, controls emission of prompt-cache
	// boundary hints on the system message and the last tool definition.
	// Set to a pointer to false to disable. No on-wire effect for OpenAI
	// backends — OpenAI prefix-caches automatically.
	PromptCaching *bool `yaml:"prompt_caching"`
}

// EffectivePromptCaching resolves the nil-default-on pointer. nil / unset
// yields true; only an explicit false disables.
func (c *AgentsConfig) EffectivePromptCaching() bool {
	if c == nil || c.PromptCaching == nil {
		return true
	}
	return *c.PromptCaching
}

// BudgetConfig holds session- and daily-level token/cost enforcement limits.
// All fields are opt-in: zero values mean "unlimited / disabled". Session limits
// apply for the lifetime of a CLI session or HTTP process; daily limits roll
// forward automatically at UTC 00:00 (in-memory; restart clears the counter).
type BudgetConfig struct {
	SessionHardTokens  int64   `yaml:"session_hard_tokens,omitempty"`   // 0 = off
	SessionHardCostUSD float64 `yaml:"session_hard_cost_usd,omitempty"` // 0 = off
	DailyHardTokens    int64   `yaml:"daily_hard_tokens,omitempty"`     // 0 = off
	DailyHardCostUSD   float64 `yaml:"daily_hard_cost_usd,omitempty"`   // 0 = off
	WarnPercent        float64 `yaml:"warn_percent,omitempty"`          // 0 → 0.8 default; shared by both layers
}

// IsEnabled reports whether any session- or daily-level limit is configured.
func (b BudgetConfig) IsEnabled() bool {
	return b.SessionHardTokens > 0 || b.SessionHardCostUSD > 0 ||
		b.DailyHardTokens > 0 || b.DailyHardCostUSD > 0
}

// TraceConfig controls structured conversation trace logging (JSONL) via an
// AsyncHook on the vage event bus. Opt-in: when Enabled is nil or false,
// no hook.Manager is installed and there is zero runtime cost. Trace files
// land at <Dir>/<project-hash>/<session-id>.jsonl with size-based rotation.
type TraceConfig struct {
	Enabled      *bool  `yaml:"enabled,omitempty"`        // default false
	Dir          string `yaml:"dir,omitempty"`            // default ~/.vv/traces
	MaxFileBytes int64  `yaml:"max_file_bytes,omitempty"` // default 64 MiB; 0 = no rotation
	BufferSize   int    `yaml:"buffer_size,omitempty"`    // default 1024
}

// IsEnabled returns true only when the user explicitly set `enabled: true`.
func (t TraceConfig) IsEnabled() bool {
	return t.Enabled != nil && *t.Enabled
}

// EffectiveDir returns the resolved trace directory, defaulting to
// <DefaultDir>/traces when not set.
func (t TraceConfig) EffectiveDir() string {
	if t.Dir != "" {
		return t.Dir
	}

	return filepath.Join(DefaultDir(), "traces")
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

		warnStaleOrchestrateKeys(data)
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

	if v := os.Getenv("VV_TRACE_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Trace.Enabled = &b
		} else {
			slog.Warn("vv: invalid VV_TRACE_ENABLED, ignoring", "value", v)
		}
	}

	if v := os.Getenv("VV_TRACE_DIR"); v != "" {
		cfg.Trace.Dir = v
	}

	if v := os.Getenv("VV_MEMORY_BACKEND"); v != "" {
		cfg.Memory.Backend = v
	}

	if v := os.Getenv("VV_AGENTS_MAX_PARALLEL_TOOL_CALLS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			cfg.Agents.MaxParallelToolCalls = n
		} else {
			slog.Warn("vv: invalid VV_AGENTS_MAX_PARALLEL_TOOL_CALLS, ignoring", "value", v)
		}
	}

	if v := os.Getenv("VV_AGENTS_PROMPT_CACHING"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Agents.PromptCaching = &b
		} else {
			slog.Warn("vv: invalid VV_AGENTS_PROMPT_CACHING, ignoring", "value", v)
		}
	}

	if v := os.Getenv("VV_MCP_CREDFILTER_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Security.MCPCredentialFilter.Enabled = &b
		} else {
			slog.Warn("vv: invalid VV_MCP_CREDFILTER_ENABLED, ignoring", "value", v)
		}
	}

	if v := os.Getenv("VV_MCP_CREDFILTER_ACTION"); v != "" {
		cfg.Security.MCPCredentialFilter.Action = v
	}

	if v := os.Getenv("VV_EVAL_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Eval.Enabled = b
		} else {
			slog.Warn("vv: invalid VV_EVAL_ENABLED, ignoring", "value", v)
		}
	}

	if v := os.Getenv("VV_ORCHESTRATE_MODE"); v != "" {
		cfg.Orchestrate.Mode = v
	}

	if v := os.Getenv("VV_ORCHESTRATE_LEGACY_PHASE_EVENTS"); v != "" {
		slog.Warn("vv: VV_ORCHESTRATE_LEGACY_PHASE_EVENTS is no longer supported as of M7; the env var is ignored", "value", v)
	}

	if v := os.Getenv("VV_PRIMARY_ALLOW_BASH"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Orchestrate.PrimaryAllowBash = b
		} else {
			slog.Warn("vv: invalid VV_PRIMARY_ALLOW_BASH, ignoring", "value", v)
		}
	}

	if v := os.Getenv("VV_ROUTER_MODEL"); v != "" {
		cfg.Orchestrate.Router.Model = v
	}

	if v := os.Getenv("VV_ROUTER_PROVIDER"); v != "" {
		cfg.Orchestrate.Router.Provider = v
	}

	if v := os.Getenv("VV_ROUTER_API_KEY"); v != "" {
		cfg.Orchestrate.Router.APIKey = v
	}

	if v := os.Getenv("VV_ROUTER_BASE_URL"); v != "" {
		cfg.Orchestrate.Router.BaseURL = v
	}

	if v := os.Getenv("VV_MCP_TRANSPORT"); v != "" {
		cfg.MCP.Server.Transport = v
	}

	if v := os.Getenv("VV_MCP_ADDR"); v != "" {
		cfg.MCP.Server.Addr = v
	}

	if v := os.Getenv("VV_MCP_AUTH_TOKEN"); v != "" {
		cfg.MCP.Server.AuthToken = v
	}

	applyBudgetEnv(cfg)

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

	if err := ValidateMCPServer(&cfg.MCP.Server); err != nil {
		return nil, err
	}

	if err := ValidateEval(&cfg.Eval); err != nil {
		return nil, err
	}

	normalized, err := ValidateMemoryBackend(cfg.Memory.Backend)
	if err != nil {
		return nil, err
	}
	cfg.Memory.Backend = normalized

	normalizedMode, err := ValidateOrchestrateMode(cfg.Orchestrate.Mode)
	if err != nil {
		return nil, err
	}
	cfg.Orchestrate.Mode = normalizedMode

	if len(cfg.CLI.ConfirmTools) > 0 {
		slog.Warn("vv: confirm_tools is deprecated; use permission_mode instead")
	}

	return cfg, nil
}

// ValidateMCPServer normalizes and validates the MCP server config.
// Transport is lower-cased; an empty value defaults to "stdio". HTTP
// transports get a default loopback Addr; non-loopback Addrs require a
// non-empty AuthToken.
func ValidateMCPServer(c *MCPServerConfig) error {
	c.Transport = strings.ToLower(strings.TrimSpace(c.Transport))
	if c.Transport == "" {
		c.Transport = "stdio"
	}

	switch c.Transport {
	case "stdio":
		// No further defaults/validation needed.
	case "http":
		if strings.TrimSpace(c.Addr) == "" {
			c.Addr = "127.0.0.1:7801"
		}

		if !isLoopbackAddr(c.Addr) && strings.TrimSpace(c.AuthToken) == "" {
			return fmt.Errorf("mcp.server.auth_token is required when mcp.server.addr binds a non-loopback host (%q)", c.Addr)
		}
	default:
		return fmt.Errorf("invalid mcp.server.transport %q; valid values: stdio, http", c.Transport)
	}

	if c.SessionTimeout < 0 {
		c.SessionTimeout = 0
	}

	return nil
}

// isLoopbackAddr reports whether addr's host is a loopback address.
// Accepts "host", "host:port", or a bare hostname. "localhost" is treated
// as loopback; an empty host (e.g. ":8080") is NOT — net.Listen on such
// an address binds every interface, so it must require an auth token.
// IPv6 bracketed hosts are handled via net.SplitHostPort.
func isLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}

	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}

	if host == "localhost" {
		return true
	}

	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}

	return false
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

// applyBudgetEnv overrides cfg.Budget from VV_BUDGET_* environment variables.
// Invalid numeric values are logged and left at their YAML value.
func applyBudgetEnv(cfg *Config) {
	envInt64 := func(key string, dst *int64) {
		v := os.Getenv(key)
		if v == "" {
			return
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil {
			slog.Warn("vv: invalid budget env, ignoring", "key", key, "value", v)
			return
		}
		*dst = n
	}
	envFloat64 := func(key string, dst *float64) {
		v := os.Getenv(key)
		if v == "" {
			return
		}
		f, err := strconv.ParseFloat(v, 64)
		if err != nil {
			slog.Warn("vv: invalid budget env, ignoring", "key", key, "value", v)
			return
		}
		*dst = f
	}

	envInt64("VV_BUDGET_SESSION_HARD_TOKENS", &cfg.Budget.SessionHardTokens)
	envFloat64("VV_BUDGET_SESSION_HARD_COST_USD", &cfg.Budget.SessionHardCostUSD)
	envInt64("VV_BUDGET_DAILY_HARD_TOKENS", &cfg.Budget.DailyHardTokens)
	envFloat64("VV_BUDGET_DAILY_HARD_COST_USD", &cfg.Budget.DailyHardCostUSD)
	envFloat64("VV_BUDGET_WARN_PERCENT", &cfg.Budget.WarnPercent)
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

	if cfg.Agents.MaxParallelToolCalls == 0 {
		cfg.Agents.MaxParallelToolCalls = 4
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

	if cfg.Eval.Concurrency <= 0 {
		cfg.Eval.Concurrency = 1
	}
	if cfg.Eval.TimeoutMs == 0 {
		cfg.Eval.TimeoutMs = 60000
	}
	if len(cfg.Eval.Evaluators) == 0 {
		cfg.Eval.Evaluators = []string{"latency", "cost"}
	}
	if cfg.Eval.LatencyThresholdMs == 0 {
		cfg.Eval.LatencyThresholdMs = 60000
	}
	if cfg.Eval.CostBudgetTokens == 0 {
		cfg.Eval.CostBudgetTokens = 10000
	}

	if cfg.Budget.WarnPercent <= 0 {
		cfg.Budget.WarnPercent = 0.8
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

// EffectiveRouterConfig resolves the router LLM configuration for design M3.
// Returns (_, false) when no router model is configured, signalling that the
// dispatcher should keep using the main LLM for routing. When enabled, any
// field left empty on Orchestrate.Router inherits from cfg.LLM so users can
// typically just set `orchestrate.router.model: <small-model>` and share the
// main provider/api_key/base_url.
func EffectiveRouterConfig(cfg *Config) (LLMConfig, bool) {
	if cfg == nil {
		return LLMConfig{}, false
	}

	r := cfg.Orchestrate.Router
	if strings.TrimSpace(r.Model) == "" {
		return LLMConfig{}, false
	}

	eff := LLMConfig{
		Provider: r.Provider,
		Model:    r.Model,
		APIKey:   r.APIKey,
		BaseURL:  r.BaseURL,
	}

	if eff.Provider == "" {
		eff.Provider = cfg.LLM.Provider
	}

	if eff.APIKey == "" {
		eff.APIKey = cfg.LLM.APIKey
	}

	if eff.BaseURL == "" {
		eff.BaseURL = cfg.LLM.BaseURL
	}

	return eff, true
}
