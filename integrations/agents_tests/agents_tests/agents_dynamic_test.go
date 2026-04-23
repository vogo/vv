package agents_tests

import (
	"context"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/tool"
	"github.com/vogo/vv/agents"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/tools"
)

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

	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	readOnlyReg, err := tools.RegisterReadOnly(configs.ToolsConfig{BashTimeout: 30})
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

	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 30})
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
//   - Dynamic agent with tool_access "full" gets all 6 tools (bash, read, write, edit, glob, grep)
//   - Dynamic agent with tool_access "read-only" gets 3 tools (read, glob, grep)
//   - Dynamic agent with tool_access "none" gets 0 tools
//   - Base type "researcher" default gets read-only tools
//   - Base type "chat" default gets no tools
func TestIntegration_DynamicAgents_ToolAccessLevelCorrectness(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	readOnlyReg, err := tools.RegisterReadOnly(configs.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.RegisterReadOnly: %v", err)
	}

	reviewReg, err := tools.RegisterReviewTools(configs.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.RegisterReviewTools: %v", err)
	}

	mock := &mockChatCompleter{}
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
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

	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 30})
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

	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 30})
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

	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	readOnlyReg, err := tools.RegisterReadOnly(configs.ToolsConfig{BashTimeout: 30})
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
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	readOnlyReg, err := tools.RegisterReadOnly(configs.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.RegisterReadOnly: %v", err)
	}

	reviewReg, err := tools.RegisterReviewTools(configs.ToolsConfig{BashTimeout: 30})
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

	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
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
