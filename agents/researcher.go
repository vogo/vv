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

const ResearcherSystemPrompt = `You are an expert code researcher. You explore codebases, read documentation, and gather information to answer questions thoroughly.

## Available Tools
- **read**: Read file contents to understand code and documentation.
- **glob**: Find files by name pattern (e.g., "**/*.go").
- **grep**: Search file contents using regular expressions.

## Guidelines
1. Start with broad exploration (glob, grep) before diving into specific files.
2. Read relevant files thoroughly to build understanding.
3. Summarize findings clearly, with references to specific files and line numbers.
4. When exploring a codebase, identify key patterns, conventions, and architecture.
5. Cross-reference multiple files to give comprehensive answers.
6. Do not attempt to modify any files -- you are read-only.`

func newResearcherAgent(
	cfg *config.Config,
	llm aimodel.ChatCompleter,
	readOnlyReg tool.ToolRegistry,
	memMgr *memory.Manager,
) *taskagent.Agent {
	var opts []taskagent.Option
	opts = append(opts,
		taskagent.WithChatCompleter(llm),
		taskagent.WithModel(cfg.LLM.Model),
		taskagent.WithToolRegistry(readOnlyReg),
		taskagent.WithSystemPrompt(prompt.StringPrompt(ResearcherSystemPrompt)),
		taskagent.WithMaxIterations(cfg.Agents.MaxIterations),
	)
	if memMgr != nil {
		opts = append(opts, taskagent.WithMemory(memMgr))
	}

	return taskagent.New(
		agent.Config{
			ID:          "researcher",
			Name:        "Researcher Agent",
			Description: "Explores codebases, reads documentation, and gathers information",
		},
		opts...,
	)
}
