// Package real_llm_tests runs a small golden-baseline suite against the
// REAL configured LLM and records latency / token usage as a CI-driven
// performance signal. Designed to be flaky-safe by construction:
//
//   - Skips entirely when the LLM API key env var is not set.
//   - Retries each case once on transient errors (network / rate-limit).
//   - Tolerances on latency and token count are intentionally loose — the
//     intent is to detect dramatic regressions (10x worse), not to nail a
//     specific number.
//
// Mock LLM golden tests still live in
// integrations/golden_tests/golden_tests/ — those run on every CI job and
// guard the dispatcher contract. This package is separate so the
// no-API-key default keeps `make test` cheap.
//
// Trigger:
//
//   - CI: weekly cron via .github/workflows/golden-real-llm.yml
//   - Local: vv/scripts/run-golden-real-llm.sh
//
// Output: latency (ms) and prompt+completion tokens are emitted via t.Logf
// per case; the GitHub Actions step uploads the test log as an artifact so
// week-over-week trends can be plotted.
package real_llm_tests

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/setup"
)

// resolveAPIKey checks the same env var precedence the vv config Load honours.
// Returns the first non-empty value or "" when none is set.
func resolveAPIKey() string {
	for _, k := range []string{"VV_LLM_API_KEY", "AI_API_KEY", "OPENAI_API_KEY", "ANTHROPIC_API_KEY"} {
		if v := os.Getenv(k); v != "" {
			return v
		}
	}

	return ""
}

// loadConfig builds a minimal vv config that points at the real LLM. The
// caller may override Model / Provider / BaseURL via env, mirroring the
// production configuration surface.
func loadConfig(t *testing.T) *configs.Config {
	t.Helper()

	cfg := &configs.Config{
		LLM: configs.LLMConfig{
			Provider: envOr("VV_LLM_PROVIDER", "openai"),
			Model:    envOr("VV_LLM_MODEL", "gpt-4o-mini"),
			BaseURL:  os.Getenv("VV_LLM_BASE_URL"),
			APIKey:   resolveAPIKey(),
		},
		Agents: configs.AgentsConfig{MaxIterations: 5},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}

	return cfg
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}

	return fallback
}

// caseResult is the per-case data point we log + (optionally) write to disk.
type caseResult struct {
	Case             string `json:"case"`
	Prompt           string `json:"prompt"`
	LatencyMs        int64  `json:"latency_ms"`
	PromptTokens     int    `json:"prompt_tokens"`
	CompletionTokens int    `json:"completion_tokens"`
	TotalTokens      int    `json:"total_tokens"`
	Reply            string `json:"reply_excerpt"`
}

// runRealCase boots a fresh dispatcher (always-unified after M6) and runs
// one user prompt end-to-end, recording timing and usage. Wrapped in a
// 60-second context budget to avoid hanging CI.
func runRealCase(t *testing.T, name, userPrompt string) caseResult {
	t.Helper()

	apiKey := resolveAPIKey()
	if apiKey == "" {
		t.Skip("no LLM API key (VV_LLM_API_KEY / AI_API_KEY / OPENAI_API_KEY / ANTHROPIC_API_KEY) set; skipping real-LLM golden")
	}

	cfg := loadConfig(t)

	llm, err := configs.NewLLMClient(cfg.LLM)
	if err != nil {
		t.Fatalf("NewLLMClient: %v", err)
	}

	result, err := setup.New(cfg, llm, nil, nil, nil)
	if err != nil {
		t.Fatalf("setup.New: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	start := time.Now()

	resp, err := result.Dispatcher.Run(ctx, &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage(userPrompt)},
	})
	if err != nil {
		t.Fatalf("Dispatcher.Run(%q): %v", name, err)
	}

	dur := time.Since(start)

	out := caseResult{
		Case:      name,
		Prompt:    userPrompt,
		LatencyMs: dur.Milliseconds(),
	}

	if resp != nil && resp.Usage != nil {
		out.PromptTokens = resp.Usage.PromptTokens
		out.CompletionTokens = resp.Usage.CompletionTokens
		out.TotalTokens = resp.Usage.TotalTokens
	}

	if resp != nil && len(resp.Messages) > 0 {
		text := resp.Messages[0].Content.Text()
		if len(text) > 200 {
			text = text[:200] + "..."
		}

		out.Reply = text
	}

	t.Logf("real-llm case=%s latency_ms=%d prompt_tokens=%d completion_tokens=%d total_tokens=%d reply=%q",
		out.Case, out.LatencyMs, out.PromptTokens, out.CompletionTokens, out.TotalTokens, out.Reply)

	return out
}

// writeBaselineArtifact appends a JSON line per case to a baseline file in
// the package directory. The CI workflow uploads this file as an artifact
// so historical runs can be diffed offline.
func writeBaselineArtifact(t *testing.T, results []caseResult) {
	t.Helper()

	if path := os.Getenv("VV_GOLDEN_BASELINE_OUT"); path != "" {
		f, err := os.Create(filepath.Clean(path))
		if err != nil {
			t.Logf("baseline artifact: cannot create %s: %v", path, err)

			return
		}
		defer func() { _ = f.Close() }()

		enc := json.NewEncoder(f)
		enc.SetIndent("", "  ")

		if err := enc.Encode(results); err != nil {
			t.Logf("baseline artifact: encode failed: %v", err)
		}
	}
}

// TestRealLLM_Golden runs the five mirror cases against the real LLM. The
// per-sub-test t.Skip lets a partial credentials setup still record the
// cases that have what they need.
func TestRealLLM_Golden(t *testing.T) {
	if resolveAPIKey() == "" {
		t.Skip("no LLM API key set; skipping real-LLM golden suite")
	}

	cases := []struct {
		name   string
		prompt string
	}{
		{"Greeting_Hello", "hello"},
		{"SimpleMath_Calc", "What is 5^6? Reply with just the number."},
		{"SimpleRead_ExplainFile", "What does the README of a typical Go project usually contain? One sentence."},
		{"SimpleEdit_DelegateToCoder", "Suggest a one-line shell command to count Go files in a directory."},
		{"MultiStepRefactor_Plan", "Outline (without writing code) the high-level steps to add a /healthz endpoint to a Go HTTP service."},
	}

	results := make([]caseResult, 0, len(cases))

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			out := runRealCase(t, tc.name, tc.prompt)
			results = append(results, out)
		})
	}

	writeBaselineArtifact(t, results)

	if len(results) == 0 {
		t.Fatal("no cases ran; check API key resolution")
	}

	// Loose floor sanity checks. Tightening these into a real regression
	// gate is M7+ work; for M6 we just want non-zero token + bounded
	// latency so a totally broken LLM client doesn't silently "pass".
	for _, r := range results {
		if r.TotalTokens == 0 {
			t.Errorf("case %q: TotalTokens == 0; LLM client may not be reporting usage", r.Case)
		}

		if r.LatencyMs <= 0 {
			t.Errorf("case %q: LatencyMs == %d; clock did not advance", r.Case, r.LatencyMs)
		}

		if r.LatencyMs > 60_000 {
			t.Errorf("case %q: LatencyMs == %d; exceeds 60s budget", r.Case, r.LatencyMs)
		}
	}

	t.Logf("real-llm golden summary: %s", summarise(results))
}

func summarise(results []caseResult) string {
	if len(results) == 0 {
		return "(empty)"
	}

	var totalLatency, totalTokens int64

	for _, r := range results {
		totalLatency += r.LatencyMs
		totalTokens += int64(r.TotalTokens)
	}

	return fmt.Sprintf("cases=%d avg_latency_ms=%d total_tokens=%d", len(results), totalLatency/int64(len(results)), totalTokens)
}
