package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
)

// RunPrompt runs a single prompt non-interactively against the given
// orchestrator and writes the agent's text response to stdout. Diagnostic
// events (phases, tool calls, sub-agents) are written to stderr. The function
// returns nil on success or the stream error on failure.
func RunPrompt(ctx context.Context, orchestrator agent.StreamAgent, prompt string, stdout, stderr io.Writer) error {
	// Generate a transient session ID.
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	sessionID := hex.EncodeToString(b)

	// Build request with a single user message.
	req := &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage(prompt)},
		SessionID: sessionID,
	}

	stream, err := orchestrator.RunStream(ctx, req)
	if err != nil {
		return fmt.Errorf("start stream: %w", err)
	}
	defer func() { _ = stream.Close() }()

	// Execution state tracking.
	var (
		nestingDepth          int
		toolCallCount         int
		taskStart             = time.Now()
		totalPromptTokens     int
		totalCompletionTokens int
		totalToolCalls        int

		// Sub-agent level stats (for fallback when SubAgentEndData lacks them).
		subAgentPromptTokens     int
		subAgentCompletionTokens int

		hasOutput bool
	)

	for {
		event, recvErr := stream.Recv()
		if recvErr != nil {
			if errors.Is(recvErr, io.EOF) {
				// Write trailing newline if we produced any output.
				if hasOutput {
					_, _ = fmt.Fprintln(stdout)
				}

				// Emit task-complete summary.
				taskDuration := time.Since(taskStart).Milliseconds()
				stats := execStats{
					ToolCalls:        totalToolCalls,
					DurationMs:       taskDuration,
					PromptTokens:     totalPromptTokens,
					CompletionTokens: totalCompletionTokens,
				}
				_, _ = fmt.Fprintf(stderr, "[done] %s\n", buildStatsLine(stats))

				return nil
			}

			return recvErr
		}

		switch event.Type {
		case schema.EventTextDelta:
			if data, ok := event.Data.(schema.TextDeltaData); ok {
				_, _ = fmt.Fprint(stdout, data.Delta)
				hasOutput = true
			}

		case schema.EventToolCallStart:
			if data, ok := event.Data.(schema.ToolCallStartData); ok {
				totalToolCalls++
				toolCallCount++
				summary := extractToolSummary(data.ToolName, data.Arguments)
				indent := strings.Repeat("  ", nestingDepth+1)
				if summary != "" {
					_, _ = fmt.Fprintf(stderr, "%s[tool] %s(%s)\n", indent, data.ToolName, summary)
				} else {
					_, _ = fmt.Fprintf(stderr, "%s[tool] %s\n", indent, data.ToolName)
				}
			}

		case schema.EventToolResult:
			if data, ok := event.Data.(schema.ToolResultData); ok {
				resultText := ""
				for _, part := range data.Result.Content {
					if part.Type == "text" {
						resultText = part.Text
						break
					}
				}

				line := promptToolResultSummary(data.ToolName, resultText)
				if line != "" {
					indent := strings.Repeat("  ", nestingDepth+1)
					_, _ = fmt.Fprintf(stderr, "%s  %s\n", indent, line)
				}
			}

		case schema.EventPhaseStart:
			if data, ok := event.Data.(schema.PhaseStartData); ok {
				_, _ = fmt.Fprintf(stderr, "[phase] %s\n", capitalizeFirst(data.Phase))
			}

		case schema.EventPhaseEnd:
			if data, ok := event.Data.(schema.PhaseEndData); ok {
				if data.Summary != "" {
					indent := "  "
					for line := range strings.SplitSeq(data.Summary, "\n") {
						_, _ = fmt.Fprintf(stderr, "%s%s\n", indent, line)
					}
				}
				stats := execStats{
					ToolCalls:        data.ToolCalls,
					DurationMs:       data.Duration,
					PromptTokens:     data.PromptTokens,
					CompletionTokens: data.CompletionTokens,
				}
				_, _ = fmt.Fprintf(stderr, "[phase] %s complete %s\n", capitalizeFirst(data.Phase), buildStatsLine(stats))
			}

		case schema.EventSubAgentStart:
			if data, ok := event.Data.(schema.SubAgentStartData); ok {
				nestingDepth++
				toolCallCount = 0
				subAgentPromptTokens = 0
				subAgentCompletionTokens = 0
				indent := strings.Repeat("  ", nestingDepth)
				stepInfo := ""
				if data.StepID != "" {
					stepInfo = fmt.Sprintf(" (%s)", data.StepID)
				}
				_, _ = fmt.Fprintf(stderr, "%s[agent] %s%s\n", indent, data.AgentName, stepInfo)
			}

		case schema.EventSubAgentEnd:
			if data, ok := event.Data.(schema.SubAgentEndData); ok {
				tc := data.ToolCalls
				if tc == 0 {
					tc = toolCallCount
				}
				pt := data.PromptTokens
				if pt == 0 {
					pt = subAgentPromptTokens
				}
				ct := data.CompletionTokens
				if ct == 0 {
					ct = subAgentCompletionTokens
				}
				stats := execStats{
					ToolCalls:        tc,
					DurationMs:       data.Duration,
					PromptTokens:     pt,
					CompletionTokens: ct,
				}
				indent := strings.Repeat("  ", nestingDepth)
				_, _ = fmt.Fprintf(stderr, "%s[agent] %s complete %s\n", indent, data.AgentName, buildStatsLine(stats))
				toolCallCount = 0
				subAgentPromptTokens = 0
				subAgentCompletionTokens = 0
				nestingDepth--
				if nestingDepth < 0 {
					nestingDepth = 0
				}
			}

		case schema.EventError:
			if data, ok := event.Data.(schema.ErrorData); ok {
				_, _ = fmt.Fprintf(stderr, "[error] %s\n", data.Message)
			}

		case schema.EventTokenBudgetExhausted:
			_, _ = fmt.Fprintf(stderr, "[warning] token budget exhausted\n")

		case schema.EventLLMCallEnd:
			if data, ok := event.Data.(schema.LLMCallEndData); ok {
				totalPromptTokens += data.PromptTokens
				totalCompletionTokens += data.CompletionTokens
				subAgentPromptTokens += data.PromptTokens
				subAgentCompletionTokens += data.CompletionTokens
			}

		case schema.EventAgentStart, schema.EventIterationStart,
			schema.EventLLMCallStart, schema.EventLLMCallError,
			schema.EventToolCallEnd, schema.EventAgentEnd:
			// Suppressed.
		}
	}
}

// promptToolResultSummary returns a plain-text summary line for a tool result,
// or empty string if the result should be suppressed.
func promptToolResultSummary(toolName, resultText string) string {
	switch toolName {
	case "read":
		// Suppress file content.
		return ""
	case "write", "edit":
		lines := strings.Split(resultText, "\n")
		added := 0
		removed := 0
		for _, line := range lines {
			trimmed := strings.TrimSpace(line)
			if strings.HasPrefix(trimmed, "+") && !strings.HasPrefix(trimmed, "+++") {
				added++
			} else if strings.HasPrefix(trimmed, "-") && !strings.HasPrefix(trimmed, "---") {
				removed++
			}
		}
		if added > 0 || removed > 0 {
			var parts []string
			if added > 0 {
				parts = append(parts, fmt.Sprintf("Added %d line%s", added, pluralS(added)))
			}
			if removed > 0 {
				parts = append(parts, fmt.Sprintf("removed %d line%s", removed, pluralS(removed)))
			}
			return fmt.Sprintf("-> %s", strings.Join(parts, ", "))
		}
		if len(resultText) > 0 {
			return fmt.Sprintf("-> %s", truncate(lines[0], 100))
		}
		return ""
	case "bash":
		if resultText != "" {
			lines := strings.Split(resultText, "\n")
			preview := truncate(lines[0], 120)
			if len(lines) > 1 {
				preview += fmt.Sprintf(" (+%d lines)", len(lines)-1)
			}
			return fmt.Sprintf("-> %s", preview)
		}
		return ""
	default:
		if resultText != "" {
			return fmt.Sprintf("-> %s", truncate(resultText, 120))
		}
		return ""
	}
}

// capitalizeFirst capitalizes the first letter of a string.
func capitalizeFirst(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}
