package dispatches

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/orchestrate"
	"github.com/vogo/vage/prompt"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/tool"
	"github.com/vogo/vv/hooks"
	"github.com/vogo/vv/registries"
)

// runPlan builds and executes a DAG from the plan.
func (d *Dispatcher) runPlan(ctx context.Context, req *schema.RunRequest, plan *Plan, classifyUsage *aimodel.Usage, contextSummary string) (*schema.RunResponse, error) {
	nodes, err := d.buildNodes(plan, req, contextSummary)
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
func (d *Dispatcher) buildNodes(plan *Plan, req *schema.RunRequest, contextSummary string) ([]orchestrate.Node, error) {
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
		} else {
			// Existing static dispatch: exact match on step.Agent always wins;
			// only when it misses do we consult the optional configured default.
			subAgent, err := d.resolveStaticAgent(step.Agent)
			if err != nil {
				return nil, err
			}

			runner = subAgent
		}

		// Wrap with lifecycle hooks.
		runner = d.wrapWithHooks(stepCopy.ID, runner)

		nodes = append(nodes, orchestrate.Node{
			ID:          step.ID,
			Runner:      runner,
			Deps:        step.DependsOn,
			InputMapper: BuildInputMapper(d.workingDir, contextSummary, plan.Goal, stepCopy, stepCopy.DependsOn, req.SessionID),
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
							sb.WriteString(m.Content.Text())
							sb.WriteString("\n")
						}

						sb.WriteString("\n")
					}
				}

				return &schema.RunRequest{
					Messages:  []schema.Message{schema.NewUserMessage(sb.String())},
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

// buildDynamicAgent creates an ephemeral taskagent from a DynamicAgentSpec.
// Uses the registry to resolve tool profile and system prompt instead of hardcoded maps.
func (d *Dispatcher) buildDynamicAgent(stepID string, spec *DynamicAgentSpec) (*taskagent.Agent, error) {
	// Look up the base type descriptor from the registry.
	desc, ok := d.registry.Get(spec.BaseType)
	if !ok {
		return nil, fmt.Errorf("unknown base type %q", spec.BaseType)
	}

	// Determine tool profile.
	var profile registries.ToolProfile
	if spec.ToolAccess != "" {
		p, ok := registries.ProfileByName(spec.ToolAccess)
		if !ok {
			return nil, fmt.Errorf("unknown tool access profile %q", spec.ToolAccess)
		}

		profile = p
	} else {
		profile = desc.ToolProfile
	}

	// Build tool registry from profile.
	var toolReg *tool.Registry
	if len(profile.Capabilities) > 0 {
		reg, err := profile.BuildRegistry(d.toolsCfg)
		if err != nil {
			return nil, fmt.Errorf("build tool registry for dynamic agent: %w", err)
		}

		toolReg = reg
	}

	// Determine system prompt.
	systemPrompt := spec.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = desc.SystemPrompt
	}

	// Append project instructions to dynamic agent prompts.
	systemPrompt = appendProjectInstructions(systemPrompt, d.projectInstructions)

	// Determine model.
	model := spec.Model
	if model == "" {
		model = d.model
	}

	// Determine max iterations.
	maxIter := d.maxIterations
	if maxIter == 0 {
		maxIter = 10 // sensible default
	}

	var opts []taskagent.Option

	opts = append(
		opts,
		taskagent.WithChatCompleter(d.llm),
		taskagent.WithModel(model),
		taskagent.WithSystemPrompt(prompt.StringPrompt(systemPrompt)),
		taskagent.WithMaxIterations(maxIter),
	)

	if toolReg != nil {
		opts = append(opts, taskagent.WithToolRegistry(toolReg))
	}

	if d.runTokenBudget > 0 {
		opts = append(opts, taskagent.WithRunTokenBudget(d.runTokenBudget))
	}

	return taskagent.New(
		agent.Config{
			ID:          fmt.Sprintf("dynamic_%s_%s", spec.BaseType, stepID),
			Name:        fmt.Sprintf("Dynamic %s Agent (%s)", spec.BaseType, stepID),
			Description: fmt.Sprintf("Dynamically created %s agent for step %s", spec.BaseType, stepID),
		},
		opts...,
	), nil
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

	for i := len(h.hooks) - 1; i >= 0; i-- {
		h.hooks[i].OnAfterRun(ctx, h.agentID, resp, err)
	}

	return resp, err
}

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
		for i := len(h.hooks) - 1; i >= 0; i-- {
			h.hooks[i].OnAfterRun(ctx, h.agentID, nil, err)
		}

		return nil, err
	}

	return stream, nil
}

func (h *hookedAgent) ID() string          { return h.inner.ID() }
func (h *hookedAgent) Name() string        { return h.inner.Name() }
func (h *hookedAgent) Description() string { return h.inner.Description() }
