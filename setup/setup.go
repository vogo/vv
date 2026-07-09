package setup

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/checkpoint"
	vctx "github.com/vogo/vage/context"
	"github.com/vogo/vage/guard"
	"github.com/vogo/vage/hook"
	"github.com/vogo/vage/largemodel"
	"github.com/vogo/vage/memory"
	"github.com/vogo/vage/prompt"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/session"
	"github.com/vogo/vage/session/tree"
	"github.com/vogo/vage/tool"
	"github.com/vogo/vage/tool/askuser"
	"github.com/vogo/vage/tool/bash"
	sessiontreetool "github.com/vogo/vage/tool/sessiontree"
	"github.com/vogo/vage/tool/todo"
	"github.com/vogo/vage/tool/toolkit"
	"github.com/vogo/vage/tool/vectorsearch"
	wsworkspace "github.com/vogo/vage/tool/workspace"
	"github.com/vogo/vage/vector"
	"github.com/vogo/vage/workspace"
	"github.com/vogo/vv/agents"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/debugs"
	"github.com/vogo/vv/dispatches"
	"github.com/vogo/vv/hooks"
	"github.com/vogo/vv/memories"
	"github.com/vogo/vv/registries"
	"github.com/vogo/vv/traces/budgets"
	"github.com/vogo/vv/traces/costtraces"
	"github.com/vogo/vv/traces/tracelog"
)

// Result holds the assembled components for the application.
type Result struct {
	Dispatcher   *dispatches.Dispatcher
	PathGuard    *toolkit.PathGuard
	PathGuardian *bash.PathGuardian
	HookManager  *hook.Manager         // nil when no hook-based features are enabled
	Workspace    workspace.Workspace   // nil when Plan Workspace is disabled (cfg.Session.Enabled=false)
	TreeStore    tree.SessionTreeStore // nil when SessionTree subsystem is disabled
	VectorStore  vector.VectorStore    // nil when vector subsystem disabled
	VectorEmb    vector.Embedder       // nil when vector subsystem disabled
	registry     *registries.Registry
	subAgents    map[string]agent.Agent
}

// Agents returns the dispatchable agents suitable for HTTP service registration.
// Results are sorted by agent ID for deterministic ordering.
func (r *Result) Agents() []agent.Agent {
	result := make([]agent.Agent, 0, len(r.subAgents))
	for _, a := range r.subAgents {
		result = append(result, a)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].ID() < result[j].ID()
	})

	return result
}

// Agent returns a specific sub-agent by ID, or nil if not found.
func (r *Result) Agent(id string) agent.Agent {
	return r.subAgents[id]
}

// Options configures the setup process.
type Options struct {
	WrapToolRegistry func(*tool.Registry) tool.ToolRegistry // optional: wraps tool registries (e.g., CLI confirmation)
	UserInteractor   askuser.UserInteractor                 // optional: interactor for ask_user tool
	AskUserTimeout   time.Duration                          // optional: timeout for ask_user responses
	DebugSink        *debugs.Sink                           // optional: debug sink (constructed only when cfg.Debug is true)
	HookManager      *hook.Manager                          // optional: pre-built event bus; injected into every TaskAgent
	// Workspace, when non-nil, exposes the per-session Plan Workspace to
	// every dispatchable agent (and the Primary Assistant) by registering
	// plan_update / notes_write / notes_read tools on Primary's tool
	// registry and appending a vctx.WorkspaceSource to the ContextBuilder
	// pipeline of every TaskAgent. nil disables Plan Workspace wiring.
	Workspace workspace.Workspace

	// TreeStore, when non-nil, exposes the per-session SessionTree to every
	// TaskAgent through a vctx.SessionTreeSource (read-only) and lets the
	// Primary write to it via the tree_* tools. nil disables wiring.
	TreeStore tree.SessionTreeStore

	// TreePredicate, when non-nil, gates SessionTreeSource on a per-session
	// basis: the source short-circuits with Status=Skipped until the
	// predicate returns true. Used to plumb the
	// session_tree.auto_enable_after_events policy from Init's event
	// counter through to every dispatched agent's ContextBuilder. nil
	// keeps the source always-on.
	TreePredicate func(ctx context.Context, sessionID string) bool

	// VectorStore + VectorEmbedder, when both non-nil, expose the vector
	// subsystem to: (1) buildExtraContextSources via VectorRecallSource;
	// (2) the Primary Assistant's tool registry via vector_search /
	// vector_add. nil disables wiring on a per-call basis.
	VectorStore    vector.VectorStore
	VectorEmbedder vector.Embedder

	// VectorTopK is the default TopK used by VectorRecallSource when its
	// own TopK option is unset. 0 falls back to the store / source default.
	VectorTopK int

	// IterationStore enables per-iteration ReAct checkpointing on every
	// TaskAgent constructed by setup.New. nil disables the option (no
	// checkpoint files written, Resume returns ErrInvalidArgument). Init
	// populates this from buildIterationStore when cfg.Session.IsEnabled().
	IterationStore checkpoint.IterationStore

	// MetricsStore persists per-session SessionMetrics. Init populates
	// this from buildMetricsStore when cfg.Session.IsEnabled(). nil
	// disables metrics persistence end-to-end (no hook attached, no
	// HTTP /metrics endpoint, no resume_count bumps).
	MetricsStore session.MetricsStore

	// MetricsHook is the hook.Hook implementation that observes
	// EventLLMCallEnd / EventAgentEnd / EventContextEdited and writes
	// counters into MetricsStore. Init constructs and registers it
	// against opts.HookManager when MetricsStore is non-nil. Exposed on
	// Options so external callers (eval, evaluators) can drive the
	// hook directly without a HookManager round-trip.
	MetricsHook *session.SessionMetricsHook

	// BuildReportSink, when non-nil, archives the per-turn BuildReport
	// produced by every TaskAgent's internal context Builder. Init
	// populates from buildBuildReportSink when cfg.Session.IsEnabled()
	// AND cfg.Session.PersistBuildReportsEnabled() is true.
	BuildReportSink vctx.BuildReportSink

	// CheckpointFailureCB is forwarded by setup.New into every
	// TaskAgent's WithCheckpointFailureCallback. Init wires this to
	// MetricsHook.RecordCheckpointFailure so the
	// CheckpointSaveFailures counter advances. nil keeps the option
	// off — taskagent's failure path then logs only via slog.Warn.
	CheckpointFailureCB taskagent.CheckpointFailureCallback

	// RouterLLM / RouterModel configure the Dispatcher's dedicated routing
	// LLM client. When both are non-zero, Dispatcher routing calls (intent,
	// unified_intent, classify, reassess) use this client instead of the
	// main LLM. Init populates them when cfg.Orchestrate.Router.Model is
	// set; external callers of setup.New may leave both zero to keep the
	// legacy behaviour.
	RouterLLM   aimodel.ChatCompleter
	RouterModel string
}

// New reads config, registers all agents, and constructs the Dispatcher.
func New(
	cfg *configs.Config,
	llm aimodel.ChatCompleter,
	memMgr *memory.Manager,
	persistentMem memory.Memory,
	opts *Options,
) (*Result, error) {
	// 0. Build path guard + path guardian from cfg.Tools.AllowedDirs.
	pathGuard, pathGuardian, err := buildPathEnforcement(&cfg.Tools)
	if err != nil {
		return nil, err
	}

	regOpts := []registries.RegistryOption{}
	if pathGuard.Allowed() {
		regOpts = append(regOpts, registries.WithPathGuard(pathGuard))
	}

	if pathGuardian != nil {
		regOpts = append(regOpts, registries.WithPathGuardian(pathGuardian))
	}

	// 1. Create the registry and register all agents. The chat and explorer
	// agents are gone: the unified Primary Assistant handles chat inline (no
	// tools) and exploration via its read/glob/grep tools.
	reg := registries.New()
	agents.RegisterCoder(reg)
	agents.RegisterResearcher(reg)
	agents.RegisterReviewer(reg)
	agents.RegisterPlanner(reg)

	// 2. Build sub-agents from registry (dispatchable agents only).
	subAgents := make(map[string]agent.Agent)

	// One todo.Store per process — shared across every dispatchable agent so
	// a multi-agent dispatcher plan (coder -> reviewer -> coder) sees one
	// monotonic list per session. Skipped entirely when VV_DISABLE_TODO=true.
	todoStore := todo.NewStore()
	todoDisabled := os.Getenv("VV_DISABLE_TODO") == "true"

	for _, desc := range reg.Dispatchable() {
		toolReg, err := desc.ToolProfile.BuildRegistry(cfg.Tools, regOpts...)
		if err != nil {
			return nil, fmt.Errorf("build tool registry for %q: %w", desc.ID, err)
		}

		// Register ask_user for agents that should have it.
		// Skip chat (direct conversation; no need for ask_user tool).
		if opts != nil && opts.UserInteractor != nil && desc.ID != "chat" {
			askuserTool := askuser.New(opts.UserInteractor, askuser.WithTimeout(opts.AskUserTimeout))
			_ = toolReg.RegisterIfAbsent(askuserTool.ToolDef(), askuserTool.Handler())
		}

		// Register todo_write for agents that have any tool capability. Chat
		// (ProfileNone) gets nothing; coder / researcher / reviewer get the
		// same shared store.
		if !todoDisabled && len(desc.ToolProfile.Capabilities) > 0 {
			if err := todo.Register(toolReg, todoStore); err != nil {
				return nil, fmt.Errorf("register todo_write for %q: %w", desc.ID, err)
			}
		}

		// Apply optional tool registry wrapping (e.g., CLI confirmation).
		var finalToolReg tool.ToolRegistry = toolReg

		if opts != nil && opts.WrapToolRegistry != nil {
			finalToolReg = opts.WrapToolRegistry(toolReg)
		}

		// Wrap with truncating registry for tool output limits.
		if cfg.Context.ToolOutputMaxTokens > 0 {
			finalToolReg = tool.NewTruncatingToolRegistry(finalToolReg, cfg.Context.ToolOutputMaxTokens)
		}

		// Debug decorator is OUTERMOST so it sees the post-truncation result the agent receives.
		if cfg.Debug && opts != nil && opts.DebugSink != nil {
			finalToolReg = debugs.NewDebuggingToolRegistry(finalToolReg, opts.DebugSink)
		}

		factoryOpts := registries.FactoryOptions{
			LLM:                  llm,
			Model:                cfg.LLM.Model,
			ToolRegistry:         finalToolReg,
			MaxIterations:        cfg.Agents.MaxIterations,
			RunTokenBudget:       cfg.Agents.RunTokenBudget,
			MaxParallelToolCalls: cfg.Agents.MaxParallelToolCalls,
			PromptCaching:        cfg.Agents.EffectivePromptCaching(),
			Memory:               memMgr,
			PersistentMemory:     persistentMem,
			ProjectInstructions:  cfg.ProjectInstructions,
			ToolResultGuards:     buildToolResultGuards(cfg.Security.ToolResultInjection),
			HookManager:          getHookManager(opts),
			ExtraContextSources:  buildExtraContextSources(opts),
			IterationStore:       getIterationStore(opts),
			BuildReportSink:      getBuildReportSink(opts),
			CheckpointFailureCB:  getCheckpointFailureCB(opts),
		}

		a, err := desc.Factory(factoryOpts)
		if err != nil {
			return nil, fmt.Errorf("create agent %q: %w", desc.ID, err)
		}

		subAgents[desc.ID] = a
	}

	// 3. Build plan summarizer.
	planGenOpts := []taskagent.Option{
		taskagent.WithChatCompleter(llm),
		taskagent.WithModel(cfg.LLM.Model),
		taskagent.WithSystemPrompt(prompt.StringPrompt(dispatches.PlanSummaryPrompt)),
		taskagent.WithMaxIterations(1),
	}

	if mgr := getHookManager(opts); mgr != nil {
		planGenOpts = append(planGenOpts, taskagent.WithHookManager(mgr))
	}

	planGen := taskagent.New(
		agent.Config{ID: "plan-gen", Name: "Plan Generator", Description: "Summarizes execution plan results"},
		planGenOpts...,
	)

	// 6. Configure hooks.
	hooks := []hooks.Hook{
		&hooks.LoggingHook{Logger: slog.Default()},
	}

	// 7. Resolve max concurrency.
	maxConcurrency := cfg.Orchestrate.MaxConcurrency
	if maxConcurrency == 0 {
		maxConcurrency = cfg.Memory.MaxConcurrency
	}

	if maxConcurrency == 0 {
		maxConcurrency = 2
	}

	// 6. Resolve max recursion depth.
	maxRecursionDepth := cfg.Orchestrate.MaxRecursionDepth
	if maxRecursionDepth == 0 {
		maxRecursionDepth = 2
	}

	// 7. Construct the dispatcher. It is unified only: it forwards to the
	// Primary Assistant (attached below) with a fallback to the degraded
	// Primary when recursion depth is exceeded. Stale orchestrate.* YAML
	// keys (summary_policy, replan, fast_path, unified_intent,
	// legacy_phase_events) are silently ignored — Load surfaces a slog.Warn
	// for any stale key.
	dispatcherOpts := []dispatches.Option{
		dispatches.WithLLM(llm, cfg.LLM.Model),
		dispatches.WithMaxConcurrency(maxConcurrency),
		dispatches.WithWorkingDir(cfg.Tools.BashWorkingDir),
		dispatches.WithToolsConfig(cfg.Tools),
		dispatches.WithHooks(hooks),
		dispatches.WithMaxIterations(cfg.Agents.MaxIterations),
		dispatches.WithRunTokenBudget(cfg.Agents.RunTokenBudget),
		dispatches.WithMaxRecursionDepth(maxRecursionDepth),
		dispatches.WithProjectInstructions(cfg.ProjectInstructions),
	}

	// SessionTree mirroring (B6 / write_tree): wire the store onto the
	// dispatcher when both the tree subsystem and the feature flag are on.
	// The dispatcher itself gates on writeTree so the store can be passed
	// unconditionally — keeping the option non-nil makes it easy for tests
	// to assert "did we wire?".
	if ts := getTreeStore(opts); ts != nil {
		dispatcherOpts = append(dispatcherOpts, dispatches.WithTreeStore(ts))
		if cfg.Orchestrate.IsWriteTreeEnabled() {
			dispatcherOpts = append(dispatcherOpts, dispatches.WithWriteTreeEnabled(true))
		}
	}

	dispatcher := dispatches.New(reg, subAgents, planGen, dispatcherOpts...)

	// 11. Build the Primary Assistant and the fallback Primary, then attach
	// both to the dispatcher.
	//
	// Constructed AFTER the dispatcher because the plan_task tool that the
	// Primary carries needs a PlanExecutor handle on the dispatcher, and
	// SetPrimaryAssistant is the post-construction setter that closes the
	// cycle.
	primary, err := buildPrimaryAssistant(cfg, llm, memMgr, regOpts, subAgents, dispatcher, todoStore, todoDisabled, opts)
	if err != nil {
		return nil, fmt.Errorf("build primary assistant: %w", err)
	}

	dispatcher.SetPrimaryAssistant(primary)

	// The fallback path (depth >= maxRecursionDepth, or mid-stream
	// classification failure) reuses the Primary persona via a degraded
	// Primary with no tools — same system prompt, but no delegate/plan/bash
	// so re-entering it cannot trigger another recursive dispatch cycle.
	fallbackPrimary, err := buildFallbackPrimary(cfg, llm, memMgr, opts)
	if err != nil {
		return nil, fmt.Errorf("build fallback primary: %w", err)
	}

	dispatcher.SetFallbackAgent(fallbackPrimary)

	return &Result{
		Dispatcher:   dispatcher,
		PathGuard:    pathGuard,
		PathGuardian: pathGuardian,
		HookManager:  getHookManager(opts),
		Workspace:    getWorkspace(opts),
		TreeStore:    getTreeStore(opts),
		VectorStore:  getVectorStore(opts),
		VectorEmb:    getVectorEmbedder(opts),
		registry:     reg,
		subAgents:    subAgents,
	}, nil
}

// getVectorStore safely extracts the optional vector store from opts.
func getVectorStore(opts *Options) vector.VectorStore {
	if opts == nil {
		return nil
	}
	return opts.VectorStore
}

// getVectorEmbedder safely extracts the optional vector embedder from opts.
func getVectorEmbedder(opts *Options) vector.Embedder {
	if opts == nil {
		return nil
	}
	return opts.VectorEmbedder
}

// buildPrimaryAssistant assembles the Primary Assistant agent for unified
// mode. The tool set combines the read-only file tools
// (read/glob/grep) with todo_write, ask_user, the per-specialist
// delegate_to_<agent> tools, and plan_task. The final registry passes
// through the same permission / truncation / debug wrapping chain applied
// to sub-agents so CLI confirmation prompts and tool-output limits cover
// the Primary unchanged.
func buildPrimaryAssistant(
	cfg *configs.Config,
	llm aimodel.ChatCompleter,
	memMgr *memory.Manager,
	regOpts []registries.RegistryOption,
	subAgents map[string]agent.Agent,
	planExec dispatches.PlanExecutor,
	todoStore *todo.Store,
	todoDisabled bool,
	opts *Options,
) (agent.Agent, error) {
	profile := primaryToolProfile(cfg)

	toolReg, err := profile.BuildRegistry(cfg.Tools, regOpts...)
	if err != nil {
		return nil, fmt.Errorf("primary: build %s registry: %w", profile.Name, err)
	}

	// ask_user — optional, mirrors the coder/researcher/reviewer wiring.
	if opts != nil && opts.UserInteractor != nil {
		askuserTool := askuser.New(opts.UserInteractor, askuser.WithTimeout(opts.AskUserTimeout))
		_ = toolReg.RegisterIfAbsent(askuserTool.ToolDef(), askuserTool.Handler())
	}

	// todo_write — share the same process-level store so Primary's plan
	// items are visible to any specialist it delegates to.
	if !todoDisabled {
		if err := todo.Register(toolReg, todoStore); err != nil {
			return nil, fmt.Errorf("primary: register todo_write: %w", err)
		}
	}

	// delegate_to_<agent> tools — one per dispatchable specialist present in
	// subAgents. chat is omitted deliberately: the Primary replaces the
	// chat agent's responsibilities.
	delegateIDs := []string{"coder", "researcher", "reviewer"}
	if err := dispatches.RegisterDelegateTools(toolReg, subAgents, delegateIDs); err != nil {
		return nil, fmt.Errorf("primary: register delegate tools: %w", err)
	}

	// plan_task — drives the dispatcher's existing DAG machinery.
	if err := dispatches.RegisterPlanTaskTool(toolReg, planExec); err != nil {
		return nil, fmt.Errorf("primary: register plan_task: %w", err)
	}

	// plan_update / notes_write / notes_read — Plan Workspace tools, only
	// registered when the workspace is wired (session subsystem enabled).
	// Specialist sub-agents read plan.md via WorkspaceSource (in
	// ExtraContextSources) but do not get the write tools — Plan
	// Workspace is the Primary's concern, specialists just consume its
	// current state.
	if opts != nil && opts.Workspace != nil {
		if err := wsworkspace.RegisterPlan(toolReg, opts.Workspace); err != nil {
			return nil, fmt.Errorf("primary: register plan_update: %w", err)
		}
		if err := wsworkspace.RegisterNotes(toolReg, opts.Workspace); err != nil {
			return nil, fmt.Errorf("primary: register notes_write/notes_read: %w", err)
		}
	}

	// tree_add / tree_update / tree_cursor / tree_promote / tree_zoom_in —
	// SessionTree tools. Same Primary-only contract as Plan Workspace tools:
	// specialists read the tree through SessionTreeSource (already in
	// ExtraContextSources) but cannot mutate it.
	if opts != nil && opts.TreeStore != nil {
		if err := sessiontreetool.Register(toolReg, opts.TreeStore); err != nil {
			return nil, fmt.Errorf("primary: register session tree tools: %w", err)
		}
	}

	// vector_search / vector_add — vector recall tools. Primary-only
	// contract: specialists already read recall via VectorRecallSource
	// in ExtraContextSources; only the Primary writes / queries the
	// store directly.
	if opts != nil && opts.VectorStore != nil && opts.VectorEmbedder != nil {
		if err := vectorsearch.Register(toolReg, opts.VectorStore, opts.VectorEmbedder); err != nil {
			return nil, fmt.Errorf("primary: register vector tools: %w", err)
		}
	}

	// Apply the same wrapping chain sub-agents get: permission wrap →
	// truncation → debug (outermost).
	var finalToolReg tool.ToolRegistry = toolReg

	if opts != nil && opts.WrapToolRegistry != nil {
		finalToolReg = opts.WrapToolRegistry(toolReg)
	}

	if cfg.Context.ToolOutputMaxTokens > 0 {
		finalToolReg = tool.NewTruncatingToolRegistry(finalToolReg, cfg.Context.ToolOutputMaxTokens)
	}

	if cfg.Debug && opts != nil && opts.DebugSink != nil {
		finalToolReg = debugs.NewDebuggingToolRegistry(finalToolReg, opts.DebugSink)
	}

	// Register the Primary descriptor lazily so callers that pre-populated
	// reg earlier in setup.New do not see a duplicate ID error on re-init.
	// The descriptor itself is shared state in vv/agents, but registry
	// Register is idempotent on duplicate ID via the !exists check.
	primaryReg := registries.New()
	agents.RegisterPrimary(primaryReg)

	desc, ok := primaryReg.Get(agents.PrimaryAgentID)
	if !ok {
		return nil, fmt.Errorf("primary: descriptor missing after RegisterPrimary")
	}

	return desc.Factory(registries.FactoryOptions{
		LLM:                  llm,
		Model:                cfg.LLM.Model,
		ToolRegistry:         finalToolReg,
		MaxIterations:        cfg.Agents.MaxIterations,
		RunTokenBudget:       cfg.Agents.RunTokenBudget,
		MaxParallelToolCalls: cfg.Agents.MaxParallelToolCalls,
		PromptCaching:        cfg.Agents.EffectivePromptCaching(),
		Memory:               memMgr,
		ProjectInstructions:  cfg.ProjectInstructions,
		ToolResultGuards:     buildToolResultGuards(cfg.Security.ToolResultInjection),
		HookManager:          getHookManager(opts),
		ExtraContextSources:  buildExtraContextSources(opts),
		IterationStore:       getIterationStore(opts),
		BuildReportSink:      getBuildReportSink(opts),
		CheckpointFailureCB:  getCheckpointFailureCB(opts),
	})
}

// buildExtraContextSources turns the (optional) Plan Workspace,
// SessionTree, and Vector subsystems into a Source list suitable for
// FactoryOptions.ExtraContextSources. Returns nil when none are wired
// so dispatched factories see the same zero-cost path that pre-existed
// every feature.
func buildExtraContextSources(opts *Options) []vctx.Source {
	if opts == nil {
		return nil
	}
	var srcs []vctx.Source
	if opts.Workspace != nil {
		srcs = append(srcs, &vctx.WorkspaceSource{Workspace: opts.Workspace})
	}
	if opts.TreeStore != nil {
		srcs = append(srcs, &vctx.SessionTreeSource{
			Store:     opts.TreeStore,
			Predicate: opts.TreePredicate,
		})
	}
	if opts.VectorStore != nil && opts.VectorEmbedder != nil {
		srcs = append(srcs, &vctx.VectorRecallSource{
			Store:    opts.VectorStore,
			Embedder: opts.VectorEmbedder,
			TopK:     opts.VectorTopK,
		})
	}
	return srcs
}

// getTreeStore safely extracts the optional SessionTree backend from opts.
func getTreeStore(opts *Options) tree.SessionTreeStore {
	if opts == nil {
		return nil
	}
	return opts.TreeStore
}

// getIterationStore safely extracts the optional checkpoint backend from
// opts. Returns nil when opts is nil or the field is unset; every
// TaskAgent factory gracefully degrades to "no checkpointing".
func getIterationStore(opts *Options) checkpoint.IterationStore {
	if opts == nil {
		return nil
	}
	return opts.IterationStore
}

// primaryToolProfile picks the capability profile that the Primary
// Assistant's toolset is built from. Default = ProfileReadOnly
// (read/glob/grep), matching the researcher agent's wiring so the
// path-guard plumbing stays consistent. When
// orchestrate.primary_allow_bash is set we promote to
// ProfileReview, the same capability set reviewer uses
// (read/glob/grep + bash), so single-line shell tasks like
// `calc`/`echo`/`ls` finish inline without a delegate_to_coder round-trip.
// The fallback Primary deliberately keeps no tools at all regardless of
// this flag — see buildFallbackPrimary.
func primaryToolProfile(cfg *configs.Config) registries.ToolProfile {
	if cfg.Orchestrate.PrimaryAllowBash {
		return registries.ProfileReview
	}

	return registries.ProfileReadOnly
}

// buildFallbackPrimary assembles a Primary Assistant with NO tools — the
// read-only file tools, todo_write, ask_user, delegate_to_*, and plan_task
// are all intentionally omitted. The resulting agent can only answer the
// user inline through plain chat, which is exactly what the dispatcher's
// fallback path needs: if we have already recursed to maxRecursionDepth or
// we are salvaging a failed classification, re-entering any tool that could
// call back into a sub-agent (and therefore back into the dispatcher) is a
// silent infinite-recursion risk.
//
// System prompt and identity stay aligned with the main Primary so the user
// sees a consistent persona; the Factory wiring is reused via
// agents.RegisterPrimary with ToolRegistry left nil.
func buildFallbackPrimary(
	cfg *configs.Config,
	llm aimodel.ChatCompleter,
	memMgr *memory.Manager,
	opts *Options,
) (agent.Agent, error) {
	primaryReg := registries.New()
	agents.RegisterPrimary(primaryReg)

	desc, ok := primaryReg.Get(agents.PrimaryAgentID)
	if !ok {
		return nil, fmt.Errorf("fallback primary: descriptor missing after RegisterPrimary")
	}

	return desc.Factory(registries.FactoryOptions{
		LLM:                  llm,
		Model:                cfg.LLM.Model,
		ToolRegistry:         nil, // tool-free by design
		MaxIterations:        1,   // one turn — no tool loops to drive
		RunTokenBudget:       cfg.Agents.RunTokenBudget,
		MaxParallelToolCalls: cfg.Agents.MaxParallelToolCalls,
		PromptCaching:        cfg.Agents.EffectivePromptCaching(),
		Memory:               memMgr,
		ProjectInstructions:  cfg.ProjectInstructions,
		ToolResultGuards:     buildToolResultGuards(cfg.Security.ToolResultInjection),
		HookManager:          getHookManager(opts),
		// Fallback Primary still benefits from a read-only Plan Workspace
		// view: it cannot write (no plan_update tool registered), but it
		// can refer to the current plan when crafting its inline reply.
		ExtraContextSources: buildExtraContextSources(opts),
	})
}

// wrapLLMClient applies the same middleware chain (debug → budget) that Init
// layers on top of every aimodel.Client it constructs. Extracted so the
// router client picks up identical observability and enforcement without
// duplicating setup code.
func wrapLLMClient(
	client aimodel.ChatCompleter,
	cfg *configs.Config,
	pricingModel string,
	opts *Options,
	sessionBudget, dailyBudget *budgets.Tracker,
) aimodel.ChatCompleter {
	wrapped := client

	if cfg.Debug && opts != nil && opts.DebugSink != nil {
		wrapped = largemodel.Chain(wrapped, largemodel.NewDebugMiddleware(debugs.SinkAdapter{S: opts.DebugSink}))
	}

	if sessionBudget != nil || dailyBudget != nil {
		pricing := costtraces.LookupPricing(pricingModel, configs.ConvertPricing(cfg.ModelPricing))
		preCheck, postRecord := budgets.Wire(sessionBudget, dailyBudget, pricing, budgetEventDispatcher())
		wrapped = largemodel.Chain(wrapped, largemodel.NewBudgetMiddleware(preCheck, postRecord))
	}

	return wrapped
}

// getHookManager safely extracts the optional hook manager from opts.
func getHookManager(opts *Options) *hook.Manager {
	if opts == nil {
		return nil
	}

	return opts.HookManager
}

// getWorkspace safely extracts the optional Plan Workspace from opts.
func getWorkspace(opts *Options) workspace.Workspace {
	if opts == nil {
		return nil
	}

	return opts.Workspace
}

// buildPathEnforcement computes the canonical allow-list, opens a PathGuard,
// and constructs a PathGuardian for the bash tool. It mutates cfg.AllowedDirs
// to the canonical list so downstream consumers see a consistent value.
func buildPathEnforcement(cfg *configs.ToolsConfig) (*toolkit.PathGuard, *bash.PathGuardian, error) {
	dirs, err := buildAllowedDirs(cfg)
	if err != nil {
		return nil, nil, err
	}

	guard, err := toolkit.NewPathGuard(dirs)
	if err != nil {
		return nil, nil, fmt.Errorf("open path guard: %w", err)
	}

	canonicalized := guard.Dirs()
	cfg.AllowedDirs = &canonicalized

	guardian := bash.NewPathGuardian(canonicalized, cfg.BashWorkingDir)

	slog.Info("vv: allowed_dirs active", "dirs", canonicalized)

	return guard, guardian, nil
}

// buildAllowedDirs resolves the final allow-list from cfg, applying defaults
// when the YAML key is absent and erroring on explicit empty.
func buildAllowedDirs(cfg *configs.ToolsConfig) ([]string, error) {
	var dirs []string

	switch {
	case cfg.AllowedDirs == nil:
		if cfg.BashWorkingDir != "" {
			dirs = append(dirs, cfg.BashWorkingDir)
		}

		if tmp := os.TempDir(); tmp != "" {
			dirs = append(dirs, tmp)
		}
	case len(*cfg.AllowedDirs) == 0:
		return nil, errors.New("tools.allowed_dirs is explicitly empty; at least one directory is required")
	default:
		dirs = make([]string, 0, len(*cfg.AllowedDirs))
		dirs = append(dirs, *cfg.AllowedDirs...)
		for i, d := range dirs {
			expanded, err := expandUserPath(d, cfg.BashWorkingDir)
			if err != nil {
				return nil, fmt.Errorf("allowed_dirs[%d]=%q: %w", i, d, err)
			}
			dirs[i] = expanded
		}
	}

	canonical, err := toolkit.CanonicalizeDirs(dirs)
	if err != nil {
		return nil, err
	}

	return canonical, nil
}

// expandUserPath expands a leading "~" to the user's home and resolves a
// relative path against the bash working directory.
func expandUserPath(p, workingDir string) (string, error) {
	if p == "" {
		return "", errors.New("empty path")
	}

	if p == "~" || strings.HasPrefix(p, "~/") {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve ~: %w", err)
		}

		if p == "~" {
			return home, nil
		}

		return filepath.Join(home, p[2:]), nil
	}

	if !filepath.IsAbs(p) {
		if workingDir == "" {
			return "", fmt.Errorf("cannot resolve relative path %q without working directory", p)
		}

		return filepath.Join(workingDir, p), nil
	}

	return p, nil
}

// InitResult holds all components initialized by Init.
type InitResult struct {
	Config        *configs.Config
	LLMClient     *aimodel.Client
	MemoryManager *memory.Manager
	PersistentMem memory.Memory
	SetupResult   *Result
	Compactor     *memory.ConversationCompactor // conversation context compactor
	SessionBudget *budgets.Tracker              // nil if session budget disabled
	DailyBudget   *budgets.Tracker              // nil if daily budget disabled
	SessionStore  session.SessionStore          // nil when cfg.Session.Enabled = false
	Workspace     workspace.Workspace           // nil when cfg.Session.Enabled = false (workspace shares the session root)
	TreeStore     tree.SessionTreeStore         // nil when cfg.SessionTree.Enabled = false
	VectorStore   vector.VectorStore            // nil when cfg.Vector.Enabled = false (or soft-fail)
	VectorEmb     vector.Embedder               // nil when cfg.Vector.Enabled = false (or soft-fail)

	// IterationStore backs vv --resume / POST /v1/sessions/{id}/resume by
	// persisting per-iteration ReAct checkpoints under <session-root>/<id>/
	// checkpoints/. nil when cfg.Session.Enabled = false (resume requires a
	// stable session id; the no-session path is one-shot anyway).
	IterationStore checkpoint.IterationStore

	// MetricsStore + MetricsHook + BuildReportSink make up the P0-5
	// observability triple. All three are nil when cfg.Session.Enabled
	// = false (per-session aggregation needs a stable session id).
	// MetricsHook is the resume callers' handle for RecordResume —
	// callers should use it instead of MetricsStore directly so the
	// counter advances in lockstep with the rest of the hook flow.
	MetricsStore    session.MetricsStore
	MetricsHook     *session.SessionMetricsHook
	BuildReportSink vctx.BuildReportSink

	// Shutdown releases process-level resources owned by Init (the
	// hook.Manager that drives trace + session hooks, plus the persistent
	// memory store). It is always non-nil — a no-op when there is nothing to
	// release. Pass an independent context with a short timeout; the main
	// context is typically already cancelled by the time defer fires.
	Shutdown func(context.Context)
}

// Init creates the LLM client, memory subsystem, and all agents from a
// pre-loaded Config. The caller is responsible for loading the config
// (including interactive prompts, flag overrides, etc.) before calling Init.
func Init(cfg *configs.Config, opts *Options) (*InitResult, error) {
	// Capture working directory.
	if cfg.Tools.BashWorkingDir == "" {
		wd, wdErr := os.Getwd()
		if wdErr != nil {
			wd = "."
		}

		cfg.Tools.BashWorkingDir = wd
	}

	// Load project instructions from VV.md.
	if cfg.ProjectInstructions == "" {
		cfg.ProjectInstructions = configs.LoadProjectInstructions(cfg.Tools.BashWorkingDir)
	}

	// Normalize opts once so every installer can populate it in place without
	// repeating the nil guard. A non-nil caller-supplied Options is amended in
	// place; the public contract is unchanged.
	if opts == nil {
		opts = &Options{}
	}

	// Create LLM client.
	llmClient, err := configs.NewLLMClient(cfg.LLM)
	if err != nil {
		return nil, fmt.Errorf("create LLM client: %w", err)
	}

	// Wrap with debug middleware BEFORE compactor and BEFORE setup.New so that
	// every LLM call (sub-agents, compactor summarizer, dispatcher intent/plan-gen)
	// is captured. Default OFF: when cfg.Debug is false the wrapper is not constructed.
	// Budget middleware — enforces session/daily hard limits across every LLM
	// call. Skipped entirely when no budget limits are configured.
	sessionBudget, dailyBudget := buildBudgetTrackers(cfg.Budget)
	wrappedLLM := wrapLLMClient(llmClient, cfg, cfg.LLM.Model, opts, sessionBudget, dailyBudget)

	// Router LLM — when Orchestrate.Router.Model is set, build a second
	// client dedicated to Dispatcher routing/classification calls. It
	// receives the same debug + budget middleware wrap so observability and
	// per-session spend limits cover both models; pricing is looked up
	// against the router's model so the cheaper routing calls are billed
	// correctly.
	var routerWrappedLLM aimodel.ChatCompleter
	var routerModel string
	if routerCfg, ok := configs.EffectiveRouterConfig(cfg); ok {
		routerClient, err := configs.NewLLMClient(routerCfg)
		if err != nil {
			return nil, fmt.Errorf("create router LLM client: %w", err)
		}

		routerWrappedLLM = wrapLLMClient(routerClient, cfg, routerCfg.Model, opts, sessionBudget, dailyBudget)
		routerModel = routerCfg.Model

		slog.Info("vv: router LLM enabled", "model", routerCfg.Model, "provider", routerCfg.Provider)
	}

	// Router client opts — populated before the installer pipeline so the
	// final agent-assembly installer sees them alongside the rest of Options.
	if routerWrappedLLM != nil {
		opts.RouterLLM = routerWrappedLLM
		opts.RouterModel = routerModel
	}

	// Optional-subsystem assembly runs as an ordered installer pipeline. Each
	// installer amends opts in place and returns a cleanup for whatever it
	// opened; runInstallers pushes cleanups onto a LIFO stack and, on the
	// first failure, rolls the whole stack back in reverse — no half-built
	// stage leaves a running hook or an open store behind. The order encodes
	// the existing dependency chain: memory + hook/session first, then the
	// session-scoped stores, tree, vector, the tree↔vector decorator, and
	// finally agent assembly consuming the fully-populated Options.
	a := &assembly{wrappedLLM: wrappedLLM}
	cleanups, err := runInstallers(cfg, opts, []subsystemInstaller{
		a.installMemory,
		a.installHookSession,
		a.installIteration,
		a.installMetrics,
		a.installBuildReport,
		a.installTree,
		a.installVector,
		a.installTreeVector,
		a.installAgents,
	})
	if err != nil {
		return nil, err
	}

	// Create conversation compactor using the LLM for summarization.
	compactSummarizer := func(ctx context.Context, messages []schema.Message) (string, error) {
		var sb strings.Builder
		sb.WriteString("Summarize the following conversation, preserving key decisions, ")
		sb.WriteString("file changes, task progress, and important context:\n\n")
		sb.WriteString(buildConversationText(messages))

		req := &aimodel.ChatRequest{
			Model: cfg.LLM.Model,
			Messages: []aimodel.Message{
				{Role: aimodel.RoleUser, Content: aimodel.NewTextContent(sb.String())},
			},
		}

		resp, err := wrappedLLM.ChatCompletion(ctx, req)
		if err != nil {
			return "", err
		}

		if len(resp.Choices) == 0 {
			return "", fmt.Errorf("empty summarization response")
		}

		return resp.Choices[0].Message.Content.Text(), nil
	}

	// Limit summarizer input to 80% of context window to prevent the summarization
	// call itself from exceeding the context limit.
	maxSummarizerInput := int(float64(cfg.Context.ModelMaxContextTokens) * 0.8)

	compactor := memory.NewConversationCompactor(compactSummarizer, cfg.Context.ProtectedTurns).
		WithMaxInputTokens(maxSummarizerInput)

	return &InitResult{
		Config:          cfg,
		LLMClient:       llmClient,
		MemoryManager:   a.memMgr,
		PersistentMem:   a.persistentMem,
		SetupResult:     a.result,
		Compactor:       compactor,
		SessionBudget:   sessionBudget,
		DailyBudget:     dailyBudget,
		SessionStore:    a.sessionStore,
		Workspace:       a.planWorkspace,
		TreeStore:       getTreeStore(opts),
		VectorStore:     getVectorStore(opts),
		VectorEmb:       getVectorEmbedder(opts),
		IterationStore:  getIterationStore(opts),
		MetricsStore:    getMetricsStore(opts),
		MetricsHook:     getMetricsHook(opts),
		BuildReportSink: getBuildReportSink(opts),
		Shutdown:        shutdownFromCleanups(cleanups),
	}, nil
}

// openMemoryStore constructs the persistent-memory Store chosen by
// cfg.Memory.Backend. Returns the Store, a close function (always non-nil;
// no-op for FileStore), and any open error. Validated upstream in
// configs.Load so the default branch is defensive only.
func openMemoryStore(cfg configs.MemoryConfig) (memory.Store, func(), error) {
	switch cfg.Backend {
	case "", configs.MemoryBackendFile:
		fs, err := memories.NewFileStore(cfg.Dir)
		if err != nil {
			return nil, func() {}, fmt.Errorf("create file store: %w", err)
		}
		return fs, func() {}, nil
	case configs.MemoryBackendSQLite:
		ss, err := memories.NewSQLiteStore(cfg.Dir)
		if err != nil {
			return nil, func() {}, fmt.Errorf("create sqlite store: %w", err)
		}
		return ss, func() { _ = ss.Close() }, nil
	default:
		return nil, func() {}, fmt.Errorf("unsupported memory backend %q", cfg.Backend)
	}
}

// buildTreeStore constructs the SessionTreeStore for the session root,
// optionally wiring an automatic-promotion Promoter + Decider when
// cfg.SessionTree.Promotion.Enabled is true. The store shares
// <root>/<sessionID>/ with FileSessionStore + FileWorkspace so the same
// SessionStore.Delete wipes everything in one os.RemoveAll.
//
// Returns an error when the promoter kind is unknown or required fields
// (e.g., LLM client for promoter=llm) are absent.
func buildTreeStore(cfg *configs.Config, llm aimodel.ChatCompleter, hookMgr *hook.Manager) (tree.SessionTreeStore, error) {
	root := sessionRootDir(cfg)

	fileOpts := []tree.FileOption{}
	if hookMgr != nil {
		fileOpts = append(fileOpts, tree.WithFileHookManager(hookMgr))
	}

	if cfg.SessionTree.Promotion.IsEnabled() {
		promoter, err := buildTreePromoter(cfg, llm)
		if err != nil {
			return nil, err
		}
		fileOpts = append(fileOpts, tree.WithFilePromoter(promoter))
		fileOpts = append(fileOpts, tree.WithFilePromotionDecider(buildTreeDecider(cfg)))
	}

	store, err := tree.NewFileTreeStore(root, fileOpts...)
	if err != nil {
		return nil, fmt.Errorf("create file tree store: %w", err)
	}
	return store, nil
}

// buildTreePromoter selects the configured Promoter implementation. The
// default ("compressor") avoids LLM cost while still producing useful
// summaries; users opt into "llm" for higher-quality folds.
func buildTreePromoter(cfg *configs.Config, llm aimodel.ChatCompleter) (tree.Promoter, error) {
	switch cfg.SessionTree.Promotion.PromoterKind() {
	case "noop":
		return tree.NoopPromoter{}, nil
	case "llm":
		if llm == nil {
			return nil, errors.New("promoter=llm requires an LLM client")
		}
		model := cfg.SessionTree.Promotion.Model
		if model == "" {
			model = cfg.LLM.Model
		}
		return &tree.LLMPromoter{Client: llm, Model: model}, nil
	case "compressor":
		// SlidingWindow keeps the most-recent N messages; for promotion the
		// "messages" are children, so a small budget produces a tight summary.
		// Fallback default 50 mirrors configs.Memory.SessionWindow's
		// post-Load default — buildTreePromoter is also reachable from tests
		// that bypass configs.Load and would otherwise crash on a zero
		// SessionWindow (NewSlidingWindowCompressor panics on <= 0).
		windowSize := cfg.Memory.SessionWindow
		if windowSize <= 0 {
			windowSize = 50
		}
		c := memory.NewSlidingWindowCompressor(windowSize)
		return &tree.CompressorPromoter{Compressor: c}, nil
	default:
		return nil, fmt.Errorf("unknown promoter kind %q", cfg.SessionTree.Promotion.Promoter)
	}
}

// buildTreeDecider composes the configured trigger set: ChildrenCount OR
// SubtreeBytes (always on); AllChildrenDone added when cfg permits. Empty
// thresholds fall back to the package defaults inside the deciders.
func buildTreeDecider(cfg *configs.Config) tree.PromotionDecider {
	deciders := []tree.PromotionDecider{
		tree.ChildrenCountDecider{Min: cfg.SessionTree.Promotion.ChildrenThreshold},
		tree.SubtreeBytesDecider{Min: cfg.SessionTree.Promotion.SubtreeBytesThreshold},
	}
	if cfg.SessionTree.Promotion.AllChildrenDoneEnabled() {
		deciders = append(deciders, tree.AllChildrenDoneDecider{})
	}
	return tree.AnyOf(deciders...)
}

// buildHookManagerAndSession constructs the process-level hook.Manager,
// registers configured async hooks (trace logger and/or session hook), and
// returns the SessionStore (when session is enabled) so callers can expose it
// to CLI/HTTP. Returns (nil, nil, no-op, nil) when no hook-driven feature is
// enabled, preserving the zero-cost default path.
//
// Startup ordering: trace hook constructed first; if the session store fails
// to open, the trace tracer is closed before returning. mgr.Start handles
// per-hook rollback automatically.
func buildHookManagerAndSession(cfg *configs.Config) (*hook.Manager, session.SessionStore, workspace.Workspace, func(context.Context), error) {
	noopShutdown := func(context.Context) {}
	traceEnabled := cfg.Trace.IsEnabled()
	sessionEnabled := cfg.Session.IsEnabled()

	if !traceEnabled && !sessionEnabled {
		return nil, nil, nil, noopShutdown, nil
	}

	mgr := hook.NewManager()

	var tracer *tracelog.JSONLHook
	if traceEnabled {
		t, err := tracelog.New(tracelog.Config{
			BaseDir:      cfg.Trace.EffectiveDir(),
			WorkingDir:   cfg.Tools.BashWorkingDir,
			MaxFileBytes: cfg.Trace.MaxFileBytes,
			BufferSize:   cfg.Trace.BufferSize,
		})
		if err != nil {
			return nil, nil, nil, noopShutdown, fmt.Errorf("trace hook: %w", err)
		}
		tracer = t
		mgr.RegisterAsync(tracer)
	}

	var sessionStore session.SessionStore
	var planWorkspace workspace.Workspace
	var sessionRoot string
	if sessionEnabled {
		sessionRoot = sessionRootDir(cfg)
		store, err := session.NewFileSessionStore(sessionRoot)
		if err != nil {
			return nil, nil, nil, noopShutdown, fmt.Errorf("session store: %w", err)
		}
		sessionStore = store
		mgr.RegisterAsync(session.NewSessionHook(store))

		// Plan Workspace shares the session root so a single
		// SessionStore.Delete (which os.RemoveAll's <root>/<id>) wipes the
		// workspace alongside meta.json/events.jsonl/state.json without
		// any extra coordination.
		ws, wsErr := workspace.NewFileWorkspace(sessionRoot)
		if wsErr != nil {
			return nil, nil, nil, noopShutdown, fmt.Errorf("plan workspace: %w", wsErr)
		}
		planWorkspace = ws
	}

	if startErr := mgr.Start(context.Background()); startErr != nil {
		// mgr.Start already rolls back any successfully-started hooks; nothing
		// further needs to be torn down here.
		return nil, nil, nil, noopShutdown, fmt.Errorf("start hooks: %w", startErr)
	}

	if tracer != nil {
		slog.Info("vv: trace logging enabled", "dir", tracer.BaseDir())
	}
	if sessionStore != nil {
		slog.Info("vv: session subsystem enabled", "dir", sessionRoot)
	}
	if planWorkspace != nil {
		slog.Info("vv: plan workspace enabled", "dir", sessionRoot)
	}

	shutdown := func(ctx context.Context) {
		if err := mgr.Stop(ctx); err != nil {
			slog.Warn("vv: stop hooks", "error", err)
		}
	}

	return mgr, sessionStore, planWorkspace, shutdown, nil
}

// buildBudgetTrackers constructs session and daily budget trackers from cfg.
// Either (or both) may be nil when the corresponding limits are not set.
func buildBudgetTrackers(cfg configs.BudgetConfig) (*budgets.Tracker, *budgets.Tracker) {
	session := budgets.NewSession(budgets.Config{
		HardTokens:  cfg.SessionHardTokens,
		HardCostUSD: cfg.SessionHardCostUSD,
		WarnPercent: cfg.WarnPercent,
	})
	daily := budgets.NewDaily(budgets.Config{
		HardTokens:  cfg.DailyHardTokens,
		HardCostUSD: cfg.DailyHardCostUSD,
		WarnPercent: cfg.WarnPercent,
	})

	return session, daily
}

// budgetEventDispatcher returns a Dispatcher that forwards budget events to
// slog. The hook.Manager built by buildHookManager is opt-in (trace logging),
// so when it is absent slog remains the only observability sink for budget
// events; CLI/HTTP additionally render the current snapshot via
// Tracker.Snapshot() on demand.
func budgetEventDispatcher() budgets.Dispatcher {
	return func(_ context.Context, e schema.Event) {
		switch d := e.Data.(type) {
		case schema.BudgetWarnData:
			slog.Warn("vv: budget warn threshold crossed",
				"scope", d.Scope, "dimension", d.Dimension,
				"used", d.Used, "limit", d.Limit, "percent", d.Percent)
		case schema.BudgetExceededData:
			slog.Warn("vv: budget exceeded",
				"scope", d.Scope, "dimension", d.Dimension,
				"used", d.Used, "used_cost", d.UsedCost,
				"limit", d.Limit, "limit_cost", d.LimitCost)
		}
	}
}

// buildConversationText formats messages for summarization display.
func buildConversationText(messages []schema.Message) string {
	var sb strings.Builder
	for _, msg := range messages {
		fmt.Fprintf(&sb, "[%s]: %s\n", msg.Role, msg.Content.Text())
	}

	return sb.String()
}

// InitFromFile is a convenience wrapper that loads config from a YAML file
// and then calls Init. If configPath is empty, the default path is used.
// explicit controls whether a missing config file is an error.
func InitFromFile(configPath string, explicit bool, opts *Options) (*InitResult, error) {
	if configPath == "" {
		configPath = configs.DefaultPath()
	}

	cfg, err := configs.Load(configPath, explicit)
	if err != nil {
		return nil, fmt.Errorf("load config: %w", err)
	}

	if configs.NeedsSetup(cfg) {
		return nil, fmt.Errorf("config at %s requires setup: LLM API key is missing", configPath)
	}

	return Init(cfg, opts)
}

// buildToolResultGuards constructs the tool-result injection guard from the
// security config. Returns nil when disabled or misconfigured so the caller
// leaves the taskagent option unset (zero-impact path).
func buildToolResultGuards(cfg configs.ToolResultInjectionConfig) []guard.Guard {
	if !cfg.IsEnabled() {
		return nil
	}

	action := parseInjectionAction(cfg.Action)
	blockOn := parseSeverity(cfg.BlockOnSeverity, guard.SeverityHigh)

	g := guard.NewToolResultInjectionGuard(guard.ToolResultInjectionConfig{
		Action:          action,
		BlockOnSeverity: blockOn,
		MaxScanBytes:    cfg.MaxScanBytes,
	})

	return []guard.Guard{g}
}

// parseInjectionAction maps a config string to InjectionAction. Empty or
// unknown values default to InjectionActionLog.
func parseInjectionAction(s string) guard.InjectionAction {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case string(guard.InjectionActionRewrite):
		return guard.InjectionActionRewrite
	case string(guard.InjectionActionBlock):
		return guard.InjectionActionBlock
	default:
		return guard.InjectionActionLog
	}
}

// parseSeverity maps a config string to Severity. Empty returns defaultSev;
// "" in user config is treated as "no escalation" only when defaultSev==0.
func parseSeverity(s string, defaultSev guard.Severity) guard.Severity {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "low":
		return guard.SeverityLow
	case "medium":
		return guard.SeverityMedium
	case "high":
		return guard.SeverityHigh
	case "":
		return defaultSev
	default:
		return defaultSev
	}
}
