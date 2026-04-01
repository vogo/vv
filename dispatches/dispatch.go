package dispatches

import (
	"context"
	"log/slog"
	"time"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/hooks"
	"github.com/vogo/vv/registries"
)

// Dispatcher is the main orchestration agent. It receives user requests,
// explores project context, classifies the task, and dispatches to sub-agents.
// Behavioral equivalent of the former OrchestratorAgent.
type Dispatcher struct {
	agent.Base
	llm                 aimodel.ChatCompleter
	model               string
	registry            *registries.Registry
	subAgents           map[string]agent.Agent // built from registry, keyed by descriptor ID
	planGen             agent.Agent            // agent.Agent interface, not *taskagent.Agent
	maxConcurrency      int
	fallbackAgent       agent.Agent
	workingDir          string
	explorerAgent       agent.Agent
	plannerAgent        agent.Agent
	toolsCfg            configs.ToolsConfig // for dynamic agent tool registry construction
	hooks               []hooks.Hook
	maxIterations       int
	runTokenBudget      int
	plannerSystemPrompt string // used for classifyDirect fallback
}

// Option configures a Dispatcher.
type Option func(*Dispatcher)

// New creates a Dispatcher with required parameters and optional configuration.
func New(
	reg *registries.Registry,
	subAgents map[string]agent.Agent,
	explorerAgent agent.Agent,
	plannerAgent agent.Agent,
	planGen agent.Agent,
	opts ...Option,
) *Dispatcher {
	d := &Dispatcher{
		Base: agent.NewBase(agent.Config{
			ID:          "orchestrator",
			Name:        "Orchestrator Agent",
			Description: "Orchestrates user requests: explores context, plans tasks, dispatches to agents",
		}),
		registry:      reg,
		subAgents:     subAgents,
		explorerAgent: explorerAgent,
		plannerAgent:  plannerAgent,
		planGen:       planGen,
	}

	for _, opt := range opts {
		opt(d)
	}

	return d
}

// WithLLM sets the LLM client for direct classification calls and dynamic agent creation.
func WithLLM(llm aimodel.ChatCompleter, model string) Option {
	return func(d *Dispatcher) {
		d.llm = llm
		d.model = model
	}
}

// WithMaxConcurrency sets DAG concurrency limit.
func WithMaxConcurrency(n int) Option {
	return func(d *Dispatcher) {
		d.maxConcurrency = n
	}
}

// WithFallbackAgent sets the fallback agent for failed classifications.
func WithFallbackAgent(a agent.Agent) Option {
	return func(d *Dispatcher) {
		d.fallbackAgent = a
	}
}

// WithWorkingDir sets the working directory for enriching requests.
func WithWorkingDir(dir string) Option {
	return func(d *Dispatcher) {
		d.workingDir = dir
	}
}

// WithToolsConfig sets tool configuration for dynamic agent registry construction.
func WithToolsConfig(cfg configs.ToolsConfig) Option {
	return func(d *Dispatcher) {
		d.toolsCfg = cfg
	}
}

// WithHooks sets lifecycle hooks for sub-agent execution.
func WithHooks(hooks []hooks.Hook) Option {
	return func(d *Dispatcher) {
		d.hooks = hooks
	}
}

// WithMaxIterations sets the max iterations for dynamic agents.
func WithMaxIterations(n int) Option {
	return func(d *Dispatcher) {
		d.maxIterations = n
	}
}

// WithRunTokenBudget sets the token budget for dynamic agents.
func WithRunTokenBudget(n int) Option {
	return func(d *Dispatcher) {
		d.runTokenBudget = n
	}
}

// WithPlannerSystemPrompt sets the system prompt for classifyDirect fallback.
func WithPlannerSystemPrompt(p string) Option {
	return func(d *Dispatcher) {
		d.plannerSystemPrompt = p
	}
}

// Run implements agent.Agent. Orchestrates: explore -> classify -> dispatch.
func (d *Dispatcher) Run(ctx context.Context, req *schema.RunRequest) (*schema.RunResponse, error) {
	// Phase 1: Explore project context.
	contextSummary, exploreUsage := d.explore(ctx, req)

	// Phase 2: Plan/classify the task.
	result, planUsage, err := d.classify(ctx, req, contextSummary)
	if err != nil {
		slog.Warn("orchestrator: planning failed, falling back to chat", "error", err)

		return d.fallbackRun(ctx, req, aggregateUsage(exploreUsage, nil))
	}

	totalUsage := aggregateUsage(exploreUsage, planUsage)

	// Phase 3: Dispatch.
	enrichedReq := d.enrichRequest(req, contextSummary)

	switch result.Mode {
	case "direct":
		return d.runDirect(ctx, enrichedReq, result, totalUsage)
	case "plan":
		return d.runPlan(ctx, enrichedReq, result.Plan, totalUsage, contextSummary)
	default:
		slog.Warn("orchestrator: unknown mode, falling back to chat", "mode", result.Mode)

		return d.fallbackRun(ctx, enrichedReq, totalUsage)
	}
}

// RunStream implements agent.StreamAgent. Same flow as Run but with streaming events.
func (d *Dispatcher) RunStream(ctx context.Context, req *schema.RunRequest) (*schema.RunStream, error) {
	return schema.NewRunStream(ctx, agent.DefaultStreamBufferSize, func(ctx context.Context, send func(schema.Event) error) error {
		agentID := d.ID()
		sessionID := req.SessionID

		// Determine total phases (explore is optional).
		totalPhases := 2 // plan + dispatch

		if d.explorerAgent != nil {
			totalPhases = 3
		}

		phaseIdx := 0

		// Phase 1: Explore project context.
		var contextSummary string

		var exploreUsage *aimodel.Usage

		if d.explorerAgent != nil {
			phaseIdx++

			if err := send(schema.NewEvent(schema.EventPhaseStart, agentID, sessionID, schema.PhaseStartData{
				Phase: "explore", PhaseIndex: phaseIdx, TotalPhase: totalPhases,
			})); err != nil {
				return err
			}

			exploreStart := time.Now()
			contextSummary, exploreUsage = d.exploreStream(ctx, req, send)

			if err := send(schema.NewEvent(schema.EventPhaseEnd, agentID, sessionID, schema.PhaseEndData{
				Phase: "explore", Duration: time.Since(exploreStart).Milliseconds(),
			})); err != nil {
				return err
			}
		}

		// Phase 2: Plan/classify the task.
		phaseIdx++

		if err := send(schema.NewEvent(schema.EventPhaseStart, agentID, sessionID, schema.PhaseStartData{
			Phase: "plan", PhaseIndex: phaseIdx, TotalPhase: totalPhases,
		})); err != nil {
			return err
		}

		planStart := time.Now()
		result, planUsage, planErr := d.classifyStream(ctx, req, contextSummary, send)

		if err := send(schema.NewEvent(schema.EventPhaseEnd, agentID, sessionID, schema.PhaseEndData{
			Phase: "plan", Duration: time.Since(planStart).Milliseconds(),
		})); err != nil {
			return err
		}

		if planErr != nil {
			slog.Warn("orchestrator: planning failed, falling back to chat stream", "error", planErr)

			return d.forwardSubAgentStream(ctx, send, d.fallbackAgent, req, "chat", "", sessionID)
		}

		enrichedReq := d.enrichRequest(req, contextSummary)
		_ = aggregateUsage(exploreUsage, planUsage) // tracked internally

		// Phase 3: Dispatch.
		phaseIdx++

		if err := send(schema.NewEvent(schema.EventPhaseStart, agentID, sessionID, schema.PhaseStartData{
			Phase: "dispatch", PhaseIndex: phaseIdx, TotalPhase: totalPhases,
		})); err != nil {
			return err
		}

		dispatchStart := time.Now()

		var dispatchErr error

		switch result.Mode {
		case "direct":
			subAgent, ok := d.subAgents[result.Agent]
			if !ok {
				subAgent = d.fallbackAgent
			}

			dispatchErr = d.forwardSubAgentStream(ctx, send, subAgent, enrichedReq, result.Agent, "", sessionID)
		case "plan":
			dispatchErr = d.streamPlan(ctx, send, enrichedReq, result.Plan, contextSummary, sessionID)
		default:
			dispatchErr = d.forwardSubAgentStream(ctx, send, d.fallbackAgent, enrichedReq, "chat", "", sessionID)
		}

		if sendErr := send(schema.NewEvent(schema.EventPhaseEnd, agentID, sessionID, schema.PhaseEndData{
			Phase: "dispatch", Duration: time.Since(dispatchStart).Milliseconds(),
		})); sendErr != nil {
			return sendErr
		}

		return dispatchErr
	}), nil
}

// Compile-time interface checks.
var (
	_ agent.Agent       = (*Dispatcher)(nil)
	_ agent.StreamAgent = (*Dispatcher)(nil)
)
