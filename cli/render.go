package cli

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/charmbracelet/glamour"
	"github.com/charmbracelet/lipgloss"
)

var (
	userStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true) // cyan
	agentStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true) // blue
	systemStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("11"))            // yellow
	toolStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("13"))            // magenta
	errorStyle  = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))           // red

	// Enhanced styles for Claude Code-like display.
	bulletStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true) // green bullet
	dimStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))             // dim gray
	toolNameStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true) // blue bold for tool names
	phaseStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true) // yellow bold for phases
	subAgentStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true) // cyan bold for sub-agents
	statsStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))             // dim for stats
	addStyle      = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))            // green for additions
	removeStyle   = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))           // red for removals
	filePathStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))             // dim for file paths
	indentStyle   = lipgloss.NewStyle().PaddingLeft(2)                              // indented content
)

const bullet = "● "

// renderMarkdown renders markdown text using glamour.
func renderMarkdown(text string, width int) string {
	if width <= 0 {
		width = 80
	}

	renderer, err := glamour.NewTermRenderer(
		glamour.WithAutoStyle(),
		glamour.WithWordWrap(width),
	)
	if err != nil {
		return text
	}

	rendered, err := renderer.Render(text)
	if err != nil {
		return text
	}

	return strings.TrimSpace(rendered)
}

// renderError renders an error message with red styling.
func renderError(err error) string {
	return errorStyle.Render(fmt.Sprintf("Error: %s", err.Error()))
}

// renderUserMessage renders a user message with styling.
func renderUserMessage(text string) string {
	return userStyle.Render("You: ") + text
}

// renderAgentMessage renders an agent message with markdown formatting.
// The "Agent: " prefix is added by refreshViewport, not here.
func renderAgentMessage(text string, width int) string {
	return renderMarkdown(text, width)
}

// renderSystemMessage renders a system message.
func renderSystemMessage(text string) string {
	return systemStyle.Render(text)
}

// renderToolMessage renders a tool-related message with bullet indicator.
func renderToolMessage(text string) string {
	return bulletStyle.Render(bullet) + toolStyle.Render(text)
}

// renderPhaseMessage renders a phase transition message.
func renderPhaseMessage(text string) string {
	return bulletStyle.Render(bullet) + phaseStyle.Render(text)
}

// renderSubAgentMessage renders a sub-agent lifecycle message.
func renderSubAgentMessage(text string) string {
	return bulletStyle.Render(bullet) + subAgentStyle.Render(text)
}

// renderToolCallStart renders a tool call start with Claude Code-like formatting.
// Shows: ● ToolName(file_path_or_summary)
func renderToolCallStart(toolName, arguments string) string {
	var sb strings.Builder
	sb.WriteString(bulletStyle.Render(bullet))
	sb.WriteString(toolNameStyle.Render(toolName))

	// Extract key info from arguments for compact display.
	summary := extractToolSummary(toolName, arguments)
	if summary != "" {
		sb.WriteString(filePathStyle.Render("("))
		sb.WriteString(filePathStyle.Render(summary))
		sb.WriteString(filePathStyle.Render(")"))
	}

	return sb.String()
}

// renderToolCallResult renders a tool result with compact change summary.
func renderToolCallResult(toolName, resultText string) string {
	var sb strings.Builder

	// Show compact change summary for write/edit tools.
	switch toolName {
	case "write", "edit":
		summary := extractChangeSummary(resultText)
		if summary != "" {
			sb.WriteString(indentStyle.Render("└ " + dimStyle.Render(summary)))
		}
	case "bash":
		// Show truncated output for bash.
		if resultText != "" {
			lines := strings.Split(resultText, "\n")
			preview := truncate(lines[0], 120)
			if len(lines) > 1 {
				preview += dimStyle.Render(fmt.Sprintf(" (+%d lines)", len(lines)-1))
			}
			sb.WriteString(indentStyle.Render("└ " + dimStyle.Render(preview)))
		}
	default:
		if resultText != "" {
			sb.WriteString(indentStyle.Render("└ " + dimStyle.Render(truncate(resultText, 120))))
		}
	}

	return sb.String()
}

// renderSubAgentStart renders a sub-agent start message.
func renderSubAgentStart(agentName, stepID, description string, stepIndex, totalSteps int) string {
	var sb strings.Builder
	sb.WriteString(bulletStyle.Render(bullet))

	if stepIndex > 0 && totalSteps > 0 {
		sb.WriteString(phaseStyle.Render(fmt.Sprintf("Step %d/%d: ", stepIndex, totalSteps)))
	}

	sb.WriteString(subAgentStyle.Render(agentName))

	if stepID != "" {
		sb.WriteString(dimStyle.Render(fmt.Sprintf(" (%s)", stepID)))
	}

	if description != "" {
		sb.WriteString("\n")
		sb.WriteString(indentStyle.Render(dimStyle.Render(truncate(description, 200))))
	}

	return sb.String()
}

// renderSubAgentEnd renders a sub-agent completion summary with stats.
func renderSubAgentEnd(agentName, stepID string, durationMs int64, toolCalls, tokensUsed int) string {
	var sb strings.Builder
	sb.WriteString(bulletStyle.Render(bullet))
	sb.WriteString(subAgentStyle.Render(agentName))

	if stepID != "" {
		sb.WriteString(dimStyle.Render(fmt.Sprintf(" (%s)", stepID)))
	}

	// Build stats line like: "Done (12 tool uses · 45.2k tokens · 2m 30s)"
	var stats []string

	if toolCalls > 0 {
		stats = append(stats, fmt.Sprintf("%d tool uses", toolCalls))
	}

	if tokensUsed > 0 {
		stats = append(stats, formatTokens(tokensUsed))
	}

	stats = append(stats, formatDuration(durationMs))

	sb.WriteString("\n")
	sb.WriteString(indentStyle.Render("└ " + statsStyle.Render("Done ("+strings.Join(stats, " · ")+")")))

	return sb.String()
}

// renderPhaseTransition renders a phase start/end transition message.
func renderPhaseTransition(phase string, phaseIndex, totalPhases int, starting bool) string {
	var sb strings.Builder
	sb.WriteString(bulletStyle.Render(bullet))

	phaseName := strings.ToUpper(phase[:1]) + phase[1:]

	if starting {
		sb.WriteString(phaseStyle.Render(fmt.Sprintf("Phase %d/%d: %s", phaseIndex, totalPhases, phaseName)))
	} else {
		sb.WriteString(dimStyle.Render(fmt.Sprintf("%s phase complete.", phaseName)))
	}

	return sb.String()
}

// extractToolSummary extracts a compact summary from tool arguments.
func extractToolSummary(toolName, arguments string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return truncate(arguments, 80)
	}

	switch toolName {
	case "read", "write":
		if fp, ok := args["file_path"].(string); ok {
			return shortenPath(fp)
		}
	case "edit":
		if fp, ok := args["file_path"].(string); ok {
			return shortenPath(fp)
		}
	case "glob":
		if pat, ok := args["pattern"].(string); ok {
			return pat
		}
	case "grep":
		if pat, ok := args["pattern"].(string); ok {
			return truncate(pat, 60)
		}
	case "bash":
		if cmd, ok := args["command"].(string); ok {
			return truncate(cmd, 80)
		}
	}

	return truncate(arguments, 80)
}

// extractChangeSummary extracts a change summary from tool result text.
func extractChangeSummary(resultText string) string {
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
		parts := []string{}
		if added > 0 {
			parts = append(parts, addStyle.Render(fmt.Sprintf("Added %d line%s", added, pluralS(added))))
		}
		if removed > 0 {
			parts = append(parts, removeStyle.Render(fmt.Sprintf("removed %d line%s", removed, pluralS(removed))))
		}
		return strings.Join(parts, ", ")
	}

	if len(resultText) > 0 {
		return truncate(lines[0], 100)
	}

	return ""
}

// shortenPath shortens a file path for display by keeping the last few components.
func shortenPath(path string) string {
	parts := strings.Split(path, "/")
	if len(parts) <= 3 {
		return path
	}
	return ".../" + strings.Join(parts[len(parts)-3:], "/")
}

// formatTokens formats a token count for display (e.g., "45.2k tokens").
func formatTokens(tokens int) string {
	if tokens >= 1000000 {
		return fmt.Sprintf("%.1fM tokens", float64(tokens)/1000000)
	}
	if tokens >= 1000 {
		return fmt.Sprintf("%.1fk tokens", float64(tokens)/1000)
	}
	return fmt.Sprintf("%d tokens", tokens)
}

// formatDuration formats milliseconds into a human-readable duration.
func formatDuration(ms int64) string {
	if ms < 1000 {
		return fmt.Sprintf("%dms", ms)
	}

	seconds := ms / 1000
	if seconds < 60 {
		return fmt.Sprintf("%ds", seconds)
	}

	minutes := seconds / 60
	remainSec := seconds % 60

	if remainSec > 0 {
		return fmt.Sprintf("%dm %ds", minutes, remainSec)
	}

	return fmt.Sprintf("%dm", minutes)
}

// truncate shortens a string to maxLen, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}

	return s[:maxLen] + "..."
}

// pluralS returns "s" for counts != 1.
func pluralS(n int) string {
	if n == 1 {
		return ""
	}
	return "s"
}
