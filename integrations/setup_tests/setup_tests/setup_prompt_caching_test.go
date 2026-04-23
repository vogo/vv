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

// End-to-end integration tests for P1-8 · Prompt caching hints.
//
// These tests verify that the full wiring (setup.New -> registries.Factory ->
// taskagent.New -> ChatCompletion) correctly threads
// cfg.Agents.EffectivePromptCaching() through to the outbound ChatRequest and
// marks the last system message + last tool with CacheBreakpoint=true (when
// enabled) or leaves them unmarked (when opted out). They complement the unit
// tests in vage/agent/taskagent (helper-level) and aimodel (translation-level)
// by proving the glue code between vv's config layer and vage's agent layer
// is intact under the default-on and explicit-opt-out paths, including the
// non-dispatchable explorer factory which is built out-of-band in setup.New.
package setup_tests

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/agents"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/registries"
	"github.com/vogo/vv/setup"
)

// recordingMockCompleter captures every ChatRequest it sees and replies with
// a pre-queued sequence of ChatResponses. A local copy (vs reusing the one
// in setup_parallel_tools_test.go) keeps this file self-contained and avoids
// coupling to an unrelated integration test's mock semantics.
type recordingMockCompleter struct {
	mu        sync.Mutex
	responses []*aimodel.ChatResponse
	requests  []*aimodel.ChatRequest
	idx       int
}

func (m *recordingMockCompleter) ChatCompletion(_ context.Context, req *aimodel.ChatRequest) (*aimodel.ChatResponse, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	// Deep-copy the request header so later mutations by the agent (appending
	// assistant / tool messages across ReAct iterations) don't retroactively
	// rewrite what we "saw" at call time. Messages and Tools share slice
	// headers but capture the underlying element values via a copy so the
	// CacheBreakpoint bool we assert on is the one emitted for *this* call.
	msgs := make([]aimodel.Message, len(req.Messages))
	copy(msgs, req.Messages)

	tools := make([]aimodel.Tool, len(req.Tools))
	copy(tools, req.Tools)

	captured := *req
	captured.Messages = msgs
	captured.Tools = tools
	m.requests = append(m.requests, &captured)

	if m.idx >= len(m.responses) {
		return nil, errors.New("mock: no more responses queued")
	}
	resp := m.responses[m.idx]
	m.idx++
	return resp, nil
}

func (m *recordingMockCompleter) ChatCompletionStream(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.Stream, error) {
	return nil, errors.New("mock: stream not supported")
}

// stopRespPromptCaching returns a finish-reason=stop assistant message so the
// ReAct loop exits after the first LLM call.
func stopRespPromptCaching(text string) *aimodel.ChatResponse {
	return &aimodel.ChatResponse{
		Choices: []aimodel.Choice{{
			Message:      aimodel.Message{Role: aimodel.RoleAssistant, Content: aimodel.NewTextContent(text)},
			FinishReason: aimodel.FinishReasonStop,
		}},
		Usage: aimodel.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
	}
}

// assertExactlyOneSystemMarked fails the test if the request does not contain
// exactly one system message with CacheBreakpoint=true. Used by the default-on
// integration assertions.
func assertExactlyOneSystemMarked(t *testing.T, req *aimodel.ChatRequest) {
	t.Helper()

	marked := 0
	for _, m := range req.Messages {
		if m.Role == aimodel.RoleSystem && m.CacheBreakpoint {
			marked++
		}
	}
	if marked != 1 {
		t.Errorf("marked system messages = %d, want exactly 1", marked)
	}
}

// assertLastToolMarked fails the test if the outbound request has no tools or
// if the last tool's CacheBreakpoint is false.
func assertLastToolMarked(t *testing.T, req *aimodel.ChatRequest) {
	t.Helper()

	if len(req.Tools) == 0 {
		t.Fatal("expected at least one tool in outbound request")
	}
	last := req.Tools[len(req.Tools)-1]
	if !last.CacheBreakpoint {
		t.Errorf("last tool %q CacheBreakpoint=false, want true", last.Function.Name)
	}
	// Earlier tools should NOT be marked — design §3.2 places the breakpoint
	// only at the tail to avoid burning Anthropic's 4-breakpoint cap.
	for i := 0; i < len(req.Tools)-1; i++ {
		if req.Tools[i].CacheBreakpoint {
			t.Errorf("tools[%d] (%q) unexpectedly marked", i, req.Tools[i].Function.Name)
		}
	}
}

// assertNoCacheMarkers fails the test if any message or tool in the outbound
// request carries CacheBreakpoint=true. Used by the opt-out integration
// assertions.
func assertNoCacheMarkers(t *testing.T, req *aimodel.ChatRequest) {
	t.Helper()

	for i, m := range req.Messages {
		if m.CacheBreakpoint {
			t.Errorf("messages[%d] (role=%s) unexpectedly marked with CacheBreakpoint", i, m.Role)
		}
	}
	for i, tl := range req.Tools {
		if tl.CacheBreakpoint {
			t.Errorf("tools[%d] (%q) unexpectedly marked with CacheBreakpoint", i, tl.Function.Name)
		}
	}
}

// baseCfgForCachingTests constructs a minimal *configs.Config suitable for
// setup.New: test model, no path guard (AllowedDirs nil), local BashTimeout.
// The `prompt_caching` knob is left for the caller to set.
func baseCfgForCachingTests() *configs.Config {
	return &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 3},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}
}

// --- Test: default-on end-to-end via setup.New (coder) ---
// Verifies AC-1.1 and US-1 end-to-end: with no explicit cfg.Agents.PromptCaching
// (nil-default-on), the coder agent built through setup.New produces an
// outbound ChatRequest whose last system message is marked and whose last
// tool in the tool-array is marked. This is the gap the unit test at the
// taskagent layer did not exercise — it proves the vv config resolver ->
// registries.FactoryOptions.PromptCaching -> coder factory ->
// taskagent.WithPromptCaching plumbing is intact under the default path.
// Test cases:
//   - cfg.Agents.PromptCaching is nil on entry (default)
//   - EffectivePromptCaching() resolves to true
//   - coder agent Run produces a ChatRequest with exactly one system message
//     marked CacheBreakpoint=true
//   - The last tool in the ChatRequest.Tools slice carries CacheBreakpoint=true
//   - Non-tail tools are untouched
func TestIntegration_SetupNew_PromptCaching_DefaultOn_Coder(t *testing.T) {
	cfg := baseCfgForCachingTests()
	if cfg.Agents.PromptCaching != nil {
		t.Fatalf("baseline cfg should leave PromptCaching nil (default), got %v", *cfg.Agents.PromptCaching)
	}
	if !cfg.Agents.EffectivePromptCaching() {
		t.Fatalf("EffectivePromptCaching() = false, want true (nil-default-on)")
	}

	mock := &recordingMockCompleter{
		responses: []*aimodel.ChatResponse{stopRespPromptCaching("done")},
	}

	result, err := setup.New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("setup.New: %v", err)
	}

	coderAgent := result.Agent("coder")
	if coderAgent == nil {
		t.Fatal("coder agent missing from setup result")
	}
	if _, ok := coderAgent.(*taskagent.Agent); !ok {
		t.Fatalf("coder is %T, want *taskagent.Agent", coderAgent)
	}

	_, err = coderAgent.Run(context.Background(), &schema.RunRequest{
		SessionID: "prompt-cache-default-session",
		Messages:  []schema.Message{schema.NewUserMessage("hello")},
	})
	if err != nil {
		t.Fatalf("coder.Run: %v", err)
	}

	if len(mock.requests) == 0 {
		t.Fatal("mock captured no LLM requests")
	}

	req := mock.requests[0]
	assertExactlyOneSystemMarked(t, req)
	assertLastToolMarked(t, req)
}

// --- Test: default-on end-to-end for researcher (read-only profile) ---
// Verifies the default-on plumbing reaches the researcher factory too (3
// read-only tools). Researcher carries a different ToolProfile from coder so
// a separate assertion guards against a regression that ends up specific to
// the coder factory. AC-1.1 applies symmetrically across all four tool-using
// agents — this test exercises the second of the four (coder, researcher,
// reviewer, explorer).
// Test cases:
//   - researcher agent built via setup.New carries the flag
//   - Exactly one system message marked CacheBreakpoint=true
//   - Last tool (among 3 read-only tools) marked CacheBreakpoint=true
func TestIntegration_SetupNew_PromptCaching_DefaultOn_Researcher(t *testing.T) {
	cfg := baseCfgForCachingTests()

	mock := &recordingMockCompleter{
		responses: []*aimodel.ChatResponse{stopRespPromptCaching("done")},
	}

	result, err := setup.New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("setup.New: %v", err)
	}

	researcherAgent := result.Agent("researcher")
	if researcherAgent == nil {
		t.Fatal("researcher agent missing from setup result")
	}

	_, err = researcherAgent.Run(context.Background(), &schema.RunRequest{
		SessionID: "prompt-cache-researcher-session",
		Messages:  []schema.Message{schema.NewUserMessage("explain the project")},
	})
	if err != nil {
		t.Fatalf("researcher.Run: %v", err)
	}

	if len(mock.requests) == 0 {
		t.Fatal("mock captured no LLM requests")
	}

	req := mock.requests[0]
	assertExactlyOneSystemMarked(t, req)
	assertLastToolMarked(t, req)
	// Researcher should have exactly 3 tools (read, glob, grep).
	if got := len(req.Tools); got != 3 {
		t.Errorf("researcher tool count = %d, want 3", got)
	}
}

// --- Test: opt-out end-to-end via configs YAML -> setup.New ---
// Verifies AC-3.1 and AC-3.2: loading a YAML file with
// `agents.prompt_caching: false`, feeding the loaded *configs.Config through
// setup.New, and running the coder agent produces an outbound ChatRequest
// with no cache markers on any message or tool. Proves the end-to-end opt-out
// pipeline (YAML parse -> configs.AgentsConfig.PromptCaching=*false ->
// EffectivePromptCaching()=false -> FactoryOptions.PromptCaching=false ->
// taskagent.WithPromptCaching(false)) works as designed.
// Test cases:
//   - Config loaded from YAML has PromptCaching=*false
//   - EffectivePromptCaching() resolves to false
//   - Coder agent built via setup.New disables markers
//   - No system message marked; no tool marked
func TestIntegration_SetupNew_PromptCaching_OptOut_Coder(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "vv.yaml")
	content := `llm:
  provider: openai
  model: test-model
  api_key: test-key
  base_url: http://127.0.0.1:0
agents:
  max_iterations: 3
  prompt_caching: false
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := configs.Load(cfgPath, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}
	if cfg.Agents.PromptCaching == nil {
		t.Fatalf("expected PromptCaching=*false after YAML load, got nil")
	}
	if *cfg.Agents.PromptCaching {
		t.Fatalf("expected PromptCaching=*false, got pointer to true")
	}
	if cfg.Agents.EffectivePromptCaching() {
		t.Fatalf("EffectivePromptCaching() = true, want false")
	}

	// Provide an allowed-dirs override so setup.New's path guard doesn't reject
	// the default temp-dir list. Load() may have initialized BashWorkingDir
	// differently in each environment — setting Tools here mirrors the other
	// setup_tests and isolates this test from env drift.
	cfg.Tools.BashTimeout = 10
	// Leave AllowedDirs nil so setup.buildAllowedDirs picks default temp-dir.

	mock := &recordingMockCompleter{
		responses: []*aimodel.ChatResponse{stopRespPromptCaching("done")},
	}

	result, err := setup.New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("setup.New: %v", err)
	}

	coderAgent := result.Agent("coder")
	if coderAgent == nil {
		t.Fatal("coder agent missing from setup result")
	}

	_, err = coderAgent.Run(context.Background(), &schema.RunRequest{
		SessionID: "prompt-cache-optout-session",
		Messages:  []schema.Message{schema.NewUserMessage("hello")},
	})
	if err != nil {
		t.Fatalf("coder.Run: %v", err)
	}

	if len(mock.requests) == 0 {
		t.Fatal("mock captured no LLM requests")
	}
	assertNoCacheMarkers(t, mock.requests[0])
}

// --- Test: env override end-to-end via VV_AGENTS_PROMPT_CACHING=false ---
// Verifies AC-3.1 (env-var opt-out): even with YAML `prompt_caching: true`,
// the env override flips the flag to false, and the coder built through
// setup.New emits no markers. Closes the last gap in the "how does the knob
// reach the agent" chain — env-var parsing in configs.Load through to the
// outbound ChatRequest.
// Test cases:
//   - YAML sets prompt_caching: true
//   - Env sets VV_AGENTS_PROMPT_CACHING=false
//   - Resolved cfg carries PromptCaching=*false
//   - Coder built via setup.New emits no markers
func TestIntegration_SetupNew_PromptCaching_EnvOverride_Coder(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "vv.yaml")
	content := `llm:
  provider: openai
  model: test-model
  api_key: test-key
  base_url: http://127.0.0.1:0
agents:
  max_iterations: 3
  prompt_caching: true
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("VV_AGENTS_PROMPT_CACHING", "false")

	cfg, err := configs.Load(cfgPath, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}
	if cfg.Agents.EffectivePromptCaching() {
		t.Fatalf("env override didn't take effect: EffectivePromptCaching()=true")
	}

	cfg.Tools.BashTimeout = 10

	mock := &recordingMockCompleter{
		responses: []*aimodel.ChatResponse{stopRespPromptCaching("done")},
	}

	result, err := setup.New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("setup.New: %v", err)
	}

	coderAgent := result.Agent("coder")
	if coderAgent == nil {
		t.Fatal("coder agent missing from setup result")
	}

	_, err = coderAgent.Run(context.Background(), &schema.RunRequest{
		SessionID: "prompt-cache-env-session",
		Messages:  []schema.Message{schema.NewUserMessage("hello")},
	})
	if err != nil {
		t.Fatalf("coder.Run: %v", err)
	}

	if len(mock.requests) == 0 {
		t.Fatal("mock captured no LLM requests")
	}
	assertNoCacheMarkers(t, mock.requests[0])
}

// --- Test: explorer factory carries the flag (non-dispatchable path) ---
// The explorer agent is built out-of-band in setup.New (not via the
// Dispatchable() loop) because it is a tool-using infrastructure agent, not
// a dispatch target. setup.Result does NOT expose it, so the only way to
// verify its PromptCaching wiring end-to-end is to reach into the registry,
// grab the "explorer" AgentDescriptor, and invoke its Factory with a
// recording mock — mirroring what setup.New does internally. This closes
// the fourth factory's coverage gap (coder/researcher/reviewer/explorer).
// Test cases:
//   - explorer registered with Dispatchable=false
//   - Factory invoked with PromptCaching=true produces an agent that marks
//     system + last tool on its outbound ChatRequest
//   - Factory invoked with PromptCaching=false produces an agent that omits
//     both markers
func TestIntegration_ExplorerFactory_PromptCaching(t *testing.T) {
	reg := registries.New()
	agents.RegisterExplorer(reg)

	desc, ok := reg.Get("explorer")
	if !ok {
		t.Fatal("explorer descriptor not registered")
	}
	if desc.Dispatchable {
		t.Errorf("explorer should be non-dispatchable")
	}

	// Build a read-only tool registry the way setup.New does.
	toolReg, err := desc.ToolProfile.BuildRegistry(configs.ToolsConfig{BashTimeout: 10})
	if err != nil {
		t.Fatalf("BuildRegistry: %v", err)
	}

	// Case A: PromptCaching=true -> markers expected.
	mockOn := &recordingMockCompleter{responses: []*aimodel.ChatResponse{stopRespPromptCaching("ok")}}
	explorerOn, err := desc.Factory(registries.FactoryOptions{
		LLM:           mockOn,
		Model:         "test-model",
		ToolRegistry:  toolReg,
		MaxIterations: 3,
		PromptCaching: true,
	})
	if err != nil {
		t.Fatalf("explorer factory (on): %v", err)
	}

	_, err = explorerOn.Run(context.Background(), &schema.RunRequest{
		SessionID: "explorer-cache-on",
		Messages:  []schema.Message{schema.NewUserMessage("explore")},
	})
	if err != nil {
		t.Fatalf("explorer.Run (on): %v", err)
	}
	if len(mockOn.requests) == 0 {
		t.Fatal("explorer (on) issued no LLM calls")
	}
	assertExactlyOneSystemMarked(t, mockOn.requests[0])
	assertLastToolMarked(t, mockOn.requests[0])

	// Case B: PromptCaching=false -> no markers.
	// Rebuild the tool registry because taskagent consumes the same one.
	toolReg2, err := desc.ToolProfile.BuildRegistry(configs.ToolsConfig{BashTimeout: 10})
	if err != nil {
		t.Fatalf("BuildRegistry (off): %v", err)
	}
	mockOff := &recordingMockCompleter{responses: []*aimodel.ChatResponse{stopRespPromptCaching("ok")}}
	explorerOff, err := desc.Factory(registries.FactoryOptions{
		LLM:           mockOff,
		Model:         "test-model",
		ToolRegistry:  toolReg2,
		MaxIterations: 3,
		PromptCaching: false,
	})
	if err != nil {
		t.Fatalf("explorer factory (off): %v", err)
	}

	_, err = explorerOff.Run(context.Background(), &schema.RunRequest{
		SessionID: "explorer-cache-off",
		Messages:  []schema.Message{schema.NewUserMessage("explore")},
	})
	if err != nil {
		t.Fatalf("explorer.Run (off): %v", err)
	}
	if len(mockOff.requests) == 0 {
		t.Fatal("explorer (off) issued no LLM calls")
	}
	assertNoCacheMarkers(t, mockOff.requests[0])

	// Guard against a field-name leak: the canonical ChatRequest JSON (what
	// an OpenAI-compatible endpoint would see) must not contain the Go field
	// name "CacheBreakpoint" anywhere, even when the field is set to true.
	// This mirrors aimodel's TestChatRequest_OpenAIShape_NoCacheControl at
	// the vv integration layer — it catches accidental jsonification regressions
	// introduced by refactors far downstream from schema.go.
	body, err := json.Marshal(mockOn.requests[0])
	if err != nil {
		t.Fatalf("marshal captured request: %v", err)
	}
	if strings.Contains(string(body), "CacheBreakpoint") {
		t.Errorf("CacheBreakpoint leaked into canonical request JSON: %s", body)
	}
	// cache_control is the Anthropic-side translated key; it lives on the
	// Anthropic request struct, never on the canonical OpenAI-shape body.
	// Its presence here would mean someone added `json:"cache_control,..."`
	// to the canonical schema by mistake.
	if strings.Contains(string(body), "cache_control") {
		t.Errorf("cache_control leaked into canonical request JSON: %s", body)
	}
}

