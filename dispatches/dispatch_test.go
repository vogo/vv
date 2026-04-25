package dispatches

import (
	"context"
	"strings"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/orchestrate"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/registries"
)

// stubAgent is a minimal agent implementation for testing.
type stubAgent struct {
	id       string
	response *schema.RunResponse
	err      error
	runs     int // call counter, zero-initialised; read via ranCount()
}

var _ agent.Agent = (*stubAgent)(nil)

func (s *stubAgent) ID() string          { return s.id }
func (s *stubAgent) Name() string        { return s.id }
func (s *stubAgent) Description() string { return s.id }

// ranCount returns how many times Run has been invoked on this stub.
// Tests use it to prove sub-agents ran (delegate_to path) or did not
// (answer_directly path).
func (s *stubAgent) ranCount() int { return s.runs }

func (s *stubAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	s.runs++

	if s.err != nil {
		return nil, s.err
	}
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

func newTestRegistry() *registries.Registry {
	reg := registries.New()
	for _, id := range []string{"coder", "researcher", "reviewer", "chat"} {
		reg.MustRegister(registries.AgentDescriptor{
			ID:           id,
			DisplayName:  id,
			Description:  id,
			Dispatchable: true,
		})
	}

	return reg
}

func TestDispatcher_ImplementsInterfaces(t *testing.T) {
	var _ agent.Agent = (*Dispatcher)(nil)
	var _ agent.StreamAgent = (*Dispatcher)(nil)
}

func TestClassifyResult_Validate_Direct(t *testing.T) {
	reg := newTestRegistry()
	subAgents := map[string]agent.Agent{
		"coder":      &stubAgent{id: "coder"},
		"researcher": &stubAgent{id: "researcher"},
		"reviewer":   &stubAgent{id: "reviewer"},
		"chat":       &stubAgent{id: "chat"},
	}

	tests := []struct {
		name    string
		result  ClassifyResult
		wantErr bool
	}{
		{
			name:    "valid direct coder",
			result:  ClassifyResult{Mode: "direct", Agent: "coder"},
			wantErr: false,
		},
		{
			name:    "valid direct chat",
			result:  ClassifyResult{Mode: "direct", Agent: "chat"},
			wantErr: false,
		},
		{
			name:    "invalid direct unknown agent",
			result:  ClassifyResult{Mode: "direct", Agent: "nonexistent"},
			wantErr: true,
		},
		{
			name: "valid plan",
			result: ClassifyResult{
				Mode: "plan",
				Plan: &Plan{
					Goal: "test",
					Steps: []PlanStep{
						{ID: "step_1", Description: "do it", Agent: "coder", DependsOn: []string{}},
					},
				},
			},
			wantErr: false,
		},
		{
			name: "plan with invalid agent",
			result: ClassifyResult{
				Mode: "plan",
				Plan: &Plan{
					Goal: "test",
					Steps: []PlanStep{
						{ID: "step_1", Description: "do it", Agent: "nonexistent", DependsOn: []string{}},
					},
				},
			},
			wantErr: true,
		},
		{
			name:    "plan with nil plan",
			result:  ClassifyResult{Mode: "plan"},
			wantErr: true,
		},
		{
			name: "plan with empty steps",
			result: ClassifyResult{
				Mode: "plan",
				Plan: &Plan{Goal: "test", Steps: []PlanStep{}},
			},
			wantErr: true,
		},
		{
			name:    "unknown mode",
			result:  ClassifyResult{Mode: "unknown"},
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.result.validate(reg, subAgents)
			if (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
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

func TestAggregateUsage(t *testing.T) {
	tests := []struct {
		name    string
		a       *aimodel.Usage
		b       *aimodel.Usage
		wantNil bool
		wantPT  int
	}{
		{
			name:    "both nil",
			wantNil: true,
		},
		{
			name:   "a only",
			a:      &aimodel.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
			wantPT: 10,
		},
		{
			name:   "b only",
			b:      &aimodel.Usage{PromptTokens: 20, CompletionTokens: 10, TotalTokens: 30},
			wantPT: 20,
		},
		{
			name:   "both present",
			a:      &aimodel.Usage{PromptTokens: 10, CompletionTokens: 5, TotalTokens: 15},
			b:      &aimodel.Usage{PromptTokens: 20, CompletionTokens: 10, TotalTokens: 30},
			wantPT: 30,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := aggregateUsage(tt.a, tt.b)
			if tt.wantNil {
				if result != nil {
					t.Errorf("expected nil, got %+v", result)
				}

				return
			}

			if result == nil {
				t.Fatal("expected non-nil usage")
			}

			if result.PromptTokens != tt.wantPT {
				t.Errorf("PromptTokens = %d, want %d", result.PromptTokens, tt.wantPT)
			}
		})
	}
}

// TestDispatcher_Explore_* / TestDispatcher_Classify_* /
// TestDispatcher_Run_WithExplorerAndPlanner were removed in M6 G2 alongside
// the explorer sub-agent and the planner-driven intent recognition path.
// The unified Primary covers exploration directly via its read/glob/grep
// tool set; the planner-as-intent path is gone.

func TestDynamicAgentSpec_Validate(t *testing.T) {
	reg := newTestRegistry()

	tests := []struct {
		name    string
		spec    DynamicAgentSpec
		wantErr bool
		errMsg  string
	}{
		{
			name:    "valid coder",
			spec:    DynamicAgentSpec{BaseType: "coder"},
			wantErr: false,
		},
		{
			name:    "valid researcher",
			spec:    DynamicAgentSpec{BaseType: "researcher"},
			wantErr: false,
		},
		{
			name:    "valid reviewer",
			spec:    DynamicAgentSpec{BaseType: "reviewer"},
			wantErr: false,
		},
		{
			name:    "valid chat",
			spec:    DynamicAgentSpec{BaseType: "chat"},
			wantErr: false,
		},
		{
			name:    "valid with all fields",
			spec:    DynamicAgentSpec{BaseType: "coder", SystemPrompt: "custom", ToolAccess: "read-only", Model: "gpt-4"},
			wantErr: false,
		},
		{
			name:    "valid tool_access full",
			spec:    DynamicAgentSpec{BaseType: "coder", ToolAccess: "full"},
			wantErr: false,
		},
		{
			name:    "valid tool_access none",
			spec:    DynamicAgentSpec{BaseType: "coder", ToolAccess: "none"},
			wantErr: false,
		},
		{
			name:    "missing base_type",
			spec:    DynamicAgentSpec{},
			wantErr: true,
			errMsg:  "base_type is required",
		},
		{
			name:    "invalid base_type",
			spec:    DynamicAgentSpec{BaseType: "unknown"},
			wantErr: true,
			errMsg:  "invalid base_type",
		},
		{
			name:    "invalid tool_access",
			spec:    DynamicAgentSpec{BaseType: "coder", ToolAccess: "write-only"},
			wantErr: true,
			errMsg:  "invalid tool_access",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.spec.validate(reg)
			if (err != nil) != tt.wantErr {
				t.Errorf("validate() error = %v, wantErr %v", err, tt.wantErr)
			}

			if tt.wantErr && err != nil && tt.errMsg != "" {
				if !strings.Contains(err.Error(), tt.errMsg) {
					t.Errorf("validate() error = %q, want to contain %q", err.Error(), tt.errMsg)
				}
			}
		})
	}
}

func TestPlanSummaryPrompt_NotEmpty(t *testing.T) {
	if PlanSummaryPrompt == "" {
		t.Fatal("PlanSummaryPrompt is empty")
	}
}

// stubStreamAgent implements agent.StreamAgent for testing.
type stubStreamAgent struct {
	id       string
	response string
}

var _ agent.StreamAgent = (*stubStreamAgent)(nil)

func (s *stubStreamAgent) ID() string          { return s.id }
func (s *stubStreamAgent) Name() string        { return s.id }
func (s *stubStreamAgent) Description() string { return s.id }

func (s *stubStreamAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	return &schema.RunResponse{
		Messages: []schema.Message{
			schema.NewAssistantMessage(aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent(s.response),
			}, s.id),
		},
	}, nil
}

func (s *stubStreamAgent) RunStream(ctx context.Context, req *schema.RunRequest) (*schema.RunStream, error) {
	return schema.NewRunStream(ctx, 8, func(ctx context.Context, send func(schema.Event) error) error {
		if err := send(schema.NewEvent(schema.EventAgentStart, s.id, req.SessionID, schema.AgentStartData{})); err != nil {
			return err
		}

		if err := send(schema.NewEvent(schema.EventTextDelta, s.id, req.SessionID, schema.TextDeltaData{Delta: s.response})); err != nil {
			return err
		}

		return send(schema.NewEvent(schema.EventAgentEnd, s.id, req.SessionID, schema.AgentEndData{
			Message: s.response,
		}))
	}), nil
}

func TestWithProjectInstructions(t *testing.T) {
	reg := registries.New()
	instructions := "Test project instructions content"

	d := New(
		reg,
		nil,
		nil,
		WithProjectInstructions(instructions),
	)

	if d.projectInstructions != instructions {
		t.Errorf("projectInstructions = %q, want %q", d.projectInstructions, instructions)
	}
}

func TestWithProjectInstructions_Empty(t *testing.T) {
	reg := registries.New()

	d := New(
		reg,
		nil,
		nil,
		WithProjectInstructions(""),
	)

	if d.projectInstructions != "" {
		t.Errorf("projectInstructions = %q, want empty", d.projectInstructions)
	}
}

func TestAppendProjectInstructions_Dispatches(t *testing.T) {
	base := "You are a task planner."
	instructions := "Always prefer Go."
	got := appendProjectInstructions(base, instructions)

	if !strings.Contains(got, base) {
		t.Error("result should contain the base prompt")
	}

	if !strings.Contains(got, "# Project Instructions") {
		t.Error("result should contain the project instructions header")
	}

	if !strings.Contains(got, instructions) {
		t.Error("result should contain the instructions content")
	}
}

func TestAppendProjectInstructions_Dispatches_Empty(t *testing.T) {
	base := "You are a task planner."
	got := appendProjectInstructions(base, "")

	if got != base {
		t.Errorf("appendProjectInstructions(base, \"\") = %q, want %q", got, base)
	}
}
