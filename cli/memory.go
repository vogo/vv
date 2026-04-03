package cli

import (
	"context"
	"fmt"
	"strings"

	tea "github.com/charmbracelet/bubbletea"
)

// handleCommand checks for slash commands (e.g. /memory, /compact) before routing to agents.
// Returns a non-nil tea.Cmd if the input was handled as a command, nil otherwise.
func (m *model) handleCommand(input string) tea.Cmd {
	parts := strings.Fields(input)
	if len(parts) == 0 {
		return nil
	}

	if parts[0] == "/compact" {
		return m.handleCompactCommand()
	}

	if parts[0] == "/permission" {
		return m.handlePermissionCommand(parts[1:])
	}

	if parts[0] != "/memory" {
		return nil
	}

	if m.app.persistentMem == nil {
		return m.printSystem("Memory is not configured.")
	}

	if len(parts) < 2 {
		return m.printSystem("Usage: /memory list|show|set|delete ...")
	}

	ctx := context.Background()

	switch parts[1] {
	case "list":
		prefix := ""
		if len(parts) >= 3 {
			prefix = parts[2]
		}
		return m.handleMemoryList(ctx, prefix)

	case "show":
		if len(parts) < 3 {
			return m.printSystem("Usage: /memory show <namespace:key>")
		}
		return m.handleMemoryShow(ctx, parts[2])

	case "set":
		if len(parts) < 4 {
			return m.printSystem("Usage: /memory set <namespace:key> <content...>")
		}
		key := parts[2]
		content := strings.Join(parts[3:], " ")
		return m.handleMemorySet(ctx, key, content)

	case "delete":
		if len(parts) < 3 {
			return m.printSystem("Usage: /memory delete <namespace:key>")
		}
		return m.handleMemoryDelete(ctx, parts[2])

	default:
		return m.printSystem("Unknown memory command: " + parts[1] + "\nUsage: /memory list|show|set|delete ...")
	}
}

func (m *model) handleMemoryList(ctx context.Context, prefix string) tea.Cmd {
	entries, err := m.app.persistentMem.List(ctx, prefix)
	if err != nil {
		return m.printError(fmt.Sprintf("Memory list: %v", err))
	}

	if len(entries) == 0 {
		return m.printSystem("No memory entries found.")
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Memory entries (%d):\n", len(entries))
	for _, e := range entries {
		val := truncateValue(e.Value)
		fmt.Fprintf(&sb, "  %s: %s\n", e.Key, val)
	}
	return m.printSystem(sb.String())
}

func (m *model) handleMemoryShow(ctx context.Context, key string) tea.Cmd {
	val, err := m.app.persistentMem.Get(ctx, key)
	if err != nil {
		return m.printError(fmt.Sprintf("Memory show: %v", err))
	}

	if val == nil {
		return m.printSystem(fmt.Sprintf("Memory entry %q not found.", key))
	}

	return m.printSystem(fmt.Sprintf("[%s]\n%v", key, val))
}

func (m *model) handleMemorySet(ctx context.Context, key, content string) tea.Cmd {
	if err := m.app.persistentMem.Set(ctx, key, content, 0); err != nil {
		return m.printError(fmt.Sprintf("Memory set: %v", err))
	}

	return m.printSystem(fmt.Sprintf("Memory entry %q saved.", key))
}

func (m *model) handleMemoryDelete(ctx context.Context, key string) tea.Cmd {
	if err := m.app.persistentMem.Delete(ctx, key); err != nil {
		return m.printError(fmt.Sprintf("Memory delete: %v", err))
	}

	return m.printSystem(fmt.Sprintf("Memory entry %q deleted.", key))
}

// truncateValue returns a short preview of a memory entry value.
func truncateValue(v any) string {
	s := fmt.Sprintf("%v", v)
	if len(s) > 80 {
		return s[:80] + "..."
	}
	return s
}

// handleCompactCommand handles the /compact slash command for manual context compression.
func (m *model) handleCompactCommand() tea.Cmd {
	if m.app.compactor == nil {
		return m.printSystem("Context compression is not configured.")
	}

	// Dispatch async compaction via a tea.Cmd.
	return m.manualCompactCmd()
}

// manualCompactCmd creates an async tea.Cmd that compresses the conversation.
// The result is returned as a message to be applied in the Update handler,
// avoiding data races on shared state.
func (m *model) manualCompactCmd() tea.Cmd {
	// Snapshot history under the single-threaded Update context.
	history := m.app.history

	return func() tea.Msg {
		// Force compact regardless of threshold for manual command.
		compressed, newTokens, err := m.app.compactor.Compact(context.Background(), history)
		if err != nil {
			return emergencyCompactResultMsg{err: err}
		}

		n := len(history) - len(compressed)

		return manualCompactResultMsg{
			compressed:      compressed,
			newTokens:       newTokens,
			summarizedCount: n,
		}
	}
}
