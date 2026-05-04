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

package setup

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/checkpoint"
	"github.com/vogo/vv/configs"
)

// TestBuildIterationStore_DisabledSession verifies that turning the
// session subsystem off short-circuits checkpoint construction. The
// caller must see (nil, nil) — a checkpoint store rooted at a directory
// no other subsystem owns would silently leak files for an opt-out
// install.
func TestBuildIterationStore_DisabledSession(t *testing.T) {
	disabled := false
	cfg := &configs.Config{
		Session: configs.SessionConfig{Enabled: &disabled},
	}

	store, err := buildIterationStore(cfg)
	if err != nil {
		t.Fatalf("buildIterationStore: %v", err)
	}
	if store != nil {
		t.Fatalf("expected nil store when session disabled, got %T", store)
	}
}

// TestBuildIterationStore_NilCfg defends against the InitResult
// short-circuit path constructing options before the config is fully
// resolved. nil cfg must yield (nil, nil), not panic.
func TestBuildIterationStore_NilCfg(t *testing.T) {
	store, err := buildIterationStore(nil)
	if err != nil {
		t.Fatalf("buildIterationStore(nil): %v", err)
	}
	if store != nil {
		t.Fatalf("expected nil store for nil cfg, got %T", store)
	}
}

// TestBuildIterationStore_EnabledRoundtrip verifies that a session-on
// config produces a usable FileIterationStore rooted at the same path as
// FileSessionStore would use, so DELETE /v1/sessions/{id} (which
// os.RemoveAll's <root>/<id>) wipes checkpoints alongside meta/events.
func TestBuildIterationStore_EnabledRoundtrip(t *testing.T) {
	dir := t.TempDir()
	cfg := &configs.Config{
		Session: configs.SessionConfig{Dir: dir},
		Tools:   configs.ToolsConfig{BashWorkingDir: "/test/proj"},
	}

	store, err := buildIterationStore(cfg)
	if err != nil {
		t.Fatalf("buildIterationStore: %v", err)
	}
	if store == nil {
		t.Fatal("expected non-nil store when session enabled")
	}

	fs, ok := store.(*checkpoint.FileIterationStore)
	if !ok {
		t.Fatalf("expected *FileIterationStore, got %T", store)
	}

	wantRoot := filepath.Join(dir, SessionProjectName("/test/proj"))
	if fs.Root() != wantRoot {
		t.Errorf("Root = %q, want %q", fs.Root(), wantRoot)
	}

	// Round-trip: save then load by latest pointer.
	cp := &checkpoint.Checkpoint{
		SessionID: "sess-build-iter-roundtrip",
		AgentID:   "coder",
		Iteration: 0,
	}
	if err := store.Save(context.Background(), cp); err != nil {
		t.Fatalf("Save: %v", err)
	}

	got, err := store.Load(context.Background(), "sess-build-iter-roundtrip", "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if got.AgentID != "coder" {
		t.Errorf("AgentID = %q, want coder", got.AgentID)
	}
}

// TestNew_IterationStoreThreaded asserts that every dispatchable
// TaskAgent picks up FactoryOptions.IterationStore. We use the public
// Resume() shape as the probe: with a store wired but no checkpoints
// saved, Resume must return ErrCheckpointNotFound — distinct from the
// "no IterationStore configured" ErrInvalidArgument the caller would see
// if the option were dropped on the floor.
func TestNew_IterationStoreThreaded(t *testing.T) {
	mock := &mockChatCompleter{}
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}

	store := checkpoint.NewMapIterationStore()
	result, err := New(cfg, mock, nil, nil, &Options{IterationStore: store})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	for _, id := range []string{"coder", "researcher", "reviewer"} {
		a := result.Agent(id)
		if a == nil {
			t.Fatalf("agent %q missing", id)
		}

		ta, ok := a.(*taskagent.Agent)
		if !ok {
			t.Fatalf("agent %q is not *taskagent.Agent (got %T)", id, a)
		}

		_, err := ta.Resume(context.Background(), "no-such-session")
		switch {
		case err == nil:
			t.Errorf("agent %q: Resume on empty store should fail", id)
		case errors.Is(err, checkpoint.ErrCheckpointNotFound):
			// expected — store is wired but empty.
		case errors.Is(err, checkpoint.ErrInvalidArgument):
			// distinguish "no store" from "no session id".
			if strings.Contains(err.Error(), "no IterationStore configured") {
				t.Errorf("agent %q: IterationStore not threaded into factory: %v", id, err)
			}
		default:
			t.Errorf("agent %q: unexpected Resume error: %v", id, err)
		}
	}
}

// TestNew_NoIterationStore_ResumeFails ensures the zero-cost path is
// preserved: when callers don't set Options.IterationStore, Resume on
// the constructed agent reports the missing store rather than silently
// hitting some default backend.
func TestNew_NoIterationStore_ResumeFails(t *testing.T) {
	mock := &mockChatCompleter{}
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}

	result, err := New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ta, ok := result.Agent("coder").(*taskagent.Agent)
	if !ok {
		t.Fatal("coder is not *taskagent.Agent")
	}

	_, err = ta.Resume(context.Background(), "any-session")
	if !errors.Is(err, checkpoint.ErrInvalidArgument) {
		t.Fatalf("Resume without store: want ErrInvalidArgument, got %v", err)
	}
	if !strings.Contains(err.Error(), "no IterationStore configured") {
		t.Fatalf("Resume without store: want 'no IterationStore configured', got %v", err)
	}
}

// TestSessionRootDir_DeterministicLayout pins the convention that
// FileSessionStore, FileWorkspace, FileTreeStore and FileIterationStore
// must share — without it, DELETE /v1/sessions/{id} would leave orphan
// files in subsystems that diverged.
func TestSessionRootDir_DeterministicLayout(t *testing.T) {
	cfg := &configs.Config{
		Session: configs.SessionConfig{Dir: "/var/vv-sessions"},
		Tools:   configs.ToolsConfig{BashWorkingDir: "/Users/me/proj"},
	}

	got := sessionRootDir(cfg)
	want := filepath.Join("/var/vv-sessions", SessionProjectName("/Users/me/proj"))
	if got != want {
		t.Errorf("sessionRootDir = %q, want %q", got, want)
	}
}

// Compile-time sanity: ensure mockChatCompleter still satisfies the
// aimodel.ChatCompleter interface used by setup.New. setup_test.go
// declares the type itself; this assertion just stops a refactor there
// from silently breaking the test that piggybacks on it.
var _ aimodel.ChatCompleter = (*mockChatCompleter)(nil)
