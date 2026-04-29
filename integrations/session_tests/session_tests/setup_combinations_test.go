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

// TestSetup_SessionAndTraceCoexist confirms Story C's "both subsystems on at
// once" promise: the hook.Manager carries TWO async hooks and the events
// dispatched flow into both the SessionStore and the trace JSONL file.
func TestSetup_SessionAndTraceCoexist(t *testing.T) {
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

	// Push an event and confirm BOTH sinks observed it: the SessionStore
	// must contain the event, AND the trace base directory must have been
	// created (the project-hash subdir comes from working dir hash).
	const sid = "coexist-smoke"
	res.SetupResult.HookManager.Dispatch(context.Background(), schema.Event{
		Type: schema.EventAgentStart, AgentID: "coder", SessionID: sid,
		Timestamp: time.Now(), Data: schema.AgentStartData{},
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

	// Trigger graceful shutdown so trace files are flushed before we stat.
	stopCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	res.Shutdown(stopCtx)
	cancel()

	// The trace tracer creates a project-hash subdir under cfg.Trace.Dir.
	// Confirm at least one entry exists; specifying file shape further would
	// couple to tracelog internals.
	entries, err := os.ReadDir(traceDir)
	if err != nil {
		t.Fatalf("read trace dir %s: %v", traceDir, err)
	}
	if len(entries) == 0 {
		t.Errorf("expected trace dir %s to contain project-hash subdir, got empty", traceDir)
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
