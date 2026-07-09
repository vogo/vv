package configs

import (
	"bytes"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

// loadYAML writes content to a temp vv.yaml and loads it explicitly.
func loadYAML(t *testing.T, content string) *Config {
	t.Helper()

	dir := t.TempDir()
	path := filepath.Join(dir, "vv.yaml")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	return cfg
}

// captureWarn redirects slog to a buffer for the duration of fn and returns
// the captured output, so tests can assert on parse-failure warnings.
func captureWarn(t *testing.T, fn func()) string {
	t.Helper()

	var buf bytes.Buffer
	prev := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelWarn})))
	t.Cleanup(func() { slog.SetDefault(prev) })

	fn()

	return buf.String()
}

// TestEnvBinding_OverrideAndFallthrough is the table-driven precedence check
// the spec calls for: for a representative binding of every value kind
// (string, bool pointer, bool value, silent int, warning int, non-negative
// int, float pointer, budget int64, budget float64), a non-empty env value
// overrides the YAML value, and a missing env var leaves the YAML value intact.
func TestEnvBinding_OverrideAndFallthrough(t *testing.T) {
	cases := []struct {
		name    string
		yaml    string
		envKey  string
		envVal  string
		fromEnv func(*Config) string // stringified field value
		wantEnv string               // expected when env set
		wantCfg string               // expected when env unset (YAML value)
	}{
		{
			name:    "string/llm_model",
			yaml:    "llm:\n  model: yaml-model\n",
			envKey:  "VV_LLM_MODEL",
			envVal:  "env-model",
			fromEnv: func(c *Config) string { return c.LLM.Model },
			wantEnv: "env-model",
			wantCfg: "yaml-model",
		},
		{
			name:    "boolptr/session_enabled",
			yaml:    "session:\n  enabled: true\n",
			envKey:  "VV_SESSION_ENABLED",
			envVal:  "false",
			fromEnv: func(c *Config) string { return boolStr(c.Session.IsEnabled()) },
			wantEnv: "false",
			wantCfg: "true",
		},
		{
			name:    "boolval/debug",
			yaml:    "debug: false\n",
			envKey:  "VV_DEBUG",
			envVal:  "true",
			fromEnv: func(c *Config) string { return boolStr(c.Debug) },
			wantEnv: "true",
			wantCfg: "false",
		},
		{
			name:    "intsilent/max_context_tokens",
			yaml:    "context:\n  model_max_context_tokens: 111\n",
			envKey:  "VV_MAX_CONTEXT_TOKENS",
			envVal:  "222",
			fromEnv: func(c *Config) string { return itoa(c.Context.ModelMaxContextTokens) },
			wantEnv: "222",
			wantCfg: "111",
		},
		{
			name:    "intwarn/max_parallel_tool_calls",
			yaml:    "agents:\n  max_parallel_tool_calls: 3\n",
			envKey:  "VV_AGENTS_MAX_PARALLEL_TOOL_CALLS",
			envVal:  "9",
			fromEnv: func(c *Config) string { return itoa(c.Agents.MaxParallelToolCalls) },
			wantEnv: "9",
			wantCfg: "3",
		},
		{
			name:    "intnonneg/vector_top_k",
			yaml:    "vector:\n  top_k: 4\n",
			envKey:  "VV_VECTOR_TOP_K",
			envVal:  "8",
			fromEnv: func(c *Config) string { return itoa(c.Vector.TopK) },
			wantEnv: "8",
			wantCfg: "4",
		},
		{
			name:    "floatptr/compression_threshold",
			yaml:    "context:\n  compression_threshold: 0.3\n",
			envKey:  "VV_CONTEXT_COMPRESSION_THRESHOLD",
			envVal:  "0.6",
			fromEnv: func(c *Config) string { return ftoa(c.Context.EffectiveCompressionThreshold()) },
			wantEnv: "0.6",
			wantCfg: "0.3",
		},
		{
			name:    "budgetint64/session_hard_tokens",
			yaml:    "budget:\n  session_hard_tokens: 100\n",
			envKey:  "VV_BUDGET_SESSION_HARD_TOKENS",
			envVal:  "500",
			fromEnv: func(c *Config) string { return i64toa(c.Budget.SessionHardTokens) },
			wantEnv: "500",
			wantCfg: "100",
		},
		{
			name:    "budgetfloat64/warn_percent",
			yaml:    "budget:\n  warn_percent: 0.6\n",
			envKey:  "VV_BUDGET_WARN_PERCENT",
			envVal:  "0.9",
			fromEnv: func(c *Config) string { return ftoa(c.Budget.WarnPercent) },
			wantEnv: "0.9",
			wantCfg: "0.6",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name+"/env_overrides_yaml", func(t *testing.T) {
			t.Setenv(tc.envKey, tc.envVal)
			cfg := loadYAML(t, tc.yaml)
			if got := tc.fromEnv(cfg); got != tc.wantEnv {
				t.Errorf("%s with env %s=%s: got %q, want %q", tc.name, tc.envKey, tc.envVal, got, tc.wantEnv)
			}
		})

		t.Run(tc.name+"/missing_env_keeps_yaml", func(t *testing.T) {
			// TestMain has already cleared inherited VV_* vars.
			cfg := loadYAML(t, tc.yaml)
			if got := tc.fromEnv(cfg); got != tc.wantCfg {
				t.Errorf("%s with env unset: got %q, want YAML %q", tc.name, got, tc.wantCfg)
			}
		})
	}
}

// TestEnvBinding_InvalidValues pins the per-variable parse-failure policy: a
// malformed value keeps the YAML value, and the warning is emitted only for
// variables that warned before (silent int overrides stay silent; budget vars
// keep their distinct "invalid budget env" wording).
func TestEnvBinding_InvalidValues(t *testing.T) {
	cases := []struct {
		name     string
		yaml     string
		envKey   string
		envVal   string
		read     func(*Config) string
		wantKept string
		wantWarn string // substring expected in log; "" means no warning
	}{
		{
			name:     "boolval/debug_invalid_warns",
			yaml:     "debug: true\n",
			envKey:   "VV_DEBUG",
			envVal:   "notabool",
			read:     func(c *Config) string { return boolStr(c.Debug) },
			wantKept: "true",
			wantWarn: "invalid VV_DEBUG",
		},
		{
			name:     "intwarn/max_parallel_invalid_warns",
			yaml:     "agents:\n  max_parallel_tool_calls: 3\n",
			envKey:   "VV_AGENTS_MAX_PARALLEL_TOOL_CALLS",
			envVal:   "abc",
			read:     func(c *Config) string { return itoa(c.Agents.MaxParallelToolCalls) },
			wantKept: "3",
			wantWarn: "invalid VV_AGENTS_MAX_PARALLEL_TOOL_CALLS",
		},
		{
			name:     "intnonneg/vector_top_k_negative_warns",
			yaml:     "vector:\n  top_k: 4\n",
			envKey:   "VV_VECTOR_TOP_K",
			envVal:   "-5",
			read:     func(c *Config) string { return itoa(c.Vector.TopK) },
			wantKept: "4",
			wantWarn: "invalid VV_VECTOR_TOP_K",
		},
		{
			name:     "intsilent/max_context_tokens_invalid_silent",
			yaml:     "context:\n  model_max_context_tokens: 111\n",
			envKey:   "VV_MAX_CONTEXT_TOKENS",
			envVal:   "abc",
			read:     func(c *Config) string { return itoa(c.Context.ModelMaxContextTokens) },
			wantKept: "111",
			wantWarn: "", // silent override: no warning
		},
		{
			name:     "budgetfloat/warn_percent_invalid_uses_budget_wording",
			yaml:     "budget:\n  warn_percent: 0.6\n",
			envKey:   "VV_BUDGET_WARN_PERCENT",
			envVal:   "notafloat",
			read:     func(c *Config) string { return ftoa(c.Budget.WarnPercent) },
			wantKept: "0.6",
			wantWarn: "invalid budget env",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv(tc.envKey, tc.envVal)

			var cfg *Config
			out := captureWarn(t, func() { cfg = loadYAML(t, tc.yaml) })

			if got := tc.read(cfg); got != tc.wantKept {
				t.Errorf("invalid %s=%s: field = %q, want kept YAML %q", tc.envKey, tc.envVal, got, tc.wantKept)
			}

			if tc.wantWarn == "" {
				if bytes.Contains([]byte(out), []byte("invalid "+tc.envKey)) {
					t.Errorf("expected no warning for silent override %s, got: %q", tc.envKey, out)
				}

				return
			}

			if !bytes.Contains([]byte(out), []byte(tc.wantWarn)) {
				t.Errorf("expected warning %q for %s, got: %q", tc.wantWarn, tc.envKey, out)
			}
		})
	}
}

// TestEnvBindings_KeysUniqueAndComplete guards the binding table against a
// silently dropped or duplicated key (the exact regression that would let a
// VV_* switch stop working). It asserts the full expected key set is present
// with no duplicates.
func TestEnvBindings_KeysUniqueAndComplete(t *testing.T) {
	seen := map[string]int{}
	for _, b := range envBindings {
		seen[b.key]++
	}

	for k, n := range seen {
		if n != 1 {
			t.Errorf("env binding key %q declared %d times, want 1", k, n)
		}
	}

	want := []string{
		"VV_LLM_API_KEY", "VV_LLM_BASE_URL", "VV_LLM_MODEL", "VV_LLM_PROVIDER",
		"VV_SERVER_ADDR", "VV_MODE", "VV_PERMISSION_MODE", "VV_DEBUG",
		"VV_MAX_CONTEXT_TOKENS", "VV_CONTEXT_COMPRESSION_THRESHOLD",
		"VV_TOOL_OUTPUT_MAX_TOKENS", "VV_CONTEXT_PROTECTED_TURNS",
		"VV_TRACE_ENABLED", "VV_TRACE_DIR",
		"VV_SESSION_ENABLED", "VV_SESSION_DIR",
		"VV_TREE_ENABLED", "VV_TREE_PROMOTION_ENABLED", "VV_TREE_PROMOTER", "VV_TREE_AUTO_AFTER_EVENTS",
		"VV_DISPATCHER_WRITE_TREE", "VV_ORCHESTRATE_MODE", "VV_ORCHESTRATE_LEGACY_PHASE_EVENTS",
		"VV_PRIMARY_ALLOW_BASH", "VV_ROUTER_MODEL", "VV_ROUTER_PROVIDER", "VV_ROUTER_API_KEY", "VV_ROUTER_BASE_URL",
		"VV_MEMORY_BACKEND",
		"VV_VECTOR_ENABLED", "VV_VECTOR_BACKEND", "VV_VECTOR_EMBEDDER", "VV_VECTOR_AUTO_WRITE",
		"VV_VECTOR_TOP_K", "VV_VECTOR_COLLECTION", "VV_QDRANT_URL", "VV_QDRANT_API_KEY",
		"VV_VECTOR_OPENAI_MODEL", "VV_VECTOR_OPENAI_BASE_URL", "VV_VECTOR_OPENAI_DIMENSIONS",
		"VV_AGENTS_MAX_PARALLEL_TOOL_CALLS", "VV_AGENTS_PROMPT_CACHING",
		"VV_MCP_CREDFILTER_ENABLED", "VV_MCP_CREDFILTER_ACTION",
		"VV_EVAL_ENABLED",
		"VV_MCP_TRANSPORT", "VV_MCP_ADDR", "VV_MCP_AUTH_TOKEN",
		"VV_WEB_SEARCH_PROVIDER", "VV_WEB_SEARCH_API_KEY", "VV_WEB_SEARCH_BASE_URL",
		"VV_WEB_SEARCH_LANGUAGE", "VV_WEB_SEARCH_CATEGORIES", "VV_WEB_SEARCH_USER_AGENT",
		"VV_WEB_SEARCH_TIMEOUT_SECONDS", "VV_WEB_SEARCH_MAX_RESULTS",
		"VV_BUDGET_SESSION_HARD_TOKENS", "VV_BUDGET_SESSION_HARD_COST_USD",
		"VV_BUDGET_DAILY_HARD_TOKENS", "VV_BUDGET_DAILY_HARD_COST_USD", "VV_BUDGET_WARN_PERCENT",
		"VV_MODEL_PRICING",
	}

	for _, k := range want {
		if seen[k] == 0 {
			t.Errorf("env binding table missing key %q", k)
		}
	}

	if len(seen) != len(want) {
		t.Errorf("env binding table has %d keys, want %d (list drifted)", len(seen), len(want))
	}
}

// TestLoad_Defaults_RepresentativeSubsystems locks in that an empty config
// with no env vars still yields the representative defaults across subsystems
// (server, agents, eval, budget) via the four-phase pipeline.
func TestLoad_Defaults_RepresentativeSubsystems(t *testing.T) {
	cfg := loadYAML(t, "")

	if cfg.Server.Addr != ":8080" {
		t.Errorf("server.addr = %q, want :8080", cfg.Server.Addr)
	}
	if cfg.Agents.MaxIterations != 10 {
		t.Errorf("agents.max_iterations = %d, want 10", cfg.Agents.MaxIterations)
	}
	if cfg.Eval.Concurrency != 1 {
		t.Errorf("eval.concurrency = %d, want 1", cfg.Eval.Concurrency)
	}
	if len(cfg.Eval.Evaluators) != 2 {
		t.Errorf("eval.evaluators = %v, want [latency cost]", cfg.Eval.Evaluators)
	}
	if cfg.Budget.WarnPercent != 0.8 {
		t.Errorf("budget.warn_percent = %v, want 0.8", cfg.Budget.WarnPercent)
	}
}

// TestVectorOpenAIKey_YAMLWinsOverEnv confirms the bespoke fallback keeps YAML
// as the highest precedence: neither env var overrides an explicit YAML key.
func TestVectorOpenAIKey_YAMLWinsOverEnv(t *testing.T) {
	t.Setenv("VV_VECTOR_OPENAI_API_KEY", "env-vector-key")
	t.Setenv("OPENAI_API_KEY", "env-openai-key")

	cfg := loadYAML(t, "llm:\n  api_key: x\nvector:\n  openai:\n    api_key: yaml-key\n")

	if cfg.Vector.OpenAI.APIKey != "yaml-key" {
		t.Errorf("YAML vector openai key must win, got %q", cfg.Vector.OpenAI.APIKey)
	}
}

// TestVectorOpenAIKey_EnvPrecedenceOrder confirms VV_VECTOR_OPENAI_API_KEY
// takes precedence over OPENAI_API_KEY when YAML is empty.
func TestVectorOpenAIKey_EnvPrecedenceOrder(t *testing.T) {
	t.Setenv("VV_VECTOR_OPENAI_API_KEY", "vector-key")
	t.Setenv("OPENAI_API_KEY", "openai-key")

	cfg := loadYAML(t, "llm:\n  api_key: x\n")

	if cfg.Vector.OpenAI.APIKey != "vector-key" {
		t.Errorf("VV_VECTOR_OPENAI_API_KEY must win over OPENAI_API_KEY, got %q", cfg.Vector.OpenAI.APIKey)
	}
}

// --- small stringify helpers kept local so assertions stay type-agnostic. ---

func boolStr(b bool) string {
	if b {
		return "true"
	}

	return "false"
}

func itoa(n int) string { return strconv.Itoa(n) }

func i64toa(n int64) string { return strconv.FormatInt(n, 10) }

func ftoa(f float64) string { return strconv.FormatFloat(f, 'g', -1, 64) }
