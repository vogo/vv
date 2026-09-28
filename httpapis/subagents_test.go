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
	"strconv"
	"testing"

	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/checkpoint"
	"github.com/vogo/vv/sessionlogs"
	"github.com/vogo/vv/setup"
)

const subAgentSession = "sess-sub"

// newSubAgentInitResult seeds a transcript store with one primary
// checkpoint and two coder dispatches.
func newSubAgentInitResult(t *testing.T) *setup.InitResult {
	t.Helper()

	store, err := sessionlogs.New(t.TempDir())
	if err != nil {
		t.Fatalf("sessionlogs.New: %v", err)
	}

	ctx := context.Background()

	if err := store.Save(ctx, &checkpoint.Checkpoint{
		SessionID: subAgentSession,
		AgentID:   sessionlogs.DefaultPrimaryAgentID,
		Messages:  []schema.Message{schema.NewUserMessage(schema.ProtocolOpenAIChat, "top level")},
	}); err != nil {
		t.Fatalf("seed primary: %v", err)
	}

	for i, task := range []string{"rename files", "run tests"} {
		runCtx := sessionlogs.WithRun(ctx, sessionlogs.Run{Key: "d" + strconv.Itoa(i), Task: task})

		if err := store.Save(runCtx, &checkpoint.Checkpoint{
			SessionID:  subAgentSession,
			AgentID:    "coder",
			Final:      true,
			StopReason: schema.StopReasonComplete,
			Usage:      schema.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
			Messages: []schema.Message{
				schema.NewUserMessage(schema.ProtocolOpenAIChat, task),
				schema.NewTextMessage(schema.ProtocolOpenAIChat, schema.RoleAssistant, "done: "+task),
			},
		}); err != nil {
			t.Fatalf("seed dispatch %d: %v", i, err)
		}
	}

	return &setup.InitResult{IterationStore: store}
}

func newSubAgentsRequest(sid string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sid+"/subagents", nil)
	req.SetPathValue("id", sid)

	return req
}

func newSubAgentRunRequest(sid, agent, run string) *http.Request {
	req := httptest.NewRequest(http.MethodGet, "/v1/sessions/"+sid+"/subagents/"+agent+"/"+run, nil)
	req.SetPathValue("id", sid)
	req.SetPathValue("agent", agent)
	req.SetPathValue("run", run)

	return req
}

func TestHandleListSubAgents_ListsEveryDispatch(t *testing.T) {
	rec := httptest.NewRecorder()
	handleListSubAgents(newSubAgentInitResult(t))(rec, newSubAgentsRequest(subAgentSession))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got subAgentsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.SessionID != subAgentSession {
		t.Errorf("session_id = %q", got.SessionID)
	}

	if len(got.Runs) != 2 {
		t.Fatalf("runs = %d, want 2", len(got.Runs))
	}

	for i, want := range []string{"rename files", "run tests"} {
		if got.Runs[i].AgentID != "coder" {
			t.Errorf("runs[%d].agent_id = %q, want coder", i, got.Runs[i].AgentID)
		}

		if got.Runs[i].Run != i+1 {
			t.Errorf("runs[%d].run = %d, want %d", i, got.Runs[i].Run, i+1)
		}

		if got.Runs[i].Task != want {
			t.Errorf("runs[%d].task = %q, want %q", i, got.Runs[i].Task, want)
		}

		if !got.Runs[i].Final {
			t.Errorf("runs[%d] should be final", i)
		}
	}
}

func TestHandleListSubAgents_UnknownSessionIsEmptyNotError(t *testing.T) {
	rec := httptest.NewRecorder()
	handleListSubAgents(newSubAgentInitResult(t))(rec, newSubAgentsRequest("no-such-session"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got subAgentsResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(got.Runs) != 0 {
		t.Errorf("runs = %d, want 0", len(got.Runs))
	}

	if !json.Valid(rec.Body.Bytes()) || got.Runs == nil {
		t.Error("empty listing must serialise as [] rather than null")
	}
}

func TestHandleListSubAgents_NoTranscriptStore_503(t *testing.T) {
	rec := httptest.NewRecorder()
	handleListSubAgents(&setup.InitResult{})(rec, newSubAgentsRequest(subAgentSession))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("status = %d, want 503: %s", rec.Code, rec.Body.String())
	}

	var body map[string]string
	_ = json.Unmarshal(rec.Body.Bytes(), &body)

	if body["code"] != "subagent_transcripts_disabled" {
		t.Errorf("code = %q", body["code"])
	}
}

func TestHandleGetSubAgentRun_ReturnsTranscript(t *testing.T) {
	rec := httptest.NewRecorder()
	handleGetSubAgentRun(newSubAgentInitResult(t))(rec, newSubAgentRunRequest(subAgentSession, "coder", "2"))

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
	}

	var got subAgentRunResponse
	if err := json.Unmarshal(rec.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if got.AgentID != "coder" || got.Run != 2 {
		t.Errorf("identity = %q/%d, want coder/2", got.AgentID, got.Run)
	}

	if len(got.Messages) != 2 || got.Messages[1].Content != "done: run tests" {
		t.Fatalf("messages = %+v", got.Messages)
	}

	if got.Usage.TotalTokens != 15 {
		t.Errorf("usage.total_tokens = %d, want 15", got.Usage.TotalTokens)
	}

	if !got.Final || got.StopReason != string(schema.StopReasonComplete) {
		t.Errorf("terminator lost: final=%v stop=%q", got.Final, got.StopReason)
	}
}

func TestHandleGetSubAgentRun_MissingRun_404(t *testing.T) {
	rec := httptest.NewRecorder()
	handleGetSubAgentRun(newSubAgentInitResult(t))(rec, newSubAgentRunRequest(subAgentSession, "coder", "9"))

	if rec.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404: %s", rec.Code, rec.Body.String())
	}
}

func TestHandleGetSubAgentRun_BadRunNumber_400(t *testing.T) {
	for _, run := range []string{"abc", "0", "-1"} {
		rec := httptest.NewRecorder()
		handleGetSubAgentRun(newSubAgentInitResult(t))(rec, newSubAgentRunRequest(subAgentSession, "coder", run))

		if rec.Code != http.StatusBadRequest {
			t.Errorf("run=%q status = %d, want 400", run, rec.Code)
		}
	}
}
