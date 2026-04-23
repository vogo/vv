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

package tracelog_tests

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/vogo/vage/schema"
)

// TestIntegration_Enabled_EndToEnd_FullPipeline (US-1 / US-5 / US-6)
// Scenario: cfg.Trace.Enabled=true; run a short stubbed agent prompt and
// assert the full setup.Init → TaskAgent.Run → JSONL flush pipeline lands
// a well-formed JSONL file on disk.
//
//   - US-1: a file exists under <tempDir>/<projectHash>/<sid>.jsonl with
//     `agent_start` first and `agent_end` last; every envelope carries
//     type/session_id/timestamp.
//   - US-5: agent_start + agent_end pair is the minimum event coverage the
//     on-disk firehose must capture for downstream four-tuple rebuild.
//   - US-6: setup.Init is the public path all three run modes (CLI / HTTP /
//     MCP) go through; HookManager being non-nil post-Init is the single
//     invariant that makes trace coverage uniform across modes.
func TestIntegration_Enabled_EndToEnd_FullPipeline(t *testing.T) {
	traceDir := t.TempDir()
	cfg := makeTraceConfig(t, traceDir, true)

	initResult, a, stub := initWithStubAgent(t, cfg, "hello from stub")
	defer shutdownWithTimeout(t, initResult)

	// US-6: Init must expose the HookManager publicly when Trace is enabled.
	if initResult.SetupResult.HookManager == nil {
		t.Fatal("US-6: SetupResult.HookManager is nil but Trace.Enabled=true")
	}

	// US-4 (related): Shutdown closure must be non-nil so main.go can defer
	// it unconditionally across all three run modes.
	if initResult.Shutdown == nil {
		t.Fatal("US-4: InitResult.Shutdown is nil; main.go cannot defer flush")
	}

	sid := "sess-enabled-e2e"
	req := &schema.RunRequest{
		SessionID: sid,
		Messages:  []schema.Message{schema.NewUserMessage("please echo hello")},
	}

	resp, err := a.Run(context.Background(), req)
	if err != nil {
		t.Fatalf("agent.Run: %v", err)
	}

	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one response message from stub agent")
	}

	if got := stub.calls.Load(); got != 1 {
		t.Errorf("stub completer called %d times, want 1", got)
	}

	// Flush: Stop the hook manager so every queued event lands on disk
	// before we assert.
	shutdownWithTimeout(t, initResult)

	projectDir := projectTraceDir(traceDir, cfg.Tools.BashWorkingDir)

	entries, err := os.ReadDir(projectDir)
	if err != nil {
		t.Fatalf("read project trace dir %q: %v", projectDir, err)
	}

	if len(entries) == 0 {
		t.Fatalf("expected at least one JSONL file under %q, got none", projectDir)
	}

	jsonlPath := filepath.Join(projectDir, sid+".jsonl")
	events := decodeEvents(t, jsonlPath)

	if len(events) < 2 {
		t.Fatalf("expected >= 2 events (agent_start + agent_end), got %d", len(events))
	}

	if events[0].Type != schema.EventAgentStart {
		t.Errorf("first event type = %q, want %q", events[0].Type, schema.EventAgentStart)
	}

	last := events[len(events)-1]
	if last.Type != schema.EventAgentEnd && last.Type != schema.EventError {
		t.Errorf("last event type = %q, want %q or %q", last.Type, schema.EventAgentEnd, schema.EventError)
	}

	for i, e := range events {
		if e.SessionID != sid {
			t.Errorf("event[%d] session_id = %q, want %q", i, e.SessionID, sid)
		}
	}
}

// TestIntegration_Enabled_MultiSessionRouting (US-1 / US-5)
// Scenario: two sequential agent runs with distinct SessionIDs — each must
// land in its own JSONL file under <projectHash>/. Verifies session
// bucketing the downstream P2-14 resume feature depends on (every session
// must be addressable by ID alone).
func TestIntegration_Enabled_MultiSessionRouting(t *testing.T) {
	traceDir := t.TempDir()
	cfg := makeTraceConfig(t, traceDir, true)

	initResult, a, _ := initWithStubAgent(t, cfg, "ok")
	defer shutdownWithTimeout(t, initResult)

	if initResult.SetupResult.HookManager == nil {
		t.Fatal("HookManager is nil but Trace.Enabled=true")
	}

	sessions := []string{"alpha-session", "beta-session"}
	for _, sid := range sessions {
		req := &schema.RunRequest{
			SessionID: sid,
			Messages:  []schema.Message{schema.NewUserMessage("ping " + sid)},
		}

		if _, err := a.Run(context.Background(), req); err != nil {
			t.Fatalf("Run(%q): %v", sid, err)
		}
	}

	shutdownWithTimeout(t, initResult)

	projectDir := projectTraceDir(traceDir, cfg.Tools.BashWorkingDir)
	for _, sid := range sessions {
		path := filepath.Join(projectDir, sid+".jsonl")
		events := decodeEvents(t, path)

		if len(events) < 2 {
			t.Errorf("session %q: got %d events, want >= 2", sid, len(events))
			continue
		}

		for i, ev := range events {
			if ev.SessionID != sid {
				t.Errorf("session %q event[%d]: wrong session_id = %q", sid, i, ev.SessionID)
			}
		}

		if events[0].Type != schema.EventAgentStart {
			t.Errorf("session %q: first event = %q, want agent_start", sid, events[0].Type)
		}
	}
}

// TestIntegration_Enabled_ShutdownIsIdempotent (US-4)
// Scenario: calling initResult.Shutdown twice must not panic or hang. This
// matches main.go's defer-style usage plus any user-initiated Shutdown from
// HTTP signal handlers.
func TestIntegration_Enabled_ShutdownIsIdempotent(t *testing.T) {
	traceDir := t.TempDir()
	cfg := makeTraceConfig(t, traceDir, true)

	initResult, a, _ := initWithStubAgent(t, cfg, "done")

	// One run so there is something to flush.
	req := &schema.RunRequest{
		SessionID: "idempotent",
		Messages:  []schema.Message{schema.NewUserMessage("hi")},
	}
	if _, err := a.Run(context.Background(), req); err != nil {
		t.Fatalf("Run: %v", err)
	}

	// First Shutdown — flushes and closes files.
	shutdownWithTimeout(t, initResult)

	// Second Shutdown — must be a no-op (the underlying JSONLHook uses
	// sync.Once; hook.Manager.Stop is also guarded).
	shutdownWithTimeout(t, initResult)
}
