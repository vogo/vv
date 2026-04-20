package configs

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLoad_EvalDefaults(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(path, []byte("llm:\n  api_key: test\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Eval.Enabled {
		t.Errorf("eval.enabled default should be false")
	}

	if cfg.Eval.Concurrency != 1 {
		t.Errorf("concurrency = %d, want 1", cfg.Eval.Concurrency)
	}

	if cfg.Eval.TimeoutMs != 60000 {
		t.Errorf("timeout_ms = %d, want 60000", cfg.Eval.TimeoutMs)
	}

	if len(cfg.Eval.Evaluators) != 2 || cfg.Eval.Evaluators[0] != "latency" || cfg.Eval.Evaluators[1] != "cost" {
		t.Errorf("evaluators = %v, want [latency cost]", cfg.Eval.Evaluators)
	}

	if cfg.Eval.LatencyThresholdMs != 60000 {
		t.Errorf("latency_threshold_ms = %d, want 60000", cfg.Eval.LatencyThresholdMs)
	}

	if cfg.Eval.CostBudgetTokens != 10000 {
		t.Errorf("cost_budget_tokens = %d, want 10000", cfg.Eval.CostBudgetTokens)
	}
}

func TestLoad_EvalUnknownEvaluator(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	content := "llm:\n  api_key: test\neval:\n  evaluators: [bogus]\n"
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	_, err := Load(path, true)
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("want error mentioning bogus, got %v", err)
	}
}

func TestLoad_EvalEnvEnabled(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")

	if err := os.WriteFile(path, []byte("llm:\n  api_key: test\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("VV_EVAL_ENABLED", "true")

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if !cfg.Eval.Enabled {
		t.Errorf("VV_EVAL_ENABLED=true should enable eval")
	}
}
