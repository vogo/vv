package configs

import (
	"encoding/json"
	"log/slog"
	"maps"
	"os"
	"strconv"
)

// envBinding maps an environment variable to a function that applies its
// value to the config. The dispatch loop in applyEnvOverrides reads each key
// and calls apply only when the value is a non-empty string, so an unset or
// empty env var never overrides the YAML value (CONFIG-R1 precedence:
// YAML < env < defaults).
//
// Each apply closure owns the parse/constraint/warn policy for its variable;
// the shared helpers below preserve the exact per-variable behaviour that used
// to live inline in Load. Adding a new env switch means appending one binding.
type envBinding struct {
	key   string
	apply func(cfg *Config, v string)
}

// applyEnvOverrides applies every VV_* (and the two OpenAI-key fallback)
// environment variable override onto cfg. It replaces the long linear block of
// os.Getenv checks in Load with a table walk. Behaviour is identical to the
// previous inline code: empty values are treated as "not provided", invalid
// values follow each variable's original parse-failure policy, and the vector
// OpenAI key keeps its bespoke two-level fallback.
func applyEnvOverrides(cfg *Config) {
	for _, b := range envBindings {
		if v := getenv(b.key); v != "" {
			b.apply(cfg, v)
		}
	}

	// The vector OpenAI key is not a single-key binding: it only applies when
	// YAML left the field empty, and then falls back across two env vars in a
	// fixed order. Kept separate so the conditional stays explicit.
	applyVectorOpenAIKeyEnv(cfg)
}

// getenv is a thin alias over os.Getenv kept package-local so the binding
// table and its callers read uniformly.
func getenv(key string) string {
	return os.Getenv(key)
}

// envBindings groups the per-subsystem binding sets into the single ordered
// table walked by applyEnvOverrides. The grouping is purely for readability;
// each field is written by exactly one binding, so relative order does not
// affect the result.
var envBindings = concatBindings(
	llmEnvBindings,
	serverModeEnvBindings,
	contextEnvBindings,
	traceEnvBindings,
	sessionEnvBindings,
	sessionTreeEnvBindings,
	orchestrateEnvBindings,
	memoryEnvBindings,
	vectorEnvBindings,
	agentsEnvBindings,
	securityEnvBindings,
	evalEnvBindings,
	mcpEnvBindings,
	webSearchEnvBindings,
	budgetEnvBindings,
	modelPricingEnvBindings,
)

func concatBindings(sets ...[]envBinding) []envBinding {
	var out []envBinding
	for _, s := range sets {
		out = append(out, s...)
	}

	return out
}

var llmEnvBindings = []envBinding{
	{"VV_LLM_API_KEY", func(c *Config, v string) { c.LLM.APIKey = v }},
	{"VV_LLM_BASE_URL", func(c *Config, v string) { c.LLM.BaseURL = v }},
	{"VV_LLM_MODEL", func(c *Config, v string) { c.LLM.Model = v }},
	{"VV_LLM_PROVIDER", func(c *Config, v string) { c.LLM.Provider = v }},
}

var serverModeEnvBindings = []envBinding{
	{"VV_SERVER_ADDR", func(c *Config, v string) { c.Server.Addr = v }},
	{"VV_MODE", func(c *Config, v string) { c.Mode = v }},
	{"VV_PERMISSION_MODE", func(c *Config, v string) { c.CLI.PermissionMode = PermissionMode(v) }},
	{"VV_DEBUG", func(c *Config, v string) { applyBoolValWarn("VV_DEBUG", v, &c.Debug) }},
}

var contextEnvBindings = []envBinding{
	// These three int overrides silently ignore parse failures (no warn),
	// matching the original inline behaviour.
	{"VV_MAX_CONTEXT_TOKENS", func(c *Config, v string) { applyIntSilent(v, &c.Context.ModelMaxContextTokens) }},
	{"VV_TOOL_OUTPUT_MAX_TOKENS", func(c *Config, v string) { applyIntSilent(v, &c.Context.ToolOutputMaxTokens) }},
	{"VV_CONTEXT_PROTECTED_TURNS", func(c *Config, v string) { applyIntSilent(v, &c.Context.ProtectedTurns) }},
	// Float pointer override, also silent on parse failure.
	{"VV_CONTEXT_COMPRESSION_THRESHOLD", func(c *Config, v string) {
		applyFloatPtrSilent(v, &c.Context.CompressionThreshold)
	}},
}

var traceEnvBindings = []envBinding{
	{"VV_TRACE_ENABLED", func(c *Config, v string) { applyBoolPtrWarn("VV_TRACE_ENABLED", v, &c.Trace.Enabled) }},
	{"VV_TRACE_DIR", func(c *Config, v string) { c.Trace.Dir = v }},
}

var sessionEnvBindings = []envBinding{
	{"VV_SESSION_ENABLED", func(c *Config, v string) { applyBoolPtrWarn("VV_SESSION_ENABLED", v, &c.Session.Enabled) }},
	{"VV_SESSION_DIR", func(c *Config, v string) { c.Session.Dir = v }},
}

var sessionTreeEnvBindings = []envBinding{
	{"VV_TREE_ENABLED", func(c *Config, v string) { applyBoolPtrWarn("VV_TREE_ENABLED", v, &c.SessionTree.Enabled) }},
	{"VV_TREE_PROMOTION_ENABLED", func(c *Config, v string) {
		applyBoolPtrWarn("VV_TREE_PROMOTION_ENABLED", v, &c.SessionTree.Promotion.Enabled)
	}},
	{"VV_TREE_PROMOTER", func(c *Config, v string) { c.SessionTree.Promotion.Promoter = v }},
	{"VV_TREE_AUTO_AFTER_EVENTS", func(c *Config, v string) {
		applyIntNonNegWarn("VV_TREE_AUTO_AFTER_EVENTS", v, &c.SessionTree.AutoEnableAfterEvents)
	}},
}

var orchestrateEnvBindings = []envBinding{
	{"VV_DISPATCHER_WRITE_TREE", func(c *Config, v string) {
		applyBoolPtrWarn("VV_DISPATCHER_WRITE_TREE", v, &c.Orchestrate.WriteTree)
	}},
	{"VV_ORCHESTRATE_MODE", func(c *Config, v string) { c.Orchestrate.Mode = v }},
	{"VV_ORCHESTRATE_LEGACY_PHASE_EVENTS", func(_ *Config, v string) {
		// Removed knob: never set anything, just surface the deprecation.
		slog.Warn("vv: VV_ORCHESTRATE_LEGACY_PHASE_EVENTS is no longer supported; the env var is ignored", "value", v)
	}},
	{"VV_PRIMARY_ALLOW_BASH", func(c *Config, v string) {
		applyBoolValWarn("VV_PRIMARY_ALLOW_BASH", v, &c.Orchestrate.PrimaryAllowBash)
	}},
	{"VV_ROUTER_MODEL", func(c *Config, v string) { c.Orchestrate.Router.Model = v }},
	{"VV_ROUTER_PROVIDER", func(c *Config, v string) { c.Orchestrate.Router.Provider = v }},
	{"VV_ROUTER_API_KEY", func(c *Config, v string) { c.Orchestrate.Router.APIKey = v }},
	{"VV_ROUTER_BASE_URL", func(c *Config, v string) { c.Orchestrate.Router.BaseURL = v }},
}

var memoryEnvBindings = []envBinding{
	{"VV_MEMORY_BACKEND", func(c *Config, v string) { c.Memory.Backend = v }},
}

var vectorEnvBindings = []envBinding{
	{"VV_VECTOR_ENABLED", func(c *Config, v string) { applyBoolPtrWarn("VV_VECTOR_ENABLED", v, &c.Vector.Enabled) }},
	{"VV_VECTOR_BACKEND", func(c *Config, v string) { c.Vector.Backend = v }},
	{"VV_VECTOR_EMBEDDER", func(c *Config, v string) { c.Vector.Embedder = v }},
	{"VV_VECTOR_AUTO_WRITE", func(c *Config, v string) {
		applyBoolPtrWarn("VV_VECTOR_AUTO_WRITE", v, &c.Vector.AutoWrite)
	}},
	{"VV_VECTOR_TOP_K", func(c *Config, v string) { applyIntNonNegWarn("VV_VECTOR_TOP_K", v, &c.Vector.TopK) }},
	{"VV_VECTOR_COLLECTION", func(c *Config, v string) { c.Vector.Collection = v }},
	{"VV_QDRANT_URL", func(c *Config, v string) { c.Vector.Qdrant.URL = v }},
	{"VV_QDRANT_API_KEY", func(c *Config, v string) { c.Vector.Qdrant.APIKey = v }},
	{"VV_VECTOR_OPENAI_MODEL", func(c *Config, v string) { c.Vector.OpenAI.Model = v }},
	{"VV_VECTOR_OPENAI_BASE_URL", func(c *Config, v string) { c.Vector.OpenAI.BaseURL = v }},
	{"VV_VECTOR_OPENAI_DIMENSIONS", func(c *Config, v string) {
		applyIntNonNegWarn("VV_VECTOR_OPENAI_DIMENSIONS", v, &c.Vector.OpenAI.Dimensions)
	}},
}

var agentsEnvBindings = []envBinding{
	{"VV_AGENTS_MAX_PARALLEL_TOOL_CALLS", func(c *Config, v string) {
		applyIntWarn("VV_AGENTS_MAX_PARALLEL_TOOL_CALLS", v, &c.Agents.MaxParallelToolCalls)
	}},
	{"VV_AGENTS_PROMPT_CACHING", func(c *Config, v string) {
		applyBoolPtrWarn("VV_AGENTS_PROMPT_CACHING", v, &c.Agents.PromptCaching)
	}},
}

var securityEnvBindings = []envBinding{
	{"VV_MCP_CREDFILTER_ENABLED", func(c *Config, v string) {
		applyBoolPtrWarn("VV_MCP_CREDFILTER_ENABLED", v, &c.Security.MCPCredentialFilter.Enabled)
	}},
	{"VV_MCP_CREDFILTER_ACTION", func(c *Config, v string) { c.Security.MCPCredentialFilter.Action = v }},
}

var evalEnvBindings = []envBinding{
	{"VV_EVAL_ENABLED", func(c *Config, v string) { applyBoolValWarn("VV_EVAL_ENABLED", v, &c.Eval.Enabled) }},
}

var mcpEnvBindings = []envBinding{
	{"VV_MCP_TRANSPORT", func(c *Config, v string) { c.MCP.Server.Transport = v }},
	{"VV_MCP_ADDR", func(c *Config, v string) { c.MCP.Server.Addr = v }},
	{"VV_MCP_AUTH_TOKEN", func(c *Config, v string) { c.MCP.Server.AuthToken = v }},
}

var webSearchEnvBindings = []envBinding{
	{"VV_WEB_SEARCH_PROVIDER", func(c *Config, v string) { c.Tools.WebSearch.Provider = v }},
	{"VV_WEB_SEARCH_API_KEY", func(c *Config, v string) { c.Tools.WebSearch.APIKey = v }},
	{"VV_WEB_SEARCH_BASE_URL", func(c *Config, v string) { c.Tools.WebSearch.BaseURL = v }},
	{"VV_WEB_SEARCH_LANGUAGE", func(c *Config, v string) { c.Tools.WebSearch.Language = v }},
	{"VV_WEB_SEARCH_CATEGORIES", func(c *Config, v string) { c.Tools.WebSearch.Categories = v }},
	{"VV_WEB_SEARCH_USER_AGENT", func(c *Config, v string) { c.Tools.WebSearch.UserAgent = v }},
	{"VV_WEB_SEARCH_TIMEOUT_SECONDS", func(c *Config, v string) {
		applyIntWarn("VV_WEB_SEARCH_TIMEOUT_SECONDS", v, &c.Tools.WebSearch.TimeoutSeconds)
	}},
	{"VV_WEB_SEARCH_MAX_RESULTS", func(c *Config, v string) {
		applyIntWarn("VV_WEB_SEARCH_MAX_RESULTS", v, &c.Tools.WebSearch.MaxResults)
	}},
}

// budgetEnvBindings keeps the budget-specific warn wording
// ("vv: invalid budget env, ignoring", key=...) rather than the per-variable
// "vv: invalid <KEY>, ignoring" used elsewhere, so the log surface is
// unchanged.
var budgetEnvBindings = []envBinding{
	{"VV_BUDGET_SESSION_HARD_TOKENS", func(c *Config, v string) {
		applyBudgetInt64("VV_BUDGET_SESSION_HARD_TOKENS", v, &c.Budget.SessionHardTokens)
	}},
	{"VV_BUDGET_SESSION_HARD_COST_USD", func(c *Config, v string) {
		applyBudgetFloat64("VV_BUDGET_SESSION_HARD_COST_USD", v, &c.Budget.SessionHardCostUSD)
	}},
	{"VV_BUDGET_DAILY_HARD_TOKENS", func(c *Config, v string) {
		applyBudgetInt64("VV_BUDGET_DAILY_HARD_TOKENS", v, &c.Budget.DailyHardTokens)
	}},
	{"VV_BUDGET_DAILY_HARD_COST_USD", func(c *Config, v string) {
		applyBudgetFloat64("VV_BUDGET_DAILY_HARD_COST_USD", v, &c.Budget.DailyHardCostUSD)
	}},
	{"VV_BUDGET_WARN_PERCENT", func(c *Config, v string) {
		applyBudgetFloat64("VV_BUDGET_WARN_PERCENT", v, &c.Budget.WarnPercent)
	}},
}

var modelPricingEnvBindings = []envBinding{
	{"VV_MODEL_PRICING", func(c *Config, v string) { applyModelPricingEnv(c, v) }},
}

// applyAnthropicEnvFallback infers an Anthropic provider from the standard
// ANTHROPIC_* environment variables when neither YAML nor VV_LLM_PROVIDER has
// pinned a provider. It runs in Load AFTER applyEnvOverrides so VV_LLM_* keeps
// priority, and BEFORE applyDefaults so an empty provider is not yet frozen to
// the openai default BaseURL.
//
// It is intentionally NOT an envBinding: the whole block is gated on
// cfg.LLM.Provider == "" (an explicit openai/anthropic choice is never
// rewritten), and each of APIKey/BaseURL/Model is filled only when its own
// field is still empty — so YAML and VV_LLM_* values already present are
// preserved. Only non-empty ANTHROPIC_* values count as "present", matching
// the empty-string-does-not-override semantics used elsewhere.
//
// Note: when OPENAI_API_KEY and ANTHROPIC_* are both set with no explicit
// provider, this fallback selects anthropic — the standard Anthropic
// convention wins. aimodel.NewClient's own AI_API_KEY/OPENAI_API_KEY/
// ANTHROPIC_API_KEY fallback is untouched; this only decides the protocol.
func applyAnthropicEnvFallback(cfg *Config) {
	if cfg.LLM.Provider != "" {
		return
	}

	apiKey := getenv("ANTHROPIC_API_KEY")
	baseURL := getenv("ANTHROPIC_BASE_URL")
	model := getenv("ANTHROPIC_MODEL")

	if apiKey == "" && baseURL == "" && model == "" {
		return
	}

	cfg.LLM.Provider = "anthropic"

	if cfg.LLM.APIKey == "" && apiKey != "" {
		cfg.LLM.APIKey = apiKey
	}

	if cfg.LLM.BaseURL == "" && baseURL != "" {
		cfg.LLM.BaseURL = baseURL
	}

	if cfg.LLM.Model == "" && model != "" {
		cfg.LLM.Model = model
	}
}

// applyVectorOpenAIKeyEnv fills the vector embedder OpenAI key when YAML left
// it empty. Precedence: explicit YAML > VV_VECTOR_OPENAI_API_KEY >
// OPENAI_API_KEY. It deliberately does NOT fall back to VV_LLM_API_KEY — the
// LLM key may target a non-OpenAI provider whose embedding endpoint would
// reject it with an opaque 4xx (see OpenAIEmbedderConfig docs).
func applyVectorOpenAIKeyEnv(cfg *Config) {
	if cfg.Vector.OpenAI.APIKey != "" {
		return
	}

	if v := getenv("VV_VECTOR_OPENAI_API_KEY"); v != "" {
		cfg.Vector.OpenAI.APIKey = v
	} else if v := getenv("OPENAI_API_KEY"); v != "" {
		cfg.Vector.OpenAI.APIKey = v
	}
}

// applyModelPricingEnv merges the VV_MODEL_PRICING JSON map into cfg. Invalid
// JSON is logged and ignored (the YAML value is kept); valid entries override
// matching YAML keys and add new ones.
func applyModelPricingEnv(cfg *Config, v string) {
	var mp map[string]ModelPricingEntry
	if err := json.Unmarshal([]byte(v), &mp); err != nil {
		slog.Warn("vv: invalid VV_MODEL_PRICING JSON, ignoring", "error", err)

		return
	}

	if cfg.ModelPricing == nil {
		cfg.ModelPricing = mp

		return
	}

	maps.Copy(cfg.ModelPricing, mp)
}

// --- shared parse/apply helpers ---
//
// Each helper reproduces one of the original inline parse policies. They are
// intentionally split (silent vs warn, constrained vs not, value vs pointer)
// so a binding cannot accidentally acquire a warning or a constraint it did
// not have before.

// applyIntSilent parses v as an int and assigns it on success. Parse failures
// are ignored without a warning (matches VV_MAX_CONTEXT_TOKENS et al.).
func applyIntSilent(v string, dst *int) {
	if n, err := strconv.Atoi(v); err == nil {
		*dst = n
	}
}

// applyIntWarn parses v as an int and assigns it on success; a parse failure
// is logged and the previous value kept. No range constraint.
func applyIntWarn(key, v string, dst *int) {
	if n, err := strconv.Atoi(v); err == nil {
		*dst = n

		return
	}

	warnInvalidEnv(key, v)
}

// applyIntNonNegWarn parses v as a non-negative int and assigns it on success;
// a parse failure OR a negative value is logged and the previous value kept.
func applyIntNonNegWarn(key, v string, dst *int) {
	if n, err := strconv.Atoi(v); err == nil && n >= 0 {
		*dst = n

		return
	}

	warnInvalidEnv(key, v)
}

// applyFloatPtrSilent parses v as a float64 and points dst at it on success.
// Parse failures are ignored without a warning.
func applyFloatPtrSilent(v string, dst **float64) {
	if f, err := strconv.ParseFloat(v, 64); err == nil {
		*dst = &f
	}
}

// applyBoolValWarn parses v as a bool and assigns it on success; a parse
// failure is logged and the previous value kept.
func applyBoolValWarn(key, v string, dst *bool) {
	if b, err := strconv.ParseBool(v); err == nil {
		*dst = b

		return
	}

	warnInvalidEnv(key, v)
}

// applyBoolPtrWarn parses v as a bool and points dst at it on success; a parse
// failure is logged and the previous value kept.
func applyBoolPtrWarn(key, v string, dst **bool) {
	if b, err := strconv.ParseBool(v); err == nil {
		*dst = &b

		return
	}

	warnInvalidEnv(key, v)
}

// applyBudgetInt64 parses v as an int64; a parse failure keeps the budget-
// specific warn wording so the log surface is unchanged from applyBudgetEnv.
func applyBudgetInt64(key, v string, dst *int64) {
	n, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		slog.Warn("vv: invalid budget env, ignoring", "key", key, "value", v)

		return
	}

	*dst = n
}

// applyBudgetFloat64 parses v as a float64 with the budget-specific warn.
func applyBudgetFloat64(key, v string, dst *float64) {
	f, err := strconv.ParseFloat(v, 64)
	if err != nil {
		slog.Warn("vv: invalid budget env, ignoring", "key", key, "value", v)

		return
	}

	*dst = f
}

// warnInvalidEnv emits the standard per-variable parse-failure warning
// ("vv: invalid <KEY>, ignoring", value=...).
func warnInvalidEnv(key, v string) {
	slog.Warn("vv: invalid "+key+", ignoring", "value", v)
}
