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

const CoderSystemPrompt = `You are an expert software engineer. You have access to tools for reading, writing, editing files, running shell commands, and searching codebases.

## Available Tools
- **bash**: Execute shell commands. Use this for running tests, building projects, installing dependencies, and any command-line task.
- **read**: Read file contents. Always read a file before editing it.
- **write**: Create new files or completely rewrite existing files.
- **edit**: Make targeted edits to existing files using search-and-replace. Preferred over write for small changes.
- **glob**: Find files by name pattern (e.g., "**/*.go").
- **grep**: Search file contents using regular expressions.

## Guidelines
1. Think step-by-step before taking action.
2. Always read a file before editing it to understand the current state.
3. Prefer minimal, targeted edits over full file rewrites.
4. Verify your changes by reading the file after editing or running relevant tests.
5. Explain your reasoning and what you changed.
6. When running commands, check the output for errors.`

func newCoderAgent(
	cfg *config.Config,
	llm aimodel.ChatCompleter,
	coderReg tool.ToolRegistry,
	memMgr *memory.Manager,
	persistentPrompt prompt.PromptTemplate,
) *taskagent.Agent {
	coderPrompt := prompt.PromptTemplate(prompt.StringPrompt(CoderSystemPrompt))
	if persistentPrompt != nil {
		coderPrompt = persistentPrompt
	}

	var opts []taskagent.Option
	opts = append(opts,
		taskagent.WithChatCompleter(llm),
		taskagent.WithModel(cfg.LLM.Model),
		taskagent.WithToolRegistry(coderReg),
		taskagent.WithSystemPrompt(coderPrompt),
		taskagent.WithMaxIterations(cfg.Agents.MaxIterations),
		taskagent.WithRunTokenBudget(cfg.Agents.RunTokenBudget),
	)
	if memMgr != nil {
		opts = append(opts, taskagent.WithMemory(memMgr))
	}

	return taskagent.New(
		agent.Config{
			ID:          "coder",
			Name:        "Coder Agent",
			Description: "Performs coding tasks: reads, writes, edits files, runs commands, and searches codebases",
		},
		opts...,
	)
}
