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

// Package vector_tests is the vv-side end-to-end suite for the vector
// subsystem. It runs against in-process backends (memory + hash) so it
// works in every CI environment without external services. The qdrant +
// OpenAI integration test lives in vage/integrations/vector_tests and
// gates on QDRANT_URL / OPENAI_API_KEY.
package vector_tests //nolint:revive // integration test package

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vogo/vage/memory"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/vector"
	"github.com/vogo/vage/vector/archivehook"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/httpapis"
)

// TestVector_AutoWriteHook_IndexesAgentEnd seeds an EventAgentEnd via
// the same archivehook the vv setup wires up; confirms the document
// lands in the store, validating the EventAgentEnd → embed → store.Add
// chain end-to-end with no LLM and no agent runtime in the loop.
func TestVector_AutoWriteHook_IndexesAgentEnd(t *testing.T) {
	store := vector.NewMapVectorStore()
	emb := vector.NewHashEmbedder(64)

	hook, err := archivehook.New(store, emb)
	if err != nil {
		t.Fatalf("archivehook.New: %v", err)
	}
	if err := hook.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}
	defer func() { _ = hook.Stop(context.Background()) }()

	hook.EventChan() <- schema.Event{
		Type:      schema.EventAgentEnd,
		SessionID: "sess-e2e",
		AgentID:   "primary",
		Timestamp: time.Now(),
		Data: schema.AgentEndData{
			Message:    "End-to-end body that survives the minimum-bytes filter.",
			StopReason: schema.StopReasonComplete,
		},
	}

	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) && store.Len() == 0 {
		time.Sleep(5 * time.Millisecond)
	}
	if got := store.Len(); got != 1 {
		t.Fatalf("expected 1 doc indexed, got %d", got)
	}
}

// TestVector_HTTPRoundTripViaServe boots httpapis.Serve in-process
// against the in-memory vector backends, exercises POST /v1/vector/add
// + GET /v1/vector/search, and asserts the round-trip works through
// the production routing surface (not just the handler functions).
func TestVector_HTTPRoundTripViaServe(t *testing.T) {
	store := vector.NewMapVectorStore()
	emb := vector.NewHashEmbedder(64)

	addr := pickPort(t)
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test", APIKey: "x"},
		Server: configs.ServerConfig{Addr: addr},
	}

	persistentMem := memory.NewPersistentMemoryWithStore(memory.NewMapStore())
	srvCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Go(func() {
		_ = httpapis.Serve(
			srvCtx, cfg, nil, stubAgent{}, nil, persistentMem,
			nil, nil, nil, nil, nil, nil, nil, store, emb, nil,
		)
	})
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})

	baseURL := "http://" + addr
	waitForHTTP(t, baseURL+"/v1/vector/search?q=ping")

	for i, text := range []string{"alpha shared keyword tokens here", "beta shared keyword tokens here"} {
		body, _ := json.Marshal(map[string]any{"id": fmt.Sprintf("doc-%d", i), "text": text})
		resp, err := http.Post(baseURL+"/v1/vector/add", "application/json", strings.NewReader(string(body)))
		if err != nil {
			t.Fatalf("POST add %d: %v", i, err)
		}
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("POST add %d status = %d", i, resp.StatusCode)
		}
	}

	resp, err := http.Get(baseURL + "/v1/vector/search?q=alpha+shared+keyword+tokens+here&top_k=2")
	if err != nil {
		t.Fatalf("GET search: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("GET search status = %d", resp.StatusCode)
	}
	body, _ := io.ReadAll(resp.Body)

	var got struct {
		Hits []struct {
			ID    string  `json:"id"`
			Score float32 `json:"score"`
			Text  string  `json:"text"`
		} `json:"hits"`
	}
	if err := json.Unmarshal(body, &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Hits) != 2 {
		t.Fatalf("hits = %d, want 2; body=%s", len(got.Hits), body)
	}
	if got.Hits[0].ID != "doc-0" {
		t.Errorf("top hit = %q, want doc-0", got.Hits[0].ID)
	}
}

// TestVector_HTTPDisabledReturns503 confirms that booting Serve with
// nil vectorStore makes the routes 404 (not registered). 503 is
// reserved for partial-wiring (store present but embedder missing) and
// is exercised by the unit suite.
func TestVector_HTTPDisabledReturns503(t *testing.T) {
	addr := pickPort(t)
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test", APIKey: "x"},
		Server: configs.ServerConfig{Addr: addr},
	}
	persistentMem := memory.NewPersistentMemoryWithStore(memory.NewMapStore())

	srvCtx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	wg.Go(func() {
		_ = httpapis.Serve(
			srvCtx, cfg, nil, stubAgent{}, nil, persistentMem,
			nil, nil, nil, nil, nil, nil, nil, nil, nil, nil,
		)
	})
	t.Cleanup(func() {
		cancel()
		wg.Wait()
	})

	baseURL := "http://" + addr
	waitForHTTP(t, baseURL+"/v1/memory")

	resp, err := http.Post(baseURL+"/v1/vector/add", "application/json", strings.NewReader(`{"text":"x"}`))
	if err != nil {
		t.Fatalf("POST: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	// Routes are not mounted when vectorStore is nil; the catch-all
	// service handler answers — typically with 404 or method-not-allowed.
	// The exact code is less important than "request did not 2xx".
	if resp.StatusCode >= 200 && resp.StatusCode < 300 {
		t.Errorf("expected non-2xx, got %d", resp.StatusCode)
	}
}

// stubAgent is the minimal agent.Agent implementation required by
// httpapis.Serve when we only care about the vector routes.
type stubAgent struct{}

func (stubAgent) ID() string          { return "stub" }
func (stubAgent) Name() string        { return "Stub" }
func (stubAgent) Description() string { return "stub for vector e2e" }
func (stubAgent) Run(_ context.Context, req *schema.RunRequest) (*schema.RunResponse, error) {
	_ = req
	return &schema.RunResponse{}, nil
}

// pickPort returns a 127.0.0.1 listener-style address that is free at
// the moment of the call. Brief race window between close and reuse is
// acceptable for test usage.
func pickPort(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("Listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr
}

// waitForHTTP blocks until the URL responds (any status) or 2s pass.
// The body is not inspected — only "the listener accepted a connection
// and returned something" matters.
func waitForHTTP(t *testing.T, url string) {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		resp, err := http.Get(url)
		if err == nil {
			_ = resp.Body.Close()
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("server at %s did not come up", url)
}
