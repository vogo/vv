package agents

import (
	"context"
	"fmt"
	"strings"

	"github.com/vogo/vage/memory"
)

// PersistentMemoryPrompt is a dynamic prompt template that appends persistent
// memory entries to a base system prompt. It loads memory on each Render call
// so that changes (via /memory set) are reflected without restarting.
type PersistentMemoryPrompt struct {
	basePrompt string
	store      memory.Memory // the persistent memory instance
}

// NewPersistentMemoryPrompt creates a new PersistentMemoryPrompt.
func NewPersistentMemoryPrompt(basePrompt string, store memory.Memory) *PersistentMemoryPrompt {
	return &PersistentMemoryPrompt{
		basePrompt: basePrompt,
		store:      store,
	}
}

func (p *PersistentMemoryPrompt) Name() string    { return "persistent-memory" }
func (p *PersistentMemoryPrompt) Version() string { return "1" }

func (p *PersistentMemoryPrompt) Render(ctx context.Context, _ map[string]any) (string, error) {
	if p.store == nil {
		return p.basePrompt, nil
	}

	entries, err := p.store.List(ctx, "")
	if err != nil {
		return p.basePrompt, nil // degrade gracefully
	}

	if len(entries) == 0 {
		return p.basePrompt, nil
	}

	return p.basePrompt + "\n\n" + formatPersistentMemory(entries), nil
}

// formatPersistentMemory formats memory entries as a structured knowledge block.
func formatPersistentMemory(entries []memory.Entry) string {
	var sb strings.Builder
	sb.WriteString("## Persistent Memory\n\n")
	sb.WriteString("The following knowledge has been stored from previous sessions:\n\n")

	for _, e := range entries {
		val := ""
		switch v := e.Value.(type) {
		case string:
			val = v
		default:
			val = fmt.Sprintf("%v", v)
		}

		fmt.Fprintf(&sb, "### %s\n%s\n\n", e.Key, val)
	}

	return sb.String()
}
