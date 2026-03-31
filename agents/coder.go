package agents

import (
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/prompt"
	"github.com/vogo/vagents/vaga/registry"
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

// RegisterCoder registers the coder agent descriptor with the registry.
func RegisterCoder(reg *registry.Registry) {
	reg.MustRegister(registry.AgentDescriptor{
		ID:           "coder",
		DisplayName:  "Coder",
		Description:  "Reads, writes, edits files, runs commands, searches codebases, debugs",
		ToolProfile:  registry.ProfileFull,
		SystemPrompt: CoderSystemPrompt,
		Dispatchable: true,
		Factory: func(opts registry.FactoryOptions) (agent.Agent, error) {
			// Build system prompt: use persistent memory prompt if available.
			var sysPrompt prompt.PromptTemplate
			if opts.PersistentMemory != nil {
				sysPrompt = NewPersistentMemoryPrompt(CoderSystemPrompt, opts.PersistentMemory)
			} else {
				sysPrompt = prompt.StringPrompt(CoderSystemPrompt)
			}

			var taskOpts []taskagent.Option

			taskOpts = append(taskOpts,
				taskagent.WithChatCompleter(opts.LLM),
				taskagent.WithModel(opts.Model),
				taskagent.WithSystemPrompt(sysPrompt),
				taskagent.WithMaxIterations(opts.MaxIterations),
				taskagent.WithRunTokenBudget(opts.RunTokenBudget),
			)

			if opts.ToolRegistry != nil {
				taskOpts = append(taskOpts, taskagent.WithToolRegistry(opts.ToolRegistry))
			}

			if opts.Memory != nil {
				taskOpts = append(taskOpts, taskagent.WithMemory(opts.Memory))
			}

			return taskagent.New(
				agent.Config{ID: "coder", Name: "Coder Agent", Description: "Performs coding tasks: reads, writes, edits files, runs commands, and searches codebases"},
				taskOpts...,
			), nil
		},
	})
}
