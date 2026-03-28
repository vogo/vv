package agents

import (
	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/memory"
	"github.com/vogo/vage/prompt"
	"github.com/vogo/vage/tool"
	"github.com/vogo/vagents/vaga/config"
)

// Agents holds all created agents.
type Agents struct {
	Coder        *taskagent.Agent
	Chat         *taskagent.Agent
	Researcher   *taskagent.Agent
	Reviewer     *taskagent.Agent
	Explorer     *taskagent.Agent
	Planner      *taskagent.Agent
	Orchestrator *OrchestratorAgent
}

// Create creates all task agents and the orchestrator.
func Create(
	cfg *config.Config,
	llm aimodel.ChatCompleter,
	coderReg tool.ToolRegistry,
	readOnlyReg tool.ToolRegistry,
	reviewReg tool.ToolRegistry,
	memMgr *memory.Manager,
	persistentPrompt prompt.PromptTemplate,
) *Agents {
	coderAgent := newCoderAgent(cfg, llm, coderReg, memMgr, persistentPrompt)
	chatAgent := newChatAgent(cfg, llm)
	researcherAgent := newResearcherAgent(cfg, llm, readOnlyReg, memMgr)
	reviewerAgent := newReviewerAgent(cfg, llm, reviewReg, memMgr)
	explorerAgent := newExplorerAgent(cfg, llm, readOnlyReg)
	plannerAgent := newPlannerAgent(cfg, llm)

	planGen := taskagent.New(
		agent.Config{
			ID:          "plan-gen",
			Name:        "Plan Generator",
			Description: "Summarizes execution plan results",
		},
		taskagent.WithChatCompleter(llm),
		taskagent.WithModel(cfg.LLM.Model),
		taskagent.WithSystemPrompt(prompt.StringPrompt(PlanSummaryPrompt)),
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
