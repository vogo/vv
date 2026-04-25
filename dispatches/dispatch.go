package dispatches

import (
	"context"
	"fmt"
	"time"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/hooks"
	"github.com/vogo/vv/registries"
)

// phaseTracker intercepts events to accumulate per-phase execution stats.
type phaseTracker struct {
	toolCalls        int
	promptTokens     int
	completionTokens int
}

// wrap returns a send function that intercepts stats events before forwarding.
func (pt *phaseTracker) wrap(send func(schema.Event) error) func(schema.Event) error {
	return func(ev schema.Event) error {
		switch ev.Type {
		case schema.EventToolCallStart:
			pt.toolCalls++
		case schema.EventLLMCallEnd:
			if data, ok := ev.Data.(schema.LLMCallEndData); ok {
				pt.promptTokens += data.PromptTokens
				pt.completionTokens += data.CompletionTokens
			}
		}

		return send(ev)
	}
}

// Dispatcher is the unified Primary Assistant entry point. As of M7 its sole
// job is forwarding requests to the Primary, with a depth-exceed fallback to
// the degraded (tool-free) Primary persona. The classical
// fastPath/intent/execute/summarize pipeline retired in M7.
type Dispatcher struct {
	agent.Base
	llm            aimodel.ChatCompleter
	model          string
	registry       *registries.Registry
	subAgents      map[string]agent.Agent
	planGen        agent.Agent
	maxConcurrency int
	fallbackAgent  agent.Agent
	workingDir     string
	toolsCfg       configs.ToolsConfig
	hooks          []hooks.Hook
	maxIterations  int
	runTokenBudget int

	projectInstructions string

	maxRecursionDepth int

	// primaryAssistant carries the unified Primary; required as of M7 — Run
	// and RunStream return an error when nil (set via WithPrimaryAssistant
	// or SetPrimaryAssistant; setup.New always installs one).
	primaryAssistant agent.Agent
}

// Option configures a Dispatcher.
type Option func(*Dispatcher)

// New creates a Dispatcher with required parameters and optional
// configuration. The Primary Assistant must be attached via
// WithPrimaryAssistant or SetPrimaryAssistant before Run/RunStream are
// called — production code wires it through setup.New.
func New(
	reg *registries.Registry,
	subAgents map[string]agent.Agent,
	planGen agent.Agent,
	opts ...Option,
) *Dispatcher {
	d := &Dispatcher{
		Base: agent.NewBase(agent.Config{
			ID:          "orchestrator",
			Name:        "Orchestrator Agent",
			Description: "Forwards user requests to the unified Primary Assistant",
		}),
		registry:          reg,
		subAgents:         subAgents,
		planGen:           planGen,
		maxRecursionDepth: 2,
	}

	for _, opt := range opts {
		opt(d)
	}

	return d
}

// WithLLM sets the LLM client used for dynamic agent creation in plan steps.
func WithLLM(llm aimodel.ChatCompleter, model string) Option {
	return func(d *Dispatcher) {
		d.llm = llm
		d.model = model
	}
}

// WithMaxConcurrency sets DAG concurrency limit for plan_task.
func WithMaxConcurrency(n int) Option {
	return func(d *Dispatcher) {
		d.maxConcurrency = n
	}
}

// WithFallbackAgent sets the fallback agent used on the depth-exceed path.
// setup.New installs the degraded (tool-free) Primary here so re-entering
// the dispatcher cannot trigger another recursive cycle.
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

// WithMaxRecursionDepth sets the max recursion depth.
func WithMaxRecursionDepth(n int) Option {
	return func(d *Dispatcher) {
		d.maxRecursionDepth = n
	}
}

// WithProjectInstructions sets the project instructions used by dynamic
// agents in plan steps.
func WithProjectInstructions(instructions string) Option {
	return func(d *Dispatcher) {
		d.projectInstructions = instructions
	}
}

// WithPrimaryAssistant attaches the unified Primary Assistant. Required as
// of M7 — the Dispatcher returns an error from Run / RunStream when no
// Primary is attached.
//
// Tool wiring (delegate_to_<agent>, plan_task, read/glob/grep, todo_write,
// ask_user) is the caller's responsibility — this Option only records the
// agent handle.
func WithPrimaryAssistant(a agent.Agent) Option {
	return func(d *Dispatcher) {
		d.primaryAssistant = a
	}
}

// SetPrimaryAssistant installs or replaces the Primary Assistant after
// construction. Used by setup.New because the Primary's plan_task tool
// holds a PlanExecutor handle on the Dispatcher itself — the Dispatcher
// must exist before the Primary can be built.
func (d *Dispatcher) SetPrimaryAssistant(a agent.Agent) {
	d.primaryAssistant = a
}

// SetFallbackAgent installs or replaces the fallback agent after
// construction. Used by setup.New to swap in the degraded (tool-free)
// Primary so the depth-exceed early-return keeps answering via the Primary
// persona.
func (d *Dispatcher) SetFallbackAgent(a agent.Agent) {
	d.fallbackAgent = a
}

// fallbackAgentName returns the agent ID label used in stream events / logs
// when the dispatcher routes through the fallback agent (depth-exceeded
// path).
func (d *Dispatcher) fallbackAgentName() string {
	if d.fallbackAgent != nil {
		return d.fallbackAgent.ID()
	}

	for id := range d.subAgents {
		return id
	}

	return "fallback"
}

// Run implements agent.Agent. Forwards to the Primary Assistant; falls back
// to the fallback agent only when recursion depth is exceeded.
func (d *Dispatcher) Run(ctx context.Context, req *schema.RunRequest) (*schema.RunResponse, error) {
	depth := DepthFrom(ctx)

	if depth >= d.maxRecursionDepth {
		return d.fallbackRun(ctx, req, nil)
	}

	if d.primaryAssistant == nil {
		return nil, fmt.Errorf("dispatcher: primary assistant required (classical pipeline removed in M7)")
	}

	return d.runPrimary(ctx, req)
}

// RunStream implements agent.StreamAgent. Same semantics as Run; on the
// depth-exceed fallback path a static `summarize` phase event is emitted
// after the fallback stream so HTTP / SSE consumers see the same event-flow
// shape as the main path (M7 G4 — zero LLM calls; the Summary text is a
// fixed sentinel rather than a real summarisation).
func (d *Dispatcher) RunStream(ctx context.Context, req *schema.RunRequest) (*schema.RunStream, error) {
	return schema.NewRunStream(ctx, agent.DefaultStreamBufferSize, func(ctx context.Context, send func(schema.Event) error) error {
		agentID := d.ID()
		sessionID := req.SessionID
		depth := DepthFrom(ctx)

		if depth >= d.maxRecursionDepth {
			if err := d.forwardSubAgentStream(ctx, send, d.fallbackAgent, req, d.fallbackAgentName(), "", sessionID); err != nil {
				return err
			}
			// M7 G4: emit a static summarize phase pair so consumers that key
			// off the main-path summarize event still see one on the
			// fallback path. Zero LLM calls — keeping the "cheap fallback"
			// invariant intact.
			start := time.Now()
			if err := send(schema.NewEvent(schema.EventPhaseStart, agentID, sessionID, schema.PhaseStartData{
				Phase: "summarize", PhaseIndex: 0, TotalPhase: 0,
			})); err != nil {
				return err
			}
			return send(schema.NewEvent(schema.EventPhaseEnd, agentID, sessionID, schema.PhaseEndData{
				Phase:    "summarize",
				Duration: time.Since(start).Milliseconds(),
				Summary:  "fallback path: no summarization performed",
			}))
		}

		if d.primaryAssistant == nil {
			return fmt.Errorf("dispatcher: primary assistant required (classical pipeline removed in M7)")
		}

		return d.runPrimaryStream(ctx, send, req, agentID, sessionID)
	}), nil
}

// Compile-time interface checks.
var (
	_ agent.Agent       = (*Dispatcher)(nil)
	_ agent.StreamAgent = (*Dispatcher)(nil)
)
