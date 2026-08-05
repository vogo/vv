package dispatches

import (
	"context"
	"fmt"
	"sync/atomic"

	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/hook"
	"github.com/vogo/vage/largemodel"
	"github.com/vogo/vage/memory"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/session/tree"
	"github.com/vogo/vage/tool"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/hooks"
	"github.com/vogo/vv/registries"
)

// Dispatcher is the unified Primary Assistant entry point. Its sole job is
// to forward requests to the Primary, with a fallback to the degraded
// (tool-free) Primary persona when recursion depth is exceeded. The
// classical fastPath/intent/execute/summarize pipeline has been retired.
type Dispatcher struct {
	agent.Base
	llm            largemodel.Caller
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

	// Capability dimensions available to derived workers. Both registries
	// are startup-time constants; nil falls back to the built-in defaults
	// (see Dispatcher.skillRegistry / contextSourceRegistry).
	skills         *registries.SkillRegistry
	contextSources *registries.ContextSourceRegistry

	// regOpts and wrapToolRegistry carry the assembly layer's enforcement
	// down to derived workers: regOpts injects path guard / guardian into
	// the tools a ToolProfile assembles, wrapToolRegistry applies the same
	// permission → truncation → debug chain sub-agents get. Both can only
	// *reduce* what the profile granted — never widen it.
	regOpts          []registries.RegistryOption
	wrapToolRegistry func(*tool.Registry) tool.ToolRegistry

	// hookManager is the shared event bus injected into derived workers so
	// their iteration / tool / context events reach trace and session
	// subsystems exactly like a registered sub-agent's do.
	hookManager *hook.Manager

	// memory is the shared session memory manager attached only to workers
	// spawned with isolation="shared".
	memory *memory.Manager

	// maxParallelToolCalls / promptCaching mirror the agents.* config onto
	// derived workers so a user who tuned (or disabled) them does not get
	// different behaviour depending on who executes the task. Zero /
	// unset leaves the taskagent defaults in place.
	maxParallelToolCalls int
	promptCaching        *bool

	// workerSeq numbers spawned worker instances within this process run.
	workerSeq atomic.Uint64

	// dagDefaultAgentID is the optional sub-agent ID used to resolve a static
	// plan step whose step.Agent is not registered. Zero value disables the
	// fallback: an unknown static agent then yields a diagnosable build error.
	// This is distinct from fallbackAgent (recursion-depth degradation) — it
	// only names an entry looked up in subAgents, never a separate instance.
	dagDefaultAgentID string

	projectInstructions string

	maxRecursionDepth int

	// primaryAssistant carries the unified Primary; required now — Run and
	// RunStream return an error when nil (set via WithPrimaryAssistant or
	// SetPrimaryAssistant; setup.New always installs one).
	primaryAssistant agent.Agent

	// treeStore is the optional SessionTree backend. When non-nil and
	// writeTree is true, RunPlan mirrors plan_task DAGs into the tree so
	// the user-visible structure tracks dispatcher activity automatically.
	treeStore tree.SessionTreeStore
	writeTree bool
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
// It also adopts the caller's wire protocol on the Dispatcher so messages
// built via orchestrator.Protocol() match the configured LLM.
func WithLLM(llm largemodel.Caller, model string) Option {
	return func(d *Dispatcher) {
		d.llm = llm
		d.model = model
		if llm != nil {
			d.AgentProtocol = llm.Protocol()
		}
	}
}

// WithMaxConcurrency sets DAG concurrency limit for plan_task.
func WithMaxConcurrency(n int) Option {
	return func(d *Dispatcher) {
		d.maxConcurrency = n
	}
}

// WithFallbackAgent sets the fallback agent used when recursion depth is
// exceeded. setup.New installs the degraded (tool-free) Primary here so
// re-entering the dispatcher cannot trigger another recursive cycle.
func WithFallbackAgent(a agent.Agent) Option {
	return func(d *Dispatcher) {
		d.fallbackAgent = a
	}
}

// WithDAGDefaultAgentID sets the sub-agent ID used to resolve a static plan
// step whose step.Agent is not registered. It is disabled by default (zero
// value); production assembly does not set it implicitly. This is NOT the same
// as WithFallbackAgent: the latter names an executable agent for recursion
// depth / DAG-build degradation, whereas this only names a subAgents entry
// consulted when exact match on step.Agent misses.
func WithDAGDefaultAgentID(id string) Option {
	return func(d *Dispatcher) {
		d.dagDefaultAgentID = id
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

// WithSkills installs the skill registry consulted when a worker spec names
// skills. nil / unset falls back to registries.DefaultSkills().
func WithSkills(reg *registries.SkillRegistry) Option {
	return func(d *Dispatcher) {
		d.skills = reg
	}
}

// WithContextSources installs the context source registry consulted when a
// worker spec names context sources. nil / unset falls back to
// registries.DefaultContextSources bound to the dispatcher's working dir.
func WithContextSources(reg *registries.ContextSourceRegistry) Option {
	return func(d *Dispatcher) {
		d.contextSources = reg
	}
}

// WithRegistryOptions passes the assembly layer's RegistryOptions (path guard,
// bash path guardian) into every tool registry built for a derived worker.
// Without it a worker's file tools would run unguarded — the profile would be
// the only boundary, which is exactly what the permission model forbids.
func WithRegistryOptions(opts ...registries.RegistryOption) Option {
	return func(d *Dispatcher) {
		d.regOpts = opts
	}
}

// WithToolRegistryWrapper installs the wrapping chain applied to a derived
// worker's tool registry (permission confirmation → output truncation → debug),
// mirroring what setup applies to registered sub-agents. The wrapper may only
// deny or transform calls to tools the profile already assembled.
func WithToolRegistryWrapper(fn func(*tool.Registry) tool.ToolRegistry) Option {
	return func(d *Dispatcher) {
		d.wrapToolRegistry = fn
	}
}

// WithHookManager injects the shared event bus into derived workers so their
// native events reach trace / session subsystems like a sub-agent's do.
func WithHookManager(mgr *hook.Manager) Option {
	return func(d *Dispatcher) {
		d.hookManager = mgr
	}
}

// WithMemory installs the shared session memory manager attached to workers
// spawned with isolation="shared". Isolated workers (the default) never see it.
func WithMemory(m *memory.Manager) Option {
	return func(d *Dispatcher) {
		d.memory = m
	}
}

// WithAgentRuntimeDefaults mirrors the agents.* runtime knobs onto derived
// workers: concurrent tool dispatch cap and prompt-cache hint emission. Passing
// the same values setup gives registered sub-agents keeps a task's behaviour
// independent of whether a worker or a preset combination executes it.
func WithAgentRuntimeDefaults(maxParallelToolCalls int, promptCaching bool) Option {
	return func(d *Dispatcher) {
		d.maxParallelToolCalls = maxParallelToolCalls
		d.promptCaching = &promptCaching
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

// WithTreeStore installs the SessionTree backend the dispatcher uses for
// plan_task -> tree mirroring. nil disables the feature regardless of
// WithWriteTreeEnabled.
func WithTreeStore(s tree.SessionTreeStore) Option {
	return func(d *Dispatcher) {
		d.treeStore = s
	}
}

// WithWriteTreeEnabled toggles plan_task -> SessionTree mirroring. Default
// false (the feature is opt-in until product reaction is observed).
func WithWriteTreeEnabled(on bool) Option {
	return func(d *Dispatcher) {
		d.writeTree = on
	}
}

// WithPrimaryAssistant attaches the unified Primary Assistant. The
// Dispatcher returns an error from Run / RunStream when no Primary is
// attached.
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

// Primary returns the attached Primary Assistant or nil. Read access is
// required by the resume path (vv --resume / POST /v1/sessions/{id}/resume)
// because checkpoints whose AgentID is the Primary's id must dispatch
// directly to the Primary instance — not through Run, which would start a
// new request from the user's perspective.
func (d *Dispatcher) Primary() agent.Agent {
	return d.primaryAssistant
}

// SetFallbackAgent installs or replaces the fallback agent after
// construction. setup.New uses it to swap in the degraded (tool-free)
// Primary so the depth-exceeded path still answers in the Primary persona.
func (d *Dispatcher) SetFallbackAgent(a agent.Agent) {
	d.fallbackAgent = a
}

// Run implements agent.Agent. Forwards to the Primary Assistant; falls back
// to the fallback agent only when recursion depth is exceeded.
func (d *Dispatcher) Run(ctx context.Context, req *schema.RunRequest) (*schema.RunResponse, error) {
	depth := DepthFrom(ctx)

	if depth >= d.maxRecursionDepth {
		return d.fallbackRun(ctx, req, nil)
	}

	if d.primaryAssistant == nil {
		return nil, fmt.Errorf("dispatcher: primary assistant required (classical pipeline removed)")
	}

	return d.runPrimary(ctx, req)
}

// RunStream implements agent.StreamAgent. Same semantics as Run; on the
// depth-exceed fallback path the degraded Primary stream is relayed directly,
// matching the main path shape (no phase or sub-agent envelope).
func (d *Dispatcher) RunStream(ctx context.Context, req *schema.RunRequest) (*schema.RunStream, error) {
	return schema.NewRunStream(ctx, agent.DefaultStreamBufferSize, func(ctx context.Context, send func(schema.Event) error) error {
		depth := DepthFrom(ctx)

		if depth >= d.maxRecursionDepth {
			return relayAgentStream(ctx, send, d.fallbackAgent, req)
		}

		if d.primaryAssistant == nil {
			return fmt.Errorf("dispatcher: primary assistant required (classical pipeline removed)")
		}

		return d.runPrimaryStream(ctx, send, req)
	}), nil
}

// Compile-time interface checks.
var (
	_ agent.Agent       = (*Dispatcher)(nil)
	_ agent.StreamAgent = (*Dispatcher)(nil)
)
