package agents

import (
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/prompt"
	"github.com/vogo/vv/registries"
)

const ResearcherSystemPrompt = `You are an expert code researcher. You explore codebases, read documentation, and gather information to answer questions thoroughly.

## Available Tools
- **read**: Read file contents to understand code and documentation.
- **web_fetch**: Fetch public web pages, documentation, or PDFs when the answer depends on external references.
- **glob**: Find files by name pattern (e.g., "**/*.go").
- **grep**: Search file contents using regular expressions.

## Guidelines
1. Start with broad exploration (glob, grep) before diving into specific files.
2. Read relevant files thoroughly to build understanding.
3. Summarize findings clearly, with references to specific files and line numbers.
4. When exploring a codebase, identify key patterns, conventions, and architecture.
5. Cross-reference multiple files to give comprehensive answers.
6. Do not attempt to modify any files -- you are read-only.

## Task Tracking
- **todo_write** is available. Use it when your investigation spans 3+ files
  or modules; skip it for short lookups.

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

// RegisterResearcher registers the researcher agent descriptor with the registries.
func RegisterResearcher(reg *registries.Registry) {
	reg.MustRegister(registries.AgentDescriptor{
		ID:           "researcher",
		DisplayName:  "Researcher",
		Description:  "Explores codebases, reads documentation, gathers information (read-only)",
		ToolProfile:  registries.ProfileReadOnly,
		SystemPrompt: ResearcherSystemPrompt,
		Dispatchable: true,
		Factory: func(opts registries.FactoryOptions) (agent.Agent, error) {
			sysPrompt := AppendProjectInstructions(ResearcherSystemPrompt, opts.ProjectInstructions)

			var taskOpts []taskagent.Option

			taskOpts = append(taskOpts,
				taskagent.WithChatCompleter(opts.LLM),
				taskagent.WithModel(opts.Model),
				taskagent.WithSystemPrompt(prompt.StringPrompt(sysPrompt)),
				taskagent.WithMaxIterations(opts.MaxIterations),
				taskagent.WithMaxParallelToolCalls(opts.MaxParallelToolCalls),
				taskagent.WithPromptCaching(opts.PromptCaching),
			)

			if opts.ToolRegistry != nil {
				taskOpts = append(taskOpts, taskagent.WithToolRegistry(opts.ToolRegistry))
			}

			if opts.Memory != nil {
				taskOpts = append(taskOpts, taskagent.WithMemory(opts.Memory))
			}

			if len(opts.ToolResultGuards) > 0 {
				taskOpts = append(taskOpts, taskagent.WithToolResultGuards(opts.ToolResultGuards...))
			}

			if opts.HookManager != nil {
				taskOpts = append(taskOpts, taskagent.WithHookManager(opts.HookManager))
			}

			return taskagent.New(
				agent.Config{
					ID:          "researcher",
					Name:        "Researcher Agent",
					Description: "Explores codebases, reads documentation, and gathers information",
				},
				taskOpts...,
			), nil
		},
	})
}
