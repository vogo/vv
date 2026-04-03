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
	phaseBulletStyle    = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true) // yellow bullet for phases
	toolBulletStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))             // gray bullet for tools
	subAgentBulletStyle = lipgloss.NewStyle().Foreground(lipgloss.Color("10")).Bold(true) // green bullet for sub-agents
	dimStyle            = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))             // dim gray
	toolNameStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true) // blue bold for tool names
	phaseStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("11")).Bold(true) // yellow bold for phases
	subAgentStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("14")).Bold(true) // cyan bold for sub-agents
	statsStyle          = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))             // dim for stats
	addStyle            = lipgloss.NewStyle().Foreground(lipgloss.Color("10"))            // green for additions
	removeStyle         = lipgloss.NewStyle().Foreground(lipgloss.Color("196"))           // red for removals
	filePathStyle       = lipgloss.NewStyle().Foreground(lipgloss.Color("8"))             // dim for file paths
)

const (
	bullet     = "● "
	indentUnit = 4 // 4 characters per nesting level
)

// execStats holds execution statistics for phases, sub-agents, and tasks.
type execStats struct {
	ToolCalls        int
	DurationMs       int64
	PromptTokens     int
	CompletionTokens int
}

// indentBlock prepends `depth * indentUnit` spaces to each line of text.
func indentBlock(text string, depth int) string {
	if depth <= 0 {
		return text
	}

	prefix := strings.Repeat(" ", depth*indentUnit)
	lines := strings.Split(text, "\n")

	for i, line := range lines {
		if line != "" {
			lines[i] = prefix + line
		}
	}

	return strings.Join(lines, "\n")
}

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

// renderSummaryMessage renders a compressed context summary with dimmed style.
func renderSummaryMessage(text string) string {
	header := dimStyle.Render("[Previous context (summarized)]")
	body := dimStyle.Render(text)
	return header + "\n" + body
}

// renderToolMessage renders a tool-related message with bullet indicator.
func renderToolMessage(text string) string {
	return toolBulletStyle.Render(bullet) + toolStyle.Render(text)
}

// renderToolCallStart renders a tool call start with Claude Code-like formatting.
// Shows: ● ToolName(file_path_or_summary)
func renderToolCallStart(toolName, arguments string, depth int) string {
	var sb strings.Builder
	sb.WriteString(toolBulletStyle.Render(bullet))
	sb.WriteString(toolNameStyle.Render(toolName))

	// Extract key info from arguments for compact display.
	summary := extractToolSummary(toolName, arguments)
	if summary != "" {
		sb.WriteString(filePathStyle.Render("("))
		sb.WriteString(filePathStyle.Render(summary))
		sb.WriteString(filePathStyle.Render(")"))
	}

	return indentBlock(sb.String(), depth)
}

// renderToolCallResult renders a tool result with compact change summary.
func renderToolCallResult(toolName, resultText string, depth int) string {
	var sb strings.Builder

	// Show compact change summary for write/edit tools.
	switch toolName {
	case "read":
		// Suppress file content — the tool call start already shows the file path.
	case "write", "edit":
		summary := extractChangeSummary(resultText)
		if summary != "" {
			sb.WriteString("└ " + dimStyle.Render(summary))
		}
	case "bash":
		// Show truncated output for bash.
		if resultText != "" {
			lines := strings.Split(resultText, "\n")
			preview := truncate(lines[0], 120)
			if len(lines) > 1 {
				preview += dimStyle.Render(fmt.Sprintf(" (+%d lines)", len(lines)-1))
			}
			sb.WriteString("└ " + dimStyle.Render(preview))
		}
	default:
		if resultText != "" {
			sb.WriteString("└ " + dimStyle.Render(truncate(resultText, 120)))
		}
	}

	return indentBlock(sb.String(), depth)
}

// renderSubAgentStart renders a sub-agent start message.
func renderSubAgentStart(agentName, stepID, description string, stepIndex, totalSteps int, depth int) string {
	var sb strings.Builder
	sb.WriteString(subAgentBulletStyle.Render(bullet))

	if stepIndex > 0 && totalSteps > 0 {
		sb.WriteString(phaseStyle.Render(fmt.Sprintf("Step %d/%d: ", stepIndex, totalSteps)))
	}

	sb.WriteString(subAgentStyle.Render(agentName))

	if stepID != "" {
		sb.WriteString(dimStyle.Render(fmt.Sprintf(" (%s)", stepID)))
	}

	if description != "" {
		sb.WriteString("\n" + dimStyle.Render(truncate(description, 200)))
	}

	return indentBlock(sb.String(), depth)
}

// renderSubAgentEnd renders a sub-agent completion summary with stats.
func renderSubAgentEnd(agentName, stepID string, stats execStats, depth int) string {
	var sb strings.Builder
	sb.WriteString(subAgentBulletStyle.Render(bullet))
	sb.WriteString("sub-agent ")
	sb.WriteString(subAgentStyle.Render(agentName))

	if stepID != "" {
		sb.WriteString(dimStyle.Render(fmt.Sprintf(" (%s)", stepID)))
	}

	sb.WriteString(" complete.  ")
	sb.WriteString(statsStyle.Render(buildStatsLine(stats)))

	return indentBlock(sb.String(), depth)
}

// renderPhaseTransition renders a phase start/end transition message.
func renderPhaseTransition(phase string, starting bool, stats execStats, depth int) string {
	var sb strings.Builder
	phaseName := strings.ToUpper(phase[:1]) + phase[1:]

	if starting {
		sb.WriteString(phaseBulletStyle.Render(bullet))
		sb.WriteString(phaseStyle.Render(phaseName))
	} else {
		sb.WriteString(phaseBulletStyle.Render(bullet))
		fmt.Fprintf(&sb, "phase %s complete.  ", phaseName)
		sb.WriteString(statsStyle.Render(buildStatsLine(stats)))
	}

	return indentBlock(sb.String(), depth)
}

// renderTaskComplete renders the task-level completion line.
func renderTaskComplete(stats execStats) string {
	var sb strings.Builder
	sb.WriteString(phaseBulletStyle.Render(bullet))
	sb.WriteString("task complete.  ")
	sb.WriteString(statsStyle.Render(buildStatsLine(stats)))

	return sb.String()
}

// renderPhaseSummary renders a phase summary (e.g., plan overview) with dim styling.
func renderPhaseSummary(summary string, depth int) string {
	return indentBlock(dimStyle.Render(summary), depth)
}

// extractToolSummary extracts a compact summary from tool arguments.
func extractToolSummary(toolName, arguments string) string {
	var args map[string]any
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return truncate(arguments, 80)
	}

	switch toolName {
	case "read":
		if fp, ok := args["file_path"].(string); ok {
			return fp
		}
	case "write", "edit":
		if fp, ok := args["file_path"].(string); ok {
			return fp
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

// formatCompactTokens formats a token count without suffix.
func formatCompactTokens(tokens int) string {
	if tokens >= 1000000 {
		return fmt.Sprintf("%.1fM", float64(tokens)/1000000)
	}

	if tokens >= 1000 {
		return fmt.Sprintf("%.1fk", float64(tokens)/1000)
	}

	return fmt.Sprintf("%d", tokens)
}

// buildStatsLine builds a parenthetical stats string like "(3 tool uses · 26s · ↑ 5.3k · ↓ 10.5k)".
func buildStatsLine(s execStats) string {
	var parts []string

	if s.ToolCalls > 0 {
		noun := "tool use"
		if s.ToolCalls != 1 {
			noun = "tool uses"
		}

		parts = append(parts, fmt.Sprintf("%d %s", s.ToolCalls, noun))
	}

	parts = append(parts, formatDuration(s.DurationMs))

	if s.PromptTokens > 0 {
		parts = append(parts, fmt.Sprintf("\u2191 %s", formatCompactTokens(s.PromptTokens)))
	}

	if s.CompletionTokens > 0 {
		parts = append(parts, fmt.Sprintf("\u2193 %s", formatCompactTokens(s.CompletionTokens)))
	}

	return "(" + strings.Join(parts, " \u00b7 ") + ")"
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
