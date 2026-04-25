package dispatches_tests

import (
	"context"
	"strings"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/prompt"
	"github.com/vogo/vage/tool"
	vvagents "github.com/vogo/vv/agents"
	"github.com/vogo/vv/dispatches"

	"github.com/vogo/vage/schema"
)

// =============================================================================
// Primary Assistant integration tests.
//
// These exercise the full unified-mode pipeline: a real taskagent running on
// a mock LLM that emits tool calls (delegate_to_*, plan_task, or none for a
// direct answer), wired into a Dispatcher configured for unified mode.
// =============================================================================

// primaryToolCallResponse builds an assistant message that invokes a tool by
// name with pre-canned JSON arguments. It mirrors toolCallChatResponse from
// the earlier suite but keeps the distinct call-ID prefix so side-by-side
// test failures stay readable.
func primaryToolCallResponse(name, argsJSON string) *aimodel.ChatResponse {
	return &aimodel.ChatResponse{
		Choices: []aimodel.Choice{
			{
				Message: aimodel.Message{
					Role: aimodel.RoleAssistant,
					ToolCalls: []aimodel.ToolCall{
						{
							ID:   "tc_primary_" + name,
							Type: "function",
							Function: aimodel.FunctionCall{
								Name:      name,
								Arguments: argsJSON,
							},
						},
					},
				},
				FinishReason: aimodel.FinishReasonToolCalls,
			},
		},
	}
}

// primaryTextResponse builds a plain-text assistant message (no tool calls).
// Used when the Primary's final iteration should fold a previous tool result
// into the user-visible response.
func primaryTextResponse(text string) *aimodel.ChatResponse {
	return &aimodel.ChatResponse{
		Choices: []aimodel.Choice{
			{
				Message: aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent(text),
				},
				FinishReason: aimodel.FinishReasonStop,
			},
		},
	}
}

// newPrimaryDispatcher wires a Dispatcher in unified mode with a real
// taskagent Primary backed by the supplied mock LLM. The delegate_to_* and
// plan_task tools are registered against the provided sub-agents and the
// dispatcher itself (the PlanExecutor).
func newPrimaryDispatcher(
	t *testing.T,
	coder, researcher, reviewer, chat agent.Agent,
	mockLLM aimodel.ChatCompleter,
) *dispatches.Dispatcher {
	t.Helper()

	reg := newIntegrationRegistry()
	subAgents := makeSubAgents(map[string]agent.Agent{
		"coder":      coder,
		"researcher": researcher,
		"reviewer":   reviewer,
		"chat":       chat,
	})

	d := dispatches.New(
		reg, subAgents, nil,
		dispatches.WithLLM(mockLLM, "test-model"),
		dispatches.WithFallbackAgent(chat),
	)

	// Build the Primary's tool registry: delegate_to_* + plan_task. We skip
	// the read-only file tools (read/glob/grep) and todo/ask_user here
	// because no test actually drives those; the dispatcher's delegate_to /
	// plan_task plumbing is what this suite covers.
	toolReg := tool.NewRegistry()
	if err := dispatches.RegisterDelegateTools(toolReg, subAgents, []string{"coder", "researcher", "reviewer"}); err != nil {
		t.Fatalf("RegisterDelegateTools: %v", err)
	}
	if err := dispatches.RegisterPlanTaskTool(toolReg, d); err != nil {
		t.Fatalf("RegisterPlanTaskTool: %v", err)
	}

	primary := taskagent.New(
		agent.Config{ID: vvagents.PrimaryAgentID, Name: "Primary Assistant", Description: "test primary"},
		taskagent.WithChatCompleter(mockLLM),
		taskagent.WithModel("test-model"),
		taskagent.WithSystemPrompt(prompt.StringPrompt(vvagents.PrimarySystemPrompt)),
		taskagent.WithToolRegistry(toolReg),
		taskagent.WithMaxIterations(5),
	)

	d.SetPrimaryAssistant(primary)

	return d
}

// TestPrimary_AnswersDirectly confirms the fast-path: Primary LLM returns
// plain text (no tool call), dispatcher returns that text in exactly one
// LLM call, and no sub-agent runs.
func TestPrimary_AnswersDirectly(t *testing.T) {
	coder := &callTrackingAgent{id: "coder"}
	researcher := &callTrackingAgent{id: "researcher"}
	reviewer := &callTrackingAgent{id: "reviewer"}
	chat := &callTrackingAgent{id: "chat"}

	mockLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			primaryTextResponse("Hi there! What can I help with?"),
		},
	}

	d := newPrimaryDispatcher(t, coder, researcher, reviewer, chat, mockLLM)

	resp, err := d.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("hi")},
		SessionID: "m4-direct",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := int(mockLLM.callCount.Load()); got != 1 {
		t.Errorf("LLM call count = %d, want 1 for direct answer path", got)
	}

	for name, a := range map[string]*callTrackingAgent{"coder": coder, "researcher": researcher, "reviewer": reviewer, "chat": chat} {
		if a.called.Load() {
			t.Errorf("%s sub-agent must not run when Primary answered directly", name)
		}
	}

	if len(resp.Messages) == 0 || !strings.Contains(resp.Messages[0].Content.Text(), "Hi there") {
		t.Errorf("unexpected direct-answer response: %+v", resp.Messages)
	}
}

// TestPrimary_DelegatesToCoder exercises the delegate_to_coder tool: round 1
// emits the tool call, the handler runs coder once, round 2 folds the
// coder result into the final assistant text.
func TestPrimary_DelegatesToCoder(t *testing.T) {
	coder := &callTrackingAgent{
		id: "coder",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("wrote fn add() { return a + b }"),
				}, "coder"),
			},
		},
	}
	researcher := &callTrackingAgent{id: "researcher"}
	reviewer := &callTrackingAgent{id: "reviewer"}
	chat := &callTrackingAgent{id: "chat"}

	mockLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			primaryToolCallResponse(dispatches.DelegateToolName("coder"), `{"task":"write add() in add.go"}`),
			primaryTextResponse("Done — coder created add()."),
		},
	}

	d := newPrimaryDispatcher(t, coder, researcher, reviewer, chat, mockLLM)

	resp, err := d.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("write add() in add.go")},
		SessionID: "m4-delegate",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if got := int(mockLLM.callCount.Load()); got != 2 {
		t.Errorf("LLM call count = %d, want 2 (delegate + final) for delegate_to path", got)
	}

	if !coder.called.Load() {
		t.Fatal("coder sub-agent must have been invoked via delegate_to_coder")
	}

	for name, a := range map[string]*callTrackingAgent{"researcher": researcher, "reviewer": reviewer, "chat": chat} {
		if a.called.Load() {
			t.Errorf("%s must not run when only coder was delegated to", name)
		}
	}

	if len(resp.Messages) == 0 || !strings.Contains(resp.Messages[0].Content.Text(), "Done") {
		t.Errorf("unexpected delegated response: %+v", resp.Messages)
	}
}

// TestPrimary_PlanTask drives plan_task → the dispatcher's DAG machinery →
// both coder and researcher stubs → Primary folds the DAG result into its
// final message.
func TestPrimary_PlanTask(t *testing.T) {
	coder := &callTrackingAgent{
		id: "coder",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("coder did step-2"),
				}, "coder"),
			},
		},
	}
	researcher := &callTrackingAgent{
		id: "researcher",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("researcher did step-1"),
				}, "researcher"),
			},
		},
	}
	reviewer := &callTrackingAgent{id: "reviewer"}
	chat := &callTrackingAgent{id: "chat"}

	args := `{"goal":"research+implement","steps":[` +
		`{"id":"s1","description":"research","agent":"researcher","depends_on":[]},` +
		`{"id":"s2","description":"implement","agent":"coder","depends_on":["s1"]}` +
		`]}`

	mockLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			primaryToolCallResponse(dispatches.PrimaryToolPlanTask, args),
			primaryTextResponse("Plan completed."),
		},
	}

	d := newPrimaryDispatcher(t, coder, researcher, reviewer, chat, mockLLM)

	resp, err := d.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("do the thing in two steps")},
		SessionID: "m4-plan",
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if !researcher.called.Load() {
		t.Error("researcher must run in step-1 of the plan")
	}

	if !coder.called.Load() {
		t.Error("coder must run in step-2 of the plan")
	}

	if reviewer.called.Load() {
		t.Error("reviewer must not run when plan did not name it")
	}

	if len(resp.Messages) == 0 || !strings.Contains(resp.Messages[0].Content.Text(), "Plan completed") {
		t.Errorf("unexpected plan response: %+v", resp.Messages)
	}
}

// TestPrimary_NilReturnsError guards the contract: when no Primary is
// attached, the dispatcher must return an error rather than fall back to a
// classical pipeline.
func TestPrimary_NilReturnsError(t *testing.T) {
	coder := &callTrackingAgent{id: "coder"}
	researcher := &callTrackingAgent{id: "researcher"}
	reviewer := &callTrackingAgent{id: "reviewer"}
	chat := &callTrackingAgent{id: "chat"}

	reg := newIntegrationRegistry()
	subAgents := makeSubAgents(map[string]agent.Agent{
		"coder": coder, "researcher": researcher, "reviewer": reviewer, "chat": chat,
	})

	d := dispatches.New(
		reg, subAgents, nil,
		dispatches.WithFallbackAgent(chat),
	)
	// Deliberately NO SetPrimaryAssistant — must error.

	_, err := d.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("hi")},
		SessionID: "m7-nil-primary",
	})
	if err == nil {
		t.Fatal("Run with nil Primary must return an error")
	}

	if !strings.Contains(err.Error(), "primary assistant required") {
		t.Errorf("error = %q, want substring %q", err.Error(), "primary assistant required")
	}
}
