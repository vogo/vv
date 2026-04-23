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

// Package tracelog_tests contains integration tests that exercise the full
// setup.Init → TaskAgent.Run → JSONL flush pipeline for the P1-5
// "Structured JSONL conversation trace logging" feature.
//
// These tests never touch the network: the ChatCompleter is a deterministic
// stub that returns a single non-tool-call assistant message so the driven
// TaskAgent emits exactly one AgentStart + AgentEnd pair per run.
package tracelog_tests

import (
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/hook"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/setup"
	"github.com/vogo/vv/traces/tracelog"
)

// stubCompleter is a deterministic aimodel.ChatCompleter that returns a
// fixed assistant response on every call and counts invocations atomically.
// Mirrors budget_tests.stubCompleter so the two packages share test style.
type stubCompleter struct {
	calls atomic.Int64
	text  string
}

func (s *stubCompleter) ChatCompletion(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.ChatResponse, error) {
	s.calls.Add(1)

	msg := aimodel.Message{Role: aimodel.RoleAssistant, Content: aimodel.NewTextContent(s.text)}

	return &aimodel.ChatResponse{
		ID:    "stub",
		Model: "stub-model",
		Choices: []aimodel.Choice{{
			Index:        0,
			Message:      msg,
			FinishReason: aimodel.FinishReasonStop,
		}},
		Usage: aimodel.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}, nil
}

func (s *stubCompleter) ChatCompletionStream(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.Stream, error) {
	s.calls.Add(1)
	return nil, nil
}

// decodedEvent mirrors tracelog_test.decodedEvent — schema.Event.Data is a
// sealed interface so we decode the on-disk JSONL lines into a generic shape.
type decodedEvent struct {
	Type      string          `json:"type"`
	AgentID   string          `json:"agent_id,omitempty"`
	SessionID string          `json:"session_id,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data,omitempty"`
}

// readLines returns the newline-delimited contents of a JSONL file.
func readLines(t *testing.T, path string) []string {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %q: %v", path, err)
	}

	defer func() { _ = f.Close() }()

	var lines []string

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}

	if err := sc.Err(); err != nil {
		t.Fatalf("scan %q: %v", path, err)
	}

	return lines
}

// decodeEvents reads and decodes every JSONL line at path, asserting each
// line is valid JSON with the required envelope fields (type, timestamp,
// session_id). This is the central US-1 assertion.
func decodeEvents(t *testing.T, path string) []decodedEvent {
	t.Helper()

	lines := readLines(t, path)
	events := make([]decodedEvent, 0, len(lines))

	for i, line := range lines {
		var ev decodedEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("line %d of %s not valid JSON: %v (%q)", i, path, err, line)
		}

		if ev.Type == "" {
			t.Fatalf("line %d of %s has empty type: %q", i, path, line)
		}

		if ev.Timestamp.IsZero() {
			t.Fatalf("line %d of %s has zero timestamp: %q", i, path, line)
		}

		if ev.SessionID == "" {
			t.Fatalf("line %d of %s has empty session_id: %q", i, path, line)
		}

		events = append(events, ev)
	}

	return events
}

// makeTraceConfig returns a base Config with Trace enabled pointing to
// dir. The config is otherwise minimal — no LLM, no memory, no tools — and
// is designed to be consumed by setup.Init, which only requires an LLM
// provider stanza. The helper sets APIKey + BaseURL so aimodel.NewClient
// does not reject the config at Init time; the underlying client is never
// invoked because the tests drive their own stub TaskAgent from the
// resulting HookManager.
func makeTraceConfig(t *testing.T, traceDir string, enabled bool) *configs.Config {
	t.Helper()

	var enabledPtr *bool
	if enabled {
		v := true
		enabledPtr = &v
	}

	// Use t.TempDir for memory + bash working dir so each test is isolated.
	workDir := t.TempDir()
	memDir := t.TempDir()

	return &configs.Config{
		LLM: configs.LLMConfig{
			Provider: "openai",
			Model:    "stub-model",
			APIKey:   "test-key-not-used",
			BaseURL:  "http://127.0.0.1:0",
		},
		Agents: configs.AgentsConfig{MaxIterations: 2},
		Memory: configs.MemoryConfig{Dir: memDir, MaxConcurrency: 1, SessionWindow: 50},
		Tools: configs.ToolsConfig{
			BashTimeout:    10,
			BashWorkingDir: workDir,
		},
		Trace: configs.TraceConfig{
			Enabled: enabledPtr,
			Dir:     traceDir,
		},
	}
}

// initWithStubAgent runs setup.Init against cfg and returns the resulting
// InitResult plus a ready-to-run TaskAgent wired to a stubCompleter and the
// process-level HookManager that Init just built. The test can Run this
// agent to produce real schema.Events that flow through the same hook
// pipeline vv uses in production — the only thing we replace is the LLM
// transport.
func initWithStubAgent(t *testing.T, cfg *configs.Config, stubResponse string) (*setup.InitResult, *taskagent.Agent, *stubCompleter) {
	t.Helper()

	initResult, err := setup.Init(cfg, nil)
	if err != nil {
		t.Fatalf("setup.Init: %v", err)
	}

	stub := &stubCompleter{text: stubResponse}

	opts := []taskagent.Option{
		taskagent.WithChatCompleter(stub),
		taskagent.WithModel(cfg.LLM.Model),
		taskagent.WithMaxIterations(1),
	}

	if mgr := initResult.SetupResult.HookManager; mgr != nil {
		opts = append(opts, taskagent.WithHookManager(mgr))
	}

	a := taskagent.New(
		agent.Config{ID: "test-agent", Name: "Test Agent", Description: "stubbed"},
		opts...,
	)

	return initResult, a, stub
}

// shutdownWithTimeout invokes the init's Shutdown closure with a bounded
// timeout so a mis-wired shutdown cannot hang the test forever.
func shutdownWithTimeout(t *testing.T, initResult *setup.InitResult) {
	t.Helper()

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	done := make(chan struct{})
	go func() {
		initResult.Shutdown(ctx)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("initResult.Shutdown did not return within 5s")
	}
}

// projectTraceDir returns the directory under baseDir where the hook writes
// session files for cfg.Tools.BashWorkingDir (matches tracelog.ProjectHash).
func projectTraceDir(baseDir, workingDir string) string {
	return filepath.Join(baseDir, tracelog.ProjectHash(workingDir))
}

// Compile-time hook.AsyncHook anchor so the import is not mistakenly
// trimmed by tooling; the hook package is also used via initResult's
// SetupResult.HookManager assertions below.
var _ hook.AsyncHook = (*tracelog.JSONLHook)(nil)
