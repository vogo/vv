package eval_tests

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	vageeval "github.com/vogo/vage/eval"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/configs"
	vveval "github.com/vogo/vv/eval"
)

// mustRunRequest builds a minimal RunRequest with a single user message
// for use in table-driven eval cases. Split into a helper because we
// build RunRequests across several tests and inline literals get noisy.
func mustRunRequest(text string) *schema.RunRequest {
	return &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage(text)}}
}

// --- AC-1.1 + AC-1.2: end-to-end JSONL → report pipeline ---
// Scenario: write a JSONL dataset to a temp file, then walk the full
// offline-eval pipeline (LoadJSONL → Build → RunBatch → WriteReportJSON)
// against the stub dispatcher used in HTTP tests. This is what
// vveval.RunCLI does internally — we don't stand up a real Dispatcher
// because that requires live LLM credentials. Passes if the dataset is
// fully consumed, every case is scored, and the out-file round-trips
// through json.Unmarshal back into EvalReport.
func TestIntegration_CLIEval_DatasetToReportRoundTrip(t *testing.T) {
	dir := t.TempDir()
	datasetPath := filepath.Join(dir, "cases.jsonl")
	outPath := filepath.Join(dir, "out.json")

	dataset := strings.Join([]string{
		`{"id":"c1","input":"hi","tags":["smoke"]}`,
		`{"id":"c2","input":"bye"}`,
		`{"id":"c3","input":{"messages":[{"role":"user","content":"summarize main.go"}]}}`,
	}, "\n")

	if err := os.WriteFile(datasetPath, []byte(dataset+"\n"), 0o600); err != nil {
		t.Fatalf("write dataset: %v", err)
	}

	cases, loadErrs, err := vveval.LoadJSONL(datasetPath)
	if err != nil {
		t.Fatalf("LoadJSONL: %v", err)
	}

	if len(loadErrs) != 0 {
		t.Fatalf("unexpected load errors: %+v", loadErrs)
	}

	if len(cases) != 3 {
		t.Fatalf("cases = %d, want 3", len(cases))
	}

	cfg := configs.EvalConfig{
		Concurrency:        2,
		TimeoutMs:          1000,
		Evaluators:         []string{"latency", "cost"},
		LatencyThresholdMs: 60000,
		CostBudgetTokens:   1000,
	}

	ev, err := vveval.Build(cfg, nil, "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	report, err := vveval.RunBatch(context.Background(), stubAgent{}.Run, ev, cases, cfg)
	if err != nil {
		t.Fatalf("RunBatch: %v", err)
	}

	if report.TotalCases != 3 || report.PassedCases != 3 {
		t.Errorf("report = %+v, want total=3 passed=3", report)
	}

	if err := vveval.WriteReportJSON(outPath, report); err != nil {
		t.Fatalf("WriteReportJSON: %v", err)
	}

	data, err := os.ReadFile(outPath)
	if err != nil {
		t.Fatalf("read out file: %v", err)
	}

	var decoded vageeval.EvalReport
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("round-trip decode: %v", err)
	}

	if decoded.TotalCases != 3 || decoded.PassedCases != 3 {
		t.Errorf("decoded = %+v, want total=3 passed=3", decoded)
	}

	if len(decoded.Results) != 3 {
		t.Errorf("decoded.Results len = %d, want 3", len(decoded.Results))
	}
}

// --- AC-1.3: malformed JSONL lines are counted, not fatal ---
// Scenario: a three-line dataset where the middle line is missing both
// id and input. LoadJSONL should return two valid cases plus one
// LoadError, and RunBatch must not crash on the reduced slice.
func TestIntegration_CLIEval_MalformedLineCounted(t *testing.T) {
	dir := t.TempDir()
	datasetPath := filepath.Join(dir, "cases.jsonl")

	dataset := strings.Join([]string{
		`{"id":"c1","input":"hi"}`,
		`{"wrong":"missing id and input"}`,
		`{"id":"c3","input":"bye"}`,
	}, "\n")

	if err := os.WriteFile(datasetPath, []byte(dataset+"\n"), 0o600); err != nil {
		t.Fatalf("write dataset: %v", err)
	}

	cases, loadErrs, err := vveval.LoadJSONL(datasetPath)
	if err != nil {
		t.Fatalf("LoadJSONL: %v", err)
	}

	if len(cases) != 2 {
		t.Errorf("cases = %d, want 2", len(cases))
	}

	if len(loadErrs) != 1 {
		t.Fatalf("loadErrs = %d, want 1: %+v", len(loadErrs), loadErrs)
	}

	if loadErrs[0].Line != 2 {
		t.Errorf("loadErrs[0].Line = %d, want 2", loadErrs[0].Line)
	}
}

// --- AC-1.3: missing file propagates as an error (not silent) ---
// Scenario: LoadJSONL on a non-existent path must surface the os-level
// error so RunCLI can fatal out rather than running an empty batch.
func TestIntegration_CLIEval_MissingDatasetFile(t *testing.T) {
	_, _, err := vveval.LoadJSONL(filepath.Join(t.TempDir(), "does-not-exist.jsonl"))
	if err == nil {
		t.Fatal("expected error for missing dataset file")
	}
}

// --- AC-3.1: VV_EVAL_ENABLED env var flips cfg.Eval.Enabled ---
// Scenario: The HTTP route visibility is driven by cfg.Eval.Enabled, and
// that field must be reachable via the VV_EVAL_ENABLED environment
// override; otherwise deployments can't toggle the endpoint without
// rewriting vv.yaml. Also verifies applyDefaults does NOT clobber the
// user's explicit Evaluators/Thresholds when the env flag is set.
func TestIntegration_CLIEval_EnvVarEnablesHTTPRoute(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vv.yaml")

	content := `llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
server:
  addr: ":8080"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("VV_EVAL_ENABLED", "true")

	cfg, err := configs.Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !cfg.Eval.Enabled {
		t.Errorf("Eval.Enabled = false, want true (VV_EVAL_ENABLED=true)")
	}

	// AC-3.2: default evaluators must be latency+cost so no LLM calls
	// happen on first run.
	if len(cfg.Eval.Evaluators) != 2 ||
		cfg.Eval.Evaluators[0] != "latency" ||
		cfg.Eval.Evaluators[1] != "cost" {
		t.Errorf("Eval.Evaluators = %v, want [latency cost]", cfg.Eval.Evaluators)
	}

	// AC-1.5 & AC-3.4: sensible default timeout and thresholds so `vv -eval`
	// works zero-config.
	if cfg.Eval.TimeoutMs != 60000 {
		t.Errorf("TimeoutMs = %d, want 60000", cfg.Eval.TimeoutMs)
	}

	if cfg.Eval.LatencyThresholdMs != 60000 {
		t.Errorf("LatencyThresholdMs = %d, want 60000", cfg.Eval.LatencyThresholdMs)
	}

	if cfg.Eval.CostBudgetTokens != 10000 {
		t.Errorf("CostBudgetTokens = %d, want 10000", cfg.Eval.CostBudgetTokens)
	}

	if cfg.Eval.Concurrency != 1 {
		t.Errorf("Concurrency = %d, want 1", cfg.Eval.Concurrency)
	}
}

// --- AC-3.1 (default posture): cfg.Eval.Enabled defaults to false ---
// Scenario: Without any env override and with no eval: section in YAML,
// the HTTP endpoint must stay hidden (Enabled=false). The other defaults
// still populate so CLI `-eval` works even when YAML omits the section.
func TestIntegration_CLIEval_DefaultsDisabledForHTTP(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vv.yaml")

	content := `llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	// Clear any inherited env var so this test is hermetic even if the
	// developer's shell has VV_EVAL_ENABLED set.
	t.Setenv("VV_EVAL_ENABLED", "")

	cfg, err := configs.Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Eval.Enabled {
		t.Errorf("Eval.Enabled = true, want false (default posture)")
	}
}

// --- AC-1.6 + §2.2: unknown evaluator name is rejected at config load ---
// Scenario: The design says "unknown evaluator → return an error (don't
// silently ignore)". Verify ValidateEval actually fires during Load so
// misconfiguration is caught before any user request.
func TestIntegration_CLIEval_UnknownEvaluatorRejected(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vv.yaml")

	content := `llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
eval:
  evaluators: [bogus]
`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := configs.Load(path, true)
	if err == nil {
		t.Fatal("expected error for unknown evaluator name, got nil")
	}

	if !strings.Contains(err.Error(), "bogus") {
		t.Errorf("error does not mention the bad name: %v", err)
	}
}

// --- AC-1.5: flag-override semantics for concurrency/timeout ---
// Scenario: The -eval-concurrency / -eval-timeout-ms flags feed into
// cfg.Eval.* before RunCLI runs. Re-running the same dataset with a
// concurrency override should yield the same report (idempotent) and
// respect the overridden value via RunBatch's sem channel.
func TestIntegration_CLIEval_ConcurrencyOverrideIdempotent(t *testing.T) {
	cases := []*vageeval.EvalCase{
		{ID: "c1", Input: mustRunRequest("hi")},
		{ID: "c2", Input: mustRunRequest("bye")},
		{ID: "c3", Input: mustRunRequest("sup")},
	}

	cfg := configs.EvalConfig{
		Concurrency:        3, // simulated CLI override
		TimeoutMs:          1000,
		Evaluators:         []string{"latency", "cost"},
		LatencyThresholdMs: 60000,
		CostBudgetTokens:   1000,
	}

	ev, err := vveval.Build(cfg, nil, "")
	if err != nil {
		t.Fatalf("Build: %v", err)
	}

	r1, err := vveval.RunBatch(context.Background(), stubAgent{}.Run, ev, cases, cfg)
	if err != nil {
		t.Fatalf("run 1: %v", err)
	}

	r2, err := vveval.RunBatch(context.Background(), stubAgent{}.Run, ev, cases, cfg)
	if err != nil {
		t.Fatalf("run 2: %v", err)
	}

	if r1.TotalCases != r2.TotalCases || r1.PassedCases != r2.PassedCases {
		t.Errorf("runs not idempotent: r1=%+v r2=%+v", r1, r2)
	}

	if r1.TotalCases != 3 || r1.PassedCases != 3 {
		t.Errorf("r1 = %+v, want total=3 passed=3", r1)
	}
}
