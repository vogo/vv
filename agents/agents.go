package agents

import (
	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/routeragent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/prompt"
	"github.com/vogo/vage/tool"
	"github.com/vogo/vagents/vaga/config"
)

// Create creates the coder and chat task agents.
func Create(cfg *config.Config, llm aimodel.ChatCompleter, reg tool.ToolRegistry) (*taskagent.Agent, *taskagent.Agent) {
	coderAgent := taskagent.New(
		agent.Config{
			ID:          "coder",
			Name:        "Coder Agent",
			Description: "Performs coding tasks: reads, writes, edits files, runs commands, and searches codebases",
		},
		taskagent.WithChatCompleter(llm),
		taskagent.WithModel(cfg.LLM.Model),
		taskagent.WithToolRegistry(reg),
		taskagent.WithSystemPrompt(prompt.StringPrompt(CoderSystemPrompt)),
		taskagent.WithMaxIterations(cfg.Agents.MaxIterations),
		taskagent.WithRunTokenBudget(cfg.Agents.RunTokenBudget),
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

	return coderAgent, chatAgent
}

// CreateRouter creates the router agent that routes requests to the coder or chat agent.
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
