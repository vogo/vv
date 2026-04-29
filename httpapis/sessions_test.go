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
	"strings"
	"testing"
	"time"

	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/session"
)

func newSeededStore(t *testing.T) session.SessionStore {
	t.Helper()
	store := session.NewMapSessionStore()

	ctx := context.Background()
	now := time.Now()

	if err := store.Create(ctx, &session.Session{
		ID:        "alpha",
		AgentID:   "coder",
		UserID:    "alice",
		Title:     "fix login bug",
		State:     session.StateActive,
		Metadata:  map[string]any{"tags": []string{"urgent"}},
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed alpha: %v", err)
	}

	if err := store.Create(ctx, &session.Session{
		ID:        "beta",
		AgentID:   "researcher",
		UserID:    "bob",
		State:     session.StateCompleted,
		CreatedAt: now,
		UpdatedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("seed beta: %v", err)
	}

	if err := store.AppendEvent(ctx, "alpha", schema.Event{
		Type: schema.EventAgentStart, SessionID: "alpha", Timestamp: now,
	}); err != nil {
		t.Fatalf("append: %v", err)
	}

	if err := store.SetState(ctx, "alpha", "plan", "step 1"); err != nil {
		t.Fatalf("set state: %v", err)
	}

	return store
}

// withPathValue clones the request and sets the URL path value the handler
// will read via r.PathValue.
func withPathValue(r *http.Request, key, val string) *http.Request {
	r.SetPathValue(key, val)
	return r
}

func TestHandleListSessions_FiltersByUserID(t *testing.T) {
	store := newSeededStore(t)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/sessions?user_id=alice", nil)
	handleListSessions(store)(rr, r)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var resp sessionListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if len(resp.Sessions) != 1 || resp.Sessions[0].ID != "alpha" {
		t.Errorf("expected [alpha], got %+v", resp.Sessions)
	}
}

func TestHandleListSessions_BadLimit(t *testing.T) {
	store := newSeededStore(t)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/sessions?limit=abc", nil)
	handleListSessions(store)(rr, r)

	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleGetSession_ReturnsMetaAndState(t *testing.T) {
	store := newSeededStore(t)
	rr := httptest.NewRecorder()
	r := withPathValue(httptest.NewRequest(http.MethodGet, "/v1/sessions/alpha", nil), "id", "alpha")
	handleGetSession(store)(rr, r)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var resp sessionDetailResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.ID != "alpha" {
		t.Errorf("ID = %q, want alpha", resp.ID)
	}
	if got := resp.State_field("plan"); got != "step 1" {
		t.Errorf("State plan = %v, want %q", got, "step 1")
	}
}

// State_field is a small helper to read a key from the embedded state map.
func (r sessionDetailResponse) State_field(key string) any { return r.State[key] }

func TestHandleGetSession_NotFound(t *testing.T) {
	store := newSeededStore(t)
	rr := httptest.NewRecorder()
	r := withPathValue(httptest.NewRequest(http.MethodGet, "/v1/sessions/missing", nil), "id", "missing")
	handleGetSession(store)(rr, r)

	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleListEvents_Returns(t *testing.T) {
	store := newSeededStore(t)
	rr := httptest.NewRecorder()
	r := withPathValue(httptest.NewRequest(http.MethodGet, "/v1/sessions/alpha/events", nil), "id", "alpha")
	handleListEvents(store)(rr, r)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var resp eventListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Events) != 1 || resp.Events[0].Type != schema.EventAgentStart {
		t.Errorf("events = %+v, want [agent_start]", resp.Events)
	}
}

func TestHandleListEvents_TypeFilter(t *testing.T) {
	store := newSeededStore(t)
	rr := httptest.NewRecorder()
	r := withPathValue(
		httptest.NewRequest(http.MethodGet, "/v1/sessions/alpha/events?type=text_delta", nil),
		"id", "alpha",
	)
	handleListEvents(store)(rr, r)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var resp eventListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Events) != 0 {
		t.Errorf("expected 0 events under type=text_delta filter, got %d", len(resp.Events))
	}
}

func TestHandleDeleteSession_Idempotent(t *testing.T) {
	store := newSeededStore(t)

	// Delete existing.
	rr := httptest.NewRecorder()
	r := withPathValue(httptest.NewRequest(http.MethodDelete, "/v1/sessions/alpha", nil), "id", "alpha")
	handleDeleteSession(store, nil)(rr, r)
	if rr.Code != http.StatusOK {
		t.Fatalf("first delete status = %d, want 200", rr.Code)
	}

	// Delete again — should still be 200 by Delete contract.
	rr2 := httptest.NewRecorder()
	r2 := withPathValue(httptest.NewRequest(http.MethodDelete, "/v1/sessions/alpha", nil), "id", "alpha")
	handleDeleteSession(store, nil)(rr2, r2)
	if rr2.Code != http.StatusOK {
		t.Fatalf("second delete status = %d, want 200 (idempotent)", rr2.Code)
	}

	// Confirm GET now 404s.
	rr3 := httptest.NewRecorder()
	r3 := withPathValue(httptest.NewRequest(http.MethodGet, "/v1/sessions/alpha", nil), "id", "alpha")
	handleGetSession(store)(rr3, r3)
	if rr3.Code != http.StatusNotFound {
		t.Errorf("post-delete GET status = %d, want 404", rr3.Code)
	}
}

func TestHandlePatchSession_UpdatesTitleAndState(t *testing.T) {
	store := newSeededStore(t)

	body := `{"title":"renamed","state":"paused"}`
	rr := httptest.NewRecorder()
	r := withPathValue(
		httptest.NewRequest(http.MethodPatch, "/v1/sessions/alpha", strings.NewReader(body)),
		"id", "alpha",
	)
	handlePatchSession(store)(rr, r)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200; body=%s", rr.Code, rr.Body.String())
	}

	var resp sessionMetaResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Title != "renamed" {
		t.Errorf("Title = %q, want renamed", resp.Title)
	}
	if resp.State != "paused" {
		t.Errorf("State = %q, want paused", resp.State)
	}
}

func TestHandlePatchSession_RejectsInvalidState(t *testing.T) {
	store := newSeededStore(t)

	rr := httptest.NewRecorder()
	r := withPathValue(
		httptest.NewRequest(http.MethodPatch, "/v1/sessions/alpha", strings.NewReader(`{"state":"bogus"}`)),
		"id", "alpha",
	)
	handlePatchSession(store)(rr, r)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandlePatchSession_NotFound(t *testing.T) {
	store := newSeededStore(t)
	rr := httptest.NewRecorder()
	r := withPathValue(
		httptest.NewRequest(http.MethodPatch, "/v1/sessions/missing", strings.NewReader(`{"title":"x"}`)),
		"id", "missing",
	)
	handlePatchSession(store)(rr, r)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestParsePositiveInt(t *testing.T) {
	cases := []struct {
		raw      string
		def, max int
		wantN    int
		wantOK   bool
	}{
		{"", 50, 200, 50, true},
		{"100", 50, 200, 100, true},
		{"500", 50, 200, 200, true}, // capped
		{"abc", 50, 200, 0, false},
		{"-5", 50, 200, 0, false},
		{"0", 50, 200, 0, false},
	}
	for _, c := range cases {
		n, ok := parsePositiveInt(c.raw, c.def, c.max)
		if n != c.wantN || ok != c.wantOK {
			t.Errorf("parsePositiveInt(%q,%d,%d) = (%d,%v), want (%d,%v)",
				c.raw, c.def, c.max, n, ok, c.wantN, c.wantOK)
		}
	}
}

func TestParseTypeFilter(t *testing.T) {
	got := parseTypeFilter("agent_start, text_delta ,")
	if len(got) != 2 {
		t.Fatalf("len = %d, want 2; got=%v", len(got), got)
	}
	if _, ok := got["agent_start"]; !ok {
		t.Errorf("missing agent_start in %v", got)
	}
	if _, ok := got["text_delta"]; !ok {
		t.Errorf("missing text_delta in %v", got)
	}
	if parseTypeFilter("") != nil {
		t.Errorf("empty input should yield nil")
	}
}
