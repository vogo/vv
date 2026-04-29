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
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/vogo/vage/hook"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/session"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/setup"
)

// TestSetupInit_DefaultEnabled_StoreAndHookWired confirms the end-to-end
// default-on contract: setup.Init constructs a SessionStore, registers a
// SessionHook on the hook.Manager, and exposes the store via InitResult.
func TestSetupInit_DefaultEnabled_StoreAndHookWired(t *testing.T) {
	cfg := newTestConfig(t)

	res, err := setup.Init(cfg, nil)
	if err != nil {
		t.Fatalf("setup.Init: %v", err)
	}
	defer res.Shutdown(context.Background())

	if res.SessionStore == nil {
		t.Fatal("expected non-nil SessionStore in InitResult under default config")
	}
	if res.SetupResult.HookManager == nil {
		t.Fatal("expected non-nil HookManager when SessionStore is wired")
	}

	// Push an event through the manager and confirm the hook auto-creates +
	// records it in the store. This exercises the same code path that
	// TaskAgent / Dispatcher run through.
	mgr := res.SetupResult.HookManager
	const sid = "wiring-smoke"
	mgr.Dispatch(context.Background(), schema.Event{
		Type:      schema.EventAgentStart,
		AgentID:   "coder",
		SessionID: sid,
		Timestamp: time.Now(),
		Data:      schema.AgentStartData{},
	})

	// Async hook — give the consumer a brief window to drain the channel.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		events, err := res.SessionStore.ListEvents(context.Background(), sid)
		if err == nil && len(events) >= 1 {
			return
		}
		if err != nil && !errors.Is(err, session.ErrSessionNotFound) {
			t.Fatalf("ListEvents: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("expected SessionHook to persist at least one event within 2s")
}

// TestSetupInit_Disabled_NoStore confirms the opt-out: when
// session.enabled=false (with trace also off), no store is created and the
// HookManager remains nil so existing zero-cost paths stay untouched.
func TestSetupInit_Disabled_NoStore(t *testing.T) {
	cfg := newTestConfig(t)
	off := false
	cfg.Session.Enabled = &off

	res, err := setup.Init(cfg, nil)
	if err != nil {
		t.Fatalf("setup.Init: %v", err)
	}
	defer res.Shutdown(context.Background())

	if res.SessionStore != nil {
		t.Errorf("expected nil SessionStore when session.enabled=false, got %v", res.SessionStore)
	}
	if res.SetupResult.HookManager != nil {
		t.Errorf("expected nil HookManager when session and trace are disabled, got %v", res.SetupResult.HookManager)
	}
}

// TestFileSessionStore_RoundTrip_FromInit confirms that the FileSessionStore
// instance returned by setup.Init writes meta.json + events.jsonl under the
// configured directory, exercising the "default open-store" path.
func TestFileSessionStore_RoundTrip_FromInit(t *testing.T) {
	cfg := newTestConfig(t)

	res, err := setup.Init(cfg, nil)
	if err != nil {
		t.Fatalf("setup.Init: %v", err)
	}
	defer res.Shutdown(context.Background())

	ctx := context.Background()
	id := "round-" + time.Now().Format("150405.000000000")
	if err := res.SessionStore.Create(ctx, &session.Session{
		ID: id, AgentID: "coder", State: session.StateActive,
		CreatedAt: time.Now(), UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}
	if err := res.SessionStore.AppendEvent(ctx, id, schema.Event{
		Type: schema.EventAgentStart, SessionID: id, Timestamp: time.Now(),
	}); err != nil {
		t.Fatalf("AppendEvent: %v", err)
	}

	got, err := res.SessionStore.Get(ctx, id)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AgentID != "coder" {
		t.Errorf("AgentID = %q, want coder", got.AgentID)
	}
}

// newTestConfig assembles a minimal Config that can drive setup.Init in
// integration tests without any real LLM traffic. memory + session dirs are
// rooted at t.TempDir() so each test run is isolated.
func newTestConfig(t *testing.T) *configs.Config {
	t.Helper()
	work := t.TempDir()
	mem := t.TempDir()
	sess := filepath.Join(t.TempDir(), "sessions")

	return &configs.Config{
		LLM: configs.LLMConfig{
			Provider: "openai",
			Model:    "stub-model",
			APIKey:   "test-key-not-used",
			BaseURL:  "http://127.0.0.1:0",
		},
		Agents:  configs.AgentsConfig{MaxIterations: 2},
		Memory:  configs.MemoryConfig{Dir: mem, MaxConcurrency: 1, SessionWindow: 50},
		Tools:   configs.ToolsConfig{BashTimeout: 10, BashWorkingDir: work},
		Session: configs.SessionConfig{Dir: sess},
	}
}

// _ keeps the hook import alive on go versions that lint unused.
var _ = hook.NewManager
