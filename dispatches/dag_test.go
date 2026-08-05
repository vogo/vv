package dispatches

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/orchestrate"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/registries"
)

// newBuildNodesDispatcher assembles a Dispatcher for buildNodes tests with the
// given registered sub-agents and optional configuration.
func newBuildNodesDispatcher(t *testing.T, subAgents map[string]agent.Agent, opts ...Option) *Dispatcher {
	t.Helper()

	planGen := &stubAgent{id: "plangen"}

	return New(newTestRegistry(), subAgents, planGen, opts...)
}

// staticStepPlan builds a single-step static plan referencing the given agent
// ID. A single step yields no summary node, isolating static agent resolution.
func staticStepPlan(agentID string) *Plan {
	return &Plan{
		Goal: "test goal",
		Steps: []PlanStep{
			{ID: "s1", Description: "do work", Agent: agentID},
		},
	}
}

// TestBuildNodes_UnknownAgent_NoDefault asserts that an unknown static
// step.Agent with no configured DAG default yields a diagnosable error naming
// the offending agent, and that no node is produced.
func TestBuildNodes_UnknownAgent_NoDefault(t *testing.T) {
	d := newBuildNodesDispatcher(t, map[string]agent.Agent{
		"coder": &stubAgent{id: "coder"},
	})

	nodes, err := d.buildNodes(context.Background(), staticStepPlan("ghost"), &schema.RunRequest{SessionID: "test-session"}, "")
	if err == nil {
		t.Fatalf("expected error for unknown agent with no default, got nodes=%v", nodes)
	}
	if nodes != nil {
		t.Errorf("expected nil nodes on error, got %v", nodes)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the unknown agent %q; got: %v", "ghost", err)
	}
}

// TestBuildNodes_DefaultAgentConfigured asserts that a configured, registered
// DAG default resolves an unknown static agent, while a valid step.Agent still
// resolves to its own agent (exact match wins).
func TestBuildNodes_DefaultAgentConfigured(t *testing.T) {
	coder := &stubAgent{id: "coder"}
	reviewer := &stubAgent{id: "reviewer"}
	subAgents := map[string]agent.Agent{
		"coder":    coder,
		"reviewer": reviewer,
	}

	d := newBuildNodesDispatcher(t, subAgents, WithDAGDefaultAgentID("coder"))

	// Unknown agent resolves to the configured default ("coder").
	nodes, err := d.buildNodes(context.Background(), staticStepPlan("ghost"), &schema.RunRequest{SessionID: "test-session"}, "")
	if err != nil {
		t.Fatalf("unexpected error resolving via default: %v", err)
	}
	if got := len(nodes); got != 1 {
		t.Fatalf("expected 1 node, got %d", got)
	}
	if id := runnerID(nodes[0].Runner); id != "coder" {
		t.Errorf("unknown agent should resolve to default %q; got %q", "coder", id)
	}

	// A valid step.Agent still uses its own agent — default must not override.
	nodes, err = d.buildNodes(context.Background(), staticStepPlan("reviewer"), &schema.RunRequest{SessionID: "test-session"}, "")
	if err != nil {
		t.Fatalf("unexpected error for valid agent: %v", err)
	}
	if id := runnerID(nodes[0].Runner); id != "reviewer" {
		t.Errorf("valid agent must resolve to itself %q, not default; got %q", "reviewer", id)
	}
}

// TestBuildNodes_DefaultAgentNotRegistered asserts that when the configured
// DAG default is itself not registered, the error names BOTH the original
// step.Agent and the misconfigured default, distinguishing "plan references
// unknown agent" from "dispatcher default is invalid". Dynamic specs are
// unaffected by the default configuration.
func TestBuildNodes_DefaultAgentNotRegistered(t *testing.T) {
	d := newBuildNodesDispatcher(t, map[string]agent.Agent{
		"reviewer": &stubAgent{id: "reviewer"},
	}, WithDAGDefaultAgentID("coder"))

	nodes, err := d.buildNodes(context.Background(), staticStepPlan("ghost"), &schema.RunRequest{SessionID: "test-session"}, "")
	if err == nil {
		t.Fatalf("expected error when default agent unregistered, got nodes=%v", nodes)
	}
	if !strings.Contains(err.Error(), "ghost") {
		t.Errorf("error should name the original agent %q; got: %v", "ghost", err)
	}
	if !strings.Contains(err.Error(), "coder") {
		t.Errorf("error should name the misconfigured default %q; got: %v", "coder", err)
	}
}

// runnerID unwraps a node runner (possibly wrapped with lifecycle hooks) to its
// underlying agent ID.
func runnerID(runner orchestrate.Runner) string {
	if h, ok := runner.(*hookedAgent); ok {
		return h.inner.ID()
	}

	if a, ok := runner.(agent.Agent); ok {
		return a.ID()
	}

	return ""
}

// A DAG dynamic node goes through the same worker construction path as
// spawn_worker: its declared context sources are injected as a read-only block
// ahead of the step description.
func TestBuildNodes_DynamicStepInjectsContextSources(t *testing.T) {
	sources := registries.NewContextSources()
	sources.MustRegister(registries.ContextSource{
		ID:          registries.ContextSourceDiff,
		Description: "stub diff",
		Provider:    func(context.Context) (string, error) { return "DIFF-BODY", nil },
	})

	d := newBuildNodesDispatcher(
		t, nil,
		WithToolsConfig(configs.ToolsConfig{}),
		WithContextSources(sources),
	)

	plan := &Plan{
		Goal: "review",
		Steps: []PlanStep{{
			ID:          "s1",
			Description: "review the change",
			Agent:       "coder",
			DynamicSpec: &WorkerSpec{
				BaseType:       "coder",
				ToolAccess:     "review",
				Skills:         []string{registries.SkillReview},
				ContextSources: []string{registries.ContextSourceDiff},
			},
		}},
	}

	nodes, err := d.buildNodes(context.Background(), plan, &schema.RunRequest{SessionID: "s"}, "")
	if err != nil {
		t.Fatalf("buildNodes: %v", err)
	}

	if len(nodes) != 1 {
		t.Fatalf("node count = %d, want 1", len(nodes))
	}

	req, err := nodes[0].InputMapper(nil)
	if err != nil {
		t.Fatalf("InputMapper: %v", err)
	}

	var joined strings.Builder
	for _, m := range req.Messages {
		joined.WriteString(m.Text())
		joined.WriteString("\n")
	}

	for _, want := range []string{"## Context: diff (read-only)", "DIFF-BODY", "review the change"} {
		if !strings.Contains(joined.String(), want) {
			t.Errorf("step input missing %q; got:\n%s", want, joined.String())
		}
	}
}

// An unresolvable context source fails the DAG build with a diagnosable error
// instead of quietly running the node without the context it declared.
func TestBuildNodes_DynamicStepContextFailureIsAnError(t *testing.T) {
	sources := registries.NewContextSources()
	sources.MustRegister(registries.ContextSource{
		ID:          registries.ContextSourceDiff,
		Description: "always fails",
		Provider:    func(context.Context) (string, error) { return "", errors.New("not a git repository") },
	})

	d := newBuildNodesDispatcher(
		t, nil,
		WithToolsConfig(configs.ToolsConfig{}),
		WithContextSources(sources),
	)

	plan := &Plan{
		Goal: "review",
		Steps: []PlanStep{{
			ID:          "s1",
			Description: "review the change",
			Agent:       "coder",
			DynamicSpec: &WorkerSpec{BaseType: "coder", ContextSources: []string{registries.ContextSourceDiff}},
		}},
	}

	nodes, err := d.buildNodes(context.Background(), plan, &schema.RunRequest{SessionID: "s"}, "")
	if err == nil {
		t.Fatal("expected buildNodes to fail when a declared context source cannot be resolved")
	}

	if !strings.Contains(err.Error(), "not a git repository") || !strings.Contains(err.Error(), "s1") {
		t.Errorf("error = %v, want it to name the step and the underlying failure", err)
	}

	if nodes != nil {
		t.Errorf("expected no nodes on failure, got %d", len(nodes))
	}
}

// A dynamic node rejects an invalid spec at build time — the DAG never runs a
// half-assembled worker.
func TestBuildNodes_DynamicStepInvalidSpec(t *testing.T) {
	d := newBuildNodesDispatcher(t, nil, WithToolsConfig(configs.ToolsConfig{}))

	plan := &Plan{
		Goal: "x",
		Steps: []PlanStep{{
			ID:          "s1",
			Description: "do x",
			Agent:       "coder",
			DynamicSpec: &WorkerSpec{BaseType: "coder", ToolAccess: "write-only"},
		}},
	}

	if _, err := d.buildNodes(context.Background(), plan, &schema.RunRequest{SessionID: "s"}, ""); err == nil {
		t.Fatal("expected buildNodes to reject an invalid tool_access")
	}
}
