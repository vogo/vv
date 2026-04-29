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

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/memory"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/session"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/httpapis"
	vvmemory "github.com/vogo/vv/memories"
)

// TestHTTP_Sessions_RoundTrip drives the live /v1/sessions/* endpoints by
// booting httpapis.Serve in-process against a real FileSessionStore. It
// covers GET list, GET detail, GET events, PATCH, DELETE in sequence so the
// happy path stays observable end-to-end.
func TestHTTP_Sessions_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, err := session.NewFileSessionStore(dir)
	if err != nil {
		t.Fatalf("NewFileSessionStore: %v", err)
	}

	// Seed two sessions and one event so List/Get/Events have data.
	ctx := context.Background()
	now := time.Now()
	if err := store.Create(ctx, &session.Session{
		ID: "alpha", AgentID: "coder", State: session.StateActive,
		Title: "fix login", CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed alpha: %v", err)
	}
	if err := store.Create(ctx, &session.Session{
		ID: "beta", AgentID: "researcher", State: session.StateCompleted,
		CreatedAt: now, UpdatedAt: now.Add(time.Minute),
	}); err != nil {
		t.Fatalf("seed beta: %v", err)
	}
	if err := store.AppendEvent(ctx, "alpha", schema.Event{
		Type: schema.EventAgentStart, SessionID: "alpha", Timestamp: now,
	}); err != nil {
		t.Fatalf("seed event: %v", err)
	}

	// Find a free port for the server.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()

	cfg := &configs.Config{
		LLM: configs.LLMConfig{
			Provider: "openai", Model: "stub", APIKey: "k", BaseURL: "http://127.0.0.1:0",
		},
		Server: configs.ServerConfig{Addr: addr},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
		Memory: configs.MemoryConfig{Dir: t.TempDir(), MaxConcurrency: 1, SessionWindow: 50},
	}

	memStore, err := vvmemory.NewFileStore(cfg.Memory.Dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}
	persistentMem := memory.NewPersistentMemoryWithStore(memStore)

	dispatcher := stubAgent{id: "orchestrator"}
	srvCtx, cancel := context.WithCancel(ctx)
	defer cancel()

	var serveErr error
	var wg sync.WaitGroup
	wg.Go(func() {
		serveErr = httpapis.Serve(
			srvCtx, cfg, nil, dispatcher, nil, persistentMem,
			nil, nil, nil, nil, store,
		)
	})

	baseURL := "http://" + addr
	waitForServer(t, baseURL)

	t.Run("list returns both sessions", func(t *testing.T) {
		body := getJSON(t, baseURL+"/v1/sessions")
		sessions := body["sessions"].([]any)
		if len(sessions) != 2 {
			t.Errorf("expected 2 sessions, got %d", len(sessions))
		}
	})

	t.Run("list filters by agent_id", func(t *testing.T) {
		body := getJSON(t, baseURL+"/v1/sessions?agent_id=coder")
		sessions := body["sessions"].([]any)
		if len(sessions) != 1 {
			t.Fatalf("expected 1 session for agent_id=coder, got %d", len(sessions))
		}
		s := sessions[0].(map[string]any)
		if s["id"] != "alpha" {
			t.Errorf("expected alpha, got %v", s["id"])
		}
	})

	t.Run("get returns meta", func(t *testing.T) {
		body := getJSON(t, baseURL+"/v1/sessions/alpha")
		if body["id"] != "alpha" {
			t.Errorf("id = %v, want alpha", body["id"])
		}
	})

	t.Run("get 404 on unknown", func(t *testing.T) {
		resp, err := http.Get(baseURL + "/v1/sessions/missing")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusNotFound {
			t.Errorf("status = %d, want 404", resp.StatusCode)
		}
	})

	t.Run("events returns one entry for alpha", func(t *testing.T) {
		body := getJSON(t, baseURL+"/v1/sessions/alpha/events")
		events := body["events"].([]any)
		if len(events) != 1 {
			t.Errorf("expected 1 event, got %d", len(events))
		}
	})

	t.Run("patch title", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodPatch, baseURL+"/v1/sessions/alpha",
			strings.NewReader(`{"title":"fix login (renamed)"}`))
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode != http.StatusOK {
			b, _ := io.ReadAll(resp.Body)
			t.Fatalf("PATCH status = %d, body=%s", resp.StatusCode, string(b))
		}
		var body map[string]any
		if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["title"] != "fix login (renamed)" {
			t.Errorf("title not updated, got %v", body["title"])
		}
	})

	t.Run("delete then 404", func(t *testing.T) {
		req, _ := http.NewRequest(http.MethodDelete, baseURL+"/v1/sessions/alpha", nil)
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			t.Fatal(err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			t.Errorf("DELETE status = %d", resp.StatusCode)
		}

		resp2, err := http.Get(baseURL + "/v1/sessions/alpha")
		if err != nil {
			t.Fatal(err)
		}
		defer func() { _ = resp2.Body.Close() }()
		if resp2.StatusCode != http.StatusNotFound {
			t.Errorf("post-delete GET status = %d, want 404", resp2.StatusCode)
		}
	})

	cancel()
	wg.Wait()
	if serveErr != nil && serveErr != http.ErrServerClosed {
		t.Logf("serve returned: %v", serveErr)
	}
}

// TestHTTP_Sessions_NotMounted_WhenStoreNil confirms /v1/sessions returns
// 404 when the SessionStore is nil — the routes should not be registered.
func TestHTTP_Sessions_NotMounted_WhenStoreNil(t *testing.T) {
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
	defer cancel()

	var wg sync.WaitGroup
	wg.Go(func() {
		_ = httpapis.Serve(
			srvCtx, cfg, nil, stubAgent{id: "orchestrator"}, nil, persistentMem,
			nil, nil, nil, nil, nil, // sessionStore = nil
		)
	})

	baseURL := "http://" + addr
	waitForServer(t, baseURL)

	resp, err := http.Get(baseURL + "/v1/sessions")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusNotFound {
		t.Errorf("status = %d, want 404 when store is disabled", resp.StatusCode)
	}

	cancel()
	wg.Wait()
}

// stubAgent is a minimal agent.Agent that the dispatcher slot needs but the
// session endpoints do not consult.
type stubAgent struct{ id string }

func (s stubAgent) ID() string          { return s.id }
func (s stubAgent) Name() string        { return s.id }
func (s stubAgent) Description() string { return "" }
func (s stubAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	return &schema.RunResponse{}, nil
}

var _ agent.Agent = stubAgent{}

func waitForServer(t *testing.T, baseURL string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(baseURL + "/v1/agents")
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server never came up at %s", baseURL)
}

func getJSON(t *testing.T, url string) map[string]any {
	t.Helper()
	resp, err := http.Get(url)
	if err != nil {
		t.Fatalf("GET %s: %v", url, err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		b, _ := io.ReadAll(resp.Body)
		t.Fatalf("GET %s status=%d body=%s", url, resp.StatusCode, string(b))
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return body
}

// silence unused imports (aimodel) when compiled in isolation.
var _ = aimodel.RoleAssistant
