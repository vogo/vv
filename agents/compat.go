package agents

import (
	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/memory"
	"github.com/vogo/vage/prompt"
	"github.com/vogo/vage/tool"
	"github.com/vogo/vv/config"
	"github.com/vogo/vv/dispatch"
	"github.com/vogo/vv/registry"
)

// ToolAccessLevel defines what tools a dynamic agent can access.
// Deprecated: Use registry.ToolProfile instead.
type ToolAccessLevel = string

const (
	ToolAccessFull     ToolAccessLevel = "full"
	ToolAccessReadOnly ToolAccessLevel = "read-only"
	ToolAccessNone     ToolAccessLevel = "none"
)

// Agents holds all created agents.
// Deprecated: Use setup.Result instead.
type Agents struct {
	Coder        *taskagent.Agent
	Chat         *taskagent.Agent
	Researcher   *taskagent.Agent
	Reviewer     *taskagent.Agent
	Explorer     *taskagent.Agent
	Planner      *taskagent.Agent
	Orchestrator *dispatch.Dispatcher
}

// OrchestratorAgent is an alias for backward compatibility.
// Deprecated: Use dispatch.Dispatcher instead.
type OrchestratorAgent = dispatch.Dispatcher

// NewOrchestratorAgent creates a new Dispatcher (backward compatibility shim).
// Deprecated: Use dispatch.New instead.
func NewOrchestratorAgent(
	cfg agent.Config,
	llm aimodel.ChatCompleter,
	model string,
	subAgents map[string]agent.Agent,
	planGen *taskagent.Agent,
	maxConcurrency int,
	fallback agent.Agent,
	workingDir string,
	explorerAgent agent.Agent,
	plannerAgent agent.Agent,
	toolRegistries map[ToolAccessLevel]tool.ToolRegistry,
	reviewReg tool.ToolRegistry,
	maxIterations int,
	runTokenBudget int,
) *dispatch.Dispatcher {
	// Build a registry from the provided sub-agents for validation.
	reg := registry.New()
	for id := range subAgents {
		reg.MustRegister(registry.AgentDescriptor{
			ID:           id,
			Dispatchable: true,
		})
	}
	// Register additional known types for dynamic agent validation.
	for _, id := range []string{"coder", "researcher", "reviewer", "chat"} {
		if !reg.ValidateRef(id) {
			profile := registry.ProfileNone
			sysPrompt := ""

			switch id {
			case "coder":
				profile = registry.ProfileFull
				sysPrompt = CoderSystemPrompt
			case "researcher":
				profile = registry.ProfileReadOnly
				sysPrompt = ResearcherSystemPrompt
			case "reviewer":
				profile = registry.ProfileReview
				sysPrompt = ReviewerSystemPrompt
			case "chat":
				sysPrompt = ChatSystemPrompt
			}

			reg.MustRegister(registry.AgentDescriptor{
				ID:           id,
				Dispatchable: true,
				ToolProfile:  profile,
				SystemPrompt: sysPrompt,
			})
		}
	}

	var planGenAgent agent.Agent
	if planGen != nil {
		planGenAgent = planGen
	}

	opts := []dispatch.Option{
		dispatch.WithLLM(llm, model),
		dispatch.WithMaxConcurrency(maxConcurrency),
		dispatch.WithWorkingDir(workingDir),
		dispatch.WithMaxIterations(maxIterations),
		dispatch.WithRunTokenBudget(runTokenBudget),
		dispatch.WithPlannerSystemPrompt(PlannerSystemPrompt),
	}

	if fallback != nil {
		opts = append(opts, dispatch.WithFallbackAgent(fallback))
	}

	return dispatch.New(
		reg,
		subAgents,
		explorerAgent,
		plannerAgent,
		planGenAgent,
		opts...,
	)
}

// Create creates all task agents and the orchestrator.
// Deprecated: Use setup.New instead.
func Create(
	cfg *config.Config,
	llm aimodel.ChatCompleter,
	coderReg tool.ToolRegistry,
	readOnlyReg tool.ToolRegistry,
	reviewReg tool.ToolRegistry,
	memMgr *memory.Manager,
	persistentPrompt prompt.PromptTemplate,
) *Agents {
	coderPrompt := prompt.PromptTemplate(prompt.StringPrompt(CoderSystemPrompt))
	if persistentPrompt != nil {
		coderPrompt = persistentPrompt
	}

	var coderOpts []taskagent.Option
	coderOpts = append(coderOpts,
		taskagent.WithChatCompleter(llm),
		taskagent.WithModel(cfg.LLM.Model),
		taskagent.WithToolRegistry(coderReg),
		taskagent.WithSystemPrompt(coderPrompt),
		taskagent.WithMaxIterations(cfg.Agents.MaxIterations),
		taskagent.WithRunTokenBudget(cfg.Agents.RunTokenBudget),
	)
	if memMgr != nil {
		coderOpts = append(coderOpts, taskagent.WithMemory(memMgr))
	}
	coderAgent := taskagent.New(
		agent.Config{ID: "coder", Name: "Coder Agent", Description: "Performs coding tasks: reads, writes, edits files, runs commands, and searches codebases"},
		coderOpts...,
	)

	chatAgent := taskagent.New(
		agent.Config{ID: "chat", Name: "Chat Agent", Description: "Handles general conversation, questions, and non-coding tasks"},
		taskagent.WithChatCompleter(llm),
		taskagent.WithModel(cfg.LLM.Model),
		taskagent.WithSystemPrompt(prompt.StringPrompt(ChatSystemPrompt)),
		taskagent.WithMaxIterations(1),
	)

	var researcherOpts []taskagent.Option
	researcherOpts = append(researcherOpts,
		taskagent.WithChatCompleter(llm),
		taskagent.WithModel(cfg.LLM.Model),
		taskagent.WithToolRegistry(readOnlyReg),
		taskagent.WithSystemPrompt(prompt.StringPrompt(ResearcherSystemPrompt)),
		taskagent.WithMaxIterations(cfg.Agents.MaxIterations),
	)
	if memMgr != nil {
		researcherOpts = append(researcherOpts, taskagent.WithMemory(memMgr))
	}
	researcherAgent := taskagent.New(
		agent.Config{ID: "researcher", Name: "Researcher Agent", Description: "Explores codebases, reads documentation, and gathers information"},
		researcherOpts...,
	)

	var reviewerOpts []taskagent.Option
	reviewerOpts = append(reviewerOpts,
		taskagent.WithChatCompleter(llm),
		taskagent.WithModel(cfg.LLM.Model),
		taskagent.WithToolRegistry(reviewReg),
		taskagent.WithSystemPrompt(prompt.StringPrompt(ReviewerSystemPrompt)),
		taskagent.WithMaxIterations(cfg.Agents.MaxIterations),
	)
	if memMgr != nil {
		reviewerOpts = append(reviewerOpts, taskagent.WithMemory(memMgr))
	}
	reviewerAgent := taskagent.New(
		agent.Config{ID: "reviewer", Name: "Reviewer Agent", Description: "Reviews code for correctness, style, performance, and security"},
		reviewerOpts...,
	)

	maxIter := min(cfg.Agents.MaxIterations, 15)
	explorerAgent := taskagent.New(
		agent.Config{ID: "explorer", Name: "Explorer Agent", Description: "Explores codebases to build project context for a given question"},
		taskagent.WithChatCompleter(llm),
		taskagent.WithModel(cfg.LLM.Model),
		taskagent.WithToolRegistry(readOnlyReg),
		taskagent.WithSystemPrompt(prompt.StringPrompt(ExplorerSystemPrompt)),
		taskagent.WithMaxIterations(maxIter),
	)

	plannerAgent := taskagent.New(
		agent.Config{ID: "planner", Name: "Planner Agent", Description: "Analyzes requests and produces task classification or execution plans"},
		taskagent.WithChatCompleter(llm),
		taskagent.WithModel(cfg.LLM.Model),
		taskagent.WithSystemPrompt(prompt.StringPrompt(PlannerSystemPrompt)),
		taskagent.WithMaxIterations(1),
	)

	planGen := taskagent.New(
		agent.Config{ID: "plan-gen", Name: "Plan Generator", Description: "Summarizes execution plan results"},
		taskagent.WithChatCompleter(llm),
		taskagent.WithModel(cfg.LLM.Model),
		taskagent.WithSystemPrompt(prompt.StringPrompt(dispatch.PlanSummaryPrompt)),
		taskagent.WithMaxIterations(1),
	)

	subAgents := map[string]agent.Agent{
		"coder":      coderAgent,
		"researcher": researcherAgent,
		"reviewer":   reviewerAgent,
		"chat":       chatAgent,
	}

	toolRegs := map[ToolAccessLevel]tool.ToolRegistry{
		ToolAccessFull:     coderReg,
		ToolAccessReadOnly: readOnlyReg,
	}

	orchestratorAgent := NewOrchestratorAgent(
		agent.Config{
			ID:          "orchestrator",
			Name:        "Orchestrator Agent",
			Description: "Orchestrates user requests: explores context, plans tasks, dispatches to agents",
		},
		llm,
		cfg.LLM.Model,
		subAgents,
		planGen,
		cfg.Memory.MaxConcurrency,
		chatAgent,
		cfg.Tools.BashWorkingDir,
		explorerAgent,
		plannerAgent,
		toolRegs,
		reviewReg,
		cfg.Agents.MaxIterations,
		cfg.Agents.RunTokenBudget,
	)

	return &Agents{
		Coder:        coderAgent,
		Chat:         chatAgent,
		Researcher:   researcherAgent,
		Reviewer:     reviewerAgent,
		Explorer:     explorerAgent,
		Planner:      plannerAgent,
		Orchestrator: orchestratorAgent,
	}
}
