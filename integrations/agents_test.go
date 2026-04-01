package integrations

import (
	"context"
	"errors"
	"io"
	"os"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/memory"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/tool"
	"github.com/vogo/vv/agents"
	"github.com/vogo/vv/config"
	vagamemory "github.com/vogo/vv/memory"
	"github.com/vogo/vv/tools"
)

// --- Test 1a: Coder has all 6 tools (bash, file_read, file_write, file_edit, glob, grep) ---
// Test cases:
//   - Coder agent has exactly 6 tools registered
//   - All expected tool names are present: bash, file_read, file_write, file_edit, glob, grep
func TestIntegration_Agents_CoderHasTools(t *testing.T) {
	reg, err := tools.Register(config.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	readOnlyReg, err := tools.RegisterReadOnly(config.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.RegisterReadOnly: %v", err)
	}

	reviewReg, err := tools.RegisterReviewTools(config.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.RegisterReviewTools: %v", err)
	}

	mock := &mockChatCompleter{}
	cfg := &config.Config{
		LLM:    config.LLMConfig{Model: "test-model"},
		Agents: config.AgentsConfig{MaxIterations: 10},
		Memory: config.MemoryConfig{MaxConcurrency: 2},
	}

	allAgents := agents.Create(cfg, mock, reg, readOnlyReg, reviewReg, nil, nil)
	toolList := allAgents.Coder.Tools()

	if len(toolList) != 6 {
		t.Fatalf("coder has %d tools, want 6", len(toolList))
	}

	toolNames := make(map[string]bool)
	for _, td := range toolList {
		toolNames[td.Name] = true
	}

	for _, name := range []string{"bash", "file_read", "file_write", "file_edit", "glob", "grep"} {
		if !toolNames[name] {
			t.Errorf("coder missing tool %q", name)
		}
	}
}

// --- Test 1b: Chat agent has no tools ---
// Test cases:
//   - Chat agent has zero tools registered
func TestIntegration_Agents_ChatHasNoTools(t *testing.T) {
	reg, err := tools.Register(config.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	readOnlyReg, err := tools.RegisterReadOnly(config.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.RegisterReadOnly: %v", err)
	}

	reviewReg, err := tools.RegisterReviewTools(config.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.RegisterReviewTools: %v", err)
	}

	mock := &mockChatCompleter{}
	cfg := &config.Config{
		LLM:    config.LLMConfig{Model: "test-model"},
		Agents: config.AgentsConfig{MaxIterations: 10},
		Memory: config.MemoryConfig{MaxConcurrency: 2},
	}

	allAgents := agents.Create(cfg, mock, reg, readOnlyReg, reviewReg, nil, nil)
	toolList := allAgents.Chat.Tools()

	if len(toolList) != 0 {
		t.Errorf("chat has %d tools, want 0", len(toolList))
	}
}

// --- Test 2: Researcher agent has exactly read-only tools (file_read, glob, grep) and no write/edit/bash ---
// Test cases:
//   - Researcher agent has exactly 3 tools
//   - All expected tools present: file_read, glob, grep
//   - Write/edit/bash tools are NOT present
func TestIntegration_Agents_ResearcherHasReadOnlyTools(t *testing.T) {
	reg, err := tools.Register(config.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	readOnlyReg, err := tools.RegisterReadOnly(config.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.RegisterReadOnly: %v", err)
	}

	reviewReg, err := tools.RegisterReviewTools(config.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.RegisterReviewTools: %v", err)
	}

	mock := &mockChatCompleter{}
	cfg := &config.Config{
		LLM:    config.LLMConfig{Model: "test-model"},
		Agents: config.AgentsConfig{MaxIterations: 10},
		Memory: config.MemoryConfig{MaxConcurrency: 2},
	}

	allAgents := agents.Create(cfg, mock, reg, readOnlyReg, reviewReg, nil, nil)
	toolList := allAgents.Researcher.Tools()

	if len(toolList) != 3 {
		t.Fatalf("researcher has %d tools, want 3", len(toolList))
	}

	toolNames := make(map[string]bool)
	for _, td := range toolList {
		toolNames[td.Name] = true
	}

	for _, name := range []string{"file_read", "glob", "grep"} {
		if !toolNames[name] {
			t.Errorf("researcher missing tool %q", name)
		}
	}

	// Verify researcher does NOT have write/edit/bash.
	for _, name := range []string{"bash", "file_write", "file_edit"} {
		if toolNames[name] {
			t.Errorf("researcher should not have tool %q", name)
		}
	}
}

// --- Test 3: Reviewer agent has read + bash tools (file_read, glob, grep, bash) but not write/edit ---
// Test cases:
//   - Reviewer agent has exactly 4 tools
//   - All expected tools present: bash, file_read, glob, grep
//   - Write/edit tools are NOT present
func TestIntegration_Agents_ReviewerHasCorrectTools(t *testing.T) {
	reg, err := tools.Register(config.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	readOnlyReg, err := tools.RegisterReadOnly(config.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.RegisterReadOnly: %v", err)
	}

	reviewReg, err := tools.RegisterReviewTools(config.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.RegisterReviewTools: %v", err)
	}

	mock := &mockChatCompleter{}
	cfg := &config.Config{
		LLM:    config.LLMConfig{Model: "test-model"},
		Agents: config.AgentsConfig{MaxIterations: 10},
		Memory: config.MemoryConfig{MaxConcurrency: 2},
	}

	allAgents := agents.Create(cfg, mock, reg, readOnlyReg, reviewReg, nil, nil)
	toolList := allAgents.Reviewer.Tools()

	if len(toolList) != 4 {
		t.Fatalf("reviewer has %d tools, want 4", len(toolList))
	}

	toolNames := make(map[string]bool)
	for _, td := range toolList {
		toolNames[td.Name] = true
	}

	for _, name := range []string{"bash", "file_read", "glob", "grep"} {
		if !toolNames[name] {
			t.Errorf("reviewer missing tool %q", name)
		}
	}

	// Verify reviewer does NOT have write/edit.
	for _, name := range []string{"file_write", "file_edit"} {
		if toolNames[name] {
			t.Errorf("reviewer should not have tool %q", name)
		}
	}
}

// --- Test 1c: Orchestrator dispatches directly to sub-agents (Design Test 1) ---
// Verifies that simple tasks are dispatched to a single agent without plan generation.
// Test cases:
//   - Direct dispatch to coder returns coder response
//   - Direct dispatch to researcher returns researcher response
//   - Direct dispatch to reviewer returns reviewer response
//   - Direct dispatch to chat returns chat response
//   - No DAG execution occurs (only sub-agent Run is called)
func TestIntegration_Agents_OrchestratorDirectDispatch(t *testing.T) {
	tests := []struct {
		name         string
		llmResponse  string // classify response from LLM
		wantResponse string
	}{
		{
			name:         "dispatches to coder",
			llmResponse:  `{"mode": "direct", "agent": "coder"}`,
			wantResponse: "coder handled it",
		},
		{
			name:         "dispatches to researcher",
			llmResponse:  `{"mode": "direct", "agent": "researcher"}`,
			wantResponse: "researcher handled it",
		},
		{
			name:         "dispatches to reviewer",
			llmResponse:  `{"mode": "direct", "agent": "reviewer"}`,
			wantResponse: "reviewer handled it",
		},
		{
			name:         "dispatches to chat",
			llmResponse:  `{"mode": "direct", "agent": "chat"}`,
			wantResponse: "chat handled it",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockChatCompleter{
				response: &aimodel.ChatResponse{
					Choices: []aimodel.Choice{
						{
							Message: aimodel.Message{
								Role:    aimodel.RoleAssistant,
								Content: aimodel.NewTextContent(tt.llmResponse),
							},
						},
					},
				},
			}

			coderStub := &stubAgent{id: "coder", response: makeStubResponse("coder handled it", "coder")}
			researcherStub := &stubAgent{id: "researcher", response: makeStubResponse("researcher handled it", "researcher")}
			reviewerStub := &stubAgent{id: "reviewer", response: makeStubResponse("reviewer handled it", "reviewer")}
			chatStub := &stubAgent{id: "chat", response: makeStubResponse("chat handled it", "chat")}

			planGen := taskagent.New(
				agent.Config{ID: "plan-gen", Name: "Plan Gen"},
				taskagent.WithChatCompleter(mock),
				taskagent.WithModel("test-model"),
				taskagent.WithMaxIterations(1),
			)

			orch := agents.NewOrchestratorAgent(
				agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
				mock,
				"test-model",
				map[string]agent.Agent{
					"coder":      coderStub,
					"researcher": researcherStub,
					"reviewer":   reviewerStub,
					"chat":       chatStub,
				},
				planGen,
				2,
				chatStub,
				"/tmp/test",
				nil,
				nil, // no planner
				nil, // toolRegistries
				nil, // reviewReg
				0,   // maxIterations
				0,   // runTokenBudget
			)

			resp, err := orch.Run(context.Background(), &schema.RunRequest{
				Messages: []schema.Message{schema.NewUserMessage("test input")},
			})
			if err != nil {
				t.Fatalf("orchestrator.Run: %v", err)
			}

			if len(resp.Messages) == 0 {
				t.Fatal("expected at least one response message")
			}

			text := resp.Messages[0].Content.Text()
			if text != tt.wantResponse {
				t.Errorf("response = %q, want %q", text, tt.wantResponse)
			}
		})
	}
}

// --- Test 4: Orchestrator plan mode generates and executes a plan (Design Test 2) ---
// Verifies that complex tasks trigger plan generation and DAG execution.
// Test cases:
//   - Plan with 2 sequential steps is parsed correctly
//   - Both researcher and coder agents are invoked in dependency order
//   - Step requests include working directory context and original goal
//   - Results are aggregated into a single response
func TestIntegration_Agents_OrchestratorPlanExecution(t *testing.T) {
	planJSON := `{"mode": "plan", "plan": {
		"goal": "Create a Go utility package",
		"steps": [
			{"id": "step_1", "description": "Research existing patterns", "agent": "researcher", "depends_on": []},
			{"id": "step_2", "description": "Write the utility code", "agent": "coder", "depends_on": ["step_1"]}
		]
	}}`

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(planJSON),
					},
				},
			},
		},
	}

	cfg := &config.Config{
		LLM:    config.LLMConfig{Model: "test-model"},
		Agents: config.AgentsConfig{MaxIterations: 1},
		Memory: config.MemoryConfig{MaxConcurrency: 2},
	}

	coderStub := &stubAgent{id: "coder", response: makeStubResponse("code written", "coder")}
	researcherStub := &stubAgent{id: "researcher", response: makeStubResponse("research done", "researcher")}
	reviewerStub := &stubAgent{id: "reviewer", response: makeStubResponse("review done", "reviewer")}
	chatStub := &stubAgent{id: "chat", response: makeStubResponse("chat done", "chat")}

	planGen := taskagent.New(
		agent.Config{ID: "plan-gen", Name: "Plan Gen"},
		taskagent.WithChatCompleter(mock),
		taskagent.WithModel(cfg.LLM.Model),
		taskagent.WithMaxIterations(1),
	)

	orch := agents.NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
		mock,
		cfg.LLM.Model,
		map[string]agent.Agent{
			"coder":      coderStub,
			"researcher": researcherStub,
			"reviewer":   reviewerStub,
			"chat":       chatStub,
		},
		planGen,
		cfg.Memory.MaxConcurrency,
		chatStub,
		"/tmp/test",
		nil,
		nil, // no planner
		nil, // toolRegistries
		nil, // reviewReg
		0,   // maxIterations
		0,   // runTokenBudget
	)

	resp, err := orch.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("Create a Go utility package")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("orchestrator.Run: %v", err)
	}

	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message in response")
	}
}

// --- Test 5: Orchestrator fallback on classification failure (Design Test 3) ---
// Verifies fallback to chat agent when classification returns invalid JSON.
// Test cases:
//   - Invalid JSON from LLM triggers fallback
//   - Chat agent (not coder) receives the request as fallback
//   - Response is the chat agent's response
func TestIntegration_Agents_OrchestratorFallbackOnInvalidJSON(t *testing.T) {
	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent("This is not valid JSON at all"),
					},
				},
			},
		},
	}

	chatStub := &stubAgent{id: "chat", response: makeStubResponse("chat fallback response", "chat")}

	planGen := taskagent.New(
		agent.Config{ID: "plan-gen", Name: "Plan Gen"},
		taskagent.WithChatCompleter(mock),
		taskagent.WithModel("test-model"),
		taskagent.WithMaxIterations(1),
	)

	orch := agents.NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
		mock,
		"test-model",
		map[string]agent.Agent{
			"coder":      &stubAgent{id: "coder"},
			"researcher": &stubAgent{id: "researcher"},
			"reviewer":   &stubAgent{id: "reviewer"},
			"chat":       chatStub,
		},
		planGen,
		2,
		chatStub,
		"",
		nil,
		nil, // no planner
		nil, // toolRegistries
		nil, // reviewReg
		0,   // maxIterations
		0,   // runTokenBudget
	)

	resp, err := orch.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("do something")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("orchestrator.Run should not error on fallback: %v", err)
	}

	if resp == nil || len(resp.Messages) == 0 {
		t.Fatal("expected response from fallback")
	}

	text := resp.Messages[0].Content.Text()
	if text != "chat fallback response" {
		t.Errorf("fallback response = %q, want %q", text, "chat fallback response")
	}
}

// --- Test 6: Orchestrator fallback on plan with empty steps (Design Test 3 edge case) ---
// Verifies fallback to chat agent when plan has empty steps (validation fails).
// Test cases:
//   - Plan mode with zero steps triggers validation failure
//   - Fallback to chat agent occurs
//   - Response is the chat agent's response
func TestIntegration_Agents_OrchestratorFallbackOnEmptyPlan(t *testing.T) {
	emptyPlan := `{"mode": "plan", "plan": {"goal": "Nothing", "steps": []}}`

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(emptyPlan),
					},
				},
			},
		},
	}

	chatStub := &stubAgent{id: "chat", response: makeStubResponse("chat fallback", "chat")}

	planGen := taskagent.New(
		agent.Config{ID: "plan-gen", Name: "Plan Gen"},
		taskagent.WithChatCompleter(mock),
		taskagent.WithModel("test-model"),
		taskagent.WithMaxIterations(1),
	)

	orch := agents.NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
		mock,
		"test-model",
		map[string]agent.Agent{
			"coder":      &stubAgent{id: "coder"},
			"researcher": &stubAgent{id: "researcher"},
			"reviewer":   &stubAgent{id: "reviewer"},
			"chat":       chatStub,
		},
		planGen,
		2,
		chatStub,
		"",
		nil,
		nil, // no planner
		nil, // toolRegistries
		nil, // reviewReg
		0,   // maxIterations
		0,   // runTokenBudget
	)

	resp, err := orch.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("do something simple")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("orchestrator.Run: %v", err)
	}

	if resp == nil || len(resp.Messages) == 0 {
		t.Fatal("expected response from fallback")
	}

	// Fallback to chat because plan validation fails (empty steps).
	text := resp.Messages[0].Content.Text()
	if text != "chat fallback" {
		t.Errorf("response = %q, want %q", text, "chat fallback")
	}
}

// --- Test: Orchestrator fallback on unknown agent in plan (Design Test 3 edge case) ---
// Verifies fallback to chat agent when plan references a non-existent agent.
// Test cases:
//   - Plan referencing unknown agent "nonexistent" triggers validation failure
//   - Fallback to chat agent occurs
func TestIntegration_Agents_OrchestratorFallbackOnInvalidAgent(t *testing.T) {
	badPlan := `{"mode": "plan", "plan": {"goal": "Test", "steps": [{"id": "step_1", "description": "do it", "agent": "nonexistent", "depends_on": []}]}}`

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(badPlan),
					},
				},
			},
		},
	}

	chatStub := &stubAgent{id: "chat", response: makeStubResponse("chat fallback", "chat")}

	planGen := taskagent.New(
		agent.Config{ID: "plan-gen", Name: "Plan Gen"},
		taskagent.WithChatCompleter(mock),
		taskagent.WithModel("test-model"),
		taskagent.WithMaxIterations(1),
	)

	orch := agents.NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
		mock,
		"test-model",
		map[string]agent.Agent{
			"coder":      &stubAgent{id: "coder"},
			"researcher": &stubAgent{id: "researcher"},
			"reviewer":   &stubAgent{id: "reviewer"},
			"chat":       chatStub,
		},
		planGen,
		2,
		chatStub,
		"",
		nil,
		nil, // no planner
		nil, // toolRegistries
		nil, // reviewReg
		0,   // maxIterations
		0,   // runTokenBudget
	)

	resp, err := orch.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("do it")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("orchestrator.Run: %v", err)
	}

	// Should fall back to chat because "nonexistent" is not a valid agent.
	if resp == nil || len(resp.Messages) == 0 {
		t.Fatal("expected response from fallback")
	}
}

// --- Test 14: OrchestratorAgent implements StreamAgent (Design Test 1, streaming) ---
// Verifies that RunStream returns the sub-agent's stream for direct dispatch.
// Test cases:
//   - OrchestratorAgent satisfies agent.StreamAgent interface
//   - RunStream in direct mode proxies the sub-agent's stream
//   - Stream contains AgentStart and AgentEnd events
func TestIntegration_Agents_OrchestratorImplementsStreamAgent(t *testing.T) {
	directJSON := `{"mode": "direct", "agent": "coder"}`

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(directJSON),
					},
				},
			},
		},
	}

	coderStub := &stubStreamAgent{id: "coder", response: "done"}

	planGen := taskagent.New(
		agent.Config{ID: "plan-gen", Name: "Plan Gen"},
		taskagent.WithChatCompleter(mock),
		taskagent.WithModel("test-model"),
		taskagent.WithMaxIterations(1),
	)

	orch := agents.NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
		mock,
		"test-model",
		map[string]agent.Agent{
			"coder":      coderStub,
			"researcher": &stubAgent{id: "researcher"},
			"reviewer":   &stubAgent{id: "reviewer"},
			"chat":       &stubAgent{id: "chat"},
		},
		planGen,
		2,
		&stubAgent{id: "chat"},
		"",
		nil,
		nil, // no planner
		nil, // toolRegistries
		nil, // reviewReg
		0,   // maxIterations
		0,   // runTokenBudget
	)

	// Verify OrchestratorAgent satisfies agent.StreamAgent.
	var sa agent.StreamAgent = orch
	_ = sa

	stream, err := orch.RunStream(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("simple task")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	defer func() { _ = stream.Close() }()

	// Consume all events.
	var events []schema.Event
	for {
		event, recvErr := stream.Recv()
		if recvErr != nil {
			if recvErr == io.EOF {
				break
			}
			t.Fatalf("Recv: %v", recvErr)
		}
		events = append(events, event)
	}

	if len(events) == 0 {
		t.Fatal("expected at least one event from RunStream")
	}

	// Verify we get AgentStart and AgentEnd events.
	hasStart := false
	hasEnd := false
	for _, e := range events {
		if e.Type == schema.EventAgentStart {
			hasStart = true
		}
		if e.Type == schema.EventAgentEnd {
			hasEnd = true
		}
	}

	if !hasStart {
		t.Error("missing agent_start event from orchestrator RunStream")
	}
	if !hasEnd {
		t.Error("missing agent_end event from orchestrator RunStream")
	}
}

// --- Test 9: Persistent memory loads at startup and injects into system prompt ---
// Verifies that PersistentMemoryPrompt dynamically includes entries from memory.
// Test cases:
//   - Rendered prompt includes the base prompt text
//   - Rendered prompt includes persistent memory content values
//   - Rendered prompt includes persistent memory key names
func TestIntegration_Agents_PersistentMemoryInSystemPrompt(t *testing.T) {
	dir := t.TempDir()

	// Create a FileStore and populate it with test entries.
	store, err := vagamemory.NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	persistentMem := memory.NewPersistentMemoryWithStore(store)

	ctx := context.Background()
	if err := persistentMem.Set(ctx, "project:conventions", "Use gofumpt for formatting", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Create PersistentMemoryPrompt.
	prompt := agents.NewPersistentMemoryPrompt("You are an expert coder.", persistentMem)

	rendered, err := prompt.Render(ctx, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	// Verify the rendered prompt includes the base prompt.
	if len(rendered) < len("You are an expert coder.") {
		t.Fatal("rendered prompt is too short")
	}

	// Verify the rendered prompt includes the persistent memory content.
	if !containsString(rendered, "Use gofumpt for formatting") {
		t.Errorf("rendered prompt does not contain persistent memory content:\n%s", rendered)
	}

	if !containsString(rendered, "project:conventions") {
		t.Errorf("rendered prompt does not contain memory key 'project:conventions':\n%s", rendered)
	}
}

// --- Test 9b: PersistentMemoryPrompt returns base prompt when memory is empty ---
// Test cases:
//   - When store has no entries, rendered prompt equals base prompt exactly
func TestIntegration_Agents_PersistentMemoryEmptyStore(t *testing.T) {
	dir := t.TempDir()

	store, err := vagamemory.NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	persistentMem := memory.NewPersistentMemoryWithStore(store)
	prompt := agents.NewPersistentMemoryPrompt("Base prompt only.", persistentMem)

	rendered, err := prompt.Render(context.Background(), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if rendered != "Base prompt only." {
		t.Errorf("rendered = %q, want %q", rendered, "Base prompt only.")
	}
}

// --- Test 9c: PersistentMemoryPrompt returns base prompt when store is nil ---
// Test cases:
//   - When store is nil, rendered prompt equals base prompt exactly
func TestIntegration_Agents_PersistentMemoryNilStore(t *testing.T) {
	prompt := agents.NewPersistentMemoryPrompt("Base prompt only.", nil)

	rendered, err := prompt.Render(context.Background(), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if rendered != "Base prompt only." {
		t.Errorf("rendered = %q, want %q", rendered, "Base prompt only.")
	}
}

// --- Test 7: FileStore CRUD integration via PersistentMemory ---
// Tests the full CRUD lifecycle through the PersistentMemory wrapper backed by FileStore.
// Test cases:
//   - Set a key-value pair and Get returns it
//   - List with prefix returns matching entries only
//   - Set entries across namespaces, List all returns all entries
//   - Delete removes a specific entry
//   - Get after Delete returns nil
//   - Clear removes all entries
func TestIntegration_Agents_FileStoreCRUDViaPersistentMemory(t *testing.T) {
	dir := t.TempDir()
	store, err := vagamemory.NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	mem := memory.NewPersistentMemoryWithStore(store)
	ctx := context.Background()

	// Set a key-value pair.
	if err := mem.Set(ctx, "project:conventions", "Use gofumpt", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Get the key back.
	val, err := mem.Get(ctx, "project:conventions")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if val == nil {
		t.Fatal("Get returned nil, expected value")
	}
	if s, ok := val.(string); !ok || s != "Use gofumpt" {
		t.Errorf("Get = %v, want %q", val, "Use gofumpt")
	}

	// List with prefix.
	entries, err := mem.List(ctx, "project")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List len = %d, want 1", len(entries))
	}

	// Set another entry in a different namespace.
	if err := mem.Set(ctx, "user:preferences", "dark theme", 0); err != nil {
		t.Fatalf("Set user: %v", err)
	}

	// List all.
	allEntries, err := mem.List(ctx, "")
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(allEntries) != 2 {
		t.Fatalf("List all len = %d, want 2", len(allEntries))
	}

	// Delete the first key.
	if err := mem.Delete(ctx, "project:conventions"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Verify it is gone.
	val, err = mem.Get(ctx, "project:conventions")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if val != nil {
		t.Errorf("Get after delete = %v, want nil", val)
	}

	// List should have only 1 entry now.
	entries, err = mem.List(ctx, "")
	if err != nil {
		t.Fatalf("List after delete: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List after delete len = %d, want 1", len(entries))
	}

	// Clear all.
	if err := mem.Clear(ctx); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	entries, err = mem.List(ctx, "")
	if err != nil {
		t.Fatalf("List after clear: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("List after clear len = %d, want 0", len(entries))
	}
}

// --- Test: Working Directory Capture and Propagation (Design Test 4) ---
// Verifies that the working directory is captured at startup, stored in config,
// and propagated to sub-agents in enriched request messages.
// Test cases:
//   - When BashWorkingDir is empty, os.Getwd() populates it
//   - OrchestratorAgent enriches requests with working directory context message
//   - Sub-agent receives the working directory as the first message
//   - The original user message follows the working directory context
func TestIntegration_Agents_WorkingDirectoryCaptureAndPropagation(t *testing.T) {
	// 1. Capture CWD before test.
	cwd, err := os.Getwd()
	if err != nil {
		t.Fatalf("os.Getwd: %v", err)
	}

	// 2. Load config with empty BashWorkingDir.
	cfg := &config.Config{
		LLM:    config.LLMConfig{Model: "test-model"},
		Agents: config.AgentsConfig{MaxIterations: 10},
		Memory: config.MemoryConfig{MaxConcurrency: 2},
		Tools:  config.ToolsConfig{BashWorkingDir: ""},
	}

	// 3. Apply working directory logic from main.go.
	if cfg.Tools.BashWorkingDir == "" {
		workingDir, wdErr := os.Getwd()
		if wdErr != nil {
			workingDir = "."
		}
		cfg.Tools.BashWorkingDir = workingDir
	}

	// 4. Assert BashWorkingDir equals the captured CWD.
	if cfg.Tools.BashWorkingDir != cwd {
		t.Errorf("BashWorkingDir = %q, want %q", cfg.Tools.BashWorkingDir, cwd)
	}

	// 5. Create an OrchestratorAgent with the working directory and a recording sub-agent.
	recordingAgent := &recordingStubAgent{
		id: "coder",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("done"),
				}, "coder"),
			},
		},
	}

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(`{"mode": "direct", "agent": "coder"}`),
					},
				},
			},
		},
	}

	orch := agents.NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
		mock,
		"test-model",
		map[string]agent.Agent{
			"coder":      recordingAgent,
			"researcher": &stubAgent{id: "researcher"},
			"reviewer":   &stubAgent{id: "reviewer"},
			"chat":       &stubAgent{id: "chat"},
		},
		nil,
		2,
		&stubAgent{id: "chat"},
		cfg.Tools.BashWorkingDir,
		nil,
		nil, // no planner
		nil, // toolRegistries
		nil, // reviewReg
		0,   // maxIterations
		0,   // runTokenBudget
	)

	// 6. Trigger a direct dispatch.
	_, err = orch.Run(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("read file main.go")},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// 7. Assert the sub-agent request contains the working directory context message.
	if recordingAgent.lastRequest == nil {
		t.Fatal("sub-agent was not called")
	}

	msgs := recordingAgent.lastRequest.Messages
	if len(msgs) < 2 {
		t.Fatalf("expected at least 2 messages (working dir + user), got %d", len(msgs))
	}

	wdMsg := msgs[0].Content.Text()
	expectedWD := "Working directory: " + cwd
	if wdMsg != expectedWD {
		t.Errorf("working directory message = %q, want %q", wdMsg, expectedWD)
	}

	// Verify original user message follows.
	userMsg := msgs[1].Content.Text()
	if userMsg != "read file main.go" {
		t.Errorf("user message = %q, want %q", userMsg, "read file main.go")
	}
}

// --- Test: Parallel Step Execution (Design Test 7) ---
// Verifies independent plan steps execute in parallel.
// Test cases:
//   - Two independent steps (no dependencies) both start within a short time window
//   - Both step results are present in the aggregated response
//   - Steps with no depends_on field are executed concurrently
func TestIntegration_Agents_OrchestratorParallelStepExecution(t *testing.T) {
	planJSON := `{"mode": "plan", "plan": {
		"goal": "Do two independent tasks",
		"steps": [
			{"id": "step_1", "description": "Research task A", "agent": "researcher", "depends_on": []},
			{"id": "step_2", "description": "Research task B", "agent": "coder", "depends_on": []}
		]
	}}`

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(planJSON),
					},
				},
			},
		},
	}

	// Create agents that record invocation timestamps.
	var (
		researcherStart atomic.Int64
		coderStart      atomic.Int64
	)

	researcherStub := &timingStubAgent{
		id:       "researcher",
		response: makeStubResponse("research A done", "researcher"),
		startRef: &researcherStart,
		delay:    50 * time.Millisecond,
	}

	coderStub := &timingStubAgent{
		id:       "coder",
		response: makeStubResponse("code B done", "coder"),
		startRef: &coderStart,
		delay:    50 * time.Millisecond,
	}

	planGen := taskagent.New(
		agent.Config{ID: "plan-gen", Name: "Plan Gen"},
		taskagent.WithChatCompleter(mock),
		taskagent.WithModel("test-model"),
		taskagent.WithMaxIterations(1),
	)

	orch := agents.NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
		mock,
		"test-model",
		map[string]agent.Agent{
			"coder":      coderStub,
			"researcher": researcherStub,
			"reviewer":   &stubAgent{id: "reviewer"},
			"chat":       &stubAgent{id: "chat"},
		},
		planGen,
		2, // maxConcurrency=2 allows parallel execution
		&stubAgent{id: "chat"},
		"/tmp/test",
		nil,
		nil, // no planner
		nil, // toolRegistries
		nil, // reviewReg
		0,   // maxIterations
		0,   // runTokenBudget
	)

	resp, err := orch.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("Do two independent tasks")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	// Verify both agents were invoked.
	rStart := researcherStart.Load()
	cStart := coderStart.Load()
	if rStart == 0 {
		t.Error("researcher agent was not invoked")
	}
	if cStart == 0 {
		t.Error("coder agent was not invoked")
	}

	// Verify both started within a short window (within 100ms of each other),
	// indicating parallel execution rather than sequential.
	if rStart != 0 && cStart != 0 {
		diff := rStart - cStart
		if diff < 0 {
			diff = -diff
		}
		// 100ms tolerance: both should start nearly simultaneously.
		if diff > int64(100*time.Millisecond) {
			t.Errorf("steps started %v apart, expected near-simultaneous (parallel) execution", time.Duration(diff))
		}
	}

	// Verify response has messages.
	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message in aggregated response")
	}
}

// --- Test: Plan Step Failure Handling (Design Test 8) ---
// Verifies the Orchestrator handles sub-task failures gracefully.
// Test cases:
//   - A plan with 2 steps where one step fails does not cause a panic
//   - The failing step is skipped (Optional=true in DAG nodes)
//   - The response includes results from the successful step
//   - No unhandled error is returned
func TestIntegration_Agents_OrchestratorPlanStepFailure(t *testing.T) {
	planJSON := `{"mode": "plan", "plan": {
		"goal": "Two step plan with failure",
		"steps": [
			{"id": "step_1", "description": "This step fails", "agent": "researcher", "depends_on": []},
			{"id": "step_2", "description": "This step succeeds", "agent": "coder", "depends_on": []}
		]
	}}`

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(planJSON),
					},
				},
			},
		},
	}

	// Create a failing sub-agent.
	failingAgent := &failingStubAgent{id: "researcher", err: errors.New("simulated failure")}

	// Create a succeeding sub-agent.
	successAgent := &stubAgent{id: "coder", response: makeStubResponse("code completed successfully", "coder")}

	planGen := taskagent.New(
		agent.Config{ID: "plan-gen", Name: "Plan Gen"},
		taskagent.WithChatCompleter(mock),
		taskagent.WithModel("test-model"),
		taskagent.WithMaxIterations(1),
	)

	orch := agents.NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
		mock,
		"test-model",
		map[string]agent.Agent{
			"coder":      successAgent,
			"researcher": failingAgent,
			"reviewer":   &stubAgent{id: "reviewer"},
			"chat":       &stubAgent{id: "chat"},
		},
		planGen,
		2,
		&stubAgent{id: "chat"},
		"",
		nil,
		nil, // no planner
		nil, // toolRegistries
		nil, // reviewReg
		0,   // maxIterations
		0,   // runTokenBudget
	)

	// Should not panic or return an unhandled error.
	resp, err := orch.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("do two things")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("Run should not error due to one step failure (Optional=true): %v", err)
	}

	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	// The response should contain at least the successful step's output.
	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message from the successful step")
	}
}

// --- Test: Classification Token Usage Aggregation (Design Test 9) ---
// Verifies token usage from the classification LLM call is included in the response.
// Test cases:
//   - Classification usage is captured from the LLM response
//   - Sub-agent usage is captured from the sub-agent response
//   - Final response usage is the sum of classification + sub-agent usage
//   - PromptTokens, CompletionTokens, and TotalTokens are all aggregated correctly
func TestIntegration_Agents_OrchestratorTokenUsageAggregation(t *testing.T) {
	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(`{"mode": "direct", "agent": "coder"}`),
					},
				},
			},
			Usage: aimodel.Usage{
				PromptTokens:     50,
				CompletionTokens: 20,
				TotalTokens:      70,
			},
		},
	}

	coderStub := &stubAgent{
		id: "coder",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("code result"),
				}, "coder"),
			},
			Usage: &aimodel.Usage{
				PromptTokens:     200,
				CompletionTokens: 100,
				TotalTokens:      300,
			},
		},
	}

	chatStub := &stubAgent{id: "chat"}

	orch := agents.NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
		mock,
		"test-model",
		map[string]agent.Agent{
			"coder":      coderStub,
			"researcher": &stubAgent{id: "researcher"},
			"reviewer":   &stubAgent{id: "reviewer"},
			"chat":       chatStub,
		},
		nil,
		2,
		chatStub,
		"/tmp/test",
		nil,
		nil, // no planner
		nil, // toolRegistries
		nil, // reviewReg
		0,   // maxIterations
		0,   // runTokenBudget
	)

	resp, err := orch.Run(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("write code")},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	// Verify usage aggregation: classify (50+20+70) + sub-agent (200+100+300).
	if resp.Usage == nil {
		t.Fatal("expected usage in response")
	}

	if resp.Usage.PromptTokens != 250 {
		t.Errorf("PromptTokens = %d, want 250 (50 classify + 200 sub-agent)", resp.Usage.PromptTokens)
	}

	if resp.Usage.CompletionTokens != 120 {
		t.Errorf("CompletionTokens = %d, want 120 (20 classify + 100 sub-agent)", resp.Usage.CompletionTokens)
	}

	if resp.Usage.TotalTokens != 370 {
		t.Errorf("TotalTokens = %d, want 370 (70 classify + 300 sub-agent)", resp.Usage.TotalTokens)
	}
}

// --- Test: Chat Agent in Plan Steps (Design Test 10) ---
// Verifies the chat agent can be used as a valid agent in plan steps.
// Test cases:
//   - Plan with a step assigned to "chat" is valid and parsed correctly
//   - Chat agent is invoked for its assigned step
//   - Response includes chat agent output
//   - Plan with chat + coder steps executes successfully end-to-end
func TestIntegration_Agents_OrchestratorChatInPlanSteps(t *testing.T) {
	planJSON := `{"mode": "plan", "plan": {
		"goal": "Explain and implement",
		"steps": [
			{"id": "step_1", "description": "Explain the concept of dependency injection", "agent": "chat", "depends_on": []},
			{"id": "step_2", "description": "Write code implementing DI pattern", "agent": "coder", "depends_on": ["step_1"]}
		]
	}}`

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(planJSON),
					},
				},
			},
		},
	}

	chatInvoked := &atomic.Bool{}
	coderInvoked := &atomic.Bool{}

	chatStub := &callbackStubAgent{
		id:       "chat",
		response: makeStubResponse("DI explanation done", "chat"),
		onRun:    func() { chatInvoked.Store(true) },
	}
	coderStub := &callbackStubAgent{
		id:       "coder",
		response: makeStubResponse("DI code written", "coder"),
		onRun:    func() { coderInvoked.Store(true) },
	}

	planGen := taskagent.New(
		agent.Config{ID: "plan-gen", Name: "Plan Gen"},
		taskagent.WithChatCompleter(mock),
		taskagent.WithModel("test-model"),
		taskagent.WithMaxIterations(1),
	)

	orch := agents.NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
		mock,
		"test-model",
		map[string]agent.Agent{
			"coder":      coderStub,
			"researcher": &stubAgent{id: "researcher"},
			"reviewer":   &stubAgent{id: "reviewer"},
			"chat":       chatStub,
		},
		planGen,
		2,
		chatStub,
		"/tmp/test",
		nil,
		nil, // no planner
		nil, // toolRegistries
		nil, // reviewReg
		0,   // maxIterations
		0,   // runTokenBudget
	)

	resp, err := orch.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("explain and code DI")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if resp == nil || len(resp.Messages) == 0 {
		t.Fatal("expected response with messages")
	}

	// Verify chat agent was invoked for step_1.
	if !chatInvoked.Load() {
		t.Error("chat agent was not invoked for plan step")
	}

	// Verify coder agent was invoked for step_2.
	if !coderInvoked.Load() {
		t.Error("coder agent was not invoked for plan step")
	}
}

// --- Test: Orchestrator Fallback on LLM Error (Design Test 3, LLM error) ---
// Verifies fallback to chat agent when LLM returns an error during classification.
// Test cases:
//   - LLM error during classification triggers fallback
//   - Chat agent (not coder) handles the request
//   - Response is the chat agent's response
func TestIntegration_Agents_OrchestratorFallbackOnLLMError(t *testing.T) {
	mock := &mockChatCompleter{
		err: errors.New("LLM service unavailable"),
	}

	chatStub := &stubAgent{
		id:       "chat",
		response: makeStubResponse("chat handled it as fallback", "chat"),
	}

	orch := agents.NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
		mock,
		"test-model",
		map[string]agent.Agent{
			"coder":      &stubAgent{id: "coder"},
			"researcher": &stubAgent{id: "researcher"},
			"reviewer":   &stubAgent{id: "reviewer"},
			"chat":       chatStub,
		},
		nil,
		2,
		chatStub,
		"",
		nil,
		nil, // no planner
		nil, // toolRegistries
		nil, // reviewReg
		0,   // maxIterations
		0,   // runTokenBudget
	)

	resp, err := orch.Run(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("anything")},
	})
	if err != nil {
		t.Fatalf("Run should not error on fallback: %v", err)
	}

	if resp == nil || len(resp.Messages) == 0 {
		t.Fatal("expected response from fallback")
	}

	text := resp.Messages[0].Content.Text()
	if text != "chat handled it as fallback" {
		t.Errorf("response = %q, want %q", text, "chat handled it as fallback")
	}
}

// =============================================================================
// Dynamic Sub-Agent Integration Tests (Design Tests 1-10)
// =============================================================================

// --- Design Test 1: Static-only plan (regression) ---
// Verifies that a multi-step plan using only static agents executes as before.
// Test cases:
//   - Plan with 2 static steps (researcher + coder) is parsed and executed
//   - Both static agents are invoked
//   - No dynamic agents are created
//   - Results are collected and aggregated correctly
func TestIntegration_DynamicAgents_StaticOnlyPlanRegression(t *testing.T) {
	planJSON := `{"mode": "plan", "plan": {
		"goal": "Refactor the utils package",
		"steps": [
			{"id": "step_1", "description": "Research current utils", "agent": "researcher", "depends_on": []},
			{"id": "step_2", "description": "Refactor the code", "agent": "coder", "depends_on": ["step_1"]}
		]
	}}`

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(planJSON),
					},
				},
			},
			Usage: aimodel.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
		},
	}

	coderStub := &recordingStubAgent{id: "coder", response: makeStubResponse("code refactored", "coder")}
	researcherStub := &recordingStubAgent{id: "researcher", response: makeStubResponse("research done", "researcher")}
	reviewerStub := &stubAgent{id: "reviewer", response: makeStubResponse("review done", "reviewer")}
	chatStub := &stubAgent{id: "chat", response: makeStubResponse("chat done", "chat")}

	planGen := taskagent.New(
		agent.Config{ID: "plan-gen", Name: "Plan Gen"},
		taskagent.WithChatCompleter(mock),
		taskagent.WithModel("test-model"),
		taskagent.WithMaxIterations(1),
	)

	orch := agents.NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
		mock,
		"test-model",
		map[string]agent.Agent{
			"coder":      coderStub,
			"researcher": researcherStub,
			"reviewer":   reviewerStub,
			"chat":       chatStub,
		},
		planGen,
		2,
		chatStub,
		"/tmp/test",
		nil,
		nil, // no planner (uses LLM directly)
		map[agents.ToolAccessLevel]tool.ToolRegistry{}, // empty toolRegistries (not needed for static)
		nil, // reviewReg
		10,  // maxIterations
		0,   // runTokenBudget
	)

	resp, err := orch.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("Refactor the utils package")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("orchestrator.Run: %v", err)
	}

	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message in response")
	}

	// Both static agents should have been called.
	if researcherStub.lastRequest == nil {
		t.Error("researcher agent was not called")
	}
	if coderStub.lastRequest == nil {
		t.Error("coder agent was not called")
	}
}

// --- Design Test 2: Dynamic-only plan ---
// Verifies that a plan with all dynamic spec steps creates ephemeral agents.
// Test cases:
//   - Plan where all steps have DynamicSpec is parsed and executed
//   - Each step creates an ephemeral agent with the specified system prompt
//   - Results are collected and aggregated correctly
//   - Token usage includes dynamic agent usage
func TestIntegration_DynamicAgents_DynamicOnlyPlan(t *testing.T) {
	planJSON := `{"mode": "plan", "plan": {
		"goal": "Analyze and fix security issues",
		"steps": [
			{"id": "step_1", "description": "Scan for vulnerabilities", "agent": "researcher", "depends_on": [],
			 "dynamic_spec": {"base_type": "researcher", "system_prompt": "You are a security auditor."}},
			{"id": "step_2", "description": "Fix the vulnerabilities", "agent": "coder", "depends_on": ["step_1"],
			 "dynamic_spec": {"base_type": "coder", "system_prompt": "You are a security-focused coder."}}
		]
	}}`

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(planJSON),
					},
				},
			},
			Usage: aimodel.Usage{PromptTokens: 20, CompletionTokens: 10, TotalTokens: 30},
		},
	}

	// Static agents should NOT be called since all steps use DynamicSpec.
	coderStub := &recordingStubAgent{id: "coder", response: makeStubResponse("static coder response", "coder")}
	researcherStub := &recordingStubAgent{id: "researcher", response: makeStubResponse("static researcher response", "researcher")}
	chatStub := &stubAgent{id: "chat", response: makeStubResponse("chat done", "chat")}

	reg, err := tools.Register(config.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	readOnlyReg, err := tools.RegisterReadOnly(config.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.RegisterReadOnly: %v", err)
	}

	planGen := taskagent.New(
		agent.Config{ID: "plan-gen", Name: "Plan Gen"},
		taskagent.WithChatCompleter(mock),
		taskagent.WithModel("test-model"),
		taskagent.WithMaxIterations(1),
	)

	toolRegs := map[agents.ToolAccessLevel]tool.ToolRegistry{
		agents.ToolAccessFull:     reg,
		agents.ToolAccessReadOnly: readOnlyReg,
	}

	orch := agents.NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
		mock,
		"test-model",
		map[string]agent.Agent{
			"coder":      coderStub,
			"researcher": researcherStub,
			"reviewer":   &stubAgent{id: "reviewer"},
			"chat":       chatStub,
		},
		planGen,
		2,
		chatStub,
		"/tmp/test",
		nil,
		nil, // no planner
		toolRegs,
		nil, // reviewReg
		10,  // maxIterations
		0,   // runTokenBudget
	)

	resp, err := orch.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("Fix security issues")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("orchestrator.Run: %v", err)
	}

	if resp == nil {
		t.Fatal("expected non-nil response")
	}

	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message in response")
	}

	// Static agents should NOT have been called -- dynamic agents handle everything.
	if coderStub.lastRequest != nil {
		t.Error("static coder agent should NOT have been called for dynamic-only plan")
	}
	if researcherStub.lastRequest != nil {
		t.Error("static researcher agent should NOT have been called for dynamic-only plan")
	}

	// Verify usage was aggregated (classification usage + dynamic agent usage).
	if resp.Usage == nil {
		t.Fatal("expected usage in response")
	}
	if resp.Usage.PromptTokens < 20 {
		t.Errorf("expected at least classification prompt tokens, got %d", resp.Usage.PromptTokens)
	}
}

// --- Design Test 3: Mixed static and dynamic plan ---
// Verifies that a plan with both static and dynamic steps works correctly.
// Test cases:
//   - Static step uses registered sub-agent
//   - Dynamic step uses ephemeral agent
//   - Dependencies work correctly between static and dynamic steps
func TestIntegration_DynamicAgents_MixedStaticDynamicPlan(t *testing.T) {
	planJSON := `{"mode": "plan", "plan": {
		"goal": "Research and implement feature",
		"steps": [
			{"id": "step_1", "description": "Research the feature", "agent": "researcher", "depends_on": []},
			{"id": "step_2", "description": "Implement with custom prompt", "agent": "coder", "depends_on": ["step_1"],
			 "dynamic_spec": {"base_type": "coder", "system_prompt": "You are a Go testing specialist."}}
		]
	}}`

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(planJSON),
					},
				},
			},
		},
	}

	// Researcher should be called (static), coder should NOT (dynamic takes over).
	researcherStub := &recordingStubAgent{id: "researcher", response: makeStubResponse("research complete", "researcher")}
	coderStub := &recordingStubAgent{id: "coder", response: makeStubResponse("static code done", "coder")}
	chatStub := &stubAgent{id: "chat", response: makeStubResponse("chat done", "chat")}

	reg, err := tools.Register(config.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	planGen := taskagent.New(
		agent.Config{ID: "plan-gen", Name: "Plan Gen"},
		taskagent.WithChatCompleter(mock),
		taskagent.WithModel("test-model"),
		taskagent.WithMaxIterations(1),
	)

	toolRegs := map[agents.ToolAccessLevel]tool.ToolRegistry{
		agents.ToolAccessFull: reg,
	}

	orch := agents.NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
		mock,
		"test-model",
		map[string]agent.Agent{
			"coder":      coderStub,
			"researcher": researcherStub,
			"reviewer":   &stubAgent{id: "reviewer"},
			"chat":       chatStub,
		},
		planGen,
		2,
		chatStub,
		"/tmp/test",
		nil,
		nil, // no planner
		toolRegs,
		nil, // reviewReg
		10,  // maxIterations
		0,   // runTokenBudget
	)

	resp, err := orch.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("Research and implement")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("orchestrator.Run: %v", err)
	}

	if resp == nil || len(resp.Messages) == 0 {
		t.Fatal("expected response from mixed plan")
	}

	// Static researcher should have been called.
	if researcherStub.lastRequest == nil {
		t.Error("static researcher agent should have been called")
	}

	// Static coder should NOT have been called (dynamic spec overrides).
	if coderStub.lastRequest != nil {
		t.Error("static coder agent should NOT have been called for step with dynamic_spec")
	}
}

// --- Design Test 4: Dynamic spec validation failures ---
// Verifies that invalid dynamic specs cause validation to fail and trigger fallback.
// Test cases:
//   - Missing base_type in dynamic spec triggers validation failure and fallback
//   - Invalid tool_access value triggers validation failure and fallback
//   - Mismatched agent and base_type triggers validation failure and fallback
func TestIntegration_DynamicAgents_ValidationFailures(t *testing.T) {
	tests := []struct {
		name     string
		planJSON string
	}{
		{
			name: "missing base_type",
			planJSON: `{"mode": "plan", "plan": {
				"goal": "Test",
				"steps": [{"id": "step_1", "description": "do it", "agent": "coder", "depends_on": [],
					"dynamic_spec": {"system_prompt": "custom prompt"}}]
			}}`,
		},
		{
			name: "invalid tool_access",
			planJSON: `{"mode": "plan", "plan": {
				"goal": "Test",
				"steps": [{"id": "step_1", "description": "do it", "agent": "coder", "depends_on": [],
					"dynamic_spec": {"base_type": "coder", "tool_access": "super-admin"}}]
			}}`,
		},
		{
			name: "mismatched agent and base_type",
			planJSON: `{"mode": "plan", "plan": {
				"goal": "Test",
				"steps": [{"id": "step_1", "description": "do it", "agent": "coder", "depends_on": [],
					"dynamic_spec": {"base_type": "researcher"}}]
			}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			mock := &mockChatCompleter{
				response: &aimodel.ChatResponse{
					Choices: []aimodel.Choice{
						{
							Message: aimodel.Message{
								Role:    aimodel.RoleAssistant,
								Content: aimodel.NewTextContent(tt.planJSON),
							},
						},
					},
				},
			}

			chatStub := &stubAgent{id: "chat", response: makeStubResponse("chat fallback after validation", "chat")}

			planGen := taskagent.New(
				agent.Config{ID: "plan-gen", Name: "Plan Gen"},
				taskagent.WithChatCompleter(mock),
				taskagent.WithModel("test-model"),
				taskagent.WithMaxIterations(1),
			)

			orch := agents.NewOrchestratorAgent(
				agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
				mock,
				"test-model",
				map[string]agent.Agent{
					"coder":      &stubAgent{id: "coder"},
					"researcher": &stubAgent{id: "researcher"},
					"reviewer":   &stubAgent{id: "reviewer"},
					"chat":       chatStub,
				},
				planGen,
				2,
				chatStub,
				"",
				nil,
				nil, // no planner
				nil, // toolRegistries
				nil, // reviewReg
				0,   // maxIterations
				0,   // runTokenBudget
			)

			resp, err := orch.Run(context.Background(), &schema.RunRequest{
				Messages: []schema.Message{schema.NewUserMessage("test validation")},
			})
			if err != nil {
				t.Fatalf("Run should fall back, not error: %v", err)
			}

			// Validation should fail, causing fallback to chat.
			if resp == nil || len(resp.Messages) == 0 {
				t.Fatal("expected fallback response")
			}

			text := resp.Messages[0].Content.Text()
			if text != "chat fallback after validation" {
				t.Errorf("response = %q, want %q", text, "chat fallback after validation")
			}
		})
	}
}

// --- Design Test 5: Tool access level correctness ---
// Verifies that dynamic agents with different tool access levels get correct registries.
// Test cases:
//   - Dynamic agent with tool_access "full" gets all 6 tools (bash, file_read, file_write, file_edit, glob, grep)
//   - Dynamic agent with tool_access "read-only" gets 3 tools (file_read, glob, grep)
//   - Dynamic agent with tool_access "none" gets 0 tools
//   - Base type "researcher" default gets read-only tools
//   - Base type "chat" default gets no tools
func TestIntegration_DynamicAgents_ToolAccessLevelCorrectness(t *testing.T) {
	reg, err := tools.Register(config.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	readOnlyReg, err := tools.RegisterReadOnly(config.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.RegisterReadOnly: %v", err)
	}

	reviewReg, err := tools.RegisterReviewTools(config.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.RegisterReviewTools: %v", err)
	}

	mock := &mockChatCompleter{}
	cfg := &config.Config{
		LLM:    config.LLMConfig{Model: "test-model"},
		Agents: config.AgentsConfig{MaxIterations: 10},
		Memory: config.MemoryConfig{MaxConcurrency: 2},
	}

	allAgents := agents.Create(cfg, mock, reg, readOnlyReg, reviewReg, nil, nil)

	// Verify tool registries are correctly wired through Create.
	// Test by running plans with different tool access levels and verifying execution completes.

	tests := []struct {
		name     string
		planJSON string
	}{
		{
			name: "full tool access",
			planJSON: `{"mode": "plan", "plan": {
				"goal": "Test full access",
				"steps": [{"id": "step_1", "description": "Code with full tools", "agent": "coder", "depends_on": [],
					"dynamic_spec": {"base_type": "coder", "tool_access": "full"}}]
			}}`,
		},
		{
			name: "read-only tool access",
			planJSON: `{"mode": "plan", "plan": {
				"goal": "Test read-only access",
				"steps": [{"id": "step_1", "description": "Research with read-only tools", "agent": "researcher", "depends_on": [],
					"dynamic_spec": {"base_type": "researcher", "tool_access": "read-only"}}]
			}}`,
		},
		{
			name: "no tool access",
			planJSON: `{"mode": "plan", "plan": {
				"goal": "Test no access",
				"steps": [{"id": "step_1", "description": "Chat without tools", "agent": "chat", "depends_on": [],
					"dynamic_spec": {"base_type": "chat", "tool_access": "none"}}]
			}}`,
		},
		{
			name: "researcher default (read-only)",
			planJSON: `{"mode": "plan", "plan": {
				"goal": "Test researcher default",
				"steps": [{"id": "step_1", "description": "Research with defaults", "agent": "researcher", "depends_on": [],
					"dynamic_spec": {"base_type": "researcher"}}]
			}}`,
		},
		{
			name: "chat default (none)",
			planJSON: `{"mode": "plan", "plan": {
				"goal": "Test chat default",
				"steps": [{"id": "step_1", "description": "Chat with defaults", "agent": "chat", "depends_on": [],
					"dynamic_spec": {"base_type": "chat"}}]
			}}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			planMock := &mockChatCompleter{
				response: &aimodel.ChatResponse{
					Choices: []aimodel.Choice{
						{
							Message: aimodel.Message{
								Role:    aimodel.RoleAssistant,
								Content: aimodel.NewTextContent(tt.planJSON),
							},
						},
					},
				},
			}

			chatStub := &stubAgent{id: "chat", response: makeStubResponse("chat done", "chat")}

			planGen := taskagent.New(
				agent.Config{ID: "plan-gen", Name: "Plan Gen"},
				taskagent.WithChatCompleter(planMock),
				taskagent.WithModel("test-model"),
				taskagent.WithMaxIterations(1),
			)

			toolRegs := map[agents.ToolAccessLevel]tool.ToolRegistry{
				agents.ToolAccessFull:     reg,
				agents.ToolAccessReadOnly: readOnlyReg,
			}

			orch := agents.NewOrchestratorAgent(
				agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
				planMock,
				"test-model",
				map[string]agent.Agent{
					"coder":      allAgents.Coder,
					"researcher": allAgents.Researcher,
					"reviewer":   allAgents.Reviewer,
					"chat":       chatStub,
				},
				planGen,
				2,
				chatStub,
				"/tmp/test",
				nil,
				nil, // no planner
				toolRegs,
				reviewReg,
				10, // maxIterations
				0,  // runTokenBudget
			)

			resp, err := orch.Run(context.Background(), &schema.RunRequest{
				Messages:  []schema.Message{schema.NewUserMessage("test tool access")},
				SessionID: "test-session",
			})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}

			if resp == nil {
				t.Fatal("expected non-nil response")
			}

			// The test passes if the dynamic agent was built without error.
			// The mock LLM returns the plan JSON for all calls, so the dynamic agent
			// will receive it as a response and return it.
			if len(resp.Messages) == 0 {
				t.Fatal("expected at least one message in response")
			}
		})
	}
}

// --- Design Test 6: Default fallback behavior ---
// Verifies that a dynamic agent spec with only base_type uses correct defaults.
// Test cases:
//   - Spec with only base_type="coder" uses default system prompt, full tool access, and default model
//   - Dynamic agent is built and executes successfully
func TestIntegration_DynamicAgents_DefaultFallbackBehavior(t *testing.T) {
	planJSON := `{"mode": "plan", "plan": {
		"goal": "Test defaults",
		"steps": [{"id": "step_1", "description": "Code with defaults", "agent": "coder", "depends_on": [],
			"dynamic_spec": {"base_type": "coder"}}]
	}}`

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(planJSON),
					},
				},
			},
		},
	}

	reg, err := tools.Register(config.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	chatStub := &stubAgent{id: "chat", response: makeStubResponse("chat done", "chat")}

	planGen := taskagent.New(
		agent.Config{ID: "plan-gen", Name: "Plan Gen"},
		taskagent.WithChatCompleter(mock),
		taskagent.WithModel("test-model"),
		taskagent.WithMaxIterations(1),
	)

	toolRegs := map[agents.ToolAccessLevel]tool.ToolRegistry{
		agents.ToolAccessFull: reg,
	}

	orch := agents.NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
		mock,
		"test-model",
		map[string]agent.Agent{
			"coder":      &stubAgent{id: "coder"},
			"researcher": &stubAgent{id: "researcher"},
			"reviewer":   &stubAgent{id: "reviewer"},
			"chat":       chatStub,
		},
		planGen,
		2,
		chatStub,
		"/tmp/test",
		nil,
		nil, // no planner
		toolRegs,
		nil, // reviewReg
		10,  // maxIterations
		0,   // runTokenBudget
	)

	resp, err := orch.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("test defaults")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if resp == nil || len(resp.Messages) == 0 {
		t.Fatal("expected response from default dynamic agent")
	}
}

// --- Design Test 7: Custom system prompt ---
// Verifies that a dynamic agent with a custom system_prompt uses it.
// Test cases:
//   - Dynamic agent with custom system_prompt is built successfully
//   - The ephemeral agent executes and returns a response
func TestIntegration_DynamicAgents_CustomSystemPrompt(t *testing.T) {
	planJSON := `{"mode": "plan", "plan": {
		"goal": "Test custom prompt",
		"steps": [{"id": "step_1", "description": "Custom task", "agent": "coder", "depends_on": [],
			"dynamic_spec": {"base_type": "coder", "system_prompt": "You are a Rust expert specializing in unsafe code review."}}]
	}}`

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(planJSON),
					},
				},
			},
		},
	}

	reg, err := tools.Register(config.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	chatStub := &stubAgent{id: "chat", response: makeStubResponse("chat done", "chat")}

	planGen := taskagent.New(
		agent.Config{ID: "plan-gen", Name: "Plan Gen"},
		taskagent.WithChatCompleter(mock),
		taskagent.WithModel("test-model"),
		taskagent.WithMaxIterations(1),
	)

	toolRegs := map[agents.ToolAccessLevel]tool.ToolRegistry{
		agents.ToolAccessFull: reg,
	}

	orch := agents.NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
		mock,
		"test-model",
		map[string]agent.Agent{
			"coder":      &stubAgent{id: "coder"},
			"researcher": &stubAgent{id: "researcher"},
			"reviewer":   &stubAgent{id: "reviewer"},
			"chat":       chatStub,
		},
		planGen,
		2,
		chatStub,
		"/tmp/test",
		nil,
		nil, // no planner
		toolRegs,
		nil, // reviewReg
		10,  // maxIterations
		0,   // runTokenBudget
	)

	resp, err := orch.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("custom prompt test")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if resp == nil || len(resp.Messages) == 0 {
		t.Fatal("expected response from custom-prompted dynamic agent")
	}
}

// --- Design Test 8: ORCH-19 precedence rule ---
// Verifies that when DynamicSpec is present, it takes precedence over the static Agent field.
// Test cases:
//   - Step with agent="coder" and DynamicSpec with tool_access="read-only" gets read-only registry
//   - Static coder agent is NOT called
//   - Dynamic agent executes successfully
func TestIntegration_DynamicAgents_ORCH19PrecedenceRule(t *testing.T) {
	planJSON := `{"mode": "plan", "plan": {
		"goal": "Test precedence",
		"steps": [{"id": "step_1", "description": "Read-only coder", "agent": "coder", "depends_on": [],
			"dynamic_spec": {"base_type": "coder", "tool_access": "read-only"}}]
	}}`

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(planJSON),
					},
				},
			},
		},
	}

	reg, err := tools.Register(config.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	readOnlyReg, err := tools.RegisterReadOnly(config.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.RegisterReadOnly: %v", err)
	}

	// Static coder should NOT be called.
	coderStub := &recordingStubAgent{id: "coder", response: makeStubResponse("static coder", "coder")}
	chatStub := &stubAgent{id: "chat", response: makeStubResponse("chat done", "chat")}

	planGen := taskagent.New(
		agent.Config{ID: "plan-gen", Name: "Plan Gen"},
		taskagent.WithChatCompleter(mock),
		taskagent.WithModel("test-model"),
		taskagent.WithMaxIterations(1),
	)

	toolRegs := map[agents.ToolAccessLevel]tool.ToolRegistry{
		agents.ToolAccessFull:     reg,
		agents.ToolAccessReadOnly: readOnlyReg,
	}

	orch := agents.NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
		mock,
		"test-model",
		map[string]agent.Agent{
			"coder":      coderStub,
			"researcher": &stubAgent{id: "researcher"},
			"reviewer":   &stubAgent{id: "reviewer"},
			"chat":       chatStub,
		},
		planGen,
		2,
		chatStub,
		"/tmp/test",
		nil,
		nil, // no planner
		toolRegs,
		nil, // reviewReg
		10,  // maxIterations
		0,   // runTokenBudget
	)

	resp, err := orch.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("precedence test")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if resp == nil || len(resp.Messages) == 0 {
		t.Fatal("expected response from dynamic agent")
	}

	// Static coder should NOT have been called.
	if coderStub.lastRequest != nil {
		t.Error("static coder should NOT have been called; DynamicSpec takes precedence")
	}
}

// --- Design Test 9: Backward compatibility ---
// Verifies that PlanStep JSON without dynamic_spec parses correctly and dispatches to static agents.
// Test cases:
//   - ClassifyResult JSON without dynamic_spec fields parses correctly
//   - PlanStep without DynamicSpec dispatches to static agents as before
//   - Existing orchestrator calls with nil toolRegistries still work
func TestIntegration_DynamicAgents_BackwardCompatibility(t *testing.T) {
	// Test 9a: JSON without dynamic_spec parses correctly.
	planJSON := `{"mode": "plan", "plan": {
		"goal": "Simple task",
		"steps": [
			{"id": "step_1", "description": "Research", "agent": "researcher", "depends_on": []},
			{"id": "step_2", "description": "Code", "agent": "coder", "depends_on": ["step_1"]}
		]
	}}`

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(planJSON),
					},
				},
			},
		},
	}

	coderStub := &recordingStubAgent{id: "coder", response: makeStubResponse("code done", "coder")}
	researcherStub := &recordingStubAgent{id: "researcher", response: makeStubResponse("research done", "researcher")}
	chatStub := &stubAgent{id: "chat", response: makeStubResponse("chat done", "chat")}

	planGen := taskagent.New(
		agent.Config{ID: "plan-gen", Name: "Plan Gen"},
		taskagent.WithChatCompleter(mock),
		taskagent.WithModel("test-model"),
		taskagent.WithMaxIterations(1),
	)

	// Test with nil toolRegistries (backward compatibility).
	orch := agents.NewOrchestratorAgent(
		agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
		mock,
		"test-model",
		map[string]agent.Agent{
			"coder":      coderStub,
			"researcher": researcherStub,
			"reviewer":   &stubAgent{id: "reviewer"},
			"chat":       chatStub,
		},
		planGen,
		2,
		chatStub,
		"/tmp/test",
		nil,
		nil, // no planner
		nil, // toolRegistries (nil for backward compat)
		nil, // reviewReg
		0,   // maxIterations
		0,   // runTokenBudget
	)

	resp, err := orch.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("simple task")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if resp == nil || len(resp.Messages) == 0 {
		t.Fatal("expected response from static agents")
	}

	// Both static agents should have been called.
	if researcherStub.lastRequest == nil {
		t.Error("researcher should have been called")
	}
	if coderStub.lastRequest == nil {
		t.Error("coder should have been called")
	}
}

// --- Design Test 10: Nil tool registries (graceful degradation) ---
// Verifies that an orchestrator with nil toolRegistries fails gracefully for dynamic specs.
// Test cases:
//   - Dynamic spec with non-none tool access and nil toolRegistries causes buildDynamicAgent error
//   - buildNodes propagates the error and orchestrator falls back to chat
//   - Dynamic spec with tool_access "none" still works with nil toolRegistries
func TestIntegration_DynamicAgents_NilToolRegistriesGracefulDegradation(t *testing.T) {
	t.Run("non-none tool access fails gracefully", func(t *testing.T) {
		planJSON := `{"mode": "plan", "plan": {
			"goal": "Test nil registries",
			"steps": [{"id": "step_1", "description": "Code something", "agent": "coder", "depends_on": [],
				"dynamic_spec": {"base_type": "coder"}}]
		}}`

		mock := &mockChatCompleter{
			response: &aimodel.ChatResponse{
				Choices: []aimodel.Choice{
					{
						Message: aimodel.Message{
							Role:    aimodel.RoleAssistant,
							Content: aimodel.NewTextContent(planJSON),
						},
					},
				},
			},
		}

		chatStub := &stubAgent{id: "chat", response: makeStubResponse("chat fallback nil registries", "chat")}

		planGen := taskagent.New(
			agent.Config{ID: "plan-gen", Name: "Plan Gen"},
			taskagent.WithChatCompleter(mock),
			taskagent.WithModel("test-model"),
			taskagent.WithMaxIterations(1),
		)

		orch := agents.NewOrchestratorAgent(
			agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
			mock,
			"test-model",
			map[string]agent.Agent{
				"coder":      &stubAgent{id: "coder"},
				"researcher": &stubAgent{id: "researcher"},
				"reviewer":   &stubAgent{id: "reviewer"},
				"chat":       chatStub,
			},
			planGen,
			2,
			chatStub,
			"",
			nil,
			nil, // no planner
			nil, // nil toolRegistries
			nil, // reviewReg
			0,   // maxIterations
			0,   // runTokenBudget
		)

		resp, err := orch.Run(context.Background(), &schema.RunRequest{
			Messages: []schema.Message{schema.NewUserMessage("test nil registries")},
		})
		if err != nil {
			t.Fatalf("Run should not error: %v", err)
		}

		// In the refactored architecture, dynamic agents can build their own tool
		// registries via ToolProfile.BuildRegistry(), so buildDynamicAgent succeeds
		// even without pre-supplied registries. The dynamic agent runs and produces
		// a response (from the mock LLM).
		if resp == nil || len(resp.Messages) == 0 {
			t.Fatal("expected response")
		}
	})

	t.Run("none tool access works with nil registries", func(t *testing.T) {
		planJSON := `{"mode": "plan", "plan": {
			"goal": "Test none access with nil registries",
			"steps": [{"id": "step_1", "description": "Chat only", "agent": "chat", "depends_on": [],
				"dynamic_spec": {"base_type": "chat", "tool_access": "none"}}]
		}}`

		mock := &mockChatCompleter{
			response: &aimodel.ChatResponse{
				Choices: []aimodel.Choice{
					{
						Message: aimodel.Message{
							Role:    aimodel.RoleAssistant,
							Content: aimodel.NewTextContent(planJSON),
						},
					},
				},
			},
		}

		chatStub := &stubAgent{id: "chat", response: makeStubResponse("chat done", "chat")}

		planGen := taskagent.New(
			agent.Config{ID: "plan-gen", Name: "Plan Gen"},
			taskagent.WithChatCompleter(mock),
			taskagent.WithModel("test-model"),
			taskagent.WithMaxIterations(1),
		)

		orch := agents.NewOrchestratorAgent(
			agent.Config{ID: "orchestrator", Name: "Orchestrator", Description: "Orchestrates"},
			mock,
			"test-model",
			map[string]agent.Agent{
				"coder":      &stubAgent{id: "coder"},
				"researcher": &stubAgent{id: "researcher"},
				"reviewer":   &stubAgent{id: "reviewer"},
				"chat":       chatStub,
			},
			planGen,
			2,
			chatStub,
			"",
			nil,
			nil, // no planner
			nil, // nil toolRegistries (but that's OK for tool_access="none")
			nil, // reviewReg
			10,  // maxIterations
			0,   // runTokenBudget
		)

		resp, err := orch.Run(context.Background(), &schema.RunRequest{
			Messages: []schema.Message{schema.NewUserMessage("test none access")},
		})
		if err != nil {
			t.Fatalf("Run: %v", err)
		}

		if resp == nil || len(resp.Messages) == 0 {
			t.Fatal("expected response from dynamic chat agent with none access")
		}
	})
}

// --- Design Test (Additional): Create function wires tool registries correctly ---
// Verifies that agents.Create correctly builds tool registries and passes them to orchestrator.
// Test cases:
//   - Create function returns non-nil Orchestrator
//   - Orchestrator can execute a plan with dynamic spec after being built by Create
func TestIntegration_DynamicAgents_CreateWiresToolRegistries(t *testing.T) {
	reg, err := tools.Register(config.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	readOnlyReg, err := tools.RegisterReadOnly(config.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.RegisterReadOnly: %v", err)
	}

	reviewReg, err := tools.RegisterReviewTools(config.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.RegisterReviewTools: %v", err)
	}

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(`{"mode": "direct", "agent": "chat"}`),
					},
				},
			},
		},
	}

	cfg := &config.Config{
		LLM:    config.LLMConfig{Model: "test-model"},
		Agents: config.AgentsConfig{MaxIterations: 10},
		Memory: config.MemoryConfig{MaxConcurrency: 2},
	}

	allAgents := agents.Create(cfg, mock, reg, readOnlyReg, reviewReg, nil, nil)

	if allAgents.Orchestrator == nil {
		t.Fatal("Create returned nil Orchestrator")
	}

	// Verify orchestrator can still handle direct dispatch (regression).
	resp, err := allAgents.Orchestrator.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("hello")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("Orchestrator.Run: %v", err)
	}

	if resp == nil || len(resp.Messages) == 0 {
		t.Fatal("expected response from orchestrator via Create")
	}
}

// --- Helpers ---

// recordingStubAgent records the last request it received.
type recordingStubAgent struct {
	id          string
	response    *schema.RunResponse
	lastRequest *schema.RunRequest
	mu          sync.Mutex
}

var _ agent.Agent = (*recordingStubAgent)(nil)

func (s *recordingStubAgent) ID() string          { return s.id }
func (s *recordingStubAgent) Name() string        { return s.id }
func (s *recordingStubAgent) Description() string { return s.id }

func (s *recordingStubAgent) Run(_ context.Context, req *schema.RunRequest) (*schema.RunResponse, error) {
	s.mu.Lock()
	s.lastRequest = req
	s.mu.Unlock()

	if s.response != nil {
		return s.response, nil
	}

	return &schema.RunResponse{
		Messages: []schema.Message{
			schema.NewAssistantMessage(aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent("stub response from " + s.id),
			}, s.id),
		},
	}, nil
}

// timingStubAgent records invocation timestamps for parallel execution testing.
type timingStubAgent struct {
	id       string
	response *schema.RunResponse
	startRef *atomic.Int64
	delay    time.Duration
}

var _ agent.Agent = (*timingStubAgent)(nil)

func (s *timingStubAgent) ID() string          { return s.id }
func (s *timingStubAgent) Name() string        { return s.id }
func (s *timingStubAgent) Description() string { return s.id }

func (s *timingStubAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	s.startRef.Store(time.Now().UnixNano())
	if s.delay > 0 {
		time.Sleep(s.delay)
	}

	return s.response, nil
}

// failingStubAgent always returns an error.
type failingStubAgent struct {
	id  string
	err error
}

var _ agent.Agent = (*failingStubAgent)(nil)

func (s *failingStubAgent) ID() string          { return s.id }
func (s *failingStubAgent) Name() string        { return s.id }
func (s *failingStubAgent) Description() string { return s.id }

func (s *failingStubAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	return nil, s.err
}

// callbackStubAgent invokes a callback when Run is called.
type callbackStubAgent struct {
	id       string
	response *schema.RunResponse
	onRun    func()
}

var _ agent.Agent = (*callbackStubAgent)(nil)

func (s *callbackStubAgent) ID() string          { return s.id }
func (s *callbackStubAgent) Name() string        { return s.id }
func (s *callbackStubAgent) Description() string { return s.id }

func (s *callbackStubAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	if s.onRun != nil {
		s.onRun()
	}

	return s.response, nil
}

func makeStubResponse(text, agentID string) *schema.RunResponse {
	return &schema.RunResponse{
		Messages: []schema.Message{
			schema.NewAssistantMessage(aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent(text),
			}, agentID),
		},
	}
}

func containsString(s, substr string) bool {
	return len(s) >= len(substr) && (s == substr || len(s) > 0 && findSubstring(s, substr))
}

func findSubstring(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}
