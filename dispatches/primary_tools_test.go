package dispatches

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/tool"
)

func TestDelegateToolName(t *testing.T) {
	if got := DelegateToolName("coder"); got != "delegate_to_coder" {
		t.Errorf("DelegateToolName(coder) = %q, want delegate_to_coder", got)
	}

	if got := DelegateToolName("reviewer"); got != "delegate_to_reviewer" {
		t.Errorf("DelegateToolName(reviewer) = %q, want delegate_to_reviewer", got)
	}
}

func TestRegisterDelegateTools_RegistersRequestedIDs(t *testing.T) {
	reg := tool.NewRegistry()

	coder := &stubAgent{id: "coder"}
	reviewer := &stubAgent{id: "reviewer"}
	subAgents := map[string]agent.Agent{
		"coder":    coder,
		"reviewer": reviewer,
		// researcher intentionally omitted from subAgents
	}

	err := RegisterDelegateTools(reg, subAgents, []string{"coder", "researcher", "reviewer"})
	if err != nil {
		t.Fatalf("RegisterDelegateTools: %v", err)
	}

	// delegate_to_coder and delegate_to_reviewer should exist; researcher skipped.
	wantPresent := []string{"delegate_to_coder", "delegate_to_reviewer"}
	for _, name := range wantPresent {
		if _, ok := reg.Get(name); !ok {
			t.Errorf("expected tool %q to be registered", name)
		}
	}

	if _, ok := reg.Get("delegate_to_researcher"); ok {
		t.Error("delegate_to_researcher should be absent (subAgents missing the agent)")
	}

	// delegate_to_coder schema must list required=[task].
	def, _ := reg.Get("delegate_to_coder")
	params, ok := def.Parameters.(map[string]any)
	if !ok {
		t.Fatalf("parameters is not map, got %T", def.Parameters)
	}

	reqd, ok := params["required"].([]string)
	if !ok {
		t.Fatalf("required is not []string, got %T", params["required"])
	}

	if len(reqd) != 1 || reqd[0] != "task" {
		t.Errorf("required = %v, want [task]", reqd)
	}

	if def.Source != schema.ToolSourceAgent || def.AgentID != "coder" {
		t.Errorf("def.Source=%q AgentID=%q, want agent/coder", def.Source, def.AgentID)
	}
}

func TestRegisterDelegateTools_HandlerRunsAgent(t *testing.T) {
	reg := tool.NewRegistry()

	coder := &stubAgent{
		id: "coder",
		response: &schema.RunResponse{
			Messages: []schema.Message{
				schema.NewAssistantMessage(aimodel.Message{
					Role:    aimodel.RoleAssistant,
					Content: aimodel.NewTextContent("refactored foo.go"),
				}, "coder"),
			},
		},
	}

	subAgents := map[string]agent.Agent{"coder": coder}

	if err := RegisterDelegateTools(reg, subAgents, []string{"coder"}); err != nil {
		t.Fatalf("RegisterDelegateTools: %v", err)
	}

	args := `{"task":"refactor foo.go","context":"file at repo root"}`
	res, err := reg.Execute(context.Background(), "delegate_to_coder", args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if res.IsError {
		t.Fatalf("handler returned IsError=true: %s", toolResultText(res))
	}

	if coder.ranCount() != 1 {
		t.Errorf("coder.ranCount = %d, want 1", coder.ranCount())
	}

	if text := toolResultText(res); !strings.Contains(text, "refactored foo.go") {
		t.Errorf("result text %q does not contain expected output", text)
	}
}

func TestRegisterDelegateTools_HandlerRejectsBadArgs(t *testing.T) {
	reg := tool.NewRegistry()
	coder := &stubAgent{id: "coder"}
	_ = RegisterDelegateTools(reg, map[string]agent.Agent{"coder": coder}, []string{"coder"})

	cases := []struct {
		name string
		args string
	}{
		{"invalid JSON", `{not json`},
		{"empty task", `{"task":""}`},
		{"missing task", `{"context":"only"}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := reg.Execute(context.Background(), "delegate_to_coder", tc.args)
			if err != nil {
				t.Fatalf("Execute returned go error: %v", err)
			}
			if !res.IsError {
				t.Errorf("want IsError=true, got content %q", toolResultText(res))
			}
			if coder.ranCount() != 0 {
				t.Errorf("stub agent ran %d times, want 0 (bad args must short-circuit)", coder.ranCount())
			}
		})
	}
}

func TestRegisterDelegateTools_HandlerIncrementsDepth(t *testing.T) {
	reg := tool.NewRegistry()

	var seenDepth int
	spy := &depthSpyAgent{id: "coder", onRun: func(ctx context.Context) {
		seenDepth = DepthFrom(ctx)
	}}

	if err := RegisterDelegateTools(reg, map[string]agent.Agent{"coder": spy}, []string{"coder"}); err != nil {
		t.Fatalf("RegisterDelegateTools: %v", err)
	}

	// Parent context at depth 0 → handler must push the sub-agent to depth 1.
	args := `{"task":"do x"}`
	if _, err := reg.Execute(context.Background(), "delegate_to_coder", args); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if seenDepth != 1 {
		t.Errorf("sub-agent saw depth=%d, want 1", seenDepth)
	}

	// Parent at depth 1 → sub-agent at depth 2.
	seenDepth = -1
	parent := WithDepth(context.Background(), 1)

	if _, err := reg.Execute(parent, "delegate_to_coder", args); err != nil {
		t.Fatalf("Execute parent: %v", err)
	}

	if seenDepth != 2 {
		t.Errorf("sub-agent saw depth=%d from parent depth=1, want 2", seenDepth)
	}
}

func TestRegisterDelegateTools_HandlerSurfacesAgentError(t *testing.T) {
	reg := tool.NewRegistry()
	boom := &stubAgent{id: "coder", err: errors.New("boom")}

	if err := RegisterDelegateTools(reg, map[string]agent.Agent{"coder": boom}, []string{"coder"}); err != nil {
		t.Fatalf("RegisterDelegateTools: %v", err)
	}

	res, err := reg.Execute(context.Background(), "delegate_to_coder", `{"task":"do x"}`)
	if err != nil {
		t.Fatalf("Execute returned go error: %v", err)
	}

	if !res.IsError {
		t.Fatal("want IsError=true when agent errors")
	}

	text := toolResultText(res)
	if !strings.Contains(text, "boom") {
		t.Errorf("error surface %q must contain underlying message", text)
	}
}

func TestRegisterPlanTaskTool_RequiresExecutor(t *testing.T) {
	reg := tool.NewRegistry()
	if err := RegisterPlanTaskTool(reg, nil); err == nil {
		t.Fatal("RegisterPlanTaskTool with nil executor must return error")
	}
}

func TestRegisterPlanTaskTool_HandlerRunsPlan(t *testing.T) {
	reg := tool.NewRegistry()

	exec := &capturingPlanExec{resp: &schema.RunResponse{
		Messages: []schema.Message{
			schema.NewAssistantMessage(aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent("step-1 done; step-2 done"),
			}, "plan-gen"),
		},
	}}

	if err := RegisterPlanTaskTool(reg, exec); err != nil {
		t.Fatalf("RegisterPlanTaskTool: %v", err)
	}

	args := mustJSON(t, primaryPlanTaskArgs{
		Goal: "fix bug x and add test",
		Steps: []PlanStep{
			{ID: "s1", Description: "fix bug x", Agent: "coder"},
			{ID: "s2", Description: "add test", Agent: "coder", DependsOn: []string{"s1"}},
		},
	})

	res, err := reg.Execute(context.Background(), PrimaryToolPlanTask, args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if res.IsError {
		t.Fatalf("IsError=true: %s", toolResultText(res))
	}

	if exec.calls != 1 {
		t.Errorf("plan executor calls = %d, want 1", exec.calls)
	}

	if exec.gotPlan == nil || exec.gotPlan.Goal != "fix bug x and add test" || len(exec.gotPlan.Steps) != 2 {
		t.Errorf("exec.gotPlan = %+v, want 2-step plan", exec.gotPlan)
	}

	text := toolResultText(res)
	if !strings.Contains(text, "step-1 done") {
		t.Errorf("result text = %q does not include plan response", text)
	}
}

func TestRegisterPlanTaskTool_HandlerRejectsBadArgs(t *testing.T) {
	reg := tool.NewRegistry()
	exec := &capturingPlanExec{}
	_ = RegisterPlanTaskTool(reg, exec)

	cases := []struct {
		name string
		args string
	}{
		{"invalid JSON", "{not json"},
		{"empty goal", `{"goal":"","steps":[{"id":"s1","description":"x","agent":"coder"}]}`},
		{"no steps", `{"goal":"do","steps":[]}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			res, err := reg.Execute(context.Background(), PrimaryToolPlanTask, tc.args)
			if err != nil {
				t.Fatalf("Execute returned go error: %v", err)
			}
			if !res.IsError {
				t.Errorf("want IsError=true, got %q", toolResultText(res))
			}
			if exec.calls != 0 {
				t.Errorf("executor should not have run for bad args, got calls=%d", exec.calls)
			}
		})
	}
}

func TestRegisterPlanTaskTool_HandlerIncrementsDepth(t *testing.T) {
	reg := tool.NewRegistry()

	var seen int
	exec := &capturingPlanExec{
		onRun: func(ctx context.Context) { seen = DepthFrom(ctx) },
	}
	_ = RegisterPlanTaskTool(reg, exec)

	args := `{"goal":"g","steps":[{"id":"s1","description":"x","agent":"coder"}]}`
	if _, err := reg.Execute(context.Background(), PrimaryToolPlanTask, args); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if seen != 1 {
		t.Errorf("plan executor saw depth=%d, want 1", seen)
	}
}

// depthSpyAgent captures the depth on its Run context so tests can assert
// that delegate_to_* handlers increment recursion depth before invoking.
type depthSpyAgent struct {
	id    string
	onRun func(ctx context.Context)
}

var _ agent.Agent = (*depthSpyAgent)(nil)

func (s *depthSpyAgent) ID() string          { return s.id }
func (s *depthSpyAgent) Name() string        { return s.id }
func (s *depthSpyAgent) Description() string { return s.id }

func (s *depthSpyAgent) Run(ctx context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	if s.onRun != nil {
		s.onRun(ctx)
	}

	return &schema.RunResponse{
		Messages: []schema.Message{
			schema.NewAssistantMessage(aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent("ok"),
			}, s.id),
		},
	}, nil
}

// capturingPlanExec captures the plan passed to RunPlan for assertions.
type capturingPlanExec struct {
	calls   int
	gotPlan *Plan
	gotReq  *schema.RunRequest
	resp    *schema.RunResponse
	err     error
	onRun   func(ctx context.Context)
}

func (c *capturingPlanExec) RunPlan(ctx context.Context, plan *Plan, req *schema.RunRequest) (*schema.RunResponse, error) {
	c.calls++
	c.gotPlan = plan
	c.gotReq = req

	if c.onRun != nil {
		c.onRun(ctx)
	}

	if c.err != nil {
		return nil, c.err
	}

	if c.resp != nil {
		return c.resp, nil
	}

	return &schema.RunResponse{}, nil
}

func mustJSON(t *testing.T, v any) string {
	t.Helper()

	b, err := json.Marshal(v)
	if err != nil {
		t.Fatalf("json.Marshal: %v", err)
	}

	return string(b)
}

// toolResultText concatenates all text parts of a ToolResult. Kept here (not
// in production code) because the dispatcher itself never needs to flatten
// tool results this way — the LLM consumes them as a list.
func toolResultText(r schema.ToolResult) string {
	var sb strings.Builder
	for _, p := range r.Content {
		if p.Type == "text" {
			sb.WriteString(p.Text)
		}
	}

	return sb.String()
}
