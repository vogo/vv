package agents

import (
	"context"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/orchestrate"
	"github.com/vogo/vage/schema"
)

func TestPlannerAgent_ImplementsInterfaces(t *testing.T) {
	// Compile-time check already in planner.go, but verify at runtime.
	var _ agent.Agent = (*PlannerAgent)(nil)
	var _ agent.StreamAgent = (*PlannerAgent)(nil)
}

func TestParsePlan_Valid(t *testing.T) {
	jsonPlan := `{
		"goal": "Set up a Go project",
		"steps": [
			{"id": "step_1", "description": "Create project structure", "agent": "coder", "depends_on": []},
			{"id": "step_2", "description": "Add tests", "agent": "coder", "depends_on": ["step_1"]}
		]
	}`

	resp := &schema.RunResponse{
		Messages: []schema.Message{
			schema.NewAssistantMessage(aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent(jsonPlan),
			}, "planner"),
		},
	}

	plan, err := parsePlan(resp)
	if err != nil {
		t.Fatalf("parsePlan: %v", err)
	}

	if plan.Goal != "Set up a Go project" {
		t.Errorf("goal = %q, want %q", plan.Goal, "Set up a Go project")
	}

	if len(plan.Steps) != 2 {
		t.Fatalf("steps = %d, want 2", len(plan.Steps))
	}

	if plan.Steps[0].ID != "step_1" {
		t.Errorf("step[0].ID = %q, want %q", plan.Steps[0].ID, "step_1")
	}

	if plan.Steps[1].Agent != "coder" {
		t.Errorf("step[1].Agent = %q, want %q", plan.Steps[1].Agent, "coder")
	}

	if len(plan.Steps[1].DependsOn) != 1 || plan.Steps[1].DependsOn[0] != "step_1" {
		t.Errorf("step[1].DependsOn = %v, want [step_1]", plan.Steps[1].DependsOn)
	}
}

func TestParsePlan_WithCodeFences(t *testing.T) {
	text := "Here is the plan:\n```json\n{\"goal\": \"test\", \"steps\": [{\"id\": \"step_1\", \"description\": \"do it\", \"agent\": \"coder\", \"depends_on\": []}]}\n```"

	resp := &schema.RunResponse{
		Messages: []schema.Message{
			schema.NewAssistantMessage(aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent(text),
			}, "planner"),
		},
	}

	plan, err := parsePlan(resp)
	if err != nil {
		t.Fatalf("parsePlan with code fences: %v", err)
	}

	if plan.Goal != "test" {
		t.Errorf("goal = %q, want %q", plan.Goal, "test")
	}
}

func TestParsePlan_InvalidJSON(t *testing.T) {
	resp := &schema.RunResponse{
		Messages: []schema.Message{
			schema.NewAssistantMessage(aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent("not json at all"),
			}, "planner"),
		},
	}

	_, err := parsePlan(resp)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestParsePlan_InvalidAgent(t *testing.T) {
	jsonPlan := `{"goal": "test", "steps": [{"id": "step_1", "description": "do", "agent": "unknown_agent", "depends_on": []}]}`

	resp := &schema.RunResponse{
		Messages: []schema.Message{
			schema.NewAssistantMessage(aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent(jsonPlan),
			}, "planner"),
		},
	}

	_, err := parsePlan(resp)
	if err == nil {
		t.Fatal("expected error for invalid agent")
	}
}

func TestParsePlan_EmptyResponse(t *testing.T) {
	_, err := parsePlan(nil)
	if err == nil {
		t.Fatal("expected error for nil response")
	}

	_, err = parsePlan(&schema.RunResponse{})
	if err == nil {
		t.Fatal("expected error for empty response")
	}
}

func TestExtractJSON(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  string
	}{
		{
			name:  "plain json",
			input: `{"goal": "test"}`,
			want:  `{"goal": "test"}`,
		},
		{
			name:  "json code fence",
			input: "```json\n{\"goal\": \"test\"}\n```",
			want:  `{"goal": "test"}`,
		},
		{
			name:  "plain code fence",
			input: "```\n{\"goal\": \"test\"}\n```",
			want:  `{"goal": "test"}`,
		},
		{
			name:  "json with surrounding text",
			input: "Here is the plan: {\"goal\": \"test\"} done.",
			want:  `{"goal": "test"}`,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := extractJSON(tt.input)
			if got != tt.want {
				t.Errorf("extractJSON = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFindTerminalNodes(t *testing.T) {
	nodes := []struct {
		id   string
		deps []string
	}{
		{"a", nil},
		{"b", []string{"a"}},
		{"c", []string{"a"}},
		{"d", []string{"b"}},
	}

	var orchNodes []orchestrate.Node
	for _, n := range nodes {
		orchNodes = append(orchNodes, orchestrate.Node{ID: n.id, Deps: n.deps})
	}

	terminals := findTerminalNodes(orchNodes)

	// Terminal nodes are c and d (no one depends on them).
	if len(terminals) != 2 {
		t.Fatalf("terminal nodes = %d, want 2", len(terminals))
	}

	termMap := make(map[string]bool)
	for _, id := range terminals {
		termMap[id] = true
	}

	if !termMap["c"] {
		t.Error("expected c to be terminal")
	}
	if !termMap["d"] {
		t.Error("expected d to be terminal")
	}
}

func TestPlannerAgent_FallbackOnEmptyPlan(t *testing.T) {
	// Create a mock that returns an empty plan.
	planMock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(`{"goal": "test", "steps": []}`),
					},
				},
			},
		},
	}

	planGen := taskagent.New(
		agent.Config{ID: "plan-gen", Name: "Plan Generator"},
		taskagent.WithChatCompleter(planMock),
		taskagent.WithModel("test"),
		taskagent.WithMaxIterations(1),
	)

	fallback := &stubAgent{
		id: "coder",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("fallback response"),
				}, "coder"),
			},
		},
	}

	planner := NewPlannerAgent(
		agent.Config{ID: "planner", Name: "Planner Agent"},
		planGen,
		map[string]agent.Agent{"coder": fallback},
		2,
		fallback,
	)

	resp, err := planner.Run(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("do something complex")},
	})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}

	if len(resp.Messages) == 0 {
		t.Fatal("expected response messages")
	}

	text := resp.Messages[0].Content.Text()
	if text != "fallback response" {
		t.Errorf("response = %q, want %q", text, "fallback response")
	}
}

func TestPlannerAgent_RunStream(t *testing.T) {
	planMock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent(`{"goal": "test", "steps": []}`),
					},
				},
			},
		},
	}

	planGen := taskagent.New(
		agent.Config{ID: "plan-gen", Name: "Plan Generator"},
		taskagent.WithChatCompleter(planMock),
		taskagent.WithModel("test"),
		taskagent.WithMaxIterations(1),
	)

	fallback := &stubAgent{id: "coder"}

	planner := NewPlannerAgent(
		agent.Config{ID: "planner", Name: "Planner Agent"},
		planGen,
		map[string]agent.Agent{"coder": fallback},
		2,
		fallback,
	)

	stream, err := planner.RunStream(context.Background(), &schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("test")},
	})
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	defer func() { _ = stream.Close() }()

	// Should get at least AgentStart and AgentEnd events.
	var gotStart, gotEnd bool
	for {
		event, err := stream.Recv()
		if err != nil {
			break
		}
		switch event.Type {
		case schema.EventAgentStart:
			gotStart = true
		case schema.EventAgentEnd:
			gotEnd = true
		}
	}

	if !gotStart {
		t.Error("expected AgentStart event")
	}
	if !gotEnd {
		t.Error("expected AgentEnd event")
	}
}

func TestPlanAggregator_SingleResult(t *testing.T) {
	agg := &PlanAggregator{}

	results := map[string]*schema.RunResponse{
		"step_1": {
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("single result"),
				}, "coder"),
			},
		},
	}

	resp, err := agg.Aggregate(context.Background(), results)
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}

	if len(resp.Messages) == 0 {
		t.Fatal("expected messages")
	}

	if resp.Messages[0].Content.Text() != "single result" {
		t.Errorf("text = %q, want %q", resp.Messages[0].Content.Text(), "single result")
	}
}

func TestPlanAggregator_EmptyResults(t *testing.T) {
	agg := &PlanAggregator{}

	resp, err := agg.Aggregate(context.Background(), map[string]*schema.RunResponse{})
	if err != nil {
		t.Fatalf("Aggregate: %v", err)
	}

	if len(resp.Messages) != 0 {
		t.Errorf("expected no messages, got %d", len(resp.Messages))
	}
}
