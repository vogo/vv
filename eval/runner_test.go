package eval

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/vogo/aimodel"
	vageeval "github.com/vogo/vage/eval"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/configs"
)

// echoRun returns the incoming user text as an assistant message with a
// fixed usage profile so the latency and cost evaluators have something
// concrete to score.
func echoRun(_ context.Context, req *schema.RunRequest) (*schema.RunResponse, error) {
	text := ""
	if len(req.Messages) > 0 {
		text = req.Messages[len(req.Messages)-1].Content.Text()
	}

	return &schema.RunResponse{
		Messages: []schema.Message{{
			Message: aimodel.Message{Role: aimodel.RoleAssistant, Content: aimodel.NewTextContent(text)},
		}},
		Usage:    &aimodel.Usage{PromptTokens: 5, CompletionTokens: 5, TotalTokens: 10},
		Duration: 10,
	}, nil
}

func TestRunBatch_HappyPath(t *testing.T) {
	cfg := configs.EvalConfig{
		Concurrency:        2,
		TimeoutMs:          1000,
		Evaluators:         []string{"latency", "cost"},
		LatencyThresholdMs: 60000,
		CostBudgetTokens:   1000,
	}

	ev, err := Build(cfg, nil, "")
	if err != nil {
		t.Fatalf("build evaluator: %v", err)
	}

	cases := []*vageeval.EvalCase{
		{ID: "c1", Input: &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage("hi")}}},
		{ID: "c2", Input: &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage("there")}}},
	}

	report, err := RunBatch(context.Background(), echoRun, ev, cases, cfg)
	if err != nil {
		t.Fatalf("run batch: %v", err)
	}

	if report.TotalCases != 2 {
		t.Errorf("total = %d, want 2", report.TotalCases)
	}

	if report.PassedCases != 2 {
		t.Errorf("passed = %d, want 2 (report %+v)", report.PassedCases, report)
	}

	if report.FailedCases != 0 || report.ErrorCases != 0 {
		t.Errorf("unexpected failures: %+v", report)
	}
}

func TestRunBatch_Timeout(t *testing.T) {
	cfg := configs.EvalConfig{
		Concurrency:        1,
		TimeoutMs:          10,
		Evaluators:         []string{"latency"},
		LatencyThresholdMs: 60000,
	}

	ev, err := Build(cfg, nil, "")
	if err != nil {
		t.Fatalf("build evaluator: %v", err)
	}

	slow := func(ctx context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
		select {
		case <-time.After(500 * time.Millisecond):
			return &schema.RunResponse{}, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}

	cases := []*vageeval.EvalCase{
		{ID: "slow", Input: &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage("hi")}}},
	}

	report, err := RunBatch(context.Background(), slow, ev, cases, cfg)
	if err != nil {
		t.Fatalf("run batch: %v", err)
	}

	if report.ErrorCases != 1 {
		t.Fatalf("want 1 error case, got %+v", report)
	}

	if got := report.Results[0].Error; got != "timeout" {
		t.Errorf("error = %q, want %q", got, "timeout")
	}
}

func TestRunBatch_AgentError(t *testing.T) {
	cfg := configs.EvalConfig{
		Concurrency:        1,
		TimeoutMs:          1000,
		Evaluators:         []string{"latency"},
		LatencyThresholdMs: 60000,
	}

	ev, err := Build(cfg, nil, "")
	if err != nil {
		t.Fatalf("build evaluator: %v", err)
	}

	boom := func(context.Context, *schema.RunRequest) (*schema.RunResponse, error) {
		return nil, errors.New("boom")
	}

	cases := []*vageeval.EvalCase{
		{ID: "kaput", Input: &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage("hi")}}},
	}

	report, err := RunBatch(context.Background(), boom, ev, cases, cfg)
	if err != nil {
		t.Fatalf("run batch: %v", err)
	}

	if report.ErrorCases != 1 {
		t.Fatalf("want 1 error case, got %+v", report)
	}

	if got := report.Results[0].Error; got != "boom" {
		t.Errorf("error = %q, want boom", got)
	}
}

func TestRunBatch_NilRun(t *testing.T) {
	if _, err := RunBatch(context.Background(), nil, nil, nil, configs.EvalConfig{}); err == nil {
		t.Fatal("want error for nil run")
	}
}

// TestRunBatch_ParentCancelled verifies that when the parent context is
// already cancelled, every case produces an error result so the report's
// counts stay consistent (passed + failed + error == total).
func TestRunBatch_ParentCancelled(t *testing.T) {
	cfg := configs.EvalConfig{
		Concurrency:        1,
		TimeoutMs:          1000,
		Evaluators:         []string{"latency"},
		LatencyThresholdMs: 60000,
	}

	ev, err := Build(cfg, nil, "")
	if err != nil {
		t.Fatalf("build evaluator: %v", err)
	}

	cases := []*vageeval.EvalCase{
		{ID: "a", Input: &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage("hi")}}},
		{ID: "b", Input: &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage("bye")}}},
	}

	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	report, err := RunBatch(ctx, echoRun, ev, cases, cfg)
	if err != nil {
		t.Fatalf("run batch: %v", err)
	}

	if report.TotalCases != 2 {
		t.Errorf("total = %d, want 2", report.TotalCases)
	}

	if report.ErrorCases != 2 {
		t.Errorf("error = %d, want 2 (cancelled), got report %+v", report.ErrorCases, report)
	}

	if got := report.PassedCases + report.FailedCases + report.ErrorCases; got != report.TotalCases {
		t.Errorf("counts don't sum: passed=%d failed=%d error=%d total=%d",
			report.PassedCases, report.FailedCases, report.ErrorCases, report.TotalCases)
	}
}
