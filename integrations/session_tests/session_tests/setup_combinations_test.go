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
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/session"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/setup"
)

// TestSetup_SessionEnabled_TraceDisabled exercises Story C's "session-only"
// path: the previous trace-gated buildHookManager would have returned nil
// when trace was off, leaving the session subsystem dead. We confirm that
// when only Session is enabled, setup.Init still constructs the hook.Manager
// AND a SessionStore.
func TestSetup_SessionEnabled_TraceDisabled(t *testing.T) {
	cfg := newTestConfig(t)
	// Trace defaults to off; be explicit so the test intent is clear.
	off := false
	cfg.Trace.Enabled = &off

	res, err := setup.Init(cfg, nil)
	if err != nil {
		t.Fatalf("setup.Init: %v", err)
	}
	defer res.Shutdown(context.Background())

	if res.SessionStore == nil {
		t.Fatal("expected non-nil SessionStore when only Session is enabled")
	}
	if res.SetupResult.HookManager == nil {
		t.Fatal("expected non-nil HookManager when only Session is enabled (trace=off)")
	}
}

// TestSetup_TraceFoldsIntoSessionEventLog confirms that asking for trace
// logging while the session subsystem is on produces ONE event sink, not
// two.
//
// This inverts the previous expectation on purpose. The trace hook wrote
// the same events a second time under a second directory convention
// (ProjectHash buckets vs the session store's readable project name);
// the request is now honoured by widening the session's own log to every
// event type, which is where the bytes were going to land anyway.
func TestSetup_TraceFoldsIntoSessionEventLog(t *testing.T) {
	cfg := newTestConfig(t)

	on := true
	traceDir := filepath.Join(t.TempDir(), "traces")
	cfg.Trace.Enabled = &on
	cfg.Trace.Dir = traceDir

	res, err := setup.Init(cfg, nil)
	if err != nil {
		t.Fatalf("setup.Init: %v", err)
	}
	defer res.Shutdown(context.Background())

	if res.SessionStore == nil {
		t.Fatal("expected non-nil SessionStore with trace+session both on")
	}
	if res.SetupResult.HookManager == nil {
		t.Fatal("expected non-nil HookManager with trace+session both on")
	}

	if got := cfg.Session.EffectiveEventPersist(); got != configs.EventPersistAll {
		t.Errorf("event_persist = %q, want %q — trace must widen the session log", got, configs.EventPersistAll)
	}

	// A text_delta is the probe: it is excluded from the default
	// whitelist precisely because messages.jsonl owns the assembled
	// text, so seeing it here proves the widening took effect.
	const sid = "coexist-smoke"
	res.SetupResult.HookManager.Dispatch(context.Background(), schema.Event{
		Type: schema.EventTextDelta, AgentID: "coder", SessionID: sid,
		Timestamp: time.Now(), Data: schema.TextDeltaData{Delta: "hello"},
	})

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		events, err := res.SessionStore.ListEvents(context.Background(), sid)
		if err == nil && len(events) >= 1 {
			break
		}
		if err != nil && !errors.Is(err, session.ErrSessionNotFound) {
			t.Fatalf("ListEvents: %v", err)
		}
		time.Sleep(20 * time.Millisecond)
	}

	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	res.Shutdown(stopCtx)
	cancel()

	events, err := res.SessionStore.ListEvents(context.Background(), sid)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}

	if len(events) == 0 || events[0].Type != schema.EventTextDelta {
		t.Errorf("session log = %+v, want the text_delta that event_persist:all admits", events)
	}

	// The old trace tree must stay untouched: that second copy is the
	// duplication this redesign removes.
	if _, err := os.ReadDir(traceDir); !os.IsNotExist(err) {
		t.Errorf("trace directory %s was created (err=%v); the second event copy is supposed to be gone", traceDir, err)
	}
}

// TestSetup_SessionDir_Override confirms that a non-empty cfg.Session.Dir is
// honoured (it bypasses the ~/.vv/sessions default). This validates the
// SessionConfig.EffectiveDir override path that the YAML / VV_SESSION_DIR env
// override relies on.
func TestSetup_SessionDir_Override(t *testing.T) {
	cfg := newTestConfig(t)
	customDir := filepath.Join(t.TempDir(), "custom-sessions")
	cfg.Session.Dir = customDir

	res, err := setup.Init(cfg, nil)
	if err != nil {
		t.Fatalf("setup.Init: %v", err)
	}
	defer res.Shutdown(context.Background())

	if res.SessionStore == nil {
		t.Fatal("expected non-nil SessionStore")
	}

	// Push an event and confirm files land under customDir.
	mgr := res.SetupResult.HookManager
	const sid = "override-smoke"
	mgr.Dispatch(context.Background(), schema.Event{
		Type: schema.EventAgentStart, AgentID: "coder", SessionID: sid,
		Timestamp: time.Now(), Data: schema.AgentStartData{},
	})

	// The on-disk layout is <customDir>/<SessionProjectName(BashWorkingDir)>/<id>/.
	// newTestConfig provides a t.TempDir as BashWorkingDir; we look the bucket
	// up via the same helper so the test stays robust if the rules change.
	wantDir := filepath.Join(customDir, setup.SessionProjectName(cfg.Tools.BashWorkingDir), sid)

	// Wait for hook drain.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if _, err := os.Stat(wantDir); err == nil {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("expected session dir %s to exist after event dispatch", wantDir)
}

// TestSetup_SessionConfig_DefaultEnabled verifies the default-on contract:
// a fresh Config with zero-valued Session.Enabled (nil pointer) is
// considered enabled, matching the documented "fresh install gets durable
// conversation history without configuration" behaviour.
func TestSetup_SessionConfig_DefaultEnabled(t *testing.T) {
	var cfg configs.SessionConfig // zero value; Enabled = nil
	if !cfg.IsEnabled() {
		t.Errorf("zero SessionConfig.IsEnabled() = false, want true (default-on)")
	}

	on := true
	cfg.Enabled = &on
	if !cfg.IsEnabled() {
		t.Errorf("Enabled=true should be IsEnabled()==true")
	}

	off := false
	cfg.Enabled = &off
	if cfg.IsEnabled() {
		t.Errorf("Enabled=false should be IsEnabled()==false")
	}
}
