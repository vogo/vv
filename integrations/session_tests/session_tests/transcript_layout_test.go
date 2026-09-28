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
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/checkpoint"
	"github.com/vogo/vv/sessionlogs"
	"github.com/vogo/vv/setup"
)

// TestTranscriptLayout_EndToEnd drives the wired-up stack the way the
// ReAct loop does — full message slice on every iteration, plus a
// delegated sub-agent — and asserts the resulting on-disk shape.
//
// The numbers here are the point of the redesign: the same conversation
// used to cost one full copy of the message list per iteration.
func TestTranscriptLayout_EndToEnd(t *testing.T) {
	cfg := newTestConfig(t)

	res, err := setup.Init(cfg, nil)
	if err != nil {
		t.Fatalf("setup.Init: %v", err)
	}
	defer res.Shutdown(context.Background())

	store := res.TranscriptStore()
	if store == nil {
		t.Fatal("expected a transcript store with the session subsystem on")
	}

	ctx := context.Background()
	const sid = "layout-e2e"

	const systemMarker = "SYSTEM-PROMPT-SENTINEL"

	systemPrompt := schema.NewSystemMessage(schema.ProtocolOpenAIChat,
		systemMarker+" "+strings.Repeat("you are a coding agent. ", 100))
	userTurn := schema.NewUserMessage(schema.ProtocolOpenAIChat, "rename CLAUDE.md to AGENTS.md")

	// Six iterations, each re-submitting everything plus one new message.
	msgs := []schema.Message{systemPrompt, userTurn}

	for i := range 6 {
		msgs = append(msgs, schema.NewTextMessage(schema.ProtocolOpenAIChat, schema.RoleAssistant, "step "+string(rune('a'+i))))

		cp := &checkpoint.Checkpoint{
			SessionID: sid,
			AgentID:   "primary",
			Iteration: i,
			Final:     i == 5,
			Messages:  append([]schema.Message{}, msgs...),
		}
		if cp.Final {
			cp.StopReason = schema.StopReasonComplete
		}

		if err := store.Save(ctx, cp); err != nil {
			t.Fatalf("save iteration %d: %v", i, err)
		}
	}

	// One delegated dispatch, as the delegate_to_<agent> tool performs it.
	runCtx := setupRunContext(ctx, "fix the imports")
	if err := store.Save(runCtx, &checkpoint.Checkpoint{
		SessionID: sid,
		AgentID:   "coder",
		Final:     true,
		Messages: []schema.Message{
			schema.NewUserMessage(schema.ProtocolOpenAIChat, "fix the imports"),
			schema.NewTextMessage(schema.ProtocolOpenAIChat, schema.RoleAssistant, "done"),
		},
	}); err != nil {
		t.Fatalf("save dispatch: %v", err)
	}

	dir := filepath.Join(store.Root(), sid)

	// --- Layout ---

	for _, want := range []string{"messages.jsonl", "subagents/coder-1.jsonl"} {
		if _, err := os.Stat(filepath.Join(dir, filepath.FromSlash(want))); err != nil {
			t.Errorf("expected %s: %v", want, err)
		}
	}

	if _, err := os.Stat(filepath.Join(dir, "checkpoints")); !os.IsNotExist(err) {
		t.Errorf("the per-iteration snapshot directory must be gone (err=%v)", err)
	}

	// --- Deduplication ---

	raw, err := os.ReadFile(filepath.Join(dir, "messages.jsonl"))
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}

	// 1 system + 1 user + 6 assistant = 8 distinct bodies, 6 checkpoints,
	// 1 dispatch pointer.
	if got := strings.Count(string(raw), `"k":"msg"`); got != 8 {
		t.Errorf("msg lines = %d, want 8 (one per distinct body)", got)
	}

	if got := strings.Count(string(raw), `"k":"ckpt"`); got != 6 {
		t.Errorf("ckpt lines = %d, want 6", got)
	}

	if got := strings.Count(string(raw), `"k":"subagent"`); got != 1 {
		t.Errorf("subagent pointer lines = %d, want 1", got)
	}

	// The 2.4 KiB system prompt is the whole story: the old store wrote it
	// once per iteration.
	if got := strings.Count(string(raw), systemMarker); got != 1 {
		t.Errorf("system prompt stored %d times, want 1", got)
	}

	// --- Resume timeline ---

	cp, err := store.Load(ctx, sid, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cp.AgentID != "primary" {
		t.Errorf("resume timeline agent = %q; a dispatch leaked into it", cp.AgentID)
	}

	if len(cp.Messages) != 8 || cp.Messages[1].Text() != "rename CLAUDE.md to AGENTS.md" {
		t.Errorf("conversation did not round-trip: %d messages", len(cp.Messages))
	}

	// --- Dispatch discovery ---

	runs, err := store.ListRuns(ctx, sid)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}

	if len(runs) != 1 || runs[0].AgentID != "coder" || runs[0].Task != "fix the imports" {
		t.Fatalf("dispatch listing = %+v", runs)
	}

	// --- Delete stays a single recursive removal (SESS-R1) ---

	if err := res.SessionStore.Delete(ctx, sid); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("session directory survived Delete (err=%v)", err)
	}
}

// TestEventLog_ExcludesPayloadsOwnedElsewhere proves the default event
// whitelist keeps the duplicated payloads out of events.jsonl.
func TestEventLog_ExcludesPayloadsOwnedElsewhere(t *testing.T) {
	cfg := newTestConfig(t)

	res, err := setup.Init(cfg, nil)
	if err != nil {
		t.Fatalf("setup.Init: %v", err)
	}
	defer res.Shutdown(context.Background())

	const sid = "filter-e2e"
	mgr := res.SetupResult.HookManager
	ctx := context.Background()

	// One event that must survive, three that must not.
	mgr.Dispatch(ctx, schema.NewEvent(schema.EventAgentStart, "primary", sid, schema.AgentStartData{}))
	mgr.Dispatch(ctx, schema.NewEvent(schema.EventTextDelta, "primary", sid, schema.TextDeltaData{Delta: "chunk"}))
	mgr.Dispatch(ctx, schema.NewEvent(schema.EventToolResult, "primary", sid, schema.ToolResultData{
		ToolCallID: "c1", ToolName: "bash", Result: schema.TextResult("c1", strings.Repeat("z", 4096)),
	}))
	mgr.Dispatch(ctx, schema.NewEvent(schema.EventContextBuilt, "primary", sid, schema.ContextBuiltData{Builder: "default"}))

	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if events, err := res.SessionStore.ListEvents(ctx, sid); err == nil && len(events) >= 1 {
			break
		}

		time.Sleep(20 * time.Millisecond)
	}

	stopCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	res.Shutdown(stopCtx)
	cancel()

	events, err := res.SessionStore.ListEvents(ctx, sid)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}

	if len(events) != 1 || events[0].Type != schema.EventAgentStart {
		types := make([]string, 0, len(events))
		for _, e := range events {
			types = append(types, e.Type)
		}

		t.Fatalf("event log = %v, want only [agent_start]: text_delta / tool_result / context_built are owned by other files", types)
	}
}

// setupRunContext tags a dispatch the way the delegate tool does.
func setupRunContext(ctx context.Context, task string) context.Context {
	return sessionlogs.WithRun(ctx, sessionlogs.Run{Key: sessionlogs.NewRunKey(), Task: task})
}
