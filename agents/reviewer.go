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

const ReviewerSystemPrompt = `You are an expert code reviewer. You analyze code for correctness, style, performance, and security issues.

## Available Tools
- **read**: Read file contents to review code.
- **glob**: Find files by name pattern (e.g., "**/*.go").
- **grep**: Search file contents using regular expressions.
- **bash**: Execute shell commands for running tests and linters.

## Guidelines
1. Read the code thoroughly before providing feedback.
2. Check for correctness, edge cases, error handling, and potential bugs.
3. Evaluate code style and consistency with project conventions.
4. Look for performance issues and suggest improvements.
5. Check for security vulnerabilities (input validation, injection, etc.).
6. Run tests and linters when available to verify the code.
7. Provide specific, actionable feedback with file references.
8. Do not modify code -- provide review feedback only.`

func newReviewerAgent(
	cfg *config.Config,
	llm aimodel.ChatCompleter,
	reviewReg tool.ToolRegistry,
	memMgr *memory.Manager,
) *taskagent.Agent {
	var opts []taskagent.Option
	opts = append(opts,
		taskagent.WithChatCompleter(llm),
		taskagent.WithModel(cfg.LLM.Model),
		taskagent.WithToolRegistry(reviewReg),
		taskagent.WithSystemPrompt(prompt.StringPrompt(ReviewerSystemPrompt)),
		taskagent.WithMaxIterations(cfg.Agents.MaxIterations),
	)
	if memMgr != nil {
		opts = append(opts, taskagent.WithMemory(memMgr))
	}

	return taskagent.New(
		agent.Config{
			ID:          "reviewer",
			Name:        "Reviewer Agent",
			Description: "Reviews code for correctness, style, performance, and security",
		},
		opts...,
	)
}
