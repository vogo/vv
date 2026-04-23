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
	"github.com/vogo/vage/guard"
	"github.com/vogo/vage/largemodel"
	"github.com/vogo/vage/memory"
	"github.com/vogo/vage/prompt"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/tool"
	"github.com/vogo/vage/tool/askuser"
	"github.com/vogo/vage/tool/bash"
	"github.com/vogo/vage/tool/toolkit"
	"github.com/vogo/vv/agents"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/debugs"
	"github.com/vogo/vv/dispatches"
	"github.com/vogo/vv/hooks"
	"github.com/vogo/vv/memories"
	"github.com/vogo/vv/registries"
	"github.com/vogo/vv/traces/budgets"
	"github.com/vogo/vv/traces/costtraces"
)

// Result holds the assembled components for the application.
type Result struct {
	Dispatcher   *dispatches.Dispatcher
	PathGuard    *toolkit.PathGuard
	PathGuardian *bash.PathGuardian
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

	// 1. Create registry and register all agents.
	reg := registries.New()
	agents.RegisterCoder(reg)
	agents.RegisterResearcher(reg)
	agents.RegisterReviewer(reg)
	agents.RegisterChat(reg)
	agents.RegisterExplorer(reg)
	agents.RegisterPlanner(reg)

	// 2. Build sub-agents from registry (dispatchable agents only).
	subAgents := make(map[string]agent.Agent)

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
			LLM:                 llm,
			Model:               cfg.LLM.Model,
			ToolRegistry:        finalToolReg,
			MaxIterations:       cfg.Agents.MaxIterations,
			RunTokenBudget:      cfg.Agents.RunTokenBudget,
			Memory:              memMgr,
			PersistentMemory:    persistentMem,
			ProjectInstructions: cfg.ProjectInstructions,
			ToolResultGuards:    buildToolResultGuards(cfg.Security.ToolResultInjection),
		}

		a, err := desc.Factory(factoryOpts)
		if err != nil {
			return nil, fmt.Errorf("create agent %q: %w", desc.ID, err)
		}

		subAgents[desc.ID] = a
	}

	// 3. Build explorer (non-dispatchable, read-only tools).
	explorerDesc, ok := reg.Get("explorer")
	if !ok {
		return nil, fmt.Errorf("explorer agent not registered")
	}

	explorerToolReg, err := explorerDesc.ToolProfile.BuildRegistry(cfg.Tools, regOpts...)
	if err != nil {
		return nil, fmt.Errorf("build explorer tool registry: %w", err)
	}

	// Wrap explorer tool registry with truncation.
	var explorerFinalToolReg tool.ToolRegistry = explorerToolReg
	if cfg.Context.ToolOutputMaxTokens > 0 {
		explorerFinalToolReg = tool.NewTruncatingToolRegistry(explorerToolReg, cfg.Context.ToolOutputMaxTokens)
	}

	if cfg.Debug && opts != nil && opts.DebugSink != nil {
		explorerFinalToolReg = debugs.NewDebuggingToolRegistry(explorerFinalToolReg, opts.DebugSink)
	}

	explorer, err := explorerDesc.Factory(registries.FactoryOptions{
		LLM:                 llm,
		Model:               cfg.LLM.Model,
		ToolRegistry:        explorerFinalToolReg,
		MaxIterations:       min(cfg.Agents.MaxIterations, 15),
		ProjectInstructions: cfg.ProjectInstructions,
	})
	if err != nil {
		return nil, fmt.Errorf("create explorer agent: %w", err)
	}

	// 4. Build planner (non-dispatchable, no tools).
	plannerDesc, ok := reg.Get("planner")
	if !ok {
		return nil, fmt.Errorf("planner agent not registered")
	}

	planner, err := plannerDesc.Factory(registries.FactoryOptions{
		LLM:                 llm,
		Model:               cfg.LLM.Model,
		MaxIterations:       1,
		ProjectInstructions: cfg.ProjectInstructions,
	})
	if err != nil {
		return nil, fmt.Errorf("create planner agent: %w", err)
	}

	// 5. Build plan summarizer.
	planGen := taskagent.New(
		agent.Config{ID: "plan-gen", Name: "Plan Generator", Description: "Summarizes execution plan results"},
		taskagent.WithChatCompleter(llm),
		taskagent.WithModel(cfg.LLM.Model),
		taskagent.WithSystemPrompt(prompt.StringPrompt(dispatches.PlanSummaryPrompt)),
		taskagent.WithMaxIterations(1),
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

	// 8. Build intent system prompt from registries.
	intentPrompt := agents.BuildPlannerSystemPrompt(reg)

	// 9. Resolve new config options.
	maxRecursionDepth := cfg.Orchestrate.MaxRecursionDepth
	if maxRecursionDepth == 0 {
		maxRecursionDepth = 2
	}

	summaryPolicy := dispatches.SummaryPolicy(cfg.Orchestrate.SummaryPolicy)
	if summaryPolicy == "" {
		summaryPolicy = dispatches.SummaryAuto
	}

	replanPolicy := dispatches.ReplanPolicy{
		TriggerOnFailure:   cfg.Orchestrate.Replan.TriggerOnFailure,
		TriggerOnDeviation: cfg.Orchestrate.Replan.TriggerOnDeviation,
		MaxReplans:         cfg.Orchestrate.Replan.MaxReplans,
	}

	if replanPolicy.MaxReplans == 0 {
		replanPolicy.MaxReplans = 2
	}

	// 10. Construct dispatcher.
	dispatcher := dispatches.New(
		reg,
		subAgents,
		explorer,
		planner,
		planGen,
		dispatches.WithLLM(llm, cfg.LLM.Model),
		dispatches.WithMaxConcurrency(maxConcurrency),
		dispatches.WithFallbackAgent(subAgents["chat"]),
		dispatches.WithWorkingDir(cfg.Tools.BashWorkingDir),
		dispatches.WithToolsConfig(cfg.Tools),
		dispatches.WithHooks(hooks),
		dispatches.WithMaxIterations(cfg.Agents.MaxIterations),
		dispatches.WithRunTokenBudget(cfg.Agents.RunTokenBudget),
		dispatches.WithIntentSystemPrompt(intentPrompt),
		dispatches.WithSummaryPolicy(summaryPolicy),
		dispatches.WithReplanPolicy(replanPolicy),
		dispatches.WithMaxRecursionDepth(maxRecursionDepth),
		dispatches.WithProjectInstructions(cfg.ProjectInstructions),
	)

	return &Result{
		Dispatcher:   dispatcher,
		PathGuard:    pathGuard,
		PathGuardian: pathGuardian,
		registry:     reg,
		subAgents:    subAgents,
	}, nil
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

	// Create LLM client.
	llmClient, err := configs.NewLLMClient(cfg.LLM)
	if err != nil {
		return nil, fmt.Errorf("create LLM client: %w", err)
	}

	// Wrap with debug middleware BEFORE compactor and BEFORE setup.New so that
	// every LLM call (sub-agents, compactor summarizer, dispatcher intent/plan-gen)
	// is captured. Default OFF: when cfg.Debug is false the wrapper is not constructed.
	var wrappedLLM aimodel.ChatCompleter = llmClient
	if cfg.Debug && opts != nil && opts.DebugSink != nil {
		wrappedLLM = largemodel.Chain(llmClient, largemodel.NewDebugMiddleware(debugs.SinkAdapter{S: opts.DebugSink}))
	}

	// Budget middleware — enforces session/daily hard limits across every LLM
	// call (sub-agents, compactor summarizer, dispatcher intent/plan-gen).
	// Skipped entirely when no budget limits are configured.
	sessionBudget, dailyBudget := buildBudgetTrackers(cfg.Budget)
	if sessionBudget != nil || dailyBudget != nil {
		pricing := costtraces.LookupPricing(cfg.LLM.Model, configs.ConvertPricing(cfg.ModelPricing))
		preCheck, postRecord := budgets.Wire(sessionBudget, dailyBudget, pricing, budgetEventDispatcher())
		wrappedLLM = largemodel.Chain(wrappedLLM, largemodel.NewBudgetMiddleware(preCheck, postRecord))
	}

	// Create persistent memory with FileStore backend.
	fileStore, err := memories.NewFileStore(cfg.Memory.Dir)
	if err != nil {
		return nil, fmt.Errorf("create file store: %w", err)
	}

	persistentMem := memory.NewPersistentMemoryWithStore(fileStore)

	// Create memory manager.
	memMgr := memory.NewManager(
		memory.WithStore(persistentMem),
		memory.WithPromoter(memory.PromoteAll()),
		memory.WithCompressor(memory.NewSlidingWindowCompressor(cfg.Memory.SessionWindow)),
	)

	// Set up all agents using the (possibly debug-wrapped) LLM client.
	result, err := New(cfg, wrappedLLM, memMgr, persistentMem, opts)
	if err != nil {
		return nil, fmt.Errorf("setup agents: %w", err)
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
		Config:        cfg,
		LLMClient:     llmClient,
		MemoryManager: memMgr,
		PersistentMem: persistentMem,
		SetupResult:   result,
		Compactor:     compactor,
		SessionBudget: sessionBudget,
		DailyBudget:   dailyBudget,
	}, nil
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
// slog. vv does not currently install a hook.Manager on the LLM call path,
// so logging is the best-effort observability sink; CLI/HTTP render the
// current snapshot via Tracker.Snapshot() on demand.
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
