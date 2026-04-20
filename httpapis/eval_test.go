package httpapis

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	vageeval "github.com/vogo/vage/eval"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/configs"
)

// stubAgent is a minimal agent.Agent used for handler tests. It echoes the
// last user message with a fixed usage profile so evaluators have inputs.
type stubAgent struct{}

func (stubAgent) ID() string          { return "stub" }
func (stubAgent) Name() string        { return "stub" }
func (stubAgent) Description() string { return "test stub" }

func (stubAgent) Run(_ context.Context, req *schema.RunRequest) (*schema.RunResponse, error) {
	text := ""
	if len(req.Messages) > 0 {
		text = req.Messages[len(req.Messages)-1].Content.Text()
	}

	return &schema.RunResponse{
		Messages: []schema.Message{{
			Message: aimodel.Message{Role: aimodel.RoleAssistant, Content: aimodel.NewTextContent(text)},
		}},
		Usage:    &aimodel.Usage{PromptTokens: 5, CompletionTokens: 5, TotalTokens: 10},
		Duration: 5,
	}, nil
}

var _ agent.Agent = (*stubAgent)(nil)

func newEvalCfg() *configs.Config {
	return &configs.Config{
		LLM: configs.LLMConfig{Model: "test-model"},
		Eval: configs.EvalConfig{
			Enabled:            true,
			Concurrency:        1,
			TimeoutMs:          1000,
			Evaluators:         []string{"latency", "cost"},
			LatencyThresholdMs: 60000,
			CostBudgetTokens:   1000,
		},
	}
}

func TestHandleEvalRun_OK(t *testing.T) {
	cfg := newEvalCfg()
	handler := handleEvalRun(cfg, stubAgent{}, nil)

	body := `{"cases":[{"id":"c1","input":"hi"},{"id":"c2","input":"bye"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/eval/run", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var report vageeval.EvalReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if report.TotalCases != 2 || report.PassedCases != 2 {
		t.Errorf("report = %+v", report)
	}
}

func TestHandleEvalRun_InvalidBody(t *testing.T) {
	cfg := newEvalCfg()
	handler := handleEvalRun(cfg, stubAgent{}, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/eval/run", bytes.NewBufferString("not-json"))
	rec := httptest.NewRecorder()

	handler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleEvalRun_EmptyCases(t *testing.T) {
	cfg := newEvalCfg()
	handler := handleEvalRun(cfg, stubAgent{}, nil)

	req := httptest.NewRequest(http.MethodPost, "/v1/eval/run", bytes.NewBufferString(`{"cases":[]}`))
	rec := httptest.NewRecorder()

	handler(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rec.Code)
	}
}

func TestHandleEvalRun_MalformedCase(t *testing.T) {
	cfg := newEvalCfg()
	handler := handleEvalRun(cfg, stubAgent{}, nil)

	body := `{"cases":[{"id":"c1","input":"hi"},{"id":"bad"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/eval/run", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var report vageeval.EvalReport
	if err := json.Unmarshal(rec.Body.Bytes(), &report); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if report.TotalCases != 2 {
		t.Errorf("total = %d, want 2", report.TotalCases)
	}

	if report.ErrorCases != 1 {
		t.Errorf("error = %d, want 1 (malformed case)", report.ErrorCases)
	}

	if report.PassedCases != 1 {
		t.Errorf("passed = %d, want 1 (the good case)", report.PassedCases)
	}
}

func TestHandleEvalRun_EvaluatorBuildError(t *testing.T) {
	cfg := newEvalCfg()
	cfg.Eval.Evaluators = []string{"contains"} // missing keywords
	handler := handleEvalRun(cfg, stubAgent{}, nil)

	body := `{"cases":[{"id":"c1","input":"hi"}]}`
	req := httptest.NewRequest(http.MethodPost, "/v1/eval/run", bytes.NewBufferString(body))
	rec := httptest.NewRecorder()

	handler(rec, req)

	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500", rec.Code)
	}
}
