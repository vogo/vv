package setup

import (
	"fmt"
	"log/slog"
	"sort"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/memory"
	"github.com/vogo/vage/prompt"
	"github.com/vogo/vage/tool"
	"github.com/vogo/vv/agents"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/dispatches"
	"github.com/vogo/vv/hooks"
	"github.com/vogo/vv/registries"
)

// Result holds the assembled components for the application.
type Result struct {
	Dispatcher *dispatches.Dispatcher
	registry   *registries.Registry
	subAgents  map[string]agent.Agent
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
}

// New reads config, registers all agents, and constructs the Dispatcher.
func New(
	cfg *configs.Config,
	llm aimodel.ChatCompleter,
	memMgr *memory.Manager,
	persistentMem memory.Memory,
	opts *Options,
) (*Result, error) {
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
		toolReg, err := desc.ToolProfile.BuildRegistry(cfg.Tools)
		if err != nil {
			return nil, fmt.Errorf("build tool registry for %q: %w", desc.ID, err)
		}

		// Apply optional tool registry wrapping (e.g., CLI confirmation).
		var finalToolReg tool.ToolRegistry = toolReg

		if opts != nil && opts.WrapToolRegistry != nil {
			finalToolReg = opts.WrapToolRegistry(toolReg)
		}

		factoryOpts := registries.FactoryOptions{
			LLM:              llm,
			Model:            cfg.LLM.Model,
			ToolRegistry:     finalToolReg,
			MaxIterations:    cfg.Agents.MaxIterations,
			RunTokenBudget:   cfg.Agents.RunTokenBudget,
			Memory:           memMgr,
			PersistentMemory: persistentMem,
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

	explorerToolReg, err := explorerDesc.ToolProfile.BuildRegistry(cfg.Tools)
	if err != nil {
		return nil, fmt.Errorf("build explorer tool registry: %w", err)
	}

	explorer, err := explorerDesc.Factory(registries.FactoryOptions{
		LLM:           llm,
		Model:         cfg.LLM.Model,
		ToolRegistry:  explorerToolReg,
		MaxIterations: min(cfg.Agents.MaxIterations, 15),
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
		LLM:           llm,
		Model:         cfg.LLM.Model,
		MaxIterations: 1,
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

	// 8. Build planner system prompt from registries.
	plannerPrompt := agents.BuildPlannerSystemPrompt(reg)

	// 9. Construct dispatcher.
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
		dispatches.WithPlannerSystemPrompt(plannerPrompt),
	)

	return &Result{
		Dispatcher: dispatcher,
		registry:   reg,
		subAgents:  subAgents,
	}, nil
}
