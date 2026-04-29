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
	"strings"
	"testing"
	"time"

	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/session"
	"github.com/vogo/vv/cli"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/setup"
)

// TestCLIResume_CrossProcess_RealStore exercises Story A's cross-process
// resume contract end-to-end. We boot setup.Init twice against the SAME
// session directory: the first boot writes events through the SessionHook,
// then the second boot calls cli.PrepareSessionID with the same id and must
// see SessionResumeExisting plus the previously persisted events.
//
// This is the strongest assertion the test layer can make about resume —
// short of forking real `vv` processes — because both boots share nothing
// except the on-disk session store.
func TestCLIResume_CrossProcess_RealStore(t *testing.T) {
	// Stable session dir AND working dir reused across both Init calls.
	// SessionProjectName(BashWorkingDir) determines the project sub-bucket; the
	// two boots must share a work dir to land in the same on-disk directory.
	sessDir := filepath.Join(t.TempDir(), "sessions")
	workDir := t.TempDir()

	// --- First boot: write events for "alpha" through the HookManager. ---
	cfg1 := newCLITestConfig(t, sessDir, workDir)
	res1, err := setup.Init(cfg1, nil)
	if err != nil {
		t.Fatalf("first setup.Init: %v", err)
	}
	if res1.SessionStore == nil {
		t.Fatal("first boot: expected non-nil SessionStore")
	}
	if res1.SetupResult.HookManager == nil {
		t.Fatal("first boot: expected non-nil HookManager")
	}

	const sid = "alpha"
	now := time.Now()
	mgr := res1.SetupResult.HookManager

	// Two events of distinct types so the second boot can read more than one.
	mgr.Dispatch(context.Background(), schema.Event{
		Type: schema.EventAgentStart, AgentID: "coder", SessionID: sid,
		Timestamp: now, Data: schema.AgentStartData{},
	})
	mgr.Dispatch(context.Background(), schema.Event{
		Type: schema.EventAgentEnd, AgentID: "coder", SessionID: sid,
		Timestamp: now.Add(time.Millisecond), Data: schema.AgentEndData{},
	})

	// Drain by stopping. SessionHook.Stop flushes buffered events.
	stopCtx, stopCancel := context.WithTimeout(context.Background(), 5*time.Second)
	res1.Shutdown(stopCtx)
	stopCancel()

	// Sanity check: store on disk has both events under "alpha". Note that
	// the actual layout is <sessDir>/<SessionProjectName(workDir)>/<id>/...
	// so we open the verify store at the same nested path.
	verify, err := session.NewFileSessionStore(filepath.Join(sessDir, setup.SessionProjectName(workDir)))
	if err != nil {
		t.Fatalf("open verify store: %v", err)
	}
	events, err := verify.ListEvents(context.Background(), sid)
	if err != nil {
		t.Fatalf("verify ListEvents: %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("expected >=2 events written by first boot, got %d", len(events))
	}

	// --- Second boot: PrepareSessionID for the same id should see Existing. ---
	cfg2 := newCLITestConfig(t, sessDir, workDir)
	res2, err := setup.Init(cfg2, nil)
	if err != nil {
		t.Fatalf("second setup.Init: %v", err)
	}
	defer res2.Shutdown(context.Background())

	id, mode, prev, err := cli.PrepareSessionID(context.Background(), res2.SessionStore, sid)
	if err != nil {
		t.Fatalf("PrepareSessionID: %v", err)
	}
	if id != sid {
		t.Errorf("returned id = %q, want %q", id, sid)
	}
	if mode != cli.SessionResumeExisting {
		t.Errorf("mode = %v, want SessionResumeExisting", mode)
	}
	if prev == nil {
		t.Fatal("expected non-nil Session for existing id")
	}
	if prev.ID != sid {
		t.Errorf("loaded session id = %q, want %q", prev.ID, sid)
	}

	// Confirm the second boot reads the events written by the first boot.
	events2, err := res2.SessionStore.ListEvents(context.Background(), sid)
	if err != nil {
		t.Fatalf("second-boot ListEvents: %v", err)
	}
	if len(events2) < 2 {
		t.Fatalf("second boot saw %d events, want >=2", len(events2))
	}
}

// TestCLIResume_UnknownID_NotFound covers Story A's "id non-existent" branch:
// `--session <unknown-id>` should bind the id and report NotFound; on the
// first AppendEvent, SessionHook autoCreates the session under that id.
func TestCLIResume_UnknownID_NotFound(t *testing.T) {
	sessDir := filepath.Join(t.TempDir(), "sessions")
	cfg := newCLITestConfig(t, sessDir)
	res, err := setup.Init(cfg, nil)
	if err != nil {
		t.Fatalf("setup.Init: %v", err)
	}
	defer res.Shutdown(context.Background())

	const unknown = "never-seen-before"

	id, mode, prev, err := cli.PrepareSessionID(context.Background(), res.SessionStore, unknown)
	if err != nil {
		t.Fatalf("PrepareSessionID: %v", err)
	}
	if id != unknown {
		t.Errorf("id = %q, want %q (caller should bind verbatim)", id, unknown)
	}
	if mode != cli.SessionResumeNotFound {
		t.Errorf("mode = %v, want SessionResumeNotFound", mode)
	}
	if prev != nil {
		t.Errorf("expected nil Session for not-found id, got %+v", prev)
	}

	// Verify that an AppendEvent under the unknown id triggers SessionHook
	// autoCreate behaviour: we dispatch through the HookManager and then
	// confirm the session became readable via Get.
	mgr := res.SetupResult.HookManager
	if mgr == nil {
		t.Fatal("expected HookManager non-nil")
	}
	mgr.Dispatch(context.Background(), schema.Event{
		Type: schema.EventAgentStart, AgentID: "coder", SessionID: unknown,
		Timestamp: time.Now(), Data: schema.AgentStartData{},
	})

	// Wait for the async hook to drain.
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		got, gerr := res.SessionStore.Get(context.Background(), unknown)
		if gerr == nil && got != nil {
			return
		}
		if gerr != nil && !errors.Is(gerr, session.ErrSessionNotFound) {
			t.Fatalf("Get: %v", gerr)
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("expected SessionHook to autoCreate the session on first event within 2s")
}

// TestCLIResume_RejectInvalidID covers Story A's "id 非法 → 退到 stderr 报错"
// branch: PrepareSessionID must refuse path-traversal-style ids ("..") at the
// boundary so that no FileSessionStore call ever sees them. This protects the
// MapStore path too — MapStore.Get does not call validateID, so a missing
// boundary check would silently bind ".." and let later code traverse out of
// the sessions directory.
func TestCLIResume_RejectInvalidID(t *testing.T) {
	sessDir := filepath.Join(t.TempDir(), "sessions")
	cfg := newCLITestConfig(t, sessDir)
	res, err := setup.Init(cfg, nil)
	if err != nil {
		t.Fatalf("setup.Init: %v", err)
	}
	defer res.Shutdown(context.Background())

	cases := []struct {
		name string
		id   string
	}{
		{"dotdot", ".."},
		{"dot", "."},
		{"slash", "alpha/beta"},
		{"empty-after-trim", "   "}, // PrepareSessionID trims; empty is treated as "mint new", which is NOT an error — covered separately
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, mode, _, err := cli.PrepareSessionID(context.Background(), res.SessionStore, tc.id)
			if tc.id == "   " {
				// Trimmed-empty is the "mint new" path; not an error.
				if err != nil {
					t.Errorf("expected no error for whitespace id, got %v", err)
				}
				if mode != cli.SessionResumeNew {
					t.Errorf("mode = %v, want SessionResumeNew", mode)
				}
				return
			}
			if err == nil {
				t.Fatalf("expected error for invalid id %q, got nil", tc.id)
			}
			if !strings.Contains(err.Error(), "invalid session id") {
				t.Errorf("error = %q, want it to mention 'invalid session id'", err.Error())
			}
		})
	}
}

// TestCLIResume_TouchSession_RefreshesOnExisting confirms the wiring that
// cli.App.Run runs at startup: TouchSession on an existing session refreshes
// the metadata UpdatedAt so `vv --session list` reflects the new activity.
// SessionHook only writes events (not meta), so without this explicit Touch
// the meta UpdatedAt would stay stuck at creation time.
func TestCLIResume_TouchSession_RefreshesOnExisting(t *testing.T) {
	sessDir := filepath.Join(t.TempDir(), "sessions")
	cfg := newCLITestConfig(t, sessDir)
	res, err := setup.Init(cfg, nil)
	if err != nil {
		t.Fatalf("setup.Init: %v", err)
	}
	defer res.Shutdown(context.Background())

	const sid = "touch-existing"
	createdAt := time.Now().Add(-time.Hour)
	if err := res.SessionStore.Create(context.Background(), &session.Session{
		ID: sid, AgentID: "coder", State: session.StateActive,
		CreatedAt: createdAt, UpdatedAt: createdAt,
	}); err != nil {
		t.Fatalf("Create: %v", err)
	}

	// Touch should refresh UpdatedAt to roughly time.Now() — implementations
	// that go through Update() will set it server-side.
	if err := cli.TouchSession(context.Background(), res.SessionStore, sid, ""); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}

	got, err := res.SessionStore.Get(context.Background(), sid)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.UpdatedAt.After(createdAt) {
		t.Errorf("UpdatedAt = %v, want strictly after createdAt %v", got.UpdatedAt, createdAt)
	}
}

// newCLITestConfig builds a minimal Config rooted at the supplied sessDir so
// the same store can be shared across two setup.Init calls in cross-process
// resume tests. Optional workDir argument lets cross-process tests pin the
// project bucket: setup uses SessionProjectName(BashWorkingDir) to nest
// sessions, so two boots that need to find the same id MUST share a work dir.
func newCLITestConfig(t *testing.T, sessDir string, workDir ...string) *configs.Config {
	t.Helper()
	work := ""
	if len(workDir) > 0 && workDir[0] != "" {
		work = workDir[0]
	} else {
		work = t.TempDir()
	}
	mem := t.TempDir()

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
		Session: configs.SessionConfig{Dir: sessDir},
	}
}
