package dispatches

import (
	"context"
	"fmt"

	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/debugs"
)

// runPrimary is the non-streaming unified-mode entry point. It forwards the
// request verbatim to the Primary Assistant and returns its response — the
// Primary owns all internal decisions (answer directly, investigate,
// delegate, plan) through its tool set.
func (d *Dispatcher) runPrimary(ctx context.Context, req *schema.RunRequest) (*schema.RunResponse, error) {
	if d.primaryAssistant == nil {
		// Defensive: Run/RunStream gate on this already, but the nil check
		// keeps the method robust if called directly from a test.
		return nil, fmt.Errorf("dispatcher: primary assistant not configured")
	}

	ctx = debugs.WithAgentName(ctx, PrimaryAgentName)

	resp, err := d.primaryAssistant.Run(ctx, req)
	if err != nil {
		return nil, fmt.Errorf("dispatcher: primary assistant failed: %w", err)
	}

	return resp, nil
}

// runPrimaryStream relays the Primary Assistant stream directly to consumers.
// Primary is the user-facing entry point, so no phase envelope or
// SubAgentStart/End wrapper is added — delegation boundaries are emitted
// later by delegate/spawn_worker/plan_task handlers when a real sub-agent runs.
func (d *Dispatcher) runPrimaryStream(
	ctx context.Context,
	send func(schema.Event) error,
	req *schema.RunRequest,
) error {
	if d.primaryAssistant == nil {
		return fmt.Errorf("dispatcher: primary assistant not configured")
	}

	ctx = debugs.WithAgentName(ctx, PrimaryAgentName)

	return relayAgentStream(ctx, send, d.primaryAssistant, req)
}

// RunPlan implements PlanExecutor, exposing the dispatcher's existing plan
// execution path as a public interface so the Primary Assistant's plan_task
// tool can drive a DAG through the same machinery the older plan-mode path
// used. Kept as a thin wrapper around runPlan so there is a single source of
// truth for DAG behaviour (aggregator, concurrency, replanning).
//
// classifyUsage is unused in unified mode, so it is passed as nil.
// contextSummary is likewise empty; the Primary has already gathered the
// relevant facts through its read tools and baked them into the plan step
// descriptions.
func (d *Dispatcher) RunPlan(ctx context.Context, plan *Plan, req *schema.RunRequest) (*schema.RunResponse, error) {
	d.maybeMirrorPlanToTree(ctx, plan, req)
	return d.runPlan(ctx, req, plan, nil, "")
}

// Compile-time check: Dispatcher satisfies the PlanExecutor interface
// consumed by RegisterPlanTaskTool. Pinned here so a signature drift on
// either side fails the build.
var _ PlanExecutor = (*Dispatcher)(nil)

// PrimaryAgentName is the debug label used for the Primary Assistant span
// in log context. Matches the registry ID from vv/agents/primary.go.
const PrimaryAgentName = "primary"

// Compile-time assertion that *Dispatcher still implements the standard
// agent interfaces after the Primary wiring landed.
var (
	_ agent.Agent       = (*Dispatcher)(nil)
	_ agent.StreamAgent = (*Dispatcher)(nil)
)
