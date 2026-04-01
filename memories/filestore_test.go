package memories

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestFileStore_SetAndGet(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	ctx := context.Background()

	if err := store.Set(ctx, "project:conventions", "Use gofumpt", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Verify file was created on disk.
	fp := filepath.Join(dir, "project", "conventions.json")
	if _, err := os.Stat(fp); os.IsNotExist(err) {
		t.Errorf("expected file %s to exist", fp)
	}

	val, found, err := store.Get(ctx, "project:conventions")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !found {
		t.Fatal("expected key to be found")
	}
	if val != "Use gofumpt" {
		t.Errorf("value = %q, want %q", val, "Use gofumpt")
	}
}

func TestFileStore_GetNotFound(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	_, found, err := store.Get(context.Background(), "nonexistent:key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if found {
		t.Error("expected key to not be found")
	}
}

func TestFileStore_Delete(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	ctx := context.Background()
	_ = store.Set(ctx, "project:key", "value", 0)

	if err := store.Delete(ctx, "project:key"); err != nil {
		t.Fatalf("Delete: %v", err)
	}

	_, found, err := store.Get(ctx, "project:key")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if found {
		t.Error("expected key to be deleted")
	}
}

func TestFileStore_DeleteNonExistent(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	// Should not error on non-existent key.
	if err := store.Delete(context.Background(), "nonexistent:key"); err != nil {
		t.Fatalf("Delete nonexistent: %v", err)
	}
}

func TestFileStore_List(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	ctx := context.Background()
	_ = store.Set(ctx, "project:conventions", "gofumpt", 0)
	_ = store.Set(ctx, "project:architecture", "layered", 0)
	_ = store.Set(ctx, "user:preferences", "dark mode", 0)

	// List all.
	entries, err := store.List(ctx, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("List all: got %d entries, want 3", len(entries))
	}

	// List with prefix.
	entries, err = store.List(ctx, "project")
	if err != nil {
		t.Fatalf("List with prefix: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("List project: got %d entries, want 2", len(entries))
	}

	// List with prefix that matches nothing.
	entries, err = store.List(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("List nonexistent: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("List nonexistent: got %d entries, want 0", len(entries))
	}
}

func TestFileStore_Clear(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	ctx := context.Background()
	_ = store.Set(ctx, "project:conventions", "gofumpt", 0)
	_ = store.Set(ctx, "user:preferences", "dark mode", 0)

	if err := store.Clear(ctx); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	entries, err := store.List(ctx, "")
	if err != nil {
		t.Fatalf("List after clear: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("List after clear: got %d entries, want 0", len(entries))
	}
}

func TestFileStore_UpdatePreservesCreatedAt(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	ctx := context.Background()
	_ = store.Set(ctx, "project:key", "value1", 0)

	entries1, _ := store.List(ctx, "project:key")
	if len(entries1) == 0 {
		t.Fatal("expected entry")
	}
	createdAt1 := entries1[0].CreatedAt

	// Update the same key.
	_ = store.Set(ctx, "project:key", "value2", 0)

	entries2, _ := store.List(ctx, "project:key")
	if len(entries2) == 0 {
		t.Fatal("expected entry after update")
	}

	if !entries2[0].CreatedAt.Equal(createdAt1) {
		t.Errorf("CreatedAt changed after update: %v != %v", entries2[0].CreatedAt, createdAt1)
	}

	val, _, _ := store.Get(ctx, "project:key")
	if val != "value2" {
		t.Errorf("value after update = %q, want %q", val, "value2")
	}
}

func TestFileStore_DefaultNamespace(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	ctx := context.Background()
	// Key without namespace should use "default".
	_ = store.Set(ctx, "simplekey", "value", 0)

	val, found, err := store.Get(ctx, "simplekey")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !found {
		t.Fatal("expected key to be found")
	}
	if val != "value" {
		t.Errorf("value = %q, want %q", val, "value")
	}
}

func TestParseKey(t *testing.T) {
	tests := []struct {
		key      string
		wantNS   string
		wantName string
	}{
		{"project:conventions", "project", "conventions"},
		{"user:preferences", "user", "preferences"},
		{"simplekey", "default", "simplekey"},
		{"a:b:c", "a", "b:c"},
	}

	for _, tt := range tests {
		ns, name := parseKey(tt.key)
		if ns != tt.wantNS || name != tt.wantName {
			t.Errorf("parseKey(%q) = (%q, %q), want (%q, %q)", tt.key, ns, name, tt.wantNS, tt.wantName)
		}
	}
}

func TestSanitize(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"simple", "simple"},
		{"with spaces", "with_spaces"},
		{"path/slash", "path_slash"},
		{"special@#$", "special___"},
		{"dots.ok", "dots.ok"},
		{"dashes-ok", "dashes-ok"},
		{"under_ok", "under_ok"},
	}

	for _, tt := range tests {
		got := sanitize(tt.input)
		if got != tt.want {
			t.Errorf("sanitize(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}
