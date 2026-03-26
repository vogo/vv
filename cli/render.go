package cli

import (
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
)

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

// renderToolMessage renders a tool-related message.
func renderToolMessage(text string) string {
	return toolStyle.Render(text)
}

// truncate shortens a string to maxLen, appending "..." if truncated.
func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}

	return s[:maxLen] + "..."
}
