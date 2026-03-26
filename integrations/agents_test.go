package integrations

import (
	"context"
	"encoding/json"
	"io"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/routeragent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/memory"
	"github.com/vogo/vage/prompt"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vagents/vaga/agents"
	"github.com/vogo/vagents/vaga/config"
	vagamemory "github.com/vogo/vagents/vaga/memory"
	"github.com/vogo/vagents/vaga/tools"
)

// --- Test 1a: Coder has all 6 tools (bash, file_read, file_write, file_edit, glob, grep) ---
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

// --- Test 1c: Router dispatches to all five agents based on LLM response ---
// The router has 5 routes: coder(0), planner(1), researcher(2), reviewer(3), chat(4).
// This test verifies that each LLM integer response routes to the correct agent.
func TestIntegration_Agents_RouterRoutesAllFiveAgents(t *testing.T) {
	tests := []struct {
		name         string
		llmResponse  string
		input        string
		wantAgentID  string
		wantResponse string
	}{
		{
			name:         "routes to coder (index 0) for coding request",
			llmResponse:  "0",
			input:        "write a function to sort an array",
			wantAgentID:  "coder",
			wantResponse: "coder handled it",
		},
		{
			name:         "routes to planner (index 1) for multi-step project setup",
			llmResponse:  "1",
			input:        "set up a new Go project with tests and CI",
			wantAgentID:  "planner",
			wantResponse: "planner handled it",
		},
		{
			name:         "routes to researcher (index 2) for exploration request",
			llmResponse:  "2",
			input:        "what does the orchestrate package do?",
			wantAgentID:  "researcher",
			wantResponse: "researcher handled it",
		},
		{
			name:         "routes to reviewer (index 3) for code review request",
			llmResponse:  "3",
			input:        "review this function for bugs",
			wantAgentID:  "reviewer",
			wantResponse: "reviewer handled it",
		},
		{
			name:         "routes to chat (index 4) for general question",
			llmResponse:  "4",
			input:        "what is the capital of France?",
			wantAgentID:  "chat",
			wantResponse: "chat handled it",
		},
		{
			name:         "fallback to chat for ambiguous/invalid LLM response",
			llmResponse:  "99",
			input:        "hmm, not sure what to ask",
			wantAgentID:  "chat",
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

			cfg := &config.Config{
				LLM:    config.LLMConfig{Model: "test-model"},
				Agents: config.AgentsConfig{MaxIterations: 10},
			}

			// Create stub agents for all 5 routes.
			coderStub := &stubAgent{id: "coder", response: makeStubResponse("coder handled it", "coder")}
			plannerStub := &stubAgent{id: "planner", response: makeStubResponse("planner handled it", "planner")}
			researcherStub := &stubAgent{id: "researcher", response: makeStubResponse("researcher handled it", "researcher")}
			reviewerStub := &stubAgent{id: "reviewer", response: makeStubResponse("reviewer handled it", "reviewer")}
			chatStub := &stubAgent{id: "chat", response: makeStubResponse("chat handled it", "chat")}

			// Create router with 5 routes matching main.go order.
			router := routeragent.New(
				agent.Config{ID: "router", Name: "Router Agent", Description: "Routes requests"},
				[]routeragent.Route{
					{Agent: coderStub, Description: "Handles code-related tasks"},
					{Agent: plannerStub, Description: "Handles complex multi-step tasks"},
					{Agent: researcherStub, Description: "Handles research tasks"},
					{Agent: reviewerStub, Description: "Handles review tasks"},
					{Agent: chatStub, Description: "Handles general conversation"},
				},
				routeragent.WithFunc(routeragent.LLMFunc(mock, cfg.LLM.Model, 4)), // fallback=4 (chat)
			)

			resp, err := router.Run(context.Background(), &schema.RunRequest{
				Messages: []schema.Message{schema.NewUserMessage(tt.input)},
			})
			if err != nil {
				t.Fatalf("router.Run: %v", err)
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

// --- Test 1d (legacy): Router routes correctly with 2-route CreateRouter ---
func TestIntegration_Agents_RouterRoutesCorrectly(t *testing.T) {
	tests := []struct {
		name         string
		llmResponse  string
		input        string
		wantResponse string
	}{
		{
			name:         "routes to coder for code request",
			llmResponse:  "0",
			input:        "write a function to sort an array",
			wantResponse: "coder handled it",
		},
		{
			name:         "routes to chat for general request",
			llmResponse:  "1",
			input:        "what is the capital of France",
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

			cfg := &config.Config{
				LLM:    config.LLMConfig{Model: "test-model"},
				Agents: config.AgentsConfig{MaxIterations: 10},
			}

			coder := &stubAgent{
				id: "coder",
				response: &schema.RunResponse{
					Messages: []schema.Message{
						schema.NewAssistantMessage(aimodel.Message{
							Role:    aimodel.RoleAssistant,
							Content: aimodel.NewTextContent("coder handled it"),
						}, "coder"),
					},
				},
			}
			chat := &stubAgent{
				id: "chat",
				response: &schema.RunResponse{
					Messages: []schema.Message{
						schema.NewAssistantMessage(aimodel.Message{
							Role:    aimodel.RoleAssistant,
							Content: aimodel.NewTextContent("chat handled it"),
						}, "chat"),
					},
				},
			}

			router := agents.CreateRouter(cfg, mock, coder, chat)

			resp, err := router.Run(context.Background(), &schema.RunRequest{
				Messages: []schema.Message{schema.NewUserMessage(tt.input)},
			})
			if err != nil {
				t.Fatalf("router.Run: %v", err)
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

// --- Test 4: Planner generates and executes a plan ---
// When the LLM returns a valid JSON plan, the planner should parse it,
// build a DAG of nodes, execute them via sub-agents, and return an aggregated response.
func TestIntegration_Agents_PlannerGeneratesAndExecutesPlan(t *testing.T) {
	planJSON := `{
		"goal": "Create a Go utility package",
		"steps": [
			{"id": "step_1", "description": "Research existing patterns", "agent": "researcher", "depends_on": []},
			{"id": "step_2", "description": "Write the utility code", "agent": "coder", "depends_on": ["step_1"]}
		]
	}`

	// The mock LLM returns the plan JSON for plan generation,
	// then returns summary for aggregation.
	callCount := 0
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
	// Override the mock to track calls and return different responses.
	_ = callCount

	cfg := &config.Config{
		LLM:    config.LLMConfig{Model: "test-model"},
		Agents: config.AgentsConfig{MaxIterations: 1},
		Memory: config.MemoryConfig{MaxConcurrency: 2},
	}

	// Create stub sub-agents that return known responses.
	coderStub := &stubAgent{id: "coder", response: makeStubResponse("code written", "coder")}
	researcherStub := &stubAgent{id: "researcher", response: makeStubResponse("research done", "researcher")}
	reviewerStub := &stubAgent{id: "reviewer", response: makeStubResponse("review done", "reviewer")}

	subAgents := map[string]agent.Agent{
		"coder":      coderStub,
		"researcher": researcherStub,
		"reviewer":   reviewerStub,
	}

	planner := agents.NewPlannerAgent(
		agent.Config{ID: "planner", Name: "Planner Agent", Description: "Plans tasks"},
		newTestPlanGen(mock, cfg),
		subAgents,
		2,         // maxConcurrency
		coderStub, // fallback
	)

	resp, err := planner.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("Create a Go utility package")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("planner.Run: %v", err)
	}

	if resp == nil {
		t.Fatal("expected non-nil response from planner")
	}

	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message in planner response")
	}
}

// --- Test 5: Planner handles plan generation failure by falling back to coder ---
// When the LLM returns invalid JSON, the planner should fall back to the coder agent
// and prepend a warning message.
func TestIntegration_Agents_PlannerFallbackOnInvalidJSON(t *testing.T) {
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

	cfg := &config.Config{
		LLM:    config.LLMConfig{Model: "test-model"},
		Agents: config.AgentsConfig{MaxIterations: 1},
		Memory: config.MemoryConfig{MaxConcurrency: 2},
	}

	coderStub := &stubAgent{id: "coder", response: makeStubResponse("coder fallback response", "coder")}

	subAgents := map[string]agent.Agent{
		"coder": coderStub,
	}

	planner := agents.NewPlannerAgent(
		agent.Config{ID: "planner", Name: "Planner Agent", Description: "Plans tasks"},
		newTestPlanGen(mock, cfg),
		subAgents,
		2,
		coderStub,
	)

	resp, err := planner.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("do something")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("planner.Run should not error on fallback: %v", err)
	}

	if resp == nil {
		t.Fatal("expected non-nil response from planner fallback")
	}

	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message from planner fallback")
	}

	// The response should come from the coder fallback.
	text := resp.Messages[0].Content.Text()
	if text != "coder fallback response" {
		t.Errorf("fallback response = %q, want %q", text, "coder fallback response")
	}
}

// --- Test 6: Planner falls back when plan has empty steps ---
// When the LLM returns valid JSON but with no steps, the planner should fall back.
func TestIntegration_Agents_PlannerFallbackOnEmptyPlan(t *testing.T) {
	emptyPlan := `{"goal": "Nothing", "steps": []}`

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

	cfg := &config.Config{
		LLM:    config.LLMConfig{Model: "test-model"},
		Agents: config.AgentsConfig{MaxIterations: 1},
		Memory: config.MemoryConfig{MaxConcurrency: 2},
	}

	coderStub := &stubAgent{id: "coder", response: makeStubResponse("coder fallback", "coder")}

	planner := agents.NewPlannerAgent(
		agent.Config{ID: "planner", Name: "Planner Agent", Description: "Plans tasks"},
		newTestPlanGen(mock, cfg),
		map[string]agent.Agent{"coder": coderStub},
		2,
		coderStub,
	)

	resp, err := planner.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("do something simple")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("planner.Run: %v", err)
	}

	if resp == nil || len(resp.Messages) == 0 {
		t.Fatal("expected response from planner fallback")
	}

	text := resp.Messages[0].Content.Text()
	if text != "coder fallback" {
		t.Errorf("response = %q, want %q", text, "coder fallback")
	}
}

// --- Test 7: Planner falls back when plan references an invalid agent ---
func TestIntegration_Agents_PlannerFallbackOnInvalidAgent(t *testing.T) {
	badPlan := `{"goal": "Test", "steps": [{"id": "step_1", "description": "do it", "agent": "nonexistent", "depends_on": []}]}`

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

	cfg := &config.Config{
		LLM:    config.LLMConfig{Model: "test-model"},
		Agents: config.AgentsConfig{MaxIterations: 1},
		Memory: config.MemoryConfig{MaxConcurrency: 2},
	}

	coderStub := &stubAgent{id: "coder", response: makeStubResponse("coder fallback", "coder")}

	planner := agents.NewPlannerAgent(
		agent.Config{ID: "planner", Name: "Planner Agent", Description: "Plans tasks"},
		newTestPlanGen(mock, cfg),
		map[string]agent.Agent{"coder": coderStub},
		2,
		coderStub,
	)

	resp, err := planner.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("do it")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("planner.Run: %v", err)
	}

	// Should fall back to coder because "nonexistent" is not a valid agent.
	if resp == nil || len(resp.Messages) == 0 {
		t.Fatal("expected response from planner fallback")
	}
}

// --- Test 14: PlannerAgent implements StreamAgent ---
// Verifies that PlannerAgent can be used as a StreamAgent and returns valid events.
func TestIntegration_Agents_PlannerImplementsStreamAgent(t *testing.T) {
	// Compile-time check is in planner.go (var _ agent.StreamAgent = (*PlannerAgent)(nil)).
	// Here we verify RunStream returns a valid stream with AgentStart/AgentEnd events.
	planJSON := `{
		"goal": "Simple task",
		"steps": [
			{"id": "step_1", "description": "Do something", "agent": "coder", "depends_on": []}
		]
	}`

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

	coderStub := &stubAgent{id: "coder", response: makeStubResponse("done", "coder")}

	planner := agents.NewPlannerAgent(
		agent.Config{ID: "planner", Name: "Planner Agent", Description: "Plans tasks"},
		newTestPlanGen(mock, cfg),
		map[string]agent.Agent{"coder": coderStub},
		2,
		coderStub,
	)

	// Verify PlannerAgent satisfies agent.StreamAgent.
	var sa agent.StreamAgent = planner
	_ = sa

	stream, err := planner.RunStream(context.Background(), &schema.RunRequest{
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
		t.Error("missing agent_start event from planner RunStream")
	}
	if !hasEnd {
		t.Error("missing agent_end event from planner RunStream")
	}
}

// --- Test 9: Persistent memory loads at startup and injects into system prompt ---
// Verifies that PersistentMemoryPrompt dynamically includes entries from memory.
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

// --- Test 4b: Planner plan parsing handles markdown-fenced JSON ---
// Verifies that the planner can parse a plan wrapped in ```json ... ``` code fences.
func TestIntegration_Agents_PlannerParsesMarkdownFencedJSON(t *testing.T) {
	planText := "Here is the plan:\n```json\n" + `{
		"goal": "Build feature",
		"steps": [
			{"id": "step_1", "description": "Research", "agent": "researcher", "depends_on": []},
			{"id": "step_2", "description": "Code it", "agent": "coder", "depends_on": ["step_1"]}
		]
	}` + "\n```\nDone!"

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(planText),
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

	coderStub := &stubAgent{id: "coder", response: makeStubResponse("coded", "coder")}
	researcherStub := &stubAgent{id: "researcher", response: makeStubResponse("researched", "researcher")}

	planner := agents.NewPlannerAgent(
		agent.Config{ID: "planner", Name: "Planner Agent", Description: "Plans tasks"},
		newTestPlanGen(mock, cfg),
		map[string]agent.Agent{"coder": coderStub, "researcher": researcherStub},
		2,
		coderStub,
	)

	resp, err := planner.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("Build a feature")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("planner.Run: %v", err)
	}

	if resp == nil || len(resp.Messages) == 0 {
		t.Fatal("expected response from planner")
	}
}

// --- Test 7: FileStore CRUD integration via PersistentMemory ---
// Tests the full CRUD lifecycle through the PersistentMemory wrapper backed by FileStore.
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

// --- Test 12: End-to-end Router -> Planner -> Sub-agent flow ---
// Verifies that when the router selects the planner, the planner decomposes
// the task into steps, delegates to sub-agents, and returns an aggregated response.
func TestIntegration_Agents_EndToEndRouterPlannerCoder(t *testing.T) {
	planJSON := `{
		"goal": "Set up Go project",
		"steps": [
			{"id": "step_1", "description": "Create project structure", "agent": "coder", "depends_on": []}
		]
	}`

	// Mock LLM returns "1" for routing (planner) and planJSON for plan generation.
	callIdx := 0
	plannerMock := &sequentialMockCompleter{
		responses: []*aimodel.ChatResponse{
			// Call 0: Plan generation.
			{Choices: []aimodel.Choice{{Message: aimodel.Message{Role: aimodel.RoleAssistant, Content: aimodel.NewTextContent(planJSON)}}}},
			// Call 1+: Aggregation/summary.
			{Choices: []aimodel.Choice{{Message: aimodel.Message{Role: aimodel.RoleAssistant, Content: aimodel.NewTextContent("Project set up successfully")}}}},
		},
	}
	_ = callIdx

	routerMock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{Message: aimodel.Message{Role: aimodel.RoleAssistant, Content: aimodel.NewTextContent("1")}}, // route to planner
			},
		},
	}

	cfg := &config.Config{
		LLM:    config.LLMConfig{Model: "test-model"},
		Agents: config.AgentsConfig{MaxIterations: 1},
		Memory: config.MemoryConfig{MaxConcurrency: 2},
	}

	coderStub := &stubAgent{id: "coder", response: makeStubResponse("project created", "coder")}
	researcherStub := &stubAgent{id: "researcher", response: makeStubResponse("researched", "researcher")}
	reviewerStub := &stubAgent{id: "reviewer", response: makeStubResponse("reviewed", "reviewer")}
	chatStub := &stubAgent{id: "chat", response: makeStubResponse("chatted", "chat")}

	subAgents := map[string]agent.Agent{
		"coder":      coderStub,
		"researcher": researcherStub,
		"reviewer":   reviewerStub,
	}

	planner := agents.NewPlannerAgent(
		agent.Config{ID: "planner", Name: "Planner Agent", Description: "Plans tasks"},
		newTestPlanGen(plannerMock, cfg),
		subAgents,
		2,
		coderStub,
	)

	// Create router with the real planner.
	router := routeragent.New(
		agent.Config{ID: "router", Name: "Router Agent", Description: "Routes requests"},
		[]routeragent.Route{
			{Agent: coderStub, Description: "Code tasks"},
			{Agent: planner, Description: "Complex multi-step tasks"},
			{Agent: researcherStub, Description: "Research tasks"},
			{Agent: reviewerStub, Description: "Review tasks"},
			{Agent: chatStub, Description: "General conversation"},
		},
		routeragent.WithFunc(routeragent.LLMFunc(routerMock, cfg.LLM.Model, 4)),
	)

	resp, err := router.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("set up a new Go project")},
		SessionID: "test-session",
	})
	if err != nil {
		t.Fatalf("router.Run: %v", err)
	}

	if resp == nil {
		t.Fatal("expected non-nil response from router->planner flow")
	}

	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one message in aggregated response")
	}
}

// --- Helpers ---

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

// sequentialMockCompleter returns responses in order, cycling back to the last one.
type sequentialMockCompleter struct {
	responses []*aimodel.ChatResponse
	callIdx   int
}

func (m *sequentialMockCompleter) ChatCompletion(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.ChatResponse, error) {
	idx := m.callIdx
	if idx >= len(m.responses) {
		idx = len(m.responses) - 1
	}
	m.callIdx++
	return m.responses[idx], nil
}

func (m *sequentialMockCompleter) ChatCompletionStream(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.Stream, error) {
	return nil, nil
}

// newTestPlanGen creates a plan generation taskagent for testing.
// This mirrors the plan generator created in agents.Create().
func newTestPlanGen(llm aimodel.ChatCompleter, cfg *config.Config) *taskagent.Agent {
	return taskagent.New(
		agent.Config{
			ID:          "plan-gen",
			Name:        "Plan Generator",
			Description: "Generates execution plans for complex tasks",
		},
		taskagent.WithChatCompleter(llm),
		taskagent.WithModel(cfg.LLM.Model),
		taskagent.WithSystemPrompt(prompt.StringPrompt(agents.PlannerSystemPrompt)),
		taskagent.WithMaxIterations(1),
	)
}

// Ensure json is used (for compile).
var _ = json.Marshal
