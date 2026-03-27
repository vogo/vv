package agents

import (
	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/prompt"
	"github.com/vogo/vage/tool"
	"github.com/vogo/vagents/vaga/config"
)

// ExplorerSystemPrompt is the system prompt for the explorer sub-agent.
const ExplorerSystemPrompt = `You are an expert codebase explorer. You quickly explore project structures and gather context relevant to a user's question.

## Available Tools
- **glob**: Find files by name pattern (e.g., "**/*.go", "src/**/*.ts").
- **grep**: Search file contents using regular expressions.
- **read**: Read file contents.

## Your Task
Given a user's question and a working directory, explore the project to build context:

1. **Determine relevance**: If the question is a general knowledge question (not about the project), respond with just: "No project exploration needed."
2. **Explore efficiently**: Start broad (glob for file patterns, grep for keywords), then narrow down to specific files.
3. **Focus**: Only explore what is needed for the question. Do not exhaustively scan the entire project.
4. **Summarize**: Produce a concise context summary describing:
   - Relevant project structure and files
   - Key types, functions, interfaces, and patterns found
   - How they relate to the user's question

Keep your summary focused and actionable -- it will be used by other agents to fulfill the user's request.`

func newExplorerAgent(
	cfg *config.Config,
	llm aimodel.ChatCompleter,
	readOnlyReg tool.ToolRegistry,
) *taskagent.Agent {
	maxIter := min(cfg.Agents.MaxIterations,
		// cap exploration iterations
		15)

	return taskagent.New(
		agent.Config{
			ID:          "explorer",
			Name:        "Explorer Agent",
			Description: "Explores codebases to build project context for a given question",
		},
		taskagent.WithChatCompleter(llm),
		taskagent.WithModel(cfg.LLM.Model),
		taskagent.WithToolRegistry(readOnlyReg),
		taskagent.WithSystemPrompt(prompt.StringPrompt(ExplorerSystemPrompt)),
		taskagent.WithMaxIterations(maxIter),
	)
}
