package golden_tests

import (
	"context"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/prompt"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/tool"
	vvagents "github.com/vogo/vv/agents"
	"github.com/vogo/vv/dispatches"
)

// =============================================================================
// M5 Golden Baseline (design doc §4 M5 · M4 result §"M5 衔接建议" #6)
//
// Five cases that exercise the full spectrum the Primary Assistant is meant
// to cover. Each case runs BOTH modes (classical + unified) against the same
// user prompt and compares instrumentation.
//
// Assertion philosophy:
//   - greeting / simple-math: unified MUST strictly beat classical on LLM
//     call count (this is the "1 call vs 2 calls" headline result from the
//     design doc). Any future change that narrows this gap surfaces here.
//   - simple-read / simple-edit / multi-step: structural parity. We assert
//     the right sub-agents were invoked in each path, not relative cost,
//     because delegation/planning on the unified path adds one Primary
//     decision round that would otherwise make the comparison noisy.
// =============================================================================

// countedAgent wraps any agent.Agent so a test can measure how many times it
// was invoked without caring whether the underlying agent is a stub or a real
// taskagent.
type countedAgent struct {
	id       string
	runCount atomic.Int32
	inner    agent.Agent
}

var _ agent.Agent = (*countedAgent)(nil)

func (c *countedAgent) ID() string          { return c.id }
func (c *countedAgent) Name() string        { return c.id }
func (c *countedAgent) Description() string { return c.id }

func (c *countedAgent) Run(ctx context.Context, req *schema.RunRequest) (*schema.RunResponse, error) {
	c.runCount.Add(1)

	return c.inner.Run(ctx, req)
}

// newCountedStub wraps a stub agent.
func newCountedStub(id string, canned *schema.RunResponse) *countedAgent {
	inner := &callTrackingAgent{id: id, response: canned}

	return &countedAgent{id: id, inner: inner}
}

// newCountedChat wraps a real chat taskagent so classical-path tests can see
// chat's own LLM round-trip.
func newCountedChat(llm aimodel.ChatCompleter) *countedAgent {
	inner := taskagent.New(
		agent.Config{ID: "chat", Name: "Chat", Description: "golden chat"},
		taskagent.WithChatCompleter(llm),
		taskagent.WithModel("test-model"),
		taskagent.WithSystemPrompt(prompt.StringPrompt(vvagents.FallbackChatPrompt)),
		taskagent.WithMaxIterations(1),
	)

	return &countedAgent{id: "chat", inner: inner}
}

// modeResult captures one end-to-end run's instrumentation.
type modeResult struct {
	mode         string
	llmCalls     int
	subAgentHits map[string]int
	response     *schema.RunResponse
}

func buildHits(subs map[string]*countedAgent) map[string]int {
	out := make(map[string]int, len(subs))
	for id, a := range subs {
		out[id] = int(a.runCount.Load())
	}

	return out
}

func buildAgentMap(subs map[string]*countedAgent) map[string]agent.Agent {
	out := make(map[string]agent.Agent, len(subs))
	for id, a := range subs {
		out[id] = a
	}

	return out
}

// runClassical runs the user prompt through a classical (intent+execute)
// dispatcher backed by the supplied mock LLM.
func runClassical(t *testing.T, userMsg string, llm *sequentialMockLLM, subs map[string]*countedAgent) *modeResult {
	t.Helper()

	reg := newGoldenRegistry()
	agentsMap := buildAgentMap(subs)

	d := dispatches.New(
		reg, agentsMap, nil, nil, nil,
		dispatches.WithLLM(llm, "test-model"),
		dispatches.WithFallbackAgent(agentsMap["chat"]),
		dispatches.WithIntentSystemPrompt(dispatches.BuildIntentSystemPrompt(reg)),
		dispatches.WithFastPath(dispatches.DisabledFastPathConfig()),
		dispatches.WithSummaryPolicy(dispatches.SummaryNever),
	)

	resp, err := d.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage(userMsg)},
		SessionID: "golden-classical",
	})
	if err != nil {
		t.Fatalf("classical Run: %v", err)
	}

	return &modeResult{
		mode:         "classical",
		llmCalls:     int(llm.callCount.Load()),
		subAgentHits: buildHits(subs),
		response:     resp,
	}
}

// runUnified runs the user prompt through a unified dispatcher with a real
// Primary Assistant taskagent; sub-agents are reached via delegate_to_* /
// plan_task.
func runUnified(t *testing.T, userMsg string, llm *sequentialMockLLM, subs map[string]*countedAgent) *modeResult {
	t.Helper()

	reg := newGoldenRegistry()
	agentsMap := buildAgentMap(subs)

	d := dispatches.New(
		reg, agentsMap, nil, nil, nil,
		dispatches.WithLLM(llm, "test-model"),
		dispatches.WithFallbackAgent(agentsMap["chat"]),
		dispatches.WithFastPath(dispatches.DisabledFastPathConfig()),
		dispatches.WithSummaryPolicy(dispatches.SummaryNever),
	)

	toolReg := tool.NewRegistry()
	if err := dispatches.RegisterDelegateTools(toolReg, agentsMap, []string{"coder", "researcher", "reviewer"}); err != nil {
		t.Fatalf("RegisterDelegateTools: %v", err)
	}

	if err := dispatches.RegisterPlanTaskTool(toolReg, d); err != nil {
		t.Fatalf("RegisterPlanTaskTool: %v", err)
	}

	primary := taskagent.New(
		agent.Config{ID: vvagents.PrimaryAgentID, Name: "Primary Assistant", Description: "golden primary"},
		taskagent.WithChatCompleter(llm),
		taskagent.WithModel("test-model"),
		taskagent.WithSystemPrompt(prompt.StringPrompt(vvagents.PrimarySystemPrompt)),
		taskagent.WithToolRegistry(toolReg),
		taskagent.WithMaxIterations(5),
	)

	d.SetPrimaryAssistant(primary)

	resp, err := d.Run(context.Background(), &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage(userMsg)},
		SessionID: "golden-unified",
	})
	if err != nil {
		t.Fatalf("unified Run: %v", err)
	}

	return &modeResult{
		mode:         "unified",
		llmCalls:     int(llm.callCount.Load()),
		subAgentHits: buildHits(subs),
		response:     resp,
	}
}

func stubResponse(agentID, text string) *schema.RunResponse {
	return &schema.RunResponse{
		Messages: []schema.Message{
			schema.NewAssistantMessage(aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent(text),
			}, agentID),
		},
	}
}

// =============================================================================
// Case 1 — Greeting ("hello")
//
// Classical: intent LLM → {direct, chat} → real chat taskagent replies.
//   = 2 LLM calls.
// Unified:   Primary answers inline, no tool call.
//   = 1 LLM call.
// =============================================================================

func TestGolden_Greeting_Hello(t *testing.T) {
	classicalLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			intentJSONResponse(`{"needs_exploration": false, "mode": "direct", "agent": "chat"}`),
			primaryTextResponse("Hello! How can I help you today?"),
		},
	}

	unifiedLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			primaryTextResponse("Hello! What can I help with?"),
		},
	}

	classicalSubs := map[string]*countedAgent{
		"coder":      newCountedStub("coder", nil),
		"researcher": newCountedStub("researcher", nil),
		"reviewer":   newCountedStub("reviewer", nil),
		"chat":       newCountedChat(classicalLLM),
	}
	classical := runClassical(t, "hello", classicalLLM, classicalSubs)

	unifiedSubs := map[string]*countedAgent{
		"coder":      newCountedStub("coder", nil),
		"researcher": newCountedStub("researcher", nil),
		"reviewer":   newCountedStub("reviewer", nil),
		"chat":       newCountedStub("chat", nil),
	}
	unified := runUnified(t, "hello", unifiedLLM, unifiedSubs)

	t.Logf("greeting: classical llmCalls=%d, unified llmCalls=%d", classical.llmCalls, unified.llmCalls)

	if classical.llmCalls != 2 {
		t.Errorf("classical greeting: llmCalls = %d, want 2 (intent + chat)", classical.llmCalls)
	}

	if unified.llmCalls != 1 {
		t.Errorf("unified greeting: llmCalls = %d, want 1 (Primary direct answer)", unified.llmCalls)
	}

	if unified.llmCalls >= classical.llmCalls {
		t.Errorf("unified must beat classical on greeting LLM count: unified=%d, classical=%d",
			unified.llmCalls, classical.llmCalls)
	}

	if len(classical.response.Messages) == 0 || !strings.Contains(classical.response.Messages[0].Content.Text(), "Hello") {
		t.Errorf("classical greeting response missing: %+v", classical.response.Messages)
	}

	if len(unified.response.Messages) == 0 || !strings.Contains(unified.response.Messages[0].Content.Text(), "Hello") {
		t.Errorf("unified greeting response missing: %+v", unified.response.Messages)
	}
}

// =============================================================================
// Case 2 — Simple math ("calc 5^6")
// =============================================================================

func TestGolden_SimpleMath_Calc(t *testing.T) {
	classicalLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			intentJSONResponse(`{"needs_exploration": false, "mode": "direct", "agent": "chat"}`),
			primaryTextResponse("5^6 = 15625"),
		},
	}

	unifiedLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			primaryTextResponse("5^6 = 15625"),
		},
	}

	classicalSubs := map[string]*countedAgent{
		"coder":      newCountedStub("coder", nil),
		"researcher": newCountedStub("researcher", nil),
		"reviewer":   newCountedStub("reviewer", nil),
		"chat":       newCountedChat(classicalLLM),
	}
	classical := runClassical(t, "calc 5^6", classicalLLM, classicalSubs)

	unifiedSubs := map[string]*countedAgent{
		"coder":      newCountedStub("coder", nil),
		"researcher": newCountedStub("researcher", nil),
		"reviewer":   newCountedStub("reviewer", nil),
		"chat":       newCountedStub("chat", nil),
	}
	unified := runUnified(t, "calc 5^6", unifiedLLM, unifiedSubs)

	t.Logf("simple-math: classical llmCalls=%d, unified llmCalls=%d", classical.llmCalls, unified.llmCalls)

	if unified.llmCalls >= classical.llmCalls {
		t.Errorf("unified simple-math must beat classical: unified=%d, classical=%d",
			unified.llmCalls, classical.llmCalls)
	}

	if !strings.Contains(classical.response.Messages[0].Content.Text(), "15625") {
		t.Errorf("classical simple-math answer missing 15625: %q",
			classical.response.Messages[0].Content.Text())
	}

	if !strings.Contains(unified.response.Messages[0].Content.Text(), "15625") {
		t.Errorf("unified simple-math answer missing 15625: %q",
			unified.response.Messages[0].Content.Text())
	}
}

// =============================================================================
// Case 3 — Simple read ("summarize foo.go")
//
// Classical: intent → researcher stub (1 LLM call + stub).
// Unified:   Primary answers inline (1 LLM call, no sub-agent).
// =============================================================================

func TestGolden_SimpleRead_ExplainFile(t *testing.T) {
	classicalLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			intentJSONResponse(`{"needs_exploration": false, "mode": "direct", "agent": "researcher"}`),
		},
	}

	unifiedLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			primaryTextResponse(`foo.go defines Foo() returning "foo".`),
		},
	}

	classicalSubs := map[string]*countedAgent{
		"coder":      newCountedStub("coder", nil),
		"researcher": newCountedStub("researcher", stubResponse("researcher", "foo.go: exports Foo()")),
		"reviewer":   newCountedStub("reviewer", nil),
		"chat":       newCountedStub("chat", nil),
	}
	classical := runClassical(t, "summarize foo.go", classicalLLM, classicalSubs)

	unifiedSubs := map[string]*countedAgent{
		"coder":      newCountedStub("coder", nil),
		"researcher": newCountedStub("researcher", nil),
		"reviewer":   newCountedStub("reviewer", nil),
		"chat":       newCountedStub("chat", nil),
	}
	unified := runUnified(t, "summarize foo.go", unifiedLLM, unifiedSubs)

	t.Logf("simple-read: classical llmCalls=%d researcher=%d; unified llmCalls=%d",
		classical.llmCalls, classical.subAgentHits["researcher"], unified.llmCalls)

	if classical.subAgentHits["researcher"] != 1 {
		t.Errorf("classical simple-read: researcher hits = %d, want 1", classical.subAgentHits["researcher"])
	}

	for _, id := range []string{"coder", "reviewer"} {
		if classical.subAgentHits[id] != 0 {
			t.Errorf("classical simple-read: %s unexpectedly invoked (%d)", id, classical.subAgentHits[id])
		}
	}

	for _, id := range []string{"coder", "researcher", "reviewer", "chat"} {
		if unified.subAgentHits[id] != 0 {
			t.Errorf("unified simple-read: %s unexpectedly invoked (%d) — Primary should have answered inline",
				id, unified.subAgentHits[id])
		}
	}

	if unified.llmCalls != 1 {
		t.Errorf("unified simple-read: llmCalls = %d, want 1 (Primary inline answer)", unified.llmCalls)
	}
}

// =============================================================================
// Case 4 — Simple edit ("add comment to main.go")
//
// Classical: intent → coder (stub).
// Unified:   Primary tool-calls delegate_to_coder, folds result into final.
// Unified uses MORE LLM calls here — the honest overhead of the Primary
// decision round when delegation is required. We assert structural
// correctness.
// =============================================================================

func TestGolden_SimpleEdit_DelegateToCoder(t *testing.T) {
	classicalLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			intentJSONResponse(`{"needs_exploration": false, "mode": "direct", "agent": "coder"}`),
		},
	}

	unifiedLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			primaryToolCallResponse(dispatches.DelegateToolName("coder"),
				`{"task":"add a comment to main.go"}`),
			primaryTextResponse("Done — coder added the requested comment."),
		},
	}

	classicalSubs := map[string]*countedAgent{
		"coder":      newCountedStub("coder", stubResponse("coder", "coder: added comment")),
		"researcher": newCountedStub("researcher", nil),
		"reviewer":   newCountedStub("reviewer", nil),
		"chat":       newCountedStub("chat", nil),
	}
	classical := runClassical(t, "add a comment to main.go", classicalLLM, classicalSubs)

	unifiedSubs := map[string]*countedAgent{
		"coder":      newCountedStub("coder", stubResponse("coder", "coder: added comment")),
		"researcher": newCountedStub("researcher", nil),
		"reviewer":   newCountedStub("reviewer", nil),
		"chat":       newCountedStub("chat", nil),
	}
	unified := runUnified(t, "add a comment to main.go", unifiedLLM, unifiedSubs)

	t.Logf("simple-edit: classical coder=%d llmCalls=%d; unified coder=%d llmCalls=%d",
		classical.subAgentHits["coder"], classical.llmCalls,
		unified.subAgentHits["coder"], unified.llmCalls)

	if classical.subAgentHits["coder"] != 1 {
		t.Errorf("classical simple-edit: coder hits = %d, want 1", classical.subAgentHits["coder"])
	}

	if unified.subAgentHits["coder"] != 1 {
		t.Errorf("unified simple-edit: coder hits = %d, want 1 (delegate_to_coder)", unified.subAgentHits["coder"])
	}

	for _, id := range []string{"researcher", "reviewer", "chat"} {
		if unified.subAgentHits[id] != 0 {
			t.Errorf("unified simple-edit: %s unexpectedly invoked (%d)", id, unified.subAgentHits[id])
		}
	}

	if !strings.Contains(unified.response.Messages[0].Content.Text(), "Done") {
		t.Errorf("unified simple-edit: final message missing 'Done': %q",
			unified.response.Messages[0].Content.Text())
	}
}

// =============================================================================
// Case 5 — Multi-step refactor ("refactor X: research then implement")
//
// Classical: intent returns plan{researcher→coder}, DAG runs both.
// Unified:   Primary tool-calls plan_task with the same DAG.
// =============================================================================

func TestGolden_MultiStepRefactor_Plan(t *testing.T) {
	classicalLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			intentJSONResponse(`{
				"needs_exploration": false,
				"mode": "plan",
				"plan": {
					"goal": "research and implement",
					"steps": [
						{"id": "s1", "description": "research", "agent": "researcher", "depends_on": []},
						{"id": "s2", "description": "implement", "agent": "coder", "depends_on": ["s1"]}
					]
				}
			}`),
		},
	}

	unifiedLLM := &sequentialMockLLM{
		responses: []*aimodel.ChatResponse{
			primaryToolCallResponse(dispatches.PrimaryToolPlanTask, `{
				"goal": "research and implement",
				"steps": [
					{"id": "s1", "description": "research", "agent": "researcher", "depends_on": []},
					{"id": "s2", "description": "implement", "agent": "coder", "depends_on": ["s1"]}
				]
			}`),
			primaryTextResponse("Plan completed: researched, then implemented."),
		},
	}

	mkSubs := func() map[string]*countedAgent {
		return map[string]*countedAgent{
			"coder":      newCountedStub("coder", stubResponse("coder", "coder: implemented")),
			"researcher": newCountedStub("researcher", stubResponse("researcher", "researcher: findings")),
			"reviewer":   newCountedStub("reviewer", nil),
			"chat":       newCountedStub("chat", nil),
		}
	}

	classicalSubs := mkSubs()
	classical := runClassical(t, "refactor X: research then implement", classicalLLM, classicalSubs)

	unifiedSubs := mkSubs()
	unified := runUnified(t, "refactor X: research then implement", unifiedLLM, unifiedSubs)

	t.Logf("multi-step: classical R=%d C=%d Rev=%d; unified R=%d C=%d Rev=%d",
		classical.subAgentHits["researcher"], classical.subAgentHits["coder"], classical.subAgentHits["reviewer"],
		unified.subAgentHits["researcher"], unified.subAgentHits["coder"], unified.subAgentHits["reviewer"])

	for mode, r := range map[string]*modeResult{"classical": classical, "unified": unified} {
		if r.subAgentHits["researcher"] != 1 {
			t.Errorf("%s multi-step: researcher hits = %d, want 1", mode, r.subAgentHits["researcher"])
		}
		if r.subAgentHits["coder"] != 1 {
			t.Errorf("%s multi-step: coder hits = %d, want 1", mode, r.subAgentHits["coder"])
		}
		if r.subAgentHits["reviewer"] != 0 {
			t.Errorf("%s multi-step: reviewer should NOT run (plan did not name it), got %d",
				mode, r.subAgentHits["reviewer"])
		}
	}
}
