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

package sessionlogs

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/checkpoint"
)

const testSession = "sess-1"

func newTestStore(t *testing.T, opts ...Option) *Store {
	t.Helper()

	s, err := New(t.TempDir(), opts...)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	return s
}

func msg(text string) schema.Message {
	return schema.NewUserMessage(schema.ProtocolOpenAIChat, text)
}

func assistant(text string) schema.Message {
	return schema.NewTextMessage(schema.ProtocolOpenAIChat, schema.RoleAssistant, text)
}

func toolMessage(text string) schema.Message {
	return schema.NewToolResultMessage(schema.ProtocolOpenAIChat, "call-1", text, false)
}

func save(t *testing.T, s *Store, ctx context.Context, cp *checkpoint.Checkpoint) *checkpoint.Checkpoint {
	t.Helper()

	if err := s.Save(ctx, cp); err != nil {
		t.Fatalf("Save: %v", err)
	}

	return cp
}

func mainLogPath(s *Store, sessionID string) string {
	return filepath.Join(s.Root(), sessionID, mainLogName)
}

func TestSave_DeduplicatesMessageBodiesAcrossIterations(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	// A realistic ReAct chain: every iteration re-submits the whole
	// conversation plus one new message. The old store wrote the whole
	// slice each time; this one must write each body once.
	base := []schema.Message{msg("system prompt that is fairly long"), msg("first user turn")}

	for i := 1; i <= 4; i++ {
		msgs := append([]schema.Message{}, base...)
		for j := 0; j < i; j++ {
			msgs = append(msgs, assistant("step "+string(rune('a'+j))))
		}

		save(t, s, ctx, &checkpoint.Checkpoint{
			SessionID: testSession,
			AgentID:   DefaultPrimaryAgentID,
			Iteration: i - 1,
			Messages:  msgs,
		})
	}

	raw, err := os.ReadFile(mainLogPath(s, testSession))
	if err != nil {
		t.Fatalf("read transcript: %v", err)
	}

	// 2 base + 4 assistant bodies = 6 msg lines, plus 4 ckpt lines.
	if got, want := strings.Count(string(raw), `"k":"msg"`), 6; got != want {
		t.Errorf("msg lines = %d, want %d (each distinct body exactly once)", got, want)
	}

	if got, want := strings.Count(string(raw), `"k":"ckpt"`), 4; got != want {
		t.Errorf("ckpt lines = %d, want %d", got, want)
	}

	// The long system prompt must appear once, not once per iteration.
	if got := strings.Count(string(raw), "system prompt that is fairly long"); got != 1 {
		t.Errorf("system prompt stored %d times, want 1", got)
	}
}

func TestLoad_RoundTripsLatestCheckpoint(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	msgs := []schema.Message{msg("hello"), assistant("hi there")}

	saved := save(t, s, ctx, &checkpoint.Checkpoint{
		SessionID:       testSession,
		AgentID:         DefaultPrimaryAgentID,
		Iteration:       2,
		Final:           true,
		StopReason:      schema.StopReasonComplete,
		Messages:        msgs,
		SessionMsgCount: 7,
		Usage:           schema.Usage{PromptTokens: 100, CompletionTokens: 20, TotalTokens: 120},
		Estimated:       true,
	})

	if saved.Sequence != 1 {
		t.Errorf("Sequence = %d, want 1", saved.Sequence)
	}

	if saved.ID == "" || saved.CreatedAt.IsZero() {
		t.Errorf("Save must populate ID and CreatedAt, got ID=%q CreatedAt=%v", saved.ID, saved.CreatedAt)
	}

	got, err := s.Load(ctx, testSession, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.ID != saved.ID || got.Sequence != saved.Sequence {
		t.Errorf("identity mismatch: got (%s,%d) want (%s,%d)", got.ID, got.Sequence, saved.ID, saved.Sequence)
	}

	if got.Iteration != 2 || !got.Final || got.StopReason != schema.StopReasonComplete {
		t.Errorf("loop position lost: iter=%d final=%v stop=%q", got.Iteration, got.Final, got.StopReason)
	}

	if got.SessionMsgCount != 7 || !got.Estimated {
		t.Errorf("SessionMsgCount=%d Estimated=%v, want 7/true", got.SessionMsgCount, got.Estimated)
	}

	if got.Usage.TotalTokens != 120 || got.Usage.PromptTokens != 100 {
		t.Errorf("usage lost: %+v", got.Usage)
	}

	if len(got.Messages) != 2 {
		t.Fatalf("messages = %d, want 2", len(got.Messages))
	}

	if got.Messages[0].Text() != "hello" || got.Messages[1].Text() != "hi there" {
		t.Errorf("message order/content lost: %q, %q", got.Messages[0].Text(), got.Messages[1].Text())
	}
}

func TestLoad_ByIDAndUnknownID(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	first := save(t, s, ctx, &checkpoint.Checkpoint{
		SessionID: testSession, AgentID: DefaultPrimaryAgentID, Messages: []schema.Message{msg("one")},
	})
	save(t, s, ctx, &checkpoint.Checkpoint{
		SessionID: testSession, AgentID: DefaultPrimaryAgentID, Messages: []schema.Message{msg("one"), msg("two")},
	})

	got, err := s.Load(ctx, testSession, first.ID)
	if err != nil {
		t.Fatalf("Load by id: %v", err)
	}

	if len(got.Messages) != 1 {
		t.Errorf("loading an older checkpoint must not return the newer message list, got %d messages", len(got.Messages))
	}

	if _, err := s.Load(ctx, testSession, "nope"); !errors.Is(err, checkpoint.ErrCheckpointNotFound) {
		t.Errorf("Load(unknown id) error = %v, want ErrCheckpointNotFound", err)
	}
}

func TestLoad_MissingSessionReportsNotFound(t *testing.T) {
	s := newTestStore(t)

	if _, err := s.Load(context.Background(), "absent", ""); !errors.Is(err, checkpoint.ErrCheckpointNotFound) {
		t.Errorf("error = %v, want ErrCheckpointNotFound", err)
	}
}

func TestSave_RejectsEmptySessionID(t *testing.T) {
	s := newTestStore(t)

	err := s.Save(context.Background(), &checkpoint.Checkpoint{Messages: []schema.Message{msg("x")}})
	if !errors.Is(err, checkpoint.ErrInvalidArgument) {
		t.Errorf("error = %v, want ErrInvalidArgument", err)
	}
}

func TestSave_SubAgentStaysOutOfTheMainTranscript(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	save(t, s, ctx, &checkpoint.Checkpoint{
		SessionID: testSession, AgentID: DefaultPrimaryAgentID,
		Messages: []schema.Message{msg("primary work")},
	})

	runCtx := WithRun(ctx, Run{Key: "dispatch-1", Task: "rename the file"})
	save(t, s, runCtx, &checkpoint.Checkpoint{
		SessionID: testSession, AgentID: "coder",
		Messages: []schema.Message{msg("coder work"), assistant("coder answer")},
	})

	// The session's resume timeline must still be the primary's.
	got, err := s.Load(ctx, testSession, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.AgentID != DefaultPrimaryAgentID {
		t.Fatalf("Load returned agent %q — a sub-agent checkpoint leaked into the resume timeline", got.AgentID)
	}

	if len(got.Messages) != 1 || got.Messages[0].Text() != "primary work" {
		t.Errorf("primary transcript polluted: %+v", got.Messages)
	}

	// And the dispatch must be findable.
	runPath := filepath.Join(s.Root(), testSession, subAgentsDir, "coder-1.jsonl")
	if _, err := os.Stat(runPath); err != nil {
		t.Fatalf("dispatch transcript missing at %s: %v", runPath, err)
	}

	raw, err := os.ReadFile(mainLogPath(s, testSession))
	if err != nil {
		t.Fatalf("read main transcript: %v", err)
	}

	if !strings.Contains(string(raw), `"k":"subagent"`) || !strings.Contains(string(raw), "subagents/coder-1.jsonl") {
		t.Error("main transcript must carry a pointer line to the dispatch transcript")
	}

	if !strings.Contains(string(raw), "rename the file") {
		t.Error("pointer line must record the subgoal")
	}
}

func TestSave_EachDispatchGetsItsOwnFile(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	for i, key := range []string{"d1", "d2"} {
		runCtx := WithRun(ctx, Run{Key: key})
		save(t, s, runCtx, &checkpoint.Checkpoint{
			SessionID: testSession, AgentID: "coder",
			Iteration: i,
			Messages:  []schema.Message{msg("task " + key)},
		})
	}

	for _, name := range []string{"coder-1.jsonl", "coder-2.jsonl"} {
		if _, err := os.Stat(filepath.Join(s.Root(), testSession, subAgentsDir, name)); err != nil {
			t.Errorf("expected %s: %v", name, err)
		}
	}
}

func TestSave_SubAgentWithoutRunContextStillAvoidsMainTranscript(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	// A dispatcher path that forgot to attach a Run must degrade to
	// "one file per agent", never to "pollutes the resume timeline".
	save(t, s, ctx, &checkpoint.Checkpoint{
		SessionID: testSession, AgentID: "reviewer",
		Messages: []schema.Message{msg("review")},
	})

	if _, err := os.Stat(mainLogPath(s, testSession)); err != nil {
		t.Fatalf("pointer line should have created the main transcript: %v", err)
	}

	if _, err := s.Load(ctx, testSession, ""); !errors.Is(err, checkpoint.ErrCheckpointNotFound) {
		t.Errorf("main transcript must hold no checkpoint, got %v", err)
	}

	if _, err := os.Stat(filepath.Join(s.Root(), testSession, subAgentsDir, "reviewer-1.jsonl")); err != nil {
		t.Errorf("reviewer transcript missing: %v", err)
	}
}

func TestListRunsAndLoadRun(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	runCtx := WithRun(ctx, Run{Key: "d1", Task: "fix the bug"})
	for i := range 2 {
		save(t, s, runCtx, &checkpoint.Checkpoint{
			SessionID: testSession, AgentID: "coder", Iteration: i,
			Final:      i == 1,
			StopReason: schema.StopReasonComplete,
			Messages:   []schema.Message{msg("task"), assistant("progress")},
		})
	}

	runs, err := s.ListRuns(ctx, testSession)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}

	if len(runs) != 1 {
		t.Fatalf("runs = %d, want 1", len(runs))
	}

	got := runs[0]
	if got.AgentID != "coder" || got.Run != 1 || got.Task != "fix the bug" {
		t.Errorf("run meta = %+v", got)
	}

	if got.Iterations != 2 || got.Messages != 2 || !got.Final {
		t.Errorf("run stats = iterations:%d messages:%d final:%v", got.Iterations, got.Messages, got.Final)
	}

	cp, err := s.LoadRun(ctx, testSession, "coder", 1)
	if err != nil {
		t.Fatalf("LoadRun: %v", err)
	}

	if len(cp.Messages) != 2 || cp.Messages[1].Text() != "progress" {
		t.Errorf("dispatch transcript did not round-trip: %+v", cp.Messages)
	}

	if _, err := s.LoadRun(ctx, testSession, "coder", 9); !errors.Is(err, ErrRunNotFound) {
		t.Errorf("LoadRun(missing) = %v, want ErrRunNotFound", err)
	}
}

func TestList_ReturnsMainTimelineOnly(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	for i := range 3 {
		save(t, s, ctx, &checkpoint.Checkpoint{
			SessionID: testSession, AgentID: DefaultPrimaryAgentID, Iteration: i,
			Messages: []schema.Message{msg("turn"), assistant("reply " + string(rune('a'+i)))},
		})
	}

	save(t, s, WithRun(ctx, Run{Key: "d1"}), &checkpoint.Checkpoint{
		SessionID: testSession, AgentID: "coder", Messages: []schema.Message{msg("sub")},
	})

	metas, err := s.List(ctx, testSession)
	if err != nil {
		t.Fatalf("List: %v", err)
	}

	if len(metas) != 3 {
		t.Fatalf("metas = %d, want 3 (sub-agent dispatches are not on this timeline)", len(metas))
	}

	for i, m := range metas {
		if m.Sequence != i+1 {
			t.Errorf("metas[%d].Sequence = %d, want %d", i, m.Sequence, i+1)
		}

		if m.MessagesCount != 2 {
			t.Errorf("metas[%d].MessagesCount = %d, want 2", i, m.MessagesCount)
		}
	}
}

func TestMessages_CapsToMostRecent(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	save(t, s, ctx, &checkpoint.Checkpoint{
		SessionID: testSession, AgentID: DefaultPrimaryAgentID,
		Messages: []schema.Message{msg("a"), msg("b"), msg("c"), msg("d")},
	})

	all, err := s.Messages(ctx, testSession, 0)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}

	if len(all) != 4 {
		t.Errorf("uncapped messages = %d, want 4", len(all))
	}

	capped, err := s.Messages(ctx, testSession, 2)
	if err != nil {
		t.Fatalf("Messages capped: %v", err)
	}

	if len(capped) != 2 || capped[0].Text() != "c" || capped[1].Text() != "d" {
		t.Errorf("cap must keep the most recent messages, got %d: %+v", len(capped), capped)
	}
}

func TestMessages_DoesNotOrphanToolResults(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	call := schema.NewAssistantTurn(schema.ProtocolOpenAIChat, "", "", []schema.ToolCall{{
		ID: "call-1", Name: "read", Arguments: `{}`,
	}})
	system := schema.NewSystemMessage(schema.ProtocolOpenAIChat, "instructions")

	save(t, s, ctx, &checkpoint.Checkpoint{
		SessionID: testSession, AgentID: DefaultPrimaryAgentID,
		Messages: []schema.Message{
			system, msg("old"), assistant("old reply"), msg("inspect"), call,
			toolMessage("result"), assistant("done"),
		},
	})

	got, err := s.Messages(ctx, testSession, 2)
	if err != nil {
		t.Fatalf("Messages: %v", err)
	}

	if len(got) != 4 {
		t.Fatalf("messages = %d, want system plus complete 3-message tool exchange", len(got))
	}
	if got[0].Role() != schema.RoleSystem || len(got[1].ToolCalls()) != 1 ||
		got[2].Role() != schema.RoleTool || got[3].Text() != "done" {
		t.Errorf("tool exchange was not restored intact: %+v", got)
	}
}

func TestDelete_RemovesEveryTranscript(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, WithToolResultSpillBytes(16))

	save(t, s, ctx, &checkpoint.Checkpoint{
		SessionID: testSession, AgentID: DefaultPrimaryAgentID,
		Messages: []schema.Message{msg("keep"), toolMessage(strings.Repeat("x", 4096))},
	})
	save(t, s, WithRun(ctx, Run{Key: "d1"}), &checkpoint.Checkpoint{
		SessionID: testSession, AgentID: "coder", Messages: []schema.Message{msg("sub")},
	})

	if err := s.Delete(ctx, testSession); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	for _, name := range []string{mainLogName, subAgentsDir, toolResultsDir} {
		if _, err := os.Stat(filepath.Join(s.Root(), testSession, name)); !os.IsNotExist(err) {
			t.Errorf("%s survived Delete (err=%v)", name, err)
		}
	}

	if err := s.Delete(ctx, testSession); err != nil {
		t.Errorf("Delete must be idempotent, second call: %v", err)
	}

	// A fresh Save after Delete must restart cleanly rather than resume
	// the stale in-memory dedup set.
	save(t, s, ctx, &checkpoint.Checkpoint{
		SessionID: testSession, AgentID: DefaultPrimaryAgentID,
		Messages: []schema.Message{msg("keep")},
	})

	got, err := s.Load(ctx, testSession, "")
	if err != nil {
		t.Fatalf("Load after re-save: %v", err)
	}

	if len(got.Messages) != 1 || got.Messages[0].Text() != "keep" {
		t.Errorf("re-save after delete lost the body: %+v", got.Messages)
	}
}

func TestSequenceSurvivesProcessRestart(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	first, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	save(t, first, ctx, &checkpoint.Checkpoint{
		SessionID: testSession, AgentID: DefaultPrimaryAgentID,
		Messages: []schema.Message{msg("hello")},
	})

	// New store == new process: nothing is cached in memory.
	second, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	cp := save(t, second, ctx, &checkpoint.Checkpoint{
		SessionID: testSession, AgentID: DefaultPrimaryAgentID,
		Messages: []schema.Message{msg("hello"), assistant("world")},
	})

	if cp.Sequence != 2 {
		t.Errorf("Sequence = %d, want 2 (a restart must not reuse sequences)", cp.Sequence)
	}

	raw, err := os.ReadFile(mainLogPath(second, testSession))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if got := strings.Count(string(raw), `"k":"msg"`); got != 2 {
		t.Errorf("msg lines = %d, want 2 — a restart re-wrote a body it already had", got)
	}
}

func TestSave_ConcurrentDispatchesDoNotInterleave(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Add(1)

		go func(i int) {
			defer wg.Done()

			runCtx := WithRun(ctx, Run{Key: "d" + string(rune('a'+i))})
			_ = s.Save(runCtx, &checkpoint.Checkpoint{
				SessionID: testSession, AgentID: "coder",
				Messages: []schema.Message{msg("task")},
			})
		}(i)
	}

	wg.Wait()

	runs, err := s.ListRuns(ctx, testSession)
	if err != nil {
		t.Fatalf("ListRuns: %v", err)
	}

	if len(runs) != 8 {
		t.Fatalf("runs = %d, want 8 distinct dispatch files", len(runs))
	}

	seen := make(map[int]bool)
	for _, r := range runs {
		if seen[r.Run] {
			t.Errorf("duplicate run number %d — dispatch numbering raced", r.Run)
		}

		seen[r.Run] = true
	}
}

func TestSpill_ExternalisesOversizedToolBodies(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, WithToolResultSpillBytes(256))

	big := strings.Repeat("y", 8192)

	save(t, s, ctx, &checkpoint.Checkpoint{
		SessionID: testSession, AgentID: DefaultPrimaryAgentID,
		Messages: []schema.Message{msg("run it"), toolMessage(big)},
	})

	raw, err := os.ReadFile(mainLogPath(s, testSession))
	if err != nil {
		t.Fatalf("read: %v", err)
	}

	if strings.Contains(string(raw), big) {
		t.Error("oversized tool body must not stay inline in the transcript")
	}

	if !strings.Contains(string(raw), `"spill"`) {
		t.Error("transcript must record a spill pointer")
	}

	entries, err := os.ReadDir(filepath.Join(s.Root(), testSession, toolResultsDir))
	if err != nil || len(entries) != 1 {
		t.Fatalf("expected exactly one spill file, got %v (%v)", entries, err)
	}

	// Rehydration must be invisible to the caller.
	got, err := s.Load(ctx, testSession, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if len(got.Messages) != 2 || !strings.Contains(got.Messages[1].Text(), big) {
		t.Error("spilled body did not rehydrate on Load")
	}
}

func TestSpill_LeavesSmallBodiesInline(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t, WithToolResultSpillBytes(1<<20))

	save(t, s, ctx, &checkpoint.Checkpoint{
		SessionID: testSession, AgentID: DefaultPrimaryAgentID,
		Messages: []schema.Message{toolMessage("small result")},
	})

	if _, err := os.Stat(filepath.Join(s.Root(), testSession, toolResultsDir)); !os.IsNotExist(err) {
		t.Errorf("spill directory must not be created for small bodies (err=%v)", err)
	}
}

func TestLoad_FallsBackToLegacyCheckpoints(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	// Write a session in the previous full-snapshot layout.
	legacy, err := checkpoint.NewFileIterationStore(root)
	if err != nil {
		t.Fatalf("legacy store: %v", err)
	}

	old := &checkpoint.Checkpoint{
		SessionID: testSession, AgentID: DefaultPrimaryAgentID,
		Messages: []schema.Message{msg("legacy turn")},
	}
	if err := legacy.Save(ctx, old); err != nil {
		t.Fatalf("legacy Save: %v", err)
	}

	s, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	got, err := s.Load(ctx, testSession, "")
	if err != nil {
		t.Fatalf("Load must fall back to the legacy layout: %v", err)
	}

	if len(got.Messages) != 1 || got.Messages[0].Text() != "legacy turn" {
		t.Errorf("legacy checkpoint did not load: %+v", got.Messages)
	}

	metas, err := s.List(ctx, testSession)
	if err != nil || len(metas) != 1 {
		t.Errorf("List must fall back too: %d metas, err=%v", len(metas), err)
	}

	// Once the session is written in the new format, the new transcript
	// wins — the legacy directory stops being consulted.
	save(t, s, ctx, &checkpoint.Checkpoint{
		SessionID: testSession, AgentID: DefaultPrimaryAgentID,
		Messages: []schema.Message{msg("new turn")},
	})

	got, err = s.Load(ctx, testSession, "")
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if got.Messages[0].Text() != "new turn" {
		t.Errorf("new transcript must take precedence, got %q", got.Messages[0].Text())
	}
}

func TestLoad_FallsBackWhenNewTranscriptOnlyHasSubAgentPointer(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	legacy, err := checkpoint.NewFileIterationStore(root)
	if err != nil {
		t.Fatalf("legacy store: %v", err)
	}
	if err := legacy.Save(ctx, &checkpoint.Checkpoint{
		SessionID: testSession, AgentID: DefaultPrimaryAgentID,
		Messages: []schema.Message{msg("legacy turn")},
	}); err != nil {
		t.Fatalf("legacy Save: %v", err)
	}

	s, err := New(root)
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	if _, err := s.runFile(testSession, "coder", "dispatch-1", "task"); err != nil {
		t.Fatalf("runFile: %v", err)
	}

	got, err := s.Load(ctx, testSession, "")
	if err != nil {
		t.Fatalf("pointer-only transcript must not hide legacy history: %v", err)
	}
	if len(got.Messages) != 1 || got.Messages[0].Text() != "legacy turn" {
		t.Errorf("legacy checkpoint did not load: %+v", got.Messages)
	}

	metas, err := s.List(ctx, testSession)
	if err != nil || len(metas) != 1 {
		t.Errorf("List must also fall back: %d metas, err=%v", len(metas), err)
	}
}

func TestStoreRejectsUnsafeSessionIDs(t *testing.T) {
	ctx := context.Background()
	s := newTestStore(t)

	for _, id := range []string{".", "..", "../escape", "/absolute", "has/slash"} {
		t.Run(id, func(t *testing.T) {
			if _, err := s.Load(ctx, id, ""); !errors.Is(err, checkpoint.ErrInvalidArgument) {
				t.Errorf("Load error = %v, want ErrInvalidArgument", err)
			}
			if err := s.Delete(ctx, id); !errors.Is(err, checkpoint.ErrInvalidArgument) {
				t.Errorf("Delete error = %v, want ErrInvalidArgument", err)
			}
			if err := s.Save(ctx, &checkpoint.Checkpoint{SessionID: id}); !errors.Is(err, checkpoint.ErrInvalidArgument) {
				t.Errorf("Save error = %v, want ErrInvalidArgument", err)
			}
		})
	}
}

func TestMessageID_IgnoresTimestampButKeepsContent(t *testing.T) {
	a := msg("same text")
	b := msg("same text")
	b.Timestamp = a.Timestamp.Add(time.Hour)

	idA, _, err := messageID(a)
	if err != nil {
		t.Fatalf("messageID: %v", err)
	}

	idB, _, err := messageID(b)
	if err != nil {
		t.Fatalf("messageID: %v", err)
	}

	if idA != idB {
		t.Error("identical content with different timestamps must share a content address")
	}

	idC, _, err := messageID(msg("other text"))
	if err != nil {
		t.Fatalf("messageID: %v", err)
	}

	if idA == idC {
		t.Error("different content must not collide")
	}
}

func TestSanitizeAgentID(t *testing.T) {
	cases := map[string]string{
		"coder":        "coder",
		"code_r-1":     "code_r-1",
		"a/b":          "a-b",
		"..":           "--",
		"":             "agent",
		"研究员":          "---",
		"with space":   "with-space",
		"UPPER123":     "UPPER123",
		"dots.in.name": "dots-in-name",
	}

	for in, want := range cases {
		if got := sanitizeAgentID(in); got != want {
			t.Errorf("sanitizeAgentID(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestParseRunFileName(t *testing.T) {
	agent, run := parseRunFileName("coder-3.jsonl")
	if agent != "coder" || run != 3 {
		t.Errorf("parseRunFileName = (%q,%d), want (coder,3)", agent, run)
	}

	agent, run = parseRunFileName("weird.jsonl")
	if agent != "weird" || run != 0 {
		t.Errorf("parseRunFileName(weird) = (%q,%d)", agent, run)
	}
}

func TestWithRun_EmptyKeyIsIgnored(t *testing.T) {
	ctx := context.Background()

	if got := WithRun(ctx, Run{Task: "no key"}); got != ctx {
		t.Error("WithRun with an empty key must return the context unchanged")
	}

	withRun := WithRun(ctx, Run{Key: "k", Task: "t"})

	run, ok := RunFromContext(withRun)
	if !ok || run.Key != "k" || run.Task != "t" {
		t.Errorf("RunFromContext = (%+v,%v)", run, ok)
	}

	if _, ok := RunFromContext(ctx); ok {
		t.Error("a bare context must not report a run")
	}
}
