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

package session_tests

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vogo/vage/memory"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/session"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/httpapis"
	vvmemory "github.com/vogo/vv/memories"
)

// httpHarness boots httpapis.Serve in-process against a SessionStore that
// callers seed before invocation. It returns the base URL and a teardown
// function. This is the same shape http_sessions_test.go already uses, but
// extracted so the edge-case tests can reuse it without copy-paste.
func httpHarness(t *testing.T, store session.SessionStore) (string, func()) {
	t.Helper()

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Provider: "openai", Model: "stub", APIKey: "k", BaseURL: "http://127.0.0.1:0"},
		Server: configs.ServerConfig{Addr: addr},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
		Memory: configs.MemoryConfig{Dir: t.TempDir(), MaxConcurrency: 1, SessionWindow: 50},
	}
	memStore, err := vvmemory.NewFileStore(cfg.Memory.Dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	persistentMem := memory.NewPersistentMemoryWithStore(memStore)

	srvCtx, cancel := context.WithCancel(context.Background())
	dispatcher := stubAgent{id: "orchestrator"}

	var wg sync.WaitGroup
	wg.Go(func() {
		_ = httpapis.Serve(
			srvCtx, cfg, nil, dispatcher, nil, persistentMem,
			nil, nil, nil, nil, store, nil, nil,
		)
	})

	baseURL := "http://" + addr
	waitForServer(t, baseURL)

	teardown := func() {
		cancel()
		wg.Wait()
	}
	return baseURL, teardown
}

// TestHTTP_List_ZeroLimit_RejectsAs400 covers the validation rule from the
// design: limit=0 is invalid (the default would be ambiguous with "unlimited"),
// and parsePositiveInt's <=0 guard must surface a 400.
func TestHTTP_List_ZeroLimit_RejectsAs400(t *testing.T) {
	store := newSeededStore(t)
	baseURL, teardown := httpHarness(t, store)
	defer teardown()

	resp, err := http.Get(baseURL + "/v1/sessions?limit=0")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d, want 400; body = %s", resp.StatusCode, string(body))
	}
}

// TestHTTP_List_NegativeOffset_RejectsAs400 covers the second branch of
// parsePositiveInt's pagination guard: offset must be >=0.
func TestHTTP_List_NegativeOffset_RejectsAs400(t *testing.T) {
	store := newSeededStore(t)
	baseURL, teardown := httpHarness(t, store)
	defer teardown()

	resp, err := http.Get(baseURL + "/v1/sessions?offset=-1")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d, want 400; body = %s", resp.StatusCode, string(body))
	}
}

// TestHTTP_List_StateFilter exercises the `state=active` filter when both an
// active and a completed session exist. Only the active one should come back.
func TestHTTP_List_StateFilter(t *testing.T) {
	store := newSeededStore(t)
	baseURL, teardown := httpHarness(t, store)
	defer teardown()

	body := getJSON(t, baseURL+"/v1/sessions?state=active")
	sessions := body["sessions"].([]any)
	if len(sessions) != 1 {
		t.Fatalf("expected 1 active session, got %d", len(sessions))
	}
	got := sessions[0].(map[string]any)
	if got["id"] != "alpha" {
		t.Errorf("active session id = %v, want alpha", got["id"])
	}
	if got["state"] != string(session.StateActive) {
		t.Errorf("state = %v, want active", got["state"])
	}
}

// TestHTTP_Events_LimitTailTruncates exercises the "tail-truncate to most
// recent N events" rule from handleListEvents. We seed 3 events and ask for
// limit=1; the response must carry only the most recently appended event.
func TestHTTP_Events_LimitTailTruncates(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewFileSessionStore(dir)
	if err != nil {
		t.Fatalf("NewFileSessionStore: %v", err)
	}
	ctx := context.Background()
	const sid = "limited"
	if err := store.Create(ctx, &session.Session{
		ID: sid, AgentID: "coder", State: session.StateActive,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Append three events with distinct types so we can assert which one
	// came back.
	for i, et := range []string{
		schema.EventAgentStart,
		schema.EventTextDelta,
		schema.EventAgentEnd,
	} {
		ev := schema.Event{
			Type: et, SessionID: sid,
			Timestamp: time.Now().Add(time.Duration(i) * time.Millisecond),
		}
		switch et {
		case schema.EventAgentStart:
			ev.Data = schema.AgentStartData{}
		case schema.EventAgentEnd:
			ev.Data = schema.AgentEndData{}
		default:
			ev.Data = schema.TextDeltaData{Delta: "hi"}
		}
		if err := store.AppendEvent(ctx, sid, ev); err != nil {
			t.Fatalf("AppendEvent[%d]: %v", i, err)
		}
	}

	baseURL, teardown := httpHarness(t, store)
	defer teardown()

	body := getJSON(t, baseURL+"/v1/sessions/"+sid+"/events?limit=1")
	events := body["events"].([]any)
	if len(events) != 1 {
		t.Fatalf("limit=1 should return 1 event, got %d", len(events))
	}
	got := events[0].(map[string]any)
	if got["type"] != schema.EventAgentEnd {
		t.Errorf("event[0].type = %v, want %s (most recent)", got["type"], schema.EventAgentEnd)
	}
}

// TestHTTP_Events_TypeFilter_MultiValue exercises the comma-separated
// `type=` filter. Seeding three events then asking for two of the three
// types must yield exactly the matching subset, in append order.
func TestHTTP_Events_TypeFilter_MultiValue(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewFileSessionStore(dir)
	if err != nil {
		t.Fatalf("NewFileSessionStore: %v", err)
	}
	ctx := context.Background()
	const sid = "filtered"
	if err := store.Create(ctx, &session.Session{
		ID: sid, AgentID: "coder", State: session.StateActive,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	types := []string{
		schema.EventAgentStart,
		schema.EventTextDelta,
		schema.EventAgentEnd,
	}
	for i, et := range types {
		ev := schema.Event{Type: et, SessionID: sid, Timestamp: time.Now().Add(time.Duration(i) * time.Millisecond)}
		switch et {
		case schema.EventAgentStart:
			ev.Data = schema.AgentStartData{}
		case schema.EventAgentEnd:
			ev.Data = schema.AgentEndData{}
		case schema.EventTextDelta:
			ev.Data = schema.TextDeltaData{Delta: "hi"}
		}
		if err := store.AppendEvent(ctx, sid, ev); err != nil {
			t.Fatalf("AppendEvent[%d]: %v", i, err)
		}
	}

	baseURL, teardown := httpHarness(t, store)
	defer teardown()

	body := getJSON(t, baseURL+"/v1/sessions/"+sid+"/events?type=agent_start,text_delta")
	events := body["events"].([]any)
	if len(events) != 2 {
		t.Fatalf("type filter should return 2 events, got %d", len(events))
	}
	want := []string{schema.EventAgentStart, schema.EventTextDelta}
	for i, ev := range events {
		got := ev.(map[string]any)["type"]
		if got != want[i] {
			t.Errorf("event[%d].type = %v, want %s", i, got, want[i])
		}
	}
}

// TestHTTP_Patch_Metadata_FullReplacement covers design §6.3: PATCH metadata
// is REPLACEMENT, not merge. Sending {"metadata": {}} must wipe an existing
// non-empty metadata map.
func TestHTTP_Patch_Metadata_FullReplacement(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewFileSessionStore(dir)
	if err != nil {
		t.Fatalf("NewFileSessionStore: %v", err)
	}
	ctx := context.Background()
	const sid = "meta-replace"
	if err := store.Create(ctx, &session.Session{
		ID: sid, AgentID: "coder", State: session.StateActive,
		Metadata:  map[string]any{"k1": "v1", "k2": "v2"},
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	baseURL, teardown := httpHarness(t, store)
	defer teardown()

	req, _ := http.NewRequest(http.MethodPatch, baseURL+"/v1/sessions/"+sid,
		strings.NewReader(`{"metadata":{}}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, body = %s", resp.StatusCode, string(body))
	}

	got, err := store.Get(ctx, sid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if len(got.Metadata) != 0 {
		t.Errorf("metadata after replace = %v, want empty", got.Metadata)
	}
}

// TestHTTP_Patch_NonExistent_404 covers the missing-id path on PATCH: the
// endpoint must surface ErrSessionNotFound as 404, not silently create.
func TestHTTP_Patch_NonExistent_404(t *testing.T) {
	store := newSeededStore(t)
	baseURL, teardown := httpHarness(t, store)
	defer teardown()

	req, _ := http.NewRequest(http.MethodPatch, baseURL+"/v1/sessions/never-existed",
		strings.NewReader(`{"title":"x"}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d, want 404; body = %s", resp.StatusCode, string(body))
	}
}

// TestHTTP_Patch_InvalidState_400 covers design §6.3: state must be one of
// active/paused/completed/failed; everything else is 400.
func TestHTTP_Patch_InvalidState_400(t *testing.T) {
	store := newSeededStore(t)
	baseURL, teardown := httpHarness(t, store)
	defer teardown()

	req, _ := http.NewRequest(http.MethodPatch, baseURL+"/v1/sessions/alpha",
		strings.NewReader(`{"state":"bogus"}`))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusBadRequest {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d, want 400; body = %s", resp.StatusCode, string(body))
	}
}

// TestHTTP_Delete_Idempotent_MissingID confirms the design's idempotent
// contract: DELETE on an id that never existed still returns 200 with the
// `deleted` status payload, mirroring SessionStore.Delete's contract.
func TestHTTP_Delete_Idempotent_MissingID(t *testing.T) {
	store := newSeededStore(t)
	baseURL, teardown := httpHarness(t, store)
	defer teardown()

	req, _ := http.NewRequest(http.MethodDelete, baseURL+"/v1/sessions/missing-id", nil)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Fatalf("status = %d, want 200 (idempotent); body = %s", resp.StatusCode, string(body))
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatal(err)
	}
	if body["status"] != "deleted" {
		t.Errorf("status payload = %v, want 'deleted'", body["status"])
	}
}

// TestHTTP_List_LimitClampedToMax confirms the design's max-limit clamp:
// a request for limit=10000 (well above the 200 ceiling) must NOT 400 — it
// is silently clamped and the request still succeeds.
func TestHTTP_List_LimitClampedToMax(t *testing.T) {
	store := newSeededStore(t)
	baseURL, teardown := httpHarness(t, store)
	defer teardown()

	resp, err := http.Get(baseURL + "/v1/sessions?limit=10000")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		t.Errorf("status = %d, want 200 (limit should be clamped, not rejected); body = %s",
			resp.StatusCode, string(body))
	}
}

// newSeededStore builds a FileSessionStore preloaded with two sessions —
// "alpha" (active, agent=coder) and "beta" (completed, agent=researcher) —
// so that filter / state / not-found tests have a non-empty corpus.
func newSeededStore(t *testing.T) session.SessionStore {
	t.Helper()
	store, err := session.NewFileSessionStore(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileSessionStore: %v", err)
	}
	now := time.Now()
	if err := store.Create(context.Background(), &session.Session{
		ID: "alpha", AgentID: "coder", State: session.StateActive,
		Title: "fix login", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed alpha: %v", err)
	}
	if err := store.Create(context.Background(), &session.Session{
		ID: "beta", AgentID: "researcher", State: session.StateCompleted,
		CreatedAt: now, UpdatedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("seed beta: %v", err)
	}
	return store
}
