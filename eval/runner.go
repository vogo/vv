package eval

import (
	"context"
	"errors"
	"fmt"
	"io"
	"sync"
	"time"

	vageeval "github.com/vogo/vage/eval"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/setup"
)

// AgentRunFunc mirrors vageeval.AgentRunFunc to avoid leaking the agent
// package into handler code.
type AgentRunFunc = vageeval.AgentRunFunc

// RunCLI executes the offline evaluation flow: load dataset, build
// evaluator, run against the dispatcher, render report. Returns the
// process exit code (0 when every case passed, 1 otherwise).
func RunCLI(
	ctx context.Context,
	init *setup.InitResult,
	cfg *configs.Config,
	datasetPath, outPath string,
	stdout, stderr io.Writer,
) (int, error) {
	cases, loadErrs, err := LoadJSONL(datasetPath)
	if err != nil {
		return 1, err
	}

	for _, le := range loadErrs {
		_, _ = fmt.Fprintf(stderr, "vv: dataset %s: %s\n", datasetPath, le)
	}

	if len(cases) == 0 && len(loadErrs) == 0 {
		return 1, fmt.Errorf("dataset %s is empty", datasetPath)
	}

	evaluator, err := Build(cfg.Eval, init.LLMClient, cfg.LLM.Model)
	if err != nil {
		return 1, fmt.Errorf("build evaluator: %w", err)
	}

	report, err := RunBatch(ctx, init.SetupResult.Dispatcher.Run, evaluator, cases, cfg.Eval)
	if err != nil {
		return 1, fmt.Errorf("run batch: %w", err)
	}

	// Fold in dataset parse failures so the report reflects the full picture.
	// Record each as an EvalResult so -eval-out contains enough context for
	// post-mortem; this matches the HTTP handler's behavior for malformed cases.
	for _, le := range loadErrs {
		report.Results = append(report.Results, &vageeval.EvalResult{
			CaseID: fmt.Sprintf("line-%d", le.Line),
			Error:  le.Err.Error(),
		})
	}

	report.TotalCases += len(loadErrs)
	report.ErrorCases += len(loadErrs)

	PrintSummary(stdout, report)

	if outPath != "" {
		if err := WriteReportJSON(outPath, report); err != nil {
			return 1, fmt.Errorf("write report: %w", err)
		}

		_, _ = fmt.Fprintf(stdout, "\nFull report written to %s\n", outPath)
	}

	if report.FailedCases > 0 || report.ErrorCases > 0 {
		return 1, nil
	}

	return 0, nil
}

// RunBatch evaluates each case with a bounded worker pool and a per-case
// timeout sourced from cfg.TimeoutMs. This intentionally does not use
// vageeval.BatchEval's WithConcurrency because we need a case-scoped
// context deadline that BatchEval does not expose.
func RunBatch(
	ctx context.Context,
	run AgentRunFunc,
	evaluator vageeval.Evaluator,
	cases []*vageeval.EvalCase,
	cfg configs.EvalConfig,
) (*vageeval.EvalReport, error) {
	if run == nil {
		return nil, errors.New("runner: agent run func is nil")
	}

	if evaluator == nil {
		return nil, errors.New("runner: evaluator is nil")
	}

	concurrency := cfg.Concurrency
	if concurrency <= 0 {
		concurrency = 1
	}

	timeout := time.Duration(cfg.TimeoutMs) * time.Millisecond

	start := time.Now()
	results := make([]*vageeval.EvalResult, len(cases))
	sem := make(chan struct{}, concurrency)

	var wg sync.WaitGroup

	for i, c := range cases {
		wg.Add(1)

		go func(idx int, ec *vageeval.EvalCase) {
			defer wg.Done()

			// Honor parent context cancellation before taking a slot so a
			// cancelled batch drains quickly instead of fighting for the
			// semaphore. Still record a result so the aggregation below
			// keeps passed+failed+error == total.
			if err := ctx.Err(); err != nil {
				results[idx] = &vageeval.EvalResult{CaseID: caseID(ec), Error: err.Error()}

				return
			}

			select {
			case sem <- struct{}{}:
			case <-ctx.Done():
				results[idx] = &vageeval.EvalResult{CaseID: caseID(ec), Error: ctx.Err().Error()}

				return
			}
			defer func() { <-sem }()

			results[idx] = evaluateCase(ctx, run, evaluator, ec, timeout)
		}(i, c)
	}

	wg.Wait()

	report := &vageeval.EvalReport{TotalCases: len(cases)}

	var scoreSum float64

	var nonErrorCount int

	for _, r := range results {
		if r == nil {
			// A nil slot means the goroutine never produced a result — treat
			// as a missing-result error so counts stay consistent.
			report.ErrorCases++

			continue
		}

		report.Results = append(report.Results, r)

		switch {
		case r.Error != "":
			report.ErrorCases++
		case r.Passed:
			report.PassedCases++
			scoreSum += r.Score
			nonErrorCount++
		default:
			report.FailedCases++
			scoreSum += r.Score
			nonErrorCount++
		}
	}

	if nonErrorCount > 0 {
		report.AvgScore = scoreSum / float64(nonErrorCount)
	}

	report.TotalDuration = time.Since(start).Milliseconds()

	return report, nil
}

// evaluateCase runs a single case with its own timeout and captures errors
// as EvalResult.Error so the batch can continue.
func evaluateCase(
	parent context.Context,
	run AgentRunFunc,
	evaluator vageeval.Evaluator,
	c *vageeval.EvalCase,
	timeout time.Duration,
) *vageeval.EvalResult {
	caseCtx := parent
	if timeout > 0 {
		var cancel context.CancelFunc

		caseCtx, cancel = context.WithTimeout(parent, timeout)
		defer cancel()
	}

	if c.Actual == nil && c.Input != nil {
		resp, err := run(caseCtx, c.Input)
		if err != nil {
			return &vageeval.EvalResult{CaseID: c.ID, Error: runError(caseCtx, err)}
		}

		c.Actual = resp
	}

	if c.Input == nil && c.Actual == nil {
		return &vageeval.EvalResult{CaseID: c.ID, Error: "case has no input and no actual"}
	}

	result, err := evaluator.Evaluate(caseCtx, c)
	if err != nil {
		return &vageeval.EvalResult{CaseID: c.ID, Error: err.Error()}
	}

	return result
}

// runError annotates DeadlineExceeded as "timeout" so operators can spot
// per-case timeouts without comparing error strings themselves.
func runError(ctx context.Context, err error) string {
	if errors.Is(err, context.DeadlineExceeded) || errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return "timeout"
	}

	return err.Error()
}

// caseID returns the case's ID or a placeholder when the case is nil. Used
// by the bookkeeping paths where we must still record a result even if the
// input case is malformed.
func caseID(c *vageeval.EvalCase) string {
	if c == nil {
		return ""
	}

	return c.ID
}

// Ensure we honor the vage/eval AgentRunFunc contract.
var _ AgentRunFunc = func(context.Context, *schema.RunRequest) (*schema.RunResponse, error) {
	return nil, nil
}
