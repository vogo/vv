/*
 * Licensed to the Apache Software Foundation (ASF) under one or more
 * contributor license agreements.  See the NOTICE file distributed with
 * this work for additional information regarding copyright ownership.
 * The ASF licenses this file to You under the Apache License, Version 2.0
 * (the "License"); you may not use this file except in compliance with
 * the License.  You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package httpapis

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/checkpoint"
	"github.com/vogo/vage/session"
	"github.com/vogo/vv/agents"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/setup"
)

// resumeStubChat is a no-op ChatCompleter that returns a single canned
// terminal response. Used by the happy-path resume test which must drive
// Resume() through the LLM at least once.
type resumeStubChat struct {
	text string
}

func (s *resumeStubChat) ChatCompletion(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.ChatResponse, error) {
	return &aimodel.ChatResponse{
		Choices: []aimodel.Choice{{
			Message:      aimodel.Message{Role: aimodel.RoleAssistant, Content: aimodel.NewTextContent(s.text)},
			FinishReason: aimodel.FinishReasonStop,
		}},
		Usage: aimodel.Usage{PromptTokens: 11, CompletionTokens: 4, TotalTokens: 15},
	}, nil
}

func (s *resumeStubChat) ChatCompletionStream(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.Stream, error) {
	return nil, nil
}

// newResumeRequest builds a httptest request that exercises the path
// pattern Go's mux registered for the route, so r.PathValue("id") works
// in the handler the same way it would behind the real mux.
func newResumeRequest(sid, query string) *http.Request {
	target := "/v1/sessions/" + sid + "/resume"
	if query != "" {
		target += "?" + query
	}
	req := httptest.NewRequest(http.MethodPost, target, nil)
	req.SetPathValue("id", sid)
	return req
}

func TestHandleResumeSession_BadRequest_EmptyID(t *testing.T) {
	h := handleResumeSession(&setup.InitResult{
		SessionStore:   session.NewMapSessionStore(),
		IterationStore: checkpoint.NewMapIterationStore(),
	})

	rec := httptest.NewRecorder()
	req := newResumeRequest("", "") // path value will be ""
	h(rec, req)

	if rec.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
}

func TestHandleResumeSession_StreamingNotImplemented(t *testing.T) {
	h := handleResumeSession(&setup.InitResult{
		SessionStore:   session.NewMapSessionStore(),
		IterationStore: checkpoint.NewMapIterationStore(),
	})

	rec := httptest.NewRecorder()
	h(rec, newResumeRequest("sid", "stream=1"))

	if rec.Code != http.StatusNotImplemented {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["code"] != "not_implemented" {
		t.Errorf("code = %q, want not_implemented", body["code"])
	}
}

func TestHandleResumeSession_SessionDisabled(t *testing.T) {
	// nil InitResult AND non-nil-but-empty both map to 503; cover both.
	cases := []struct {
		name string
		ir   *setup.InitResult
	}{
		{"nil InitResult", nil},
		{"empty InitResult", &setup.InitResult{}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := handleResumeSession(c.ir)
			rec := httptest.NewRecorder()
			h(rec, newResumeRequest("sid", ""))
			if rec.Code != http.StatusServiceUnavailable {
				t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
			}
			var body map[string]string
			_ = json.Unmarshal(rec.Body.Bytes(), &body)
			if body["code"] != "session_disabled" {
				t.Errorf("code = %q, want session_disabled", body["code"])
			}
		})
	}
}

func TestHandleResumeSession_NoIterationStore(t *testing.T) {
	h := handleResumeSession(&setup.InitResult{
		SessionStore: session.NewMapSessionStore(),
		// IterationStore intentionally nil — setup wiring regression
	})

	rec := httptest.NewRecorder()
	h(rec, newResumeRequest("sid", ""))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["code"] != "iteration_store_missing" {
		t.Errorf("code = %q, want iteration_store_missing", body["code"])
	}
}

func TestHandleResumeSession_CheckpointNotFound(t *testing.T) {
	h := handleResumeSession(&setup.InitResult{
		SessionStore:   session.NewMapSessionStore(),
		IterationStore: checkpoint.NewMapIterationStore(),
	})

	rec := httptest.NewRecorder()
	h(rec, newResumeRequest("ghost-session", ""))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["code"] != "checkpoint_not_found" {
		t.Errorf("code = %q, want checkpoint_not_found", body["code"])
	}
}

func TestHandleResumeSession_AlreadyFinal(t *testing.T) {
	store := checkpoint.NewMapIterationStore()
	if err := store.Save(context.Background(), &checkpoint.Checkpoint{
		SessionID: "sid-final",
		AgentID:   agents.PrimaryAgentID,
		Final:     true,
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	h := handleResumeSession(&setup.InitResult{
		SessionStore:   session.NewMapSessionStore(),
		IterationStore: store,
	})

	rec := httptest.NewRecorder()
	h(rec, newResumeRequest("sid-final", ""))

	if rec.Code != http.StatusConflict {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["code"] != "already_final" {
		t.Errorf("code = %q, want already_final", body["code"])
	}
}

func TestHandleResumeSession_AgentNotFound(t *testing.T) {
	// Need a real Result.subAgents map so the lookup returns nil for an
	// unknown id; build via setup.New with a minimal cfg.
	stub := &resumeStubChat{text: "ok"}
	result, err := setup.New(minimalResumeCfg(), stub, nil, nil, nil)
	if err != nil {
		t.Fatalf("setup.New: %v", err)
	}

	store := checkpoint.NewMapIterationStore()
	if err := store.Save(context.Background(), &checkpoint.Checkpoint{
		SessionID: "sid-ghost-agent",
		AgentID:   "ghost-agent-id",
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	h := handleResumeSession(&setup.InitResult{
		SessionStore:   session.NewMapSessionStore(),
		IterationStore: store,
		SetupResult:    result,
	})

	rec := httptest.NewRecorder()
	h(rec, newResumeRequest("sid-ghost-agent", ""))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)
	if body["code"] != "agent_not_found" {
		t.Errorf("code = %q, want agent_not_found", body["code"])
	}
}

func TestHandleResumeSession_HappyPath_Primary(t *testing.T) {
	store := checkpoint.NewMapIterationStore()
	if err := store.Save(context.Background(), &checkpoint.Checkpoint{
		SessionID: "sid-resume-ok",
		AgentID:   agents.PrimaryAgentID,
		Iteration: 0,
		Messages: []aimodel.Message{
			{Role: aimodel.RoleSystem, Content: aimodel.NewTextContent("system")},
			{Role: aimodel.RoleUser, Content: aimodel.NewTextContent("continue")},
		},
	}); err != nil {
		t.Fatalf("Save: %v", err)
	}

	stub := &resumeStubChat{text: "resume http ok"}
	primary := taskagent.New(
		agent.Config{ID: agents.PrimaryAgentID, Name: "Primary"},
		taskagent.WithChatCompleter(stub),
		taskagent.WithModel("test-model"),
		taskagent.WithMaxIterations(2),
		taskagent.WithIterationStore(store),
	)

	result, err := setup.New(minimalResumeCfg(), stub, nil, nil, nil)
	if err != nil {
		t.Fatalf("setup.New: %v", err)
	}
	result.Dispatcher.SetPrimaryAssistant(primary)

	h := handleResumeSession(&setup.InitResult{
		SessionStore:   session.NewMapSessionStore(),
		IterationStore: store,
		SetupResult:    result,
	})

	rec := httptest.NewRecorder()
	h(rec, newResumeRequest("sid-resume-ok", ""))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}

	var body resumeResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.SessionID != "sid-resume-ok" {
		t.Errorf("session_id = %q", body.SessionID)
	}
	if body.AgentID != agents.PrimaryAgentID {
		t.Errorf("agent_id = %q", body.AgentID)
	}
	if body.StopReason != "complete" {
		t.Errorf("stop_reason = %q, want complete", body.StopReason)
	}
	if body.PromptTokens == 0 || body.CompletionTokens == 0 {
		t.Errorf("usage zero: prompt=%d completion=%d", body.PromptTokens, body.CompletionTokens)
	}

	// The final assistant message must be in the wire payload — clients
	// rely on it for rendering and audit.
	foundFinal := false
	for _, m := range body.Messages {
		if m.Role == "assistant" && m.Content == "resume http ok" {
			foundFinal = true
			break
		}
	}
	if !foundFinal {
		t.Errorf("final assistant text missing from response: %+v", body.Messages)
	}
}

// minimalResumeCfg returns the smallest config setup.New accepts —
// mirrors TestNew_AllAgentsCreated in vv/setup/setup_test.go so the
// resume tests stay aligned with the supported construction surface.
func minimalResumeCfg() *configs.Config {
	return &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 4},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}
}
