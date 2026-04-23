package agents

import (
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/prompt"
	"github.com/vogo/vv/registries"
)

const ChatSystemPrompt = `You are a helpful, knowledgeable assistant. You provide accurate, clear, and well-structured responses.

## Guidelines
1. Be concise but thorough. Provide enough detail to fully answer the question.
2. When uncertain, say so rather than guessing.
3. Use formatting (lists, code blocks) when it improves clarity.
4. If a question is ambiguous, address the most likely interpretation and note alternatives.`

// RegisterChat registers the chat agent descriptor with the registries.
func RegisterChat(reg *registries.Registry) {
	reg.MustRegister(registries.AgentDescriptor{
		ID:           "chat",
		DisplayName:  "Chat",
		Description:  "General conversation, questions, explanations, brainstorming",
		ToolProfile:  registries.ProfileNone,
		SystemPrompt: ChatSystemPrompt,
		Dispatchable: true,
		Factory: func(opts registries.FactoryOptions) (agent.Agent, error) {
			sysPrompt := AppendProjectInstructions(ChatSystemPrompt, opts.ProjectInstructions)

			taskOpts := []taskagent.Option{
				taskagent.WithChatCompleter(opts.LLM),
				taskagent.WithModel(opts.Model),
				taskagent.WithSystemPrompt(prompt.StringPrompt(sysPrompt)),
				taskagent.WithMaxIterations(1), // hardcoded to 1, preserving current behavior
			}

			if opts.HookManager != nil {
				taskOpts = append(taskOpts, taskagent.WithHookManager(opts.HookManager))
			}

			return taskagent.New(
				agent.Config{
					ID:          "chat",
					Name:        "Chat Agent",
					Description: "Handles general conversation, questions, and non-coding tasks",
				},
				taskOpts...,
			), nil
		},
	})
}
