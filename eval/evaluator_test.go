package eval

import (
	"strings"
	"testing"

	vageeval "github.com/vogo/vage/eval"
	"github.com/vogo/vv/configs"
)

func TestBuild_Latency(t *testing.T) {
	cfg := configs.EvalConfig{
		Evaluators:         []string{"latency"},
		LatencyThresholdMs: 1000,
	}

	e, err := Build(cfg, nil, "")
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if _, ok := e.(*vageeval.LatencyEval); !ok {
		t.Errorf("want *LatencyEval, got %T", e)
	}
}

func TestBuild_Cost(t *testing.T) {
	cfg := configs.EvalConfig{
		Evaluators:       []string{"cost"},
		CostBudgetTokens: 1000,
	}

	e, err := Build(cfg, nil, "")
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if _, ok := e.(*vageeval.CostEval); !ok {
		t.Errorf("want *CostEval, got %T", e)
	}
}

func TestBuild_Composite(t *testing.T) {
	cfg := configs.EvalConfig{
		Evaluators:         []string{"latency", "cost"},
		LatencyThresholdMs: 1000,
		CostBudgetTokens:   500,
	}

	e, err := Build(cfg, nil, "")
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if _, ok := e.(*vageeval.CompositeEvaluator); !ok {
		t.Errorf("want *CompositeEvaluator, got %T", e)
	}
}

func TestBuild_EmptyEvaluators(t *testing.T) {
	if _, err := Build(configs.EvalConfig{}, nil, ""); err == nil {
		t.Fatal("want error for empty evaluators")
	}
}

func TestBuild_Unknown(t *testing.T) {
	cfg := configs.EvalConfig{Evaluators: []string{"bogus"}}

	_, err := Build(cfg, nil, "")
	if err == nil || !strings.Contains(err.Error(), "bogus") {
		t.Fatalf("want error mentioning %q, got %v", "bogus", err)
	}
}

func TestBuild_ContainsRequiresKeywords(t *testing.T) {
	cfg := configs.EvalConfig{Evaluators: []string{"contains"}}

	_, err := Build(cfg, nil, "")
	if err == nil || !strings.Contains(err.Error(), "contains_keywords") {
		t.Fatalf("want error about contains_keywords, got %v", err)
	}
}

func TestBuild_Contains(t *testing.T) {
	cfg := configs.EvalConfig{
		Evaluators:       []string{"contains"},
		ContainsKeywords: []string{"go"},
	}

	e, err := Build(cfg, nil, "")
	if err != nil {
		t.Fatalf("build: %v", err)
	}

	if _, ok := e.(*vageeval.ContainsEval); !ok {
		t.Errorf("want *ContainsEval, got %T", e)
	}
}

func TestBuild_LLMJudgeRequiresLLM(t *testing.T) {
	cfg := configs.EvalConfig{Evaluators: []string{"llm_judge"}}

	_, err := Build(cfg, nil, "")
	if err == nil || !strings.Contains(err.Error(), "LLM client") {
		t.Fatalf("want error about LLM client, got %v", err)
	}
}
