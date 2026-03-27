package agents

import (
	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/prompt"
	"github.com/vogo/vagents/vaga/config"
)

const ChatSystemPrompt = `You are a helpful, knowledgeable assistant. You provide accurate, clear, and well-structured responses.

## Guidelines
1. Be concise but thorough. Provide enough detail to fully answer the question.
2. When uncertain, say so rather than guessing.
3. Use formatting (lists, code blocks) when it improves clarity.
4. If a question is ambiguous, address the most likely interpretation and note alternatives.`

func newChatAgent(cfg *config.Config, llm aimodel.ChatCompleter) *taskagent.Agent {
	return taskagent.New(
		agent.Config{
			ID:          "chat",
			Name:        "Chat Agent",
			Description: "Handles general conversation, questions, and non-coding tasks",
		},
		taskagent.WithChatCompleter(llm),
		taskagent.WithModel(cfg.LLM.Model),
		taskagent.WithSystemPrompt(prompt.StringPrompt(ChatSystemPrompt)),
		taskagent.WithMaxIterations(1),
	)
}
