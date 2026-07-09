package dispatches

import (
	"strings"
	"testing"

	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/orchestrate"
	"github.com/vogo/vage/schema"
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

	nodes, err := d.buildNodes(staticStepPlan("ghost"), &schema.RunRequest{SessionID: "test-session"}, "")
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
	nodes, err := d.buildNodes(staticStepPlan("ghost"), &schema.RunRequest{SessionID: "test-session"}, "")
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
	nodes, err = d.buildNodes(staticStepPlan("reviewer"), &schema.RunRequest{SessionID: "test-session"}, "")
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

	nodes, err := d.buildNodes(staticStepPlan("ghost"), &schema.RunRequest{SessionID: "test-session"}, "")
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
