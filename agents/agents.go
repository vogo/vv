package agents

import (
	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/routeragent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/memory"
	"github.com/vogo/vage/prompt"
	"github.com/vogo/vage/tool"
	"github.com/vogo/vagents/vaga/config"
)

// Agents holds all created agents.
type Agents struct {
	Coder      *taskagent.Agent
	Chat       *taskagent.Agent
	Researcher *taskagent.Agent
	Reviewer   *taskagent.Agent
	Planner    *PlannerAgent
	Router     *routeragent.Agent
}

// Create creates all task agents: coder, chat, researcher, reviewer.
func Create(
	cfg *config.Config,
	llm aimodel.ChatCompleter,
	coderReg tool.ToolRegistry,
	readOnlyReg tool.ToolRegistry,
	reviewReg tool.ToolRegistry,
	memMgr *memory.Manager,
	persistentPrompt prompt.PromptTemplate,
) *Agents {
	// Determine system prompt for coder.
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
		agent.Config{
			ID:          "coder",
			Name:        "Coder Agent",
			Description: "Performs coding tasks: reads, writes, edits files, runs commands, and searches codebases",
		},
		coderOpts...,
	)

	chatAgent := taskagent.New(
		agent.Config{
			ID:          "chat",
			Name:        "Chat Agent",
			Description: "Handles general conversation, questions, and non-coding tasks",
		},
		taskagent.WithChatCompleter(llm),
		taskagent.WithModel(cfg.LLM.Model),
		taskagent.WithSystemPrompt(prompt.StringPrompt(ChatSystemPrompt)),
		taskagent.WithMaxIterations(1), // no tool loop needed
	)

	// Researcher: read-only tools.
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
		agent.Config{
			ID:          "researcher",
			Name:        "Researcher Agent",
			Description: "Explores codebases, reads documentation, and gathers information",
		},
		researcherOpts...,
	)

	// Reviewer: read + bash tools.
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
		agent.Config{
			ID:          "reviewer",
			Name:        "Reviewer Agent",
			Description: "Reviews code for correctness, style, performance, and security",
		},
		reviewerOpts...,
	)

	// Planner: plan generation + DAG execution.
	planGen := taskagent.New(
		agent.Config{
			ID:          "plan-gen",
			Name:        "Plan Generator",
			Description: "Generates execution plans for complex tasks",
		},
		taskagent.WithChatCompleter(llm),
		taskagent.WithModel(cfg.LLM.Model),
		taskagent.WithSystemPrompt(prompt.StringPrompt(PlannerSystemPrompt)),
		taskagent.WithMaxIterations(1),
	)

	subAgents := map[string]agent.Agent{
		"coder":      coderAgent,
		"researcher": researcherAgent,
		"reviewer":   reviewerAgent,
	}

	plannerAgent := NewPlannerAgent(
		agent.Config{
			ID:          "planner",
			Name:        "Planner Agent",
			Description: "Handles complex multi-step tasks with planning and coordination",
		},
		planGen,
		subAgents,
		cfg.Memory.MaxConcurrency,
		coderAgent, // fallback
	)

	// Router: 5 routes.
	router := routeragent.New(
		agent.Config{
			ID:          "router",
			Name:        "Router Agent",
			Description: "Routes requests to the appropriate specialized agent",
		},
		[]routeragent.Route{
			{Agent: coderAgent, Description: "Handles code-related tasks: reading, writing, editing files, running commands, debugging"},
			{Agent: plannerAgent, Description: "Handles complex multi-step tasks: project setup, large refactors, multi-file coordinated changes"},
			{Agent: researcherAgent, Description: "Handles research tasks: codebase exploration, documentation lookup, information gathering"},
			{Agent: reviewerAgent, Description: "Handles review tasks: code review, design review, quality assessment"},
			{Agent: chatAgent, Description: "Handles general conversation, questions, explanations, brainstorming"},
		},
		routeragent.WithFunc(routeragent.LLMFunc(llm, cfg.LLM.Model, 4)), // fallback=4 (chat)
	)

	return &Agents{
		Coder:      coderAgent,
		Chat:       chatAgent,
		Researcher: researcherAgent,
		Reviewer:   reviewerAgent,
		Planner:    plannerAgent,
		Router:     router,
	}
}

// CreateRouter creates the router agent that routes requests to the coder or chat agent.
// Deprecated: Use Create which builds all agents including the router.
func CreateRouter(cfg *config.Config, llm aimodel.ChatCompleter, coder, chat agent.Agent) *routeragent.Agent {
	return routeragent.New(
		agent.Config{
			ID:          "router",
			Name:        "Router Agent",
			Description: "Routes requests to the appropriate specialized agent",
		},
		[]routeragent.Route{
			{Agent: coder, Description: "Handles code-related tasks: reading files, writing code, editing files, running commands, searching codebases, debugging, and software engineering tasks"},
			{Agent: chat, Description: "Handles general conversation, questions, explanations, brainstorming, and non-coding tasks"},
		},
		routeragent.WithFunc(routeragent.LLMFunc(llm, cfg.LLM.Model, 1)), // fallback=1 (chat)
	)
}
