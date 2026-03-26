package cli

import (
	"context"
	"fmt"
	"strings"
)

// handleCommand checks for slash commands (e.g. /memory) before routing to agents.
// Returns true if the input was handled as a command.
func (m *model) handleCommand(input string) bool {
	parts := strings.Fields(input)
	if len(parts) == 0 || parts[0] != "/memory" {
		return false
	}

	if m.app.persistentMem == nil {
		m.appendSystemMessage("Memory is not configured.")
		return true
	}

	if len(parts) < 2 {
		m.appendSystemMessage("Usage: /memory list|show|set|delete ...")
		return true
	}

	ctx := context.Background()

	switch parts[1] {
	case "list":
		prefix := ""
		if len(parts) >= 3 {
			prefix = parts[2]
		}
		m.handleMemoryList(ctx, prefix)

	case "show":
		if len(parts) < 3 {
			m.appendSystemMessage("Usage: /memory show <namespace:key>")
			return true
		}
		m.handleMemoryShow(ctx, parts[2])

	case "set":
		if len(parts) < 4 {
			m.appendSystemMessage("Usage: /memory set <namespace:key> <content...>")
			return true
		}
		key := parts[2]
		content := strings.Join(parts[3:], " ")
		m.handleMemorySet(ctx, key, content)

	case "delete":
		if len(parts) < 3 {
			m.appendSystemMessage("Usage: /memory delete <namespace:key>")
			return true
		}
		m.handleMemoryDelete(ctx, parts[2])

	default:
		m.appendSystemMessage("Unknown memory command: " + parts[1] + "\nUsage: /memory list|show|set|delete ...")
	}

	return true
}

func (m *model) handleMemoryList(ctx context.Context, prefix string) {
	entries, err := m.app.persistentMem.List(ctx, prefix)
	if err != nil {
		m.appendErrorMessage(fmt.Sprintf("Memory list: %v", err))
		return
	}

	if len(entries) == 0 {
		m.appendSystemMessage("No memory entries found.")
		return
	}

	var sb strings.Builder
	fmt.Fprintf(&sb, "Memory entries (%d):\n", len(entries))
	for _, e := range entries {
		val := truncateValue(e.Value)
		fmt.Fprintf(&sb, "  %s: %s\n", e.Key, val)
	}
	m.appendSystemMessage(sb.String())
}

func (m *model) handleMemoryShow(ctx context.Context, key string) {
	val, err := m.app.persistentMem.Get(ctx, key)
	if err != nil {
		m.appendErrorMessage(fmt.Sprintf("Memory show: %v", err))
		return
	}

	if val == nil {
		m.appendSystemMessage(fmt.Sprintf("Memory entry %q not found.", key))
		return
	}

	m.appendSystemMessage(fmt.Sprintf("[%s]\n%v", key, val))
}

func (m *model) handleMemorySet(ctx context.Context, key, content string) {
	if err := m.app.persistentMem.Set(ctx, key, content, 0); err != nil {
		m.appendErrorMessage(fmt.Sprintf("Memory set: %v", err))
		return
	}

	m.appendSystemMessage(fmt.Sprintf("Memory entry %q saved.", key))
}

func (m *model) handleMemoryDelete(ctx context.Context, key string) {
	if err := m.app.persistentMem.Delete(ctx, key); err != nil {
		m.appendErrorMessage(fmt.Sprintf("Memory delete: %v", err))
		return
	}

	m.appendSystemMessage(fmt.Sprintf("Memory entry %q deleted.", key))
}

// truncateValue returns a short preview of a memory entry value.
func truncateValue(v any) string {
	s := fmt.Sprintf("%v", v)
	if len(s) > 80 {
		return s[:80] + "..."
	}
	return s
}
