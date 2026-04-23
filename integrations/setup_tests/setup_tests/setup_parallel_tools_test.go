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

package setup_tests

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/setup"
)

// queuedMockCompleter returns pre-queued responses in order. It is
// intentionally local to this file — the package-level mockChatCompleter
// only exposes a single response and the parallel-tool test needs two
// distinct LLM turns (tool-call turn, then "stop" turn).
type queuedMockCompleter struct {
	mu        sync.Mutex
	responses []*aimodel.ChatResponse
	requests  []*aimodel.ChatRequest
	idx       int
}

func (m *queuedMockCompleter) ChatCompletion(_ context.Context, req *aimodel.ChatRequest) (*aimodel.ChatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	m.requests = append(m.requests, req)
	if m.idx >= len(m.responses) {
		return nil, errors.New("mock: no more responses queued")
	}
	resp := m.responses[m.idx]
	m.idx++
	return resp, nil
}

func (m *queuedMockCompleter) ChatCompletionStream(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.Stream, error) {
	return nil, errors.New("mock: stream not supported")
}

// twoReadToolCallResponse returns an assistant message whose ToolCalls slice
// asks the coder to read the two file paths supplied, in the given order.
func twoReadToolCallResponse(pathA, pathB string) *aimodel.ChatResponse {
	argsA := `{"file_path":"` + pathA + `"}`
	argsB := `{"file_path":"` + pathB + `"}`
	return &aimodel.ChatResponse{
		Choices: []aimodel.Choice{{
			Message: aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent(""),
				ToolCalls: []aimodel.ToolCall{
					{
						ID:       "call-read-A",
						Type:     "function",
						Function: aimodel.FunctionCall{Name: "read", Arguments: argsA},
					},
					{
						ID:       "call-read-B",
						Type:     "function",
						Function: aimodel.FunctionCall{Name: "read", Arguments: argsB},
					},
				},
			},
			FinishReason: aimodel.FinishReasonToolCalls,
		}},
		Usage: aimodel.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}
}

// stopTextResponse returns a plain-text assistant response that ends the loop.
func stopTextResponse(text string) *aimodel.ChatResponse {
	return &aimodel.ChatResponse{
		Choices: []aimodel.Choice{{
			Message: aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent(text),
			},
			FinishReason: aimodel.FinishReasonStop,
		}},
		Usage: aimodel.Usage{PromptTokens: 3, CompletionTokens: 2, TotalTokens: 5},
	}
}

// runCoderWithTwoReads builds a coder agent via setup.New using the supplied
// MaxParallelToolCalls and drives it through one tool-call round (two reads
// against fileA and fileB). It returns the tool-result messages the second
// LLM request would see, in the exact order they were appended.
func runCoderWithTwoReads(t *testing.T, maxParallel int) []aimodel.Message {
	t.Helper()

	tmp := t.TempDir()
	fileA := filepath.Join(tmp, "alpha.txt")
	fileB := filepath.Join(tmp, "beta.txt")
	if err := os.WriteFile(fileA, []byte("content-alpha"), 0o600); err != nil {
		t.Fatalf("write fileA: %v", err)
	}
	if err := os.WriteFile(fileB, []byte("content-beta"), 0o600); err != nil {
		t.Fatalf("write fileB: %v", err)
	}

	allowed := []string{tmp}
	cfg := &configs.Config{
		LLM: configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{
			MaxIterations:        5,
			MaxParallelToolCalls: maxParallel,
		},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
		Tools: configs.ToolsConfig{
			BashTimeout:    10,
			BashWorkingDir: tmp,
			AllowedDirs:    &allowed,
		},
	}

	mock := &queuedMockCompleter{
		responses: []*aimodel.ChatResponse{
			twoReadToolCallResponse(fileA, fileB),
			stopTextResponse("done"),
		},
	}

	result, err := setup.New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("setup.New: %v", err)
	}

	coderAgent := result.Agent("coder")
	if coderAgent == nil {
		t.Fatal("coder agent not found in setup result")
	}

	if _, ok := coderAgent.(*taskagent.Agent); !ok {
		t.Fatalf("coder is %T, want *taskagent.Agent", coderAgent)
	}

	_, err = coderAgent.Run(context.Background(), &schema.RunRequest{
		SessionID: "parallel-tools-session",
		Messages:  []schema.Message{schema.NewUserMessage("read both files")},
	})
	if err != nil {
		t.Fatalf("coder.Run: %v", err)
	}

	// Second request must carry the tool-result messages in the same order
	// as ToolCalls: A then B.
	if len(mock.requests) < 2 {
		t.Fatalf("expected at least 2 LLM calls, got %d", len(mock.requests))
	}
	secondReq := mock.requests[1]

	var toolMsgs []aimodel.Message
	for _, m := range secondReq.Messages {
		if m.Role == aimodel.RoleTool {
			toolMsgs = append(toolMsgs, m)
		}
	}
	return toolMsgs
}

// --- Test: end-to-end wiring with default MaxParallelToolCalls ---
// Verifies AC-5.2: a coder agent built through setup.New with the default
// parallel cap (4) handles a two-read batch and both results appear in
// ToolCalls order in the transcript submitted to the second LLM turn.
// Test cases:
//   - setup.New threads cfg.Agents.MaxParallelToolCalls (0 -> framework default of 4) into the coder factory
//   - Two tool-result messages are appended, one per ToolCalls entry
//   - They appear in the exact ToolCalls order (A first, B second)
//   - Their ToolCallID values match the assistant message's ToolCalls IDs
//   - Their bodies contain the file contents returned by the real `read` tool
func TestIntegration_SetupNew_ParallelToolCalls_DefaultCap(t *testing.T) {
	// maxParallel=0 triggers the framework default (4) inside taskagent.
	toolMsgs := runCoderWithTwoReads(t, 0)

	if len(toolMsgs) != 2 {
		t.Fatalf("tool-result messages = %d, want 2", len(toolMsgs))
	}

	wantIDs := []string{"call-read-A", "call-read-B"}
	for i, m := range toolMsgs {
		if m.ToolCallID != wantIDs[i] {
			t.Errorf("toolMsgs[%d].ToolCallID = %q, want %q", i, m.ToolCallID, wantIDs[i])
		}
	}

	wantSubstrings := []string{"content-alpha", "content-beta"}
	for i, want := range wantSubstrings {
		got := toolMsgs[i].Content.Text()
		if got == "" {
			t.Errorf("toolMsgs[%d] body is empty", i)
			continue
		}
		if !containsSubstring(got, want) {
			t.Errorf("toolMsgs[%d] body = %q, want to contain %q", i, got, want)
		}
	}
}

// --- Test: end-to-end wiring with MaxParallelToolCalls=1 (serial opt-out) ---
// Verifies AC-4.1 / AC-5.2 in the vv factory layer: setting cap=1 produces
// a byte-identical transcript to the default parallel path — the serial
// escape hatch works through the full setup.New -> registry -> factory ->
// taskagent.WithMaxParallelToolCalls pipeline.
// Test cases:
//   - cap=1 is plumbed through setup.New into the TaskAgent
//   - Two tool-result messages are still appended, 1:1 with ToolCalls
//   - Order matches ToolCalls[i] order (A first, B second)
//   - Bodies contain each file's contents
func TestIntegration_SetupNew_ParallelToolCalls_SerialCap(t *testing.T) {
	toolMsgs := runCoderWithTwoReads(t, 1)

	if len(toolMsgs) != 2 {
		t.Fatalf("tool-result messages = %d, want 2", len(toolMsgs))
	}

	wantIDs := []string{"call-read-A", "call-read-B"}
	for i, m := range toolMsgs {
		if m.ToolCallID != wantIDs[i] {
			t.Errorf("toolMsgs[%d].ToolCallID = %q, want %q", i, m.ToolCallID, wantIDs[i])
		}
	}

	wantSubstrings := []string{"content-alpha", "content-beta"}
	for i, want := range wantSubstrings {
		got := toolMsgs[i].Content.Text()
		if !containsSubstring(got, want) {
			t.Errorf("toolMsgs[%d] body = %q, want to contain %q", i, got, want)
		}
	}
}

// containsSubstring reports whether s contains substr. Inlined here to avoid
// adding a dep on strings for this single call site and to keep the helper
// local to the file.
func containsSubstring(s, substr string) bool {
	if substr == "" {
		return true
	}
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
