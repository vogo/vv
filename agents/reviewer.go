package agents

import (
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/prompt"
	"github.com/vogo/vv/registries"
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
8. Do not modify code -- provide review feedback only.

## Clarifying Questions
- **ask_user**: Ask the user a clarifying question when you encounter ambiguity. The user's text response is returned as the result.

Use ask_user when:
- The user's instruction is ambiguous and multiple interpretations exist.
- Multiple valid approaches exist and the choice significantly affects the outcome.
- A destructive or irreversible action is about to be taken and the intent is unclear.
- Critical information (file paths, variable names, scope) is missing and cannot be reasonably inferred.

Do NOT use ask_user when:
- The answer can be reasonably inferred from context.
- The question is trivial or would interrupt flow unnecessarily.
- You have already asked a question in the current turn.`

// RegisterReviewer registers the reviewer agent descriptor with the registries.
func RegisterReviewer(reg *registries.Registry) {
	reg.MustRegister(registries.AgentDescriptor{
		ID:           "reviewer",
		DisplayName:  "Reviewer",
		Description:  "Reviews code for correctness, style, performance, security",
		ToolProfile:  registries.ProfileReview,
		SystemPrompt: ReviewerSystemPrompt,
		Dispatchable: true,
		Factory: func(opts registries.FactoryOptions) (agent.Agent, error) {
			sysPrompt := AppendProjectInstructions(ReviewerSystemPrompt, opts.ProjectInstructions)

			var taskOpts []taskagent.Option

			taskOpts = append(taskOpts,
				taskagent.WithChatCompleter(opts.LLM),
				taskagent.WithModel(opts.Model),
				taskagent.WithSystemPrompt(prompt.StringPrompt(sysPrompt)),
				taskagent.WithMaxIterations(opts.MaxIterations),
			)

			if opts.ToolRegistry != nil {
				taskOpts = append(taskOpts, taskagent.WithToolRegistry(opts.ToolRegistry))
			}

			if opts.Memory != nil {
				taskOpts = append(taskOpts, taskagent.WithMemory(opts.Memory))
			}

			return taskagent.New(
				agent.Config{
					ID:          "reviewer",
					Name:        "Reviewer Agent",
					Description: "Reviews code for correctness, style, performance, and security",
				},
				taskOpts...,
			), nil
		},
	})
}
