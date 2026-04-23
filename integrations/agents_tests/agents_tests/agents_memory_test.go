package agents_tests

import (
	"context"
	"testing"

	"github.com/vogo/vage/memory"
	"github.com/vogo/vv/agents"
	vvmemory "github.com/vogo/vv/memories"
)

// --- Test 9: Persistent memory loads at startup and injects into system prompt ---
// Verifies that PersistentMemoryPrompt dynamically includes entries from memory.
// Test cases:
//   - Rendered prompt includes the base prompt text
//   - Rendered prompt includes persistent memory content values
//   - Rendered prompt includes persistent memory key names
func TestIntegration_Agents_PersistentMemoryInSystemPrompt(t *testing.T) {
	dir := t.TempDir()

	// Create a FileStore and populate it with test entries.
	store, err := vvmemory.NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	persistentMem := memory.NewPersistentMemoryWithStore(store)

	ctx := context.Background()
	if err := persistentMem.Set(ctx, "project:conventions", "Use gofumpt for formatting", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Create PersistentMemoryPrompt.
	prompt := agents.NewPersistentMemoryPrompt("You are an expert coder.", persistentMem)

	rendered, err := prompt.Render(ctx, nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	// Verify the rendered prompt includes the base prompt.
	if len(rendered) < len("You are an expert coder.") {
		t.Fatal("rendered prompt is too short")
	}

	// Verify the rendered prompt includes the persistent memory content.
	if !containsString(rendered, "Use gofumpt for formatting") {
		t.Errorf("rendered prompt does not contain persistent memory content:\n%s", rendered)
	}

	if !containsString(rendered, "project:conventions") {
		t.Errorf("rendered prompt does not contain memory key 'project:conventions':\n%s", rendered)
	}
}

// --- Test 9b: PersistentMemoryPrompt returns base prompt when memory is empty ---
// Test cases:
//   - When store has no entries, rendered prompt equals base prompt exactly
func TestIntegration_Agents_PersistentMemoryEmptyStore(t *testing.T) {
	dir := t.TempDir()

	store, err := vvmemory.NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	persistentMem := memory.NewPersistentMemoryWithStore(store)
	prompt := agents.NewPersistentMemoryPrompt("Base prompt only.", persistentMem)

	rendered, err := prompt.Render(context.Background(), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if rendered != "Base prompt only." {
		t.Errorf("rendered = %q, want %q", rendered, "Base prompt only.")
	}
}

// --- Test 9c: PersistentMemoryPrompt returns base prompt when store is nil ---
// Test cases:
//   - When store is nil, rendered prompt equals base prompt exactly
func TestIntegration_Agents_PersistentMemoryNilStore(t *testing.T) {
	prompt := agents.NewPersistentMemoryPrompt("Base prompt only.", nil)

	rendered, err := prompt.Render(context.Background(), nil)
	if err != nil {
		t.Fatalf("Render: %v", err)
	}

	if rendered != "Base prompt only." {
		t.Errorf("rendered = %q, want %q", rendered, "Base prompt only.")
	}
}

// --- Test 7: FileStore CRUD integration via PersistentMemory ---
// Tests the full CRUD lifecycle through the PersistentMemory wrapper backed by FileStore.
// Test cases:
//   - Set a key-value pair and Get returns it
//   - List with prefix returns matching entries only
//   - Set entries across namespaces, List all returns all entries
//   - Delete removes a specific entry
//   - Get after Delete returns nil
//   - Clear removes all entries
func TestIntegration_Agents_FileStoreCRUDViaPersistentMemory(t *testing.T) {
	dir := t.TempDir()
	store, err := vvmemory.NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	mem := memory.NewPersistentMemoryWithStore(store)
	// Memory CRUD via the PersistentMemory wrapper is an administrative
	// (user-side) path, so Clear and writes to shared namespaces must run
	// under the user-path marker to pass session-binding checks.
	ctx := vvmemory.WithUserPath(context.Background())

	// Set a key-value pair.
	if err := mem.Set(ctx, "project:conventions", "Use gofumpt", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Get the key back.
	val, err := mem.Get(ctx, "project:conventions")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if val == nil {
		t.Fatal("Get returned nil, expected value")
	}
	if s, ok := val.(string); !ok || s != "Use gofumpt" {
		t.Errorf("Get = %v, want %q", val, "Use gofumpt")
	}

	// List with prefix.
	entries, err := mem.List(ctx, "project")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List len = %d, want 1", len(entries))
	}

	// Set another entry in a different namespace.
	if err := mem.Set(ctx, "user:preferences", "dark theme", 0); err != nil {
		t.Fatalf("Set user: %v", err)
	}

	// List all.
	allEntries, err := mem.List(ctx, "")
	if err != nil {
		t.Fatalf("List all: %v", err)
	}
	if len(allEntries) != 2 {
		t.Fatalf("List all len = %d, want 2", len(allEntries))
	}

	// Delete the first key.
	if err := mem.Delete(ctx, "project:conventions"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	// Verify it is gone.
	val, err = mem.Get(ctx, "project:conventions")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if val != nil {
		t.Errorf("Get after delete = %v, want nil", val)
	}

	// List should have only 1 entry now.
	entries, err = mem.List(ctx, "")
	if err != nil {
		t.Fatalf("List after delete: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("List after delete len = %d, want 1", len(entries))
	}

	// Clear all.
	if err := mem.Clear(ctx); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	entries, err = mem.List(ctx, "")
	if err != nil {
		t.Fatalf("List after clear: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("List after clear len = %d, want 0", len(entries))
	}
}
