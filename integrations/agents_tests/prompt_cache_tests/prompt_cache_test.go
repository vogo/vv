package prompt_cache_tests

import (
	"testing"
	"time"

	"github.com/vogo/vage/schema"
)

// TestIntegration_PromptCache_NormalChat sends two independent researcher
// turns that share the same system prefix and tool list but do not invoke
// tools. The second LLM call must read from the vendor prompt cache
// (Usage.CacheReadTokens > 0). Skips when no API key is set.
func TestIntegration_PromptCache_NormalChat(t *testing.T) {
	h := bootResearcher(t)

	const timeout = 90 * time.Second

	first := h.run(t, timeout, &schema.RunRequest{
		SessionID: "prompt-cache-chat-a",
		Messages: []schema.Message{
			schema.NewUserMessage(h.proto, "Reply with exactly the word ping. Do not call any tool."),
		},
	})
	if len(first.Messages) == 0 {
		t.Fatal("first chat turn produced no messages")
	}

	second := h.run(t, timeout, &schema.RunRequest{
		SessionID: "prompt-cache-chat-b",
		Messages: []schema.Message{
			schema.NewUserMessage(h.proto, "Reply with exactly the word pong. Do not call any tool."),
		},
	})
	if len(second.Messages) == 0 {
		t.Fatal("second chat turn produced no messages")
	}

	calls := h.rec.snapshot()
	logCalls(t, calls)
	assertPromptCacheUsed(t, calls)
}

// TestIntegration_PromptCache_ToolCalling forces a ReAct loop that calls
// glob, then asks the model to answer from the tool result. The second (and
// later) LLM iteration re-sends the same system prompt and tool definitions
// and must hit the vendor prompt cache. Skips when no API key is set.
func TestIntegration_PromptCache_ToolCalling(t *testing.T) {
	h := bootResearcher(t)

	prompt := "You must call the glob tool exactly once with pattern \"*.go\" and path " +
		h.workDir +
		". After the tool result arrives, reply with the number of matching files as a single integer. " +
		"Do not skip the tool call."

	resp := h.run(t, 120*time.Second, &schema.RunRequest{
		SessionID: "prompt-cache-tool",
		Messages: []schema.Message{
			schema.NewUserMessage(h.proto, prompt),
		},
		Options: &schema.RunOptions{
			Tools:         []string{"glob"},
			MaxIterations: 5,
		},
	})
	if len(resp.Messages) == 0 {
		t.Fatal("tool-calling run produced no messages")
	}

	calls := h.rec.snapshot()
	logCalls(t, calls)

	if len(calls) < 2 {
		t.Fatalf("expected a ReAct loop (tool call then follow-up LLM call), got %d LLM call(s); the model did not invoke glob",
			len(calls))
	}

	if calls[0].Tools == 0 {
		t.Fatal("first LLM call carried no tools; glob filter did not reach the request")
	}

	assertPromptCacheUsed(t, calls)
}
