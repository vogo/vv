package eval

import (
	"errors"
	"fmt"
	"strings"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/eval"
	"github.com/vogo/vv/configs"
)

// Build assembles the configured evaluator chain. A single evaluator is
// returned directly; multiple are composed via vage/eval.CompositeEvaluator
// with equal weights. Returns an error for misconfiguration so the caller
// can fail fast instead of surfacing it per-case.
func Build(cfg configs.EvalConfig, llm aimodel.ChatCompleter, defaultModel string) (eval.Evaluator, error) {
	names := cfg.Evaluators
	if len(names) == 0 {
		return nil, errors.New("eval.evaluators is empty")
	}

	evaluators := make([]eval.WeightedEvaluator, 0, len(names))

	for _, raw := range names {
		name := strings.ToLower(strings.TrimSpace(raw))

		var (
			e   eval.Evaluator
			err error
		)

		switch name {
		case "latency":
			e, err = eval.NewLatencyEval(cfg.LatencyThresholdMs)
		case "cost":
			e, err = eval.NewCostEval(&eval.CostConfig{Budget: cfg.CostBudgetTokens})
		case "contains":
			if len(cfg.ContainsKeywords) == 0 {
				return nil, errors.New(`eval.contains_keywords required when "contains" is in eval.evaluators`)
			}

			e, err = eval.NewContainsEval(&eval.ContainsConfig{Keywords: cfg.ContainsKeywords})
		case "llm_judge":
			if llm == nil {
				return nil, errors.New(`eval "llm_judge" requires an LLM client`)
			}

			model := cfg.LLMJudgeModel
			if model == "" {
				model = defaultModel
			}

			if model == "" {
				return nil, errors.New(`eval "llm_judge" requires eval.llm_judge_model or llm.model`)
			}

			e, err = eval.NewLLMJudgeEval(llm, model)
		default:
			return nil, fmt.Errorf("unknown evaluator %q", raw)
		}

		if err != nil {
			return nil, fmt.Errorf("build %s evaluator: %w", name, err)
		}

		evaluators = append(evaluators, eval.WeightedEvaluator{Evaluator: e, Weight: 1.0})
	}

	if len(evaluators) == 1 {
		return evaluators[0].Evaluator, nil
	}

	return eval.NewCompositeEvaluator(&eval.CompositeConfig{}, evaluators...)
}
