package configs

import (
	"bufio"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"slices"
	"strings"

	largemodel "github.com/vogo/largemodel/model"
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

	// ExecutionModel controls whether the Primary executes code mutations
	// itself or delegates them to coder. Empty defaults to delegated for
	// backward compatibility. Env override: VV_EXECUTION_MODEL.
	ExecutionModel string `yaml:"execution_model,omitempty"`

	// Router optionally points the dispatcher's routing/classification LLM
	// calls at a cheaper/smaller model. Any empty field inherits from
	// Config.LLM; an empty Router.Model leaves the feature disabled.
	Router LLMConfig `yaml:"router,omitempty"`

	// Mode is kept only for compatibility with older configs. The unified
	// Primary Assistant pipeline is the only supported path now, and this
	// field is normalised to "unified" by ValidateOrchestrateMode. Any other
	// value (including the long-removed "classical") triggers a slog.Warn
	// but does not abort startup. Other stale orchestrate.* keys
	// (legacy_phase_events, fast_path, unified_intent, summary_policy,
	// replan) trigger one slog.Warn per key during Load via the raw-YAML
	// stale-key sweep.
	Mode string `yaml:"mode,omitempty"`

	// PrimaryAllowBash is inert. The Primary Assistant now always carries the
	// full tool profile (read/glob/grep + write/edit + bash) so it can finish
	// ordinary file and shell work inline; mutations are bounded by the path
	// guard, the bash path guardian, and the permission confirmation chain
	// rather than by withholding tools. The key (and VV_PRIMARY_ALLOW_BASH)
	// is still accepted so existing configs load unchanged. The fallback
	// (depth-exceeded) Primary always stays tool-free.
	PrimaryAllowBash bool `yaml:"primary_allow_bash,omitempty"`

	// WriteTree, when set true, mirrors plan_task DAG plans into the
	// SessionTree so the user-visible tree reflects the dispatcher's plan
	// structure as well as the LLM's manual tree edits. Requires
	// session_tree.enabled=true. Default false. Env override:
	// VV_DISPATCHER_WRITE_TREE.
	WriteTree *bool `yaml:"write_tree,omitempty"`
}

// IsWriteTreeEnabled reports whether the dispatcher should mirror plans into
// the SessionTree. Default false.
func (o OrchestrateConfig) IsWriteTreeEnabled() bool {
	return o.WriteTree != nil && *o.WriteTree
}

// OrchestrateModeUnified is the only supported orchestrate pipeline.
// Empty `orchestrate.mode` normalises to this value.
const OrchestrateModeUnified = "unified"

const (
	ExecutionModelDelegated = "delegated"
	ExecutionModelHybrid    = "hybrid"
	ExecutionModelDirect    = "direct"
)

// EffectiveExecutionModel returns the configured execution model, preserving
// the historical delegated behaviour for an empty value.
func (o OrchestrateConfig) EffectiveExecutionModel() string {
	model := strings.ToLower(strings.TrimSpace(o.ExecutionModel))
	if model == "" {
		return ExecutionModelDelegated
	}
	return model
}

// ValidateExecutionModel normalises and validates orchestrate.execution_model.
func ValidateExecutionModel(model string) (string, error) {
	normalized := strings.ToLower(strings.TrimSpace(model))
	if normalized == "" {
		return ExecutionModelDelegated, nil
	}

	switch normalized {
	case ExecutionModelDelegated, ExecutionModelHybrid, ExecutionModelDirect:
		return normalized, nil
	default:
		return "", fmt.Errorf("unknown execution model %q (expected %q, %q, or %q)",
			model, ExecutionModelDelegated, ExecutionModelHybrid, ExecutionModelDirect)
	}
}

// staleOrchestrateKeys lists YAML keys under `orchestrate:` that were
// removed with the classical pipeline. They are silently dropped on
// unmarshal (the struct fields no longer exist), but Load surfaces a
// slog.Warn for each occurrence so existing vv.yaml files surface the
// deprecation instead of failing quietly.
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
			slog.Warn("vv: orchestrate."+key+" is deprecated; the key is ignored", "key", key)
		}
	}
}

// ValidateOrchestrateMode normalises mode strings. The unified pipeline is
// the only supported path; "" / "unified" pass through, and any other
// value (including the long-removed "classical") is normalised to
// OrchestrateModeUnified with a slog.Warn so misconfiguration is surfaced
// without aborting startup.
//
// The error return is retained so the (string, error) signature stays
// source-compatible with older callers; it is always nil.
func ValidateOrchestrateMode(mode string) (string, error) {
	m := strings.ToLower(strings.TrimSpace(mode))
	switch m {
	case "", OrchestrateModeUnified:
		return OrchestrateModeUnified, nil
	default:
		slog.Warn("vv: orchestrate.mode is deprecated; the field is ignored and the unified pipeline is always used", "received", mode)

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
	Session      SessionConfig                `yaml:"session,omitempty"`
	SessionTree  SessionTreeConfig            `yaml:"session_tree,omitempty"`
	Vector       VectorConfig                 `yaml:"vector,omitempty"`
	Debug        bool                         `yaml:"debug,omitempty"` // CLI > env (VV_DEBUG) > YAML > false

	// ProjectInstructionsFiles overrides the file names searched for project
	// instructions in the working directory. Empty falls back to
	// DefaultProjectInstructionsFiles (VV.md, AGENTS.md, CLAUDE.md); the first
	// readable non-empty file wins.
	ProjectInstructionsFiles []string `yaml:"project_instructions_files,omitempty"`

	// ProjectInstructions holds content loaded from the first matching
	// project instructions file in the working directory.
	// Runtime-only; not persisted to vv.yaml.
	ProjectInstructions string `yaml:"-"`

	// ProjectInstructionsFile records the base name the instructions above
	// came from (e.g. "AGENTS.md"); empty when no file matched. Runtime-only;
	// surfaced in the agent environment block and startup logs so a missing
	// project-instruction load is visible instead of silent.
	ProjectInstructionsFile string `yaml:"-"`
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

// Supported LLM protocol identifiers for LLMConfig.Provider. An empty
// Provider is treated as ProviderOpenAI throughout (defaults, client
// construction and the setup prompt).
const (
	ProviderOpenAI    = "openai"
	ProviderAnthropic = "anthropic"
)

// Default endpoints per provider. The anthropic value is only used as the
// interactive prompt's suggestion: vage treats an empty base URL as "use
// Anthropic's own endpoint", so it is never written into the config by
// applyDefaults.
const (
	defaultOpenAIBaseURL    = "https://api.openai.com/v1"
	defaultAnthropicBaseURL = "https://api.anthropic.com"
)

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
	WebSearch      WebSearchConfig `yaml:"web_search,omitempty"`   // optional web_search tool wiring
}

// Recognized web_search providers. Empty / unknown values disable the tool.
const (
	WebSearchProviderTavily  = "tavily"
	WebSearchProviderBrave   = "brave"
	WebSearchProviderSearXNG = "searxng"
)

// WebSearchConfig wires the optional web_search tool. Zero-value disables it
// entirely (tool is not registered on any agent), keeping the default path
// cost-free. The api_key field is sensitive — never log it.
//
// Provider-specific requirements:
//   - tavily / brave: require api_key.
//   - searxng: require base_url (operator self-hosts; no public endpoint);
//     api_key is optional (forwarded as bearer when set).
type WebSearchConfig struct {
	Provider       string `yaml:"provider,omitempty"`        // "" | "tavily" | "brave" | "searxng"
	APIKey         string `yaml:"api_key,omitempty"`         // never log
	BaseURL        string `yaml:"base_url,omitempty"`        // searxng only; e.g. http://host[/search]
	Language       string `yaml:"language,omitempty"`        // searxng only; "auto" when empty
	Categories     string `yaml:"categories,omitempty"`      // searxng only; comma-separated
	UserAgent      string `yaml:"user_agent,omitempty"`      // searxng only; browser UA when limiter blocks bots
	TimeoutSeconds int    `yaml:"timeout_seconds,omitempty"` // 0 → default 10
	MaxResults     int    `yaml:"max_results,omitempty"`     // 0 → default 5; cap 20
}

// IsEnabled reports whether the configuration carries a usable provider id
// and the credentials that provider requires.
func (w WebSearchConfig) IsEnabled() bool {
	switch NormalizedWebSearchProvider(w.Provider) {
	case WebSearchProviderTavily, WebSearchProviderBrave:
		return strings.TrimSpace(w.APIKey) != ""
	case WebSearchProviderSearXNG:
		return strings.TrimSpace(w.BaseURL) != ""
	default:
		return false
	}
}

// NormalizedWebSearchProvider lower-cases / trims the input and returns "" for
// any value that is not a recognized provider id. Used by setup wiring to
// branch without re-implementing the case-fold rules.
func NormalizedWebSearchProvider(p string) string {
	switch strings.ToLower(strings.TrimSpace(p)) {
	case WebSearchProviderTavily:
		return WebSearchProviderTavily
	case WebSearchProviderBrave:
		return WebSearchProviderBrave
	case WebSearchProviderSearXNG:
		return WebSearchProviderSearXNG
	default:
		return ""
	}
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
	MaxIterations int `yaml:"max_iterations"` // default 10

	// PrimaryMaxIterations is the ReAct ceiling for the Primary Assistant
	// alone. The Primary pays for a full investigate → act/delegate → report
	// cycle inside a single run, so the specialist default (10) leaves it
	// prone to exhausting the budget mid-investigation and returning nothing.
	// 0 falls back to DefaultPrimaryMaxIterations. Env override:
	// VV_PRIMARY_MAX_ITERATIONS.
	PrimaryMaxIterations int `yaml:"primary_max_iterations,omitempty"`

	RunTokenBudget int `yaml:"run_token_budget"` // default 0 (unlimited)
	AskUserTimeout int `yaml:"ask_user_timeout"` // seconds, default 300 (5 minutes)
	// InterruptEnabled wires vage interrupt onto Primary and long-lived
	// Full-profile agents. Default false (zero-cost). Requires session.enabled
	// and bash_rules not disabled; see setup.installInterrupt.
	InterruptEnabled bool `yaml:"interrupt_enabled,omitempty"`
	// InterruptLeaseTTL is the ResumeInterrupt lease in seconds. 0 falls
	// back to AskUserTimeout so the two HITL waits stay the same order of
	// magnitude. Env: VV_AGENTS_INTERRUPT_LEASE_TTL.
	InterruptLeaseTTL int `yaml:"interrupt_lease_ttl,omitempty"`
	// MaxParallelToolCalls caps concurrent tool dispatch within a single
	// assistant message. 0 uses the framework default (4); <=1 serializes.
	MaxParallelToolCalls int `yaml:"max_parallel_tool_calls"`
	// PromptCaching, nil-default-on, controls emission of prompt-cache
	// boundary hints on the system message and the last tool definition.
	// Set to a pointer to false to disable. No on-wire effect for OpenAI
	// backends — OpenAI prefix-caches automatically.
	PromptCaching *bool `yaml:"prompt_caching"`
}

// DefaultPrimaryMaxIterations is the fallback ReAct ceiling for the Primary
// Assistant. Sized for "investigate a few turns, then act or delegate, then
// report" — the shape every non-trivial request takes through the front door.
const DefaultPrimaryMaxIterations = 24

// EffectivePrimaryMaxIterations resolves the Primary's ReAct ceiling,
// falling back to DefaultPrimaryMaxIterations when unset. A configured
// agents.max_iterations larger than the Primary value wins, so raising the
// global ceiling never silently narrows the front door.
func (c *AgentsConfig) EffectivePrimaryMaxIterations() int {
	if c == nil {
		return DefaultPrimaryMaxIterations
	}

	n := c.PrimaryMaxIterations
	if n <= 0 {
		n = DefaultPrimaryMaxIterations
	}

	if c.MaxIterations > n {
		return c.MaxIterations
	}

	return n
}

// EffectivePromptCaching resolves the nil-default-on pointer. nil / unset
// yields true; only an explicit false disables.
func (c *AgentsConfig) EffectivePromptCaching() bool {
	if c == nil || c.PromptCaching == nil {
		return true
	}
	return *c.PromptCaching
}

// EffectiveInterruptLeaseTTL is the resume lease in seconds: an explicit
// interrupt_lease_ttl wins, otherwise AskUserTimeout (already defaulted).
func (c *AgentsConfig) EffectiveInterruptLeaseTTL() int {
	if c == nil {
		return 300
	}
	if c.InterruptLeaseTTL > 0 {
		return c.InterruptLeaseTTL
	}
	if c.AskUserTimeout > 0 {
		return c.AskUserTimeout
	}
	return 300
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

// SessionConfig controls the persistent session subsystem (vage/session
// integration). Default-on: a nil Enabled pointer means "enabled" so that a
// fresh install gets durable conversation history without any configuration.
// Set `enabled: false` (YAML) or VV_SESSION_ENABLED=false to opt out.
type SessionConfig struct {
	Enabled *bool  `yaml:"enabled,omitempty"` // default true
	Dir     string `yaml:"dir,omitempty"`     // default ~/.vv/sessions

	// EventPersist selects which slice of the event bus reaches
	// events.jsonl. Every payload that already has an owner file is
	// excluded from the default so no byte is written twice:
	//
	//   control (default) — control-plane whitelist; text_delta,
	//                       tool_result and context_built are dropped
	//                       because their owners are messages.jsonl
	//                       (message bodies) and build_reports/.
	//   all               — no filter; restores the pre-redesign
	//                       behaviour and subsumes trace logging.
	//   none              — no events persisted; messages.jsonl alone.
	//
	// Empty falls back to control. See doc/domains/core/session/design.md.
	EventPersist string `yaml:"event_persist,omitempty"`

	// ResumeMaxMessages caps how many messages a resume replays out of
	// messages.jsonl into the restored conversation. 0 falls back to
	// DefaultResumeMaxMessages.
	//
	// Replaces history_replay_max_events, which was sized in events back
	// when the (unreplayable) event stream was the only transcript;
	// the legacy key is still parsed into this field.
	ResumeMaxMessages int `yaml:"resume_max_messages,omitempty"`

	// HistoryReplayMaxEvents is the deprecated spelling of
	// ResumeMaxMessages. Parsed for backward compatibility and folded
	// into ResumeMaxMessages by Load with a warning; remove after one
	// minor release.
	//
	// Deprecated: use ResumeMaxMessages.
	HistoryReplayMaxEvents int `yaml:"history_replay_max_events,omitempty"`

	// PersistBuildReports toggles per-turn BuildReport persistence to
	// <session-root>/<id>/build_reports/. Default true — the disk cost
	// is bounded by BuildReportLimit. nil keeps the default-on path so
	// existing configs stay observable without edits.
	PersistBuildReports *bool `yaml:"persist_build_reports,omitempty"`

	// BuildReportLimit caps how many per-turn BuildReport files a
	// single session retains. Older files are unlinked LRU-style on
	// each new write. 0 falls back to vage's DefaultBuildReportLimit
	// (50). Increase for long debugging sessions; decrease in disk-
	// constrained environments.
	BuildReportLimit int `yaml:"build_report_limit,omitempty"`

	// ToolResultMaxInlineBytes is the size above which a tool message
	// body is spilled to <session>/tool-results/<hash>.txt and replaced
	// by a pointer inside messages.jsonl. The spill is a storage detail:
	// reads rehydrate transparently, so agents and HTTP callers never
	// see the pointer. 0 falls back to DefaultToolResultMaxInlineBytes;
	// negative disables spilling.
	ToolResultMaxInlineBytes int `yaml:"tool_result_max_inline_bytes,omitempty"`

	// EventsMaxFileBytes rotates events.jsonl once it exceeds this size
	// (events.jsonl → events.1.jsonl → …). Reads stitch the rotated
	// segments back together, so rotation is invisible to API callers.
	// 0 falls back to DefaultEventsMaxFileBytes; negative disables
	// rotation. Inherited from the trace hook, which event_persist:all
	// now subsumes.
	EventsMaxFileBytes int64 `yaml:"events_max_file_bytes,omitempty"`

	// RetentionDays deletes session directories untouched for longer
	// than this many days, swept once at startup. 0 (the default) keeps
	// everything: sessions are the user's history, and silently
	// deleting it must be opted into.
	RetentionDays int `yaml:"retention_days,omitempty"`
}

// Session event_persist modes.
const (
	EventPersistControl = "control"
	EventPersistAll     = "all"
	EventPersistNone    = "none"
)

// DefaultResumeMaxMessages bounds a resume replay when the user has not
// configured resume_max_messages.
const DefaultResumeMaxMessages = 5000

// DefaultToolResultMaxInlineBytes is the inline ceiling for tool message
// bodies in messages.jsonl. 8 KiB keeps ordinary results (file reads,
// command output) inline while pushing the rare multi-megabyte dump into
// its own file, where it costs one write instead of one per checkpoint.
const DefaultToolResultMaxInlineBytes = 8 << 10

// DefaultEventsMaxFileBytes is the events.jsonl rotation threshold. It
// matches the trace hook's historical default so merging trace into the
// event log does not change how much a debugging session may accumulate
// in one file.
const DefaultEventsMaxFileBytes int64 = 64 << 20

// IsEnabled returns true unless the user explicitly set `enabled: false`.
// Default-on so a fresh install gets persistent sessions without configuration.
func (s SessionConfig) IsEnabled() bool {
	return s.Enabled == nil || *s.Enabled
}

// PersistBuildReportsEnabled returns the effective default-on toggle
// for per-turn BuildReport persistence. nil and "" both mean "use the
// default" which is true.
func (s SessionConfig) PersistBuildReportsEnabled() bool {
	return s.PersistBuildReports == nil || *s.PersistBuildReports
}

// EffectiveEventPersist returns the normalised event_persist mode.
// Unknown values are rejected by Validate, so the fallback here only
// covers the empty (unset) case.
func (s SessionConfig) EffectiveEventPersist() string {
	if s.EventPersist == "" {
		return EventPersistControl
	}

	return strings.ToLower(strings.TrimSpace(s.EventPersist))
}

// EffectiveResumeMaxMessages returns the resume replay cap, defaulting to
// DefaultResumeMaxMessages.
func (s SessionConfig) EffectiveResumeMaxMessages() int {
	if s.ResumeMaxMessages > 0 {
		return s.ResumeMaxMessages
	}

	return DefaultResumeMaxMessages
}

// EffectiveToolResultMaxInlineBytes returns the tool-body spill threshold.
// A negative configured value disables spilling and is returned as 0.
func (s SessionConfig) EffectiveToolResultMaxInlineBytes() int {
	switch {
	case s.ToolResultMaxInlineBytes < 0:
		return 0
	case s.ToolResultMaxInlineBytes == 0:
		return DefaultToolResultMaxInlineBytes
	default:
		return s.ToolResultMaxInlineBytes
	}
}

// EffectiveEventsMaxFileBytes returns the events.jsonl rotation
// threshold. A negative configured value disables rotation and is
// returned as 0.
func (s SessionConfig) EffectiveEventsMaxFileBytes() int64 {
	switch {
	case s.EventsMaxFileBytes < 0:
		return 0
	case s.EventsMaxFileBytes == 0:
		return DefaultEventsMaxFileBytes
	default:
		return s.EventsMaxFileBytes
	}
}

// EffectiveDir returns the resolved session root directory, defaulting to
// <DefaultDir>/sessions when not set.
func (s SessionConfig) EffectiveDir() string {
	if s.Dir != "" {
		return s.Dir
	}

	return filepath.Join(DefaultDir(), "sessions")
}

// SessionTreeConfig controls the SessionTree subsystem (vage/session/tree
// integration). Default-off: long-lived structured tree memory is opt-in
// because most short interactions get nothing from it and pay a small
// per-turn rendering cost when it is enabled.
//
// Storage shares the session root, so DELETE /v1/sessions/{id} naturally
// wipes the tree alongside meta/events/state/workspace.
type SessionTreeConfig struct {
	Enabled   *bool                      `yaml:"enabled,omitempty"` // default false
	Promotion SessionTreePromotionConfig `yaml:"promotion,omitempty"`

	// AutoEnableAfterEvents lazy-activates the SessionTreeSource on a
	// per-session basis: until the session has accumulated this many
	// AgentEnd events, the source short-circuits with Status=Skipped and
	// the tree is not rendered into the prompt. The tools and HTTP routes
	// stay available throughout — only the prompt-injected view is gated,
	// so the LLM can still bootstrap the tree explicitly via tree_add.
	//
	// 0 (default) disables gating; the source activates on every request
	// once the subsystem is enabled. A positive value (e.g., 16) skips
	// the per-turn rendering cost on short conversations and engages
	// only when the dialogue is genuinely long. Recommended: 8–32.
	//
	// Counts AgentEnd events only — tool calls and other intermediate
	// events do not count toward the threshold so the metric tracks
	// "user turns" rather than internal activity.
	AutoEnableAfterEvents int `yaml:"auto_enable_after_events,omitempty"`

	// VectorIndex enables dual-indexing tree node summaries into the
	// vector store (§4.8.6 step 3 of the design doc): every successful
	// AddNode/UpdateNode/PromoteNode also embeds the node summary and
	// upserts a `tree:<sid>:<nid>` document. Requires Vector subsystem
	// enabled (otherwise wiring is silently skipped).
	VectorIndex SessionTreeVectorIndexConfig `yaml:"vector_index,omitempty"`
}

// SessionTreeVectorIndexConfig controls the SessionTree → Vector dual
// index. Off by default: the index is only useful when the Vector
// subsystem is also active and the user expects similarity-based
// recall over tree summaries.
type SessionTreeVectorIndexConfig struct {
	Enabled *bool `yaml:"enabled,omitempty"` // default false
}

// IsEnabled reports whether the dual index should be wired.
func (v SessionTreeVectorIndexConfig) IsEnabled() bool {
	return v.Enabled != nil && *v.Enabled
}

// IsEnabled reports whether the SessionTree subsystem should be wired.
// Default false (the design doc treats long-task tree memory as opt-in).
func (s SessionTreeConfig) IsEnabled() bool {
	return s.Enabled != nil && *s.Enabled
}

// SessionTreePromotionConfig controls automatic promotion ("folding") of
// child nodes into a parent's summary.
type SessionTreePromotionConfig struct {
	Enabled               *bool  `yaml:"enabled,omitempty"`                 // default false
	Promoter              string `yaml:"promoter,omitempty"`                // "llm" | "compressor" | "noop"; default "compressor"
	Model                 string `yaml:"model,omitempty"`                   // "" = inherit Config.LLM.Model (only used by promoter=llm)
	ChildrenThreshold     int    `yaml:"children_threshold,omitempty"`      // default 8
	SubtreeBytesThreshold int    `yaml:"subtree_bytes_threshold,omitempty"` // default 8192
	AllChildrenDone       *bool  `yaml:"all_children_done,omitempty"`       // default true
}

// IsEnabled reports whether automatic promotion should fire on AddNode /
// UpdateNode. Manual PromoteNode (and tree_promote) work independently of
// this flag.
func (s SessionTreePromotionConfig) IsEnabled() bool {
	return s.Enabled != nil && *s.Enabled
}

// PromoterKind returns the configured promoter kind, falling back to
// "compressor" — the cheapest option that does something useful — when the
// field is empty.
func (s SessionTreePromotionConfig) PromoterKind() string {
	if s.Promoter == "" {
		return "compressor"
	}
	return strings.ToLower(strings.TrimSpace(s.Promoter))
}

// AllChildrenDoneEnabled reports whether the AllChildrenDoneDecider should
// be ANDed into the trigger set. Defaults to true so a parent whose
// subtasks are all done gets folded automatically.
func (s SessionTreePromotionConfig) AllChildrenDoneEnabled() bool {
	return s.AllChildrenDone == nil || *s.AllChildrenDone
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

	// ANTHROPIC_* runs before the VV_* table: both overwrite the YAML values,
	// and applying VV_* last makes the vv-native variables win any overlap.
	// Both run before defaults, so an empty provider is not yet frozen to
	// openai.
	applyAnthropicEnvOverride(cfg)

	applyEnvOverrides(cfg)

	applyDefaults(cfg)

	if err := Validate(cfg); err != nil {
		return nil, err
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

	provider := strings.ToLower(prompt(scanner, w,
		"LLM provider (openai/anthropic)",
		cfg.LLM.Provider, ProviderOpenAI))

	// An empty provider means openai everywhere else, so normalize before
	// comparing — otherwise accepting the openai default would count as a
	// switch and wipe a perfectly valid openai endpoint.
	current := cfg.LLM.Provider
	if current == "" {
		current = ProviderOpenAI
	}

	// A provider switch invalidates the endpoint/model/key carried in cfg:
	// they describe the previous provider's API. Drop them so the questions
	// below suggest the new provider's defaults instead of stale values.
	if provider != current {
		cfg.LLM.BaseURL = ""
		cfg.LLM.Model = ""
		cfg.LLM.APIKey = ""
	}

	cfg.LLM.Provider = provider

	defaultModel := "gpt-4o"
	defaultBaseURL := defaultOpenAIBaseURL

	if provider == ProviderAnthropic {
		defaultModel = "claude-sonnet-4"
		defaultBaseURL = defaultAnthropicBaseURL
	}

	cfg.LLM.Model = prompt(scanner, w,
		"LLM model",
		cfg.LLM.Model, defaultModel)

	cfg.LLM.APIKey = prompt(scanner, w,
		"LLM API key",
		cfg.LLM.APIKey, "")

	// Asked for every provider: an anthropic-protocol endpoint is not
	// necessarily Anthropic's own (DeepSeek, Bedrock gateways, local proxies),
	// and silently defaulting to api.anthropic.com turns a third-party key into
	// an opaque 401 at the first request.
	cfg.LLM.BaseURL = prompt(scanner, w,
		"LLM base URL (API endpoint)",
		cfg.LLM.BaseURL, defaultBaseURL)

	cfg.Server.Addr = prompt(scanner, w,
		"HTTP mode listen address (host:port, not an API endpoint)",
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

	if cfg.Agents.PrimaryMaxIterations == 0 {
		cfg.Agents.PrimaryMaxIterations = DefaultPrimaryMaxIterations
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

	// Provider-specific defaults. anthropic is deliberately absent: vage reads
	// an empty base URL as "use Anthropic's own endpoint", and pinning the
	// official URL here would hide it from the startup banner's
	// third-party-endpoint hint.
	if cfg.LLM.Provider == ProviderOpenAI || cfg.LLM.Provider == "" {
		if cfg.LLM.BaseURL == "" {
			cfg.LLM.BaseURL = defaultOpenAIBaseURL
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

	applySessionDefaults(&cfg.Session)
}

// applySessionDefaults normalises the session storage knobs: it folds the
// deprecated history_replay_max_events key into ResumeMaxMessages and
// coerces an unrecognised event_persist mode back to the default rather
// than silently persisting nothing.
func applySessionDefaults(s *SessionConfig) {
	if s.HistoryReplayMaxEvents > 0 && s.ResumeMaxMessages == 0 {
		slog.Warn("vv: session.history_replay_max_events is deprecated, use session.resume_max_messages",
			"value", s.HistoryReplayMaxEvents)
		s.ResumeMaxMessages = s.HistoryReplayMaxEvents
	}

	if s.ResumeMaxMessages == 0 {
		s.ResumeMaxMessages = DefaultResumeMaxMessages
	}
	// Keep the deprecated field in sync so existing readers (and the
	// config dump) see a consistent value during the grace period.
	s.HistoryReplayMaxEvents = s.ResumeMaxMessages

	switch mode := strings.ToLower(strings.TrimSpace(s.EventPersist)); mode {
	case "":
		s.EventPersist = EventPersistControl
	case EventPersistControl, EventPersistAll, EventPersistNone:
		s.EventPersist = mode
	default:
		slog.Warn("vv: unknown session.event_persist, falling back to control",
			"value", s.EventPersist, "valid", []string{EventPersistControl, EventPersistAll, EventPersistNone})
		s.EventPersist = EventPersistControl
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

// EndpointLabel returns the host of the API endpoint this configuration will
// actually call, for display in the CLI banner and startup log. An empty
// anthropic base URL resolves to Anthropic's own endpoint, mirroring how vage
// treats it; an unparseable value is returned verbatim so a typo stays visible.
func (c LLMConfig) EndpointLabel() string {
	raw := c.BaseURL
	if raw == "" {
		raw = defaultOpenAIBaseURL
		if c.Provider == ProviderAnthropic {
			raw = defaultAnthropicBaseURL
		}
	}

	if u, err := url.Parse(raw); err == nil && u.Host != "" {
		return u.Host
	}

	return raw
}

// NewLLMClient creates the protocol-specific vage caller selected by the LLM
// configuration. The endpoint type picks the protocol; retries, endpoint
// routing and health are owned by largemodel/router inside the caller. The
// lone endpoint is left unnamed, so largemodel aliases it DefaultEndpointAlias.
func NewLLMClient(cfg LLMConfig) (largemodel.Caller, error) {
	switch cfg.Provider {
	case ProviderAnthropic:
		return largemodel.NewCaller(largemodel.AnthropicEndpoint{
			APIKey:  cfg.APIKey,
			BaseURL: cfg.BaseURL,
		})
	case ProviderOpenAI, "":
		return largemodel.NewCaller(largemodel.OpenAIEndpoint{
			APIKey:  cfg.APIKey,
			BaseURL: cfg.BaseURL,
		})
	default:
		return nil, fmt.Errorf("unsupported LLM provider: %q (supported: openai, anthropic)", cfg.Provider)
	}
}

// EffectiveRouterConfig resolves the router LLM configuration.
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
