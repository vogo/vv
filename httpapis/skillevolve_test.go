package httpapis

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/checkpoint"
	"github.com/vogo/vv/registries"
	"github.com/vogo/vv/sessionlogs"
	"github.com/vogo/vv/setup"
	"github.com/vogo/vv/skillevolve"
)

func TestMountSkillEvolveRoutes_NilEngine404(t *testing.T) {
	mux := http.NewServeMux()
	mountSkillEvolveRoutes(mux, &setup.InitResult{})

	reqs := []struct {
		method, path string
	}{
		{http.MethodPost, "/v1/sessions/sess-ok/skill-extract"},
		{http.MethodGet, "/v1/skill-proposals"},
		{http.MethodPost, "/v1/skill-proposals/p1/approve"},
		{http.MethodPost, "/v1/skill-proposals/p1/reject"},
	}
	for _, r := range reqs {
		rr := httptest.NewRecorder()
		req := httptest.NewRequest(r.method, r.path, nil)
		mux.ServeHTTP(rr, req)
		if rr.Code != http.StatusNotFound {
			t.Errorf("%s %s status = %d, want 404", r.method, r.path, rr.Code)
		}
	}
}

func TestMountSkillEvolveRoutes_ExtractApproveList(t *testing.T) {
	eng := newHTTPSkillEngine(t)
	seedEligibleSession(t, eng.Logs, "sess-ok")

	mux := http.NewServeMux()
	mountSkillEvolveRoutes(mux, &setup.InitResult{SkillEvolve: eng})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/sess-ok/skill-extract", nil)
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("extract status = %d body=%s", rr.Code, rr.Body.String())
	}
	var extracted skillevolve.Proposal
	if err := json.Unmarshal(rr.Body.Bytes(), &extracted); err != nil {
		t.Fatal(err)
	}
	if extracted.Status != skillevolve.StatusPending {
		t.Fatalf("status = %s", extracted.Status)
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/skill-proposals/"+extracted.ID+"/approve", nil)
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("approve status = %d body=%s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodGet, "/v1/skill-proposals", nil)
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("list status = %d body=%s", rr.Code, rr.Body.String())
	}
	var listed struct {
		Proposals []skillevolve.Proposal `json:"proposals"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &listed); err != nil {
		t.Fatal(err)
	}
	if len(listed.Proposals) != 1 || listed.Proposals[0].Status != skillevolve.StatusApproved {
		t.Fatalf("list = %+v", listed.Proposals)
	}
}

func TestMountSkillEvolveRoutes_InvalidIDsAndNotEligible(t *testing.T) {
	eng := newHTTPSkillEngine(t)
	seedInterruptedSession(t, eng.Logs, "sess-int")

	mux := http.NewServeMux()
	mountSkillEvolveRoutes(mux, &setup.InitResult{SkillEvolve: eng})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/sessions/@@@/skill-extract", nil)
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid session status = %d, want 400", rr.Code)
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/skill-proposals/foo..bar/approve", nil)
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("invalid proposal status = %d body=%s", rr.Code, rr.Body.String())
	}

	rr = httptest.NewRecorder()
	req = httptest.NewRequest(http.MethodPost, "/v1/sessions/sess-int/skill-extract", nil)
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusConflict {
		t.Fatalf("not eligible status = %d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "not_eligible") {
		t.Fatalf("body = %s", rr.Body.String())
	}
}

func newHTTPSkillEngine(t *testing.T) *skillevolve.Engine {
	t.Helper()
	logs, err := sessionlogs.New(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	skillDir := t.TempDir()
	stack := registries.LoadSkillStack(context.Background(), "", nil)
	return &skillevolve.Engine{
		Logs: logs,
		Extractor: skillevolve.ExtractorFunc(func(context.Context, skillevolve.SessionFeatures) (skillevolve.ExtractedSkill, error) {
			return skillevolve.ExtractedSkill{
				Name: "http-flow", Description: "d", Instructions: "do the flow",
			}, nil
		}),
		Deduper: &skillevolve.LexicalDeduper{Threshold: 0.85},
		Queue:   skillevolve.NewFileQueue(skillDir),
		Registrar: &skillevolve.Registrar{
			SkillDir: skillDir,
			VV:       stack.Registry,
			Vage:     stack.VageRegistry,
		},
		MinTurns:   1,
		MinSuccess: 0.9,
		Vage:       stack.VageRegistry,
	}
}

func seedEligibleSession(t *testing.T, logs *sessionlogs.Store, sid string) {
	t.Helper()
	proto := schema.ProtocolOpenAIChat
	msgs := []schema.Message{schema.NewUserMessage(proto, "please do it")}
	msgs = append(msgs, schema.NewAssistantTurn(proto, "", "", []schema.ToolCall{
		{ID: "c1", Name: "grep", Arguments: `{"q":"x"}`},
	}))
	msgs = append(msgs, schema.NewToolResultMessage(proto, "c1", "ok", false))
	msgs = append(msgs, schema.NewTextMessage(proto, schema.RoleAssistant, "done"))
	if err := logs.Save(context.Background(), &checkpoint.Checkpoint{
		SessionID: sid, AgentID: sessionlogs.DefaultPrimaryAgentID,
		Final: true, StopReason: schema.StopReasonComplete, Messages: msgs,
	}); err != nil {
		t.Fatal(err)
	}
}

func seedInterruptedSession(t *testing.T, logs *sessionlogs.Store, sid string) {
	t.Helper()
	if err := logs.Save(context.Background(), &checkpoint.Checkpoint{
		SessionID: sid, AgentID: sessionlogs.DefaultPrimaryAgentID,
		Final: true, StopReason: schema.StopReasonInterrupted,
		Messages: []schema.Message{schema.NewUserMessage(schema.ProtocolOpenAIChat, "x")},
	}); err != nil {
		t.Fatal(err)
	}
}
