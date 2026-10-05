package httpapis

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/interrupt"
	"github.com/vogo/vv/setup"
)

func seedHTTPInterrupt(t *testing.T, store interrupt.Store, sessionID, agentID string) *interrupt.Record {
	t.Helper()
	rec := &interrupt.Record{
		SessionID: sessionID,
		AgentID:   agentID,
		Protocol:  schema.ProtocolOpenAIChat,
		ToolCalls: []schema.ToolCall{
			{ID: "call-1", Name: "bash", Arguments: `{"command":"rm -rf ./dist"}`},
		},
		Pending: []string{"call-1"},
		Messages: []schema.Message{
			schema.NewTextMessage(schema.ProtocolOpenAIChat, schema.RoleUser, "clean dist"),
		},
		Params: interrupt.EffectiveParams{Model: "test", MaxIterations: 4},
	}
	if err := store.Create(context.Background(), rec); err != nil {
		t.Fatalf("Create: %v", err)
	}
	return rec
}

func TestHandleListInterrupts(t *testing.T) {
	store := interrupt.NewMapStore()
	rec := seedHTTPInterrupt(t, store, "sess-a", "primary")

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess-a/interrupts", nil)
	req.SetPathValue("id", "sess-a")
	handleListInterrupts(store)(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var got interruptListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Interrupts) != 1 || got.Interrupts[0].ID != rec.ID {
		t.Fatalf("list = %+v, want id %s", got.Interrupts, rec.ID)
	}
}

func TestHandleSubmitInterruptDecisions_ApproveExecute(t *testing.T) {
	store := interrupt.NewMapStore()
	rec := seedHTTPInterrupt(t, store, "sess-a", "primary")

	body := `{"decisions":[{"tool_call_id":"call-1","execute":true}]}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/interrupts/"+rec.ID+"/decisions", strings.NewReader(body))
	req.SetPathValue("id", rec.ID)
	handleSubmitInterruptDecisions(store)(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}

	var got interruptDecisionsResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !got.Ready || got.Status != string(interrupt.StatusReady) {
		t.Fatalf("got %+v, want ready", got)
	}
}

func TestHandleSubmitInterruptDecisions_Conflict(t *testing.T) {
	store := interrupt.NewMapStore()
	rec := seedHTTPInterrupt(t, store, "sess-a", "primary")

	first := `{"decisions":[{"tool_call_id":"call-1","execute":true}]}`
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/interrupts/"+rec.ID+"/decisions", strings.NewReader(first))
	req.SetPathValue("id", rec.ID)
	handleSubmitInterruptDecisions(store)(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("first submit status = %d body=%s", rr.Code, rr.Body.String())
	}

	conflict := `{"decisions":[{"tool_call_id":"call-1","is_error":true,"content":"no"}]}`
	rr2 := httptest.NewRecorder()
	req2 := httptest.NewRequest(http.MethodPost, "/v1/interrupts/"+rec.ID+"/decisions", strings.NewReader(conflict))
	req2.SetPathValue("id", rec.ID)
	handleSubmitInterruptDecisions(store)(rr2, req2)
	if rr2.Code != http.StatusConflict {
		t.Fatalf("conflict status = %d, want 409, body=%s", rr2.Code, rr2.Body.String())
	}
}

func TestHandleSubmitInterruptDecisions_NotFound(t *testing.T) {
	store := interrupt.NewMapStore()
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/interrupts/missing/decisions", strings.NewReader(`{"decisions":[]}`))
	req.SetPathValue("id", "missing")
	handleSubmitInterruptDecisions(store)(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404", rr.Code)
	}
}

func TestHandleResumeInterrupt_EmptyID(t *testing.T) {
	h := handleResumeInterrupt(interrupt.NewMapStore(), &setup.InitResult{})
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/interrupts//resume", nil)
	req.SetPathValue("id", "")
	h(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestHandleResumeInterrupt_AgentNotFound(t *testing.T) {
	store := interrupt.NewMapStore()
	rec := seedHTTPInterrupt(t, store, "sess-a", "ghost")
	h := handleResumeInterrupt(store, &setup.InitResult{SetupResult: &setup.Result{}})

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/v1/interrupts/"+rec.ID+"/resume", nil)
	req.SetPathValue("id", rec.ID)
	h(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404, body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleDeleteSession_AlsoDeletesInterrupts(t *testing.T) {
	sessions := newSeededStore(t)
	istore := interrupt.NewMapStore()
	rec := seedHTTPInterrupt(t, istore, "alpha", "primary")

	rr := httptest.NewRecorder()
	req := withPathValue(httptest.NewRequest(http.MethodDelete, "/v1/sessions/alpha", nil), "id", "alpha")
	handleDeleteSession(sessions, nil, istore)(rr, req)
	if rr.Code != http.StatusOK {
		t.Fatalf("delete status = %d", rr.Code)
	}

	if _, err := istore.Get(context.Background(), rec.ID); err == nil {
		t.Fatal("interrupt record should be deleted with the session")
	}
}
