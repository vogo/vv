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

package cli

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/checkpoint"
	"github.com/vogo/vage/session"
	"github.com/vogo/vv/agents"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/setup"
)

// resumeStubChat is a minimal aimodel.ChatCompleter that returns a single
// canned response. It supports the happy-path resume test where Resume
// must drive the LLM at least once to reach a terminal stop.
type resumeStubChat struct {
	text string
}

func (s *resumeStubChat) ChatCompletion(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.ChatResponse, error) {
	return &aimodel.ChatResponse{
		Choices: []aimodel.Choice{
			{
				Message:      aimodel.Message{Role: aimodel.RoleAssistant, Content: aimodel.NewTextContent(s.text)},
				FinishReason: aimodel.FinishReasonStop,
			},
		},
		Usage: aimodel.Usage{PromptTokens: 7, CompletionTokens: 3, TotalTokens: 10},
	}, nil
}

func (s *resumeStubChat) ChatCompletionStream(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.Stream, error) {
	return nil, errors.New("resumeStubChat: streaming not supported")
}

// TestRunResume_NilInitResult guards the nil-deref shape — main.go is
// the canonical caller but library users should also see a clean error.
func TestRunResume_NilInitResult(t *testing.T) {
	err := RunResume(context.Background(), nil, "sid", &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil {
		t.Fatal("expected error for nil InitResult")
	}
}

// TestRunResume_SessionDisabled fires when the user enabled --resume but
// turned off the session subsystem — checkpoint resume requires a stable
// session id that only the session subsystem provides.
func TestRunResume_SessionDisabled(t *testing.T) {
	ir := &setup.InitResult{} // no SessionStore, no IterationStore
	err := RunResume(context.Background(), ir, "sid", &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, setup.ErrSessionDisabled) {
		t.Fatalf("err = %v, want setup.ErrSessionDisabled", err)
	}
}

// TestRunResume_NoIterationStore catches a setup-wiring regression where
// session is on but the checkpoint backend was somehow dropped.
func TestRunResume_NoIterationStore(t *testing.T) {
	ir := &setup.InitResult{
		SessionStore: session.NewMapSessionStore(),
		// IterationStore intentionally nil
	}
	err := RunResume(context.Background(), ir, "sid", &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, setup.ErrNoIterationStore) {
		t.Fatalf("err = %v, want setup.ErrNoIterationStore", err)
	}
}

// TestRunResume_EmptySessionID rejects the obvious user-error of running
// `vv --resume ""`.
func TestRunResume_EmptySessionID(t *testing.T) {
	ir := &setup.InitResult{
		SessionStore:   session.NewMapSessionStore(),
		IterationStore: checkpoint.NewMapIterationStore(),
	}
	err := RunResume(context.Background(), ir, "", &bytes.Buffer{}, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "non-empty session id") {
		t.Fatalf("err = %v, want non-empty-session-id error", err)
	}
}

// TestRunResume_CheckpointNotFound surfaces a friendly hint that includes
// the original errors.Is chain so callers can branch programmatically.
func TestRunResume_CheckpointNotFound(t *testing.T) {
	ir := &setup.InitResult{
		SessionStore:   session.NewMapSessionStore(),
		IterationStore: checkpoint.NewMapIterationStore(),
	}
	err := RunResume(context.Background(), ir, "ghost-session", &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, checkpoint.ErrCheckpointNotFound) {
		t.Fatalf("err = %v, want chain to include ErrCheckpointNotFound", err)
	}
	if !strings.Contains(err.Error(), "no checkpoints found") {
		t.Fatalf("err message lacks user guidance: %v", err)
	}
}

// TestRunResume_FinalCheckpoint trips the pre-Resume guard that a final
// checkpoint cannot be re-driven; the message tells the user the next
// move (start a new session — fork is not yet implemented).
func TestRunResume_FinalCheckpoint(t *testing.T) {
	store := checkpoint.NewMapIterationStore()
	cp := &checkpoint.Checkpoint{
		SessionID: "sid-final",
		AgentID:   agents.PrimaryAgentID,
		Final:     true,
	}
	if err := store.Save(context.Background(), cp); err != nil {
		t.Fatalf("Save: %v", err)
	}

	ir := &setup.InitResult{
		SessionStore:   session.NewMapSessionStore(),
		IterationStore: store,
	}

	err := RunResume(context.Background(), ir, "sid-final", &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, checkpoint.ErrAlreadyFinal) {
		t.Fatalf("err = %v, want chain to include ErrAlreadyFinal", err)
	}
	if !strings.Contains(err.Error(), "already finalized") {
		t.Fatalf("err message lacks 'already finalized' guidance: %v", err)
	}
}

// TestRunResume_AgentNotFound triggers when the checkpoint references an
// agent id no longer in the registry — happens after agent rename or
// removal between writes and the resume call.
func TestRunResume_AgentNotFound(t *testing.T) {
	store := checkpoint.NewMapIterationStore()
	cp := &checkpoint.Checkpoint{
		SessionID: "sid-ghost-agent",
		AgentID:   "ghost-agent-id",
	}
	if err := store.Save(context.Background(), cp); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Build a real Result via setup.New so Result.Agent returns the
	// regular nil-on-miss for unknown ids.
	mock := &resumeStubChat{text: "ok"}
	cfg := minimalCfgForResume()
	result, err := setup.New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("setup.New: %v", err)
	}

	ir := &setup.InitResult{
		SessionStore:   session.NewMapSessionStore(),
		IterationStore: store,
		SetupResult:    result,
	}

	err = RunResume(context.Background(), ir, "sid-ghost-agent", &bytes.Buffer{}, &bytes.Buffer{})
	if !errors.Is(err, setup.ErrAgentNotFound) {
		t.Fatalf("err = %v, want setup.ErrAgentNotFound", err)
	}
}

// TestRunResume_PrimaryHappyPath drives the full resume flow with a real
// *taskagent.Agent for the Primary id and a stub LLM that returns a
// terminal message immediately. Verifies that:
//  1. the Primary on the dispatcher is correctly resolved via AgentID
//     "primary" (which is NOT in the dispatchable subAgents map);
//  2. Resume actually runs and the final assistant text reaches stdout;
//  3. the [done] summary line lands on stderr.
func TestRunResume_PrimaryHappyPath(t *testing.T) {
	store := checkpoint.NewMapIterationStore()

	// Seed a non-final checkpoint that points to the Primary.
	cp := &checkpoint.Checkpoint{
		SessionID: "sid-primary-resume",
		AgentID:   agents.PrimaryAgentID,
		Iteration: 0,
		Messages: []aimodel.Message{
			{Role: aimodel.RoleSystem, Content: aimodel.NewTextContent("you are vv")},
			{Role: aimodel.RoleUser, Content: aimodel.NewTextContent("continue")},
		},
	}
	if err := store.Save(context.Background(), cp); err != nil {
		t.Fatalf("Save: %v", err)
	}

	// Construct a TaskAgent that *is* the Primary — same id so the
	// AgentID match in Resume() succeeds.
	stub := &resumeStubChat{text: "resumed final answer"}
	primary := taskagent.New(
		agent.Config{ID: agents.PrimaryAgentID, Name: "Primary"},
		taskagent.WithChatCompleter(stub),
		taskagent.WithModel("test-model"),
		taskagent.WithMaxIterations(2),
		taskagent.WithIterationStore(store),
	)

	mock := &resumeStubChat{text: "unused"}
	cfg := minimalCfgForResume()
	result, err := setup.New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("setup.New: %v", err)
	}
	// Replace the dispatcher's Primary with our test instance — setup.New
	// installed a generic one that does not see our seeded checkpoints.
	result.Dispatcher.SetPrimaryAssistant(primary)

	ir := &setup.InitResult{
		SessionStore:   session.NewMapSessionStore(),
		IterationStore: store,
		SetupResult:    result,
	}

	var stdout, stderr bytes.Buffer
	if err := RunResume(context.Background(), ir, "sid-primary-resume", &stdout, &stderr); err != nil {
		t.Fatalf("RunResume: %v", err)
	}

	if got := stdout.String(); !strings.Contains(got, "resumed final answer") {
		t.Errorf("stdout missing final answer: %q", got)
	}
	if got := stderr.String(); !strings.Contains(got, "[resume]") || !strings.Contains(got, "[done]") {
		t.Errorf("stderr missing resume/done banners: %q", got)
	}
}

// TestResolveResumeAgent_PrimaryWithoutDispatcher verifies the defensive
// nil checks in the resolve method. A nil SetupResult or nil Dispatcher
// must surface as ErrAgentNotFound rather than panic.
func TestResolveResumeAgent_PrimaryWithoutDispatcher(t *testing.T) {
	cases := []struct {
		name string
		ir   *setup.InitResult
	}{
		{"nil setup result", &setup.InitResult{}},
		{"setup result with nil dispatcher", &setup.InitResult{SetupResult: &setup.Result{}}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := c.ir.ResumeAgent(agents.PrimaryAgentID)
			if !errors.Is(err, setup.ErrAgentNotFound) {
				t.Fatalf("err = %v, want setup.ErrAgentNotFound", err)
			}
		})
	}
}

// minimalCfgForResume builds the smallest cfg that setup.New accepts —
// mirrors TestNew_AllAgentsCreated in setup_test.go so the resume tests
// stay aligned with the supported construction surface.
func minimalCfgForResume() *configs.Config {
	return &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 4},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}
}
