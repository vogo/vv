package dispatches

import (
	"context"
	"fmt"
	"log/slog"
	"slices"
	"strings"

	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/orchestrate"
	"github.com/vogo/vv/hooks"
)

// runPlan builds and executes a DAG from the plan.
func (d *Dispatcher) runPlan(ctx context.Context, req *schema.RunRequest, plan *Plan, classifyUsage *schema.Usage, contextSummary string) (*schema.RunResponse, error) {
	nodes, err := d.buildNodes(ctx, plan, req, contextSummary)
	if err != nil {
		slog.Warn("orchestrator: DAG build failed, falling back to chat", "error", err)

		return d.fallbackRun(ctx, req, classifyUsage)
	}

	dagCfg := orchestrate.DAGConfig{
		MaxConcurrency: d.maxConcurrency,
		ErrorStrategy:  orchestrate.Skip,
		Aggregator:     &PlanAggregator{Summarizer: d.planGen},
	}

	result, err := orchestrate.ExecuteDAG(ctx, dagCfg, nodes, req)
	if err != nil {
		return nil, fmt.Errorf("orchestrator: DAG execution failed: %w", err)
	}

	resp := result.FinalOutput
	if resp == nil {
		resp = &schema.RunResponse{}
	}

	resp.Usage = aggregateUsage(classifyUsage, resp.Usage)

	return resp, nil
}

// buildNodes converts a Plan into orchestrate.Node slices for DAG execution.
// ctx is the caller's request context: dynamic steps resolve their declared
// context sources under it, so cancellation stops that work too.
func (d *Dispatcher) buildNodes(ctx context.Context, plan *Plan, req *schema.RunRequest, contextSummary string) ([]orchestrate.Node, error) {
	nodes := make([]orchestrate.Node, 0, len(plan.Steps)+1)

	for _, step := range plan.Steps {
		stepCopy := step

		var runner agent.Agent

		if stepCopy.DynamicSpec != nil {
			// Build dynamic agent from spec.
			dynAgent, err := d.buildDynamicAgent(stepCopy.ID, stepCopy.DynamicSpec)
			if err != nil {
				return nil, fmt.Errorf("orchestrator: build dynamic agent for step %q: %w", stepCopy.ID, err)
			}

			runner = dynAgent

			// Declared context sources enter the node as a read-only block
			// ahead of the step description.
			desc, err := d.dynamicStepDescription(ctx, stepCopy)
			if err != nil {
				return nil, fmt.Errorf("orchestrator: resolve context sources for step %q: %w", stepCopy.ID, err)
			}

			stepCopy.Description = desc
		} else {
			// Existing static dispatch: exact match on step.Agent always wins;
			// only when it misses do we consult the optional configured default.
			subAgent, err := d.resolveStaticAgent(step.Agent)
			if err != nil {
				return nil, err
			}

			runner = subAgent
		}

		// Each plan step is one sub-agent dispatch and gets its own
		// transcript file; tagging must sit inside the hook wrapper so
		// the hooks still observe the un-tagged agent identity.
		runner = tagRun(runner, stepCopy.Description)

		// Wrap with lifecycle hooks.
		runner = d.wrapWithHooks(stepCopy.ID, runner)

		nodes = append(nodes, orchestrate.Node{
			ID:          step.ID,
			Runner:      runner,
			Deps:        step.DependsOn,
			InputMapper: BuildInputMapper(runner.Protocol(), d.workingDir, contextSummary, plan.Goal, stepCopy, stepCopy.DependsOn, req.SessionID),
			Optional:    true,
		})
	}

	// Add summary node if there are multiple terminal nodes.
	terminalIDs := findTerminalNodes(nodes)

	if len(terminalIDs) > 1 {
		summaryNode := orchestrate.Node{
			ID:     "summary",
			Runner: d.planGen,
			Deps:   terminalIDs,
			InputMapper: func(upstream map[string]*schema.RunResponse) (*schema.RunRequest, error) {
				var sb strings.Builder

				sb.WriteString("Summarize the following completed step results:\n\n")

				for id, resp := range upstream {
					if resp != nil {
						fmt.Fprintf(&sb, "## Step: %s\n", id)

						for _, m := range resp.Messages {
							sb.WriteString(m.Text())
							sb.WriteString("\n")
						}

						sb.WriteString("\n")
					}
				}

				return &schema.RunRequest{
					Messages:  []schema.Message{schema.NewUserMessage(d.planGen.Protocol(), sb.String())},
					SessionID: req.SessionID,
				}, nil
			},
		}

		nodes = append(nodes, summaryNode)
	}

	return nodes, nil
}

// resolveStaticAgent maps a static plan step's agent ID to a registered
// sub-agent. Resolution order: exact match on stepAgent wins; on a miss, if a
// DAG default agent ID is configured it is looked up; otherwise the step is
// unresolvable and a diagnosable error is returned. The default fallback is
// disabled unless the caller configured it via WithDAGDefaultAgentID.
func (d *Dispatcher) resolveStaticAgent(stepAgent string) (agent.Agent, error) {
	if subAgent, ok := d.subAgents[stepAgent]; ok {
		return subAgent, nil
	}

	if d.dagDefaultAgentID == "" {
		return nil, fmt.Errorf("orchestrator: no agent registered for plan step agent %q and no DAG default agent configured", stepAgent)
	}

	subAgent, ok := d.subAgents[d.dagDefaultAgentID]
	if !ok {
		return nil, fmt.Errorf("orchestrator: no agent registered for plan step agent %q; configured DAG default agent %q is also not registered", stepAgent, d.dagDefaultAgentID)
	}

	return subAgent, nil
}

// buildDynamicAgent creates an ephemeral worker for a DAG node from its
// WorkerSpec. It is a thin adapter over the shared buildWorker path so a DAG
// dynamic node and a `spawn_worker` derivation resolve identical tool subsets,
// skills, guards and permission wrapping — one contract, one construction path.
//
// The `dynamic_<base_type>_<step_id>` instance ID is retained so existing
// traces and plan-step correlation keep reading the same way.
func (d *Dispatcher) buildDynamicAgent(stepID string, spec *DynamicAgentSpec) (*taskagent.Agent, error) {
	return d.buildWorker(fmt.Sprintf("dynamic_%s_%s", spec.BaseType, stepID), spec)
}

// dynamicStepDescription prepends a dynamic step's resolved context sources to
// its description, so a DAG node declaring `context: ["diff"]` reads the same
// read-only block a spawn_worker derivation would receive.
//
// A resolution failure is an error, not a degradation: running the node with
// its description alone would hand the worker a task that references context
// it never received (ORCH-R11). The error surfaces through buildNodes exactly
// like an invalid tool_access does.
func (d *Dispatcher) dynamicStepDescription(ctx context.Context, step PlanStep) (string, error) {
	if step.DynamicSpec == nil || len(step.DynamicSpec.ContextSources) == 0 {
		return step.Description, nil
	}

	block, err := d.resolveWorkerContext(ctx, step.DynamicSpec)
	if err != nil {
		return "", err
	}

	return joinBackground(block, step.Description), nil
}

// findTerminalNodes returns IDs of nodes that have no downstream dependents.
func findTerminalNodes(nodes []orchestrate.Node) []string {
	hasDependents := make(map[string]bool)

	for _, n := range nodes {
		for _, dep := range n.Deps {
			hasDependents[dep] = true
		}
	}

	var terminals []string

	for _, n := range nodes {
		if !hasDependents[n.ID] {
			terminals = append(terminals, n.ID)
		}
	}

	return terminals
}

// wrapWithHooks wraps an agent.Agent with lifecycle hooks.
func (d *Dispatcher) wrapWithHooks(agentID string, runner agent.Agent) agent.Agent {
	if len(d.hooks) == 0 {
		return runner
	}

	return &hookedAgent{
		inner:   runner,
		hooks:   d.hooks,
		agentID: agentID,
	}
}

// hookedAgent wraps an agent.Agent and invokes lifecycle hooks around Run calls.
type hookedAgent struct {
	inner   agent.Agent
	hooks   []hooks.Hook
	agentID string
}

func (h *hookedAgent) Run(ctx context.Context, req *schema.RunRequest) (*schema.RunResponse, error) {
	for _, hook := range h.hooks {
		if err := hook.OnBeforeRun(ctx, h.agentID, req); err != nil {
			return nil, fmt.Errorf("hook aborted run for %q: %w", h.agentID, err)
		}
	}

	resp, err := h.inner.Run(ctx, req)

	for _, v := range slices.Backward(h.hooks) {
		v.OnAfterRun(ctx, h.agentID, resp, err)
	}

	return resp, err
}

func (h *hookedAgent) Protocol() schema.Protocol { return h.inner.Protocol() }

// RunStream implements agent.StreamAgent for hookedAgent, enabling streaming through hooked agents.
func (h *hookedAgent) RunStream(ctx context.Context, req *schema.RunRequest) (*schema.RunStream, error) {
	for _, hook := range h.hooks {
		if err := hook.OnBeforeRun(ctx, h.agentID, req); err != nil {
			return nil, fmt.Errorf("hook aborted run for %q: %w", h.agentID, err)
		}
	}

	sa, ok := h.inner.(agent.StreamAgent)
	if !ok {
		return agent.RunToStream(ctx, h.inner, req), nil
	}

	stream, err := sa.RunStream(ctx, req)
	if err != nil {
		for _, v := range slices.Backward(h.hooks) {
			v.OnAfterRun(ctx, h.agentID, nil, err)
		}

		return nil, err
	}

	return stream, nil
}

func (h *hookedAgent) ID() string          { return h.inner.ID() }
func (h *hookedAgent) Name() string        { return h.inner.Name() }
func (h *hookedAgent) Description() string { return h.inner.Description() }
