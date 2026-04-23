/*
 * Licensed to the Apache Software Foundation (ASF) under one or more
 * contributor license agreements.  See the NOTICE file distributed with
 * this work for additional information regarding copyright ownership.
 * The ASF licenses this file to You under the Apache License, Version 2.0
 * (the "License"); you may not use this file except in compliance with
 * the License.  You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package memories

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/vogo/vage/memory"
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

	// Deleting a non-existent key in a shared namespace is a no-op.
	if err := store.Delete(context.Background(), "project:missing"); err != nil {
		t.Fatalf("Delete nonexistent shared: %v", err)
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

	// Clear is user-path-only.
	userCtx := WithUserPath(ctx)
	if err := store.Clear(userCtx); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	entries, err := store.List(userCtx, "")
	if err != nil {
		t.Fatalf("List after clear: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("List after clear: got %d entries, want 0", len(entries))
	}
}

func TestFileStore_Clear_NonUserPathForbidden(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir)
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	err = store.Clear(context.Background())
	if !errors.Is(err, ErrSessionForbidden) {
		t.Errorf("Clear without user path: err = %v, want ErrSessionForbidden", err)
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

func TestIsSharedNamespace(t *testing.T) {
	cases := []struct {
		ns   string
		want bool
	}{
		{"project", true},
		{"user", true},
		{"conventions", true},
		{"notes", true},
		{"default", true},
		{"session", false},
		{"scratch", false},
		{"ephemeral", false},
	}
	for _, c := range cases {
		if got := IsSharedNamespace(c.ns); got != c.want {
			t.Errorf("IsSharedNamespace(%q) = %v, want %v", c.ns, got, c.want)
		}
	}
}

func TestFileStore_WithSharedNamespaces_Extends(t *testing.T) {
	dir := t.TempDir()
	store, err := NewFileStore(dir, WithSharedNamespaces("team"))
	if err != nil {
		t.Fatalf("NewFileStore: %v", err)
	}

	ctx := WithUserPath(context.Background())

	// "team" is now shared: user-path write should succeed.
	if err := store.Set(ctx, "team:charter", "ship it", 0); err != nil {
		t.Fatalf("Set on configured shared namespace: %v", err)
	}
	val, found, err := store.Get(ctx, "team:charter")
	if err != nil || !found || val != "ship it" {
		t.Errorf("Get team:charter: val=%v found=%v err=%v", val, found, err)
	}
}

// --- Session-binding tests (US-1 .. US-7) ---

func TestFileStore_UserPath_PrivateWrite_Forbidden(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewFileStore(dir)

	ctx := WithUserPath(context.Background())
	err := store.Set(ctx, "scratch:x", "v", 0)
	if !errors.Is(err, ErrSessionForbidden) {
		t.Errorf("user-path Set on private ns: err = %v, want ErrSessionForbidden", err)
	}

	err = store.Delete(ctx, "scratch:x")
	if !errors.Is(err, ErrSessionForbidden) {
		t.Errorf("user-path Delete on private ns: err = %v, want ErrSessionForbidden", err)
	}
}

func TestFileStore_AgentPath_PrivateRoundTrip(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewFileStore(dir)

	ctxA := WithSessionID(context.Background(), "session-A")
	if err := store.Set(ctxA, "scratch:x", "v", 0); err != nil {
		t.Fatalf("Set session-A: %v", err)
	}

	// Physical isolation: the file should live under <dir>/session/session-A/
	fp := filepath.Join(dir, "session", "session-A", "scratch__x.json")
	if _, err := os.Stat(fp); err != nil {
		t.Errorf("expected private record at %s: %v", fp, err)
	}

	val, found, err := store.Get(ctxA, "scratch:x")
	if err != nil || !found || val != "v" {
		t.Errorf("Get session-A: val=%v found=%v err=%v", val, found, err)
	}
}

func TestFileStore_AgentPath_CrossSessionRead_NotFound(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewFileStore(dir)

	ctxA := WithSessionID(context.Background(), "session-A")
	_ = store.Set(ctxA, "scratch:x", "private", 0)

	ctxB := WithSessionID(context.Background(), "session-B")
	val, found, err := store.Get(ctxB, "scratch:x")
	if err != nil {
		t.Fatalf("Get session-B: %v", err)
	}
	if found {
		t.Errorf("session-B sees session-A entry: val=%v", val)
	}
}

// TestFileStore_AgentPath_CrossSessionIsolation_Overwrite verifies that two
// sessions writing the same logical key land in disjoint files on disk, so
// one cannot overwrite the other's value. This is isolation via path
// separation — not the defense-in-depth SessionID-mismatch guard; see
// TestFileStore_AgentPath_DefenseInDepth_OverwriteBlocked for that.
func TestFileStore_AgentPath_CrossSessionIsolation_Overwrite(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewFileStore(dir)

	ctxA := WithSessionID(context.Background(), "session-A")
	_ = store.Set(ctxA, "scratch:x", "v-A", 0)

	ctxB := WithSessionID(context.Background(), "session-B")
	if err := store.Set(ctxB, "scratch:x", "v-B", 0); err != nil {
		t.Fatalf("Set session-B on its own private path: %v", err)
	}

	// Verify A still sees A's value.
	val, found, _ := store.Get(ctxA, "scratch:x")
	if !found || val != "v-A" {
		t.Errorf("session-A value tampered: val=%v found=%v", val, found)
	}
	val, found, _ = store.Get(ctxB, "scratch:x")
	if !found || val != "v-B" {
		t.Errorf("session-B value mismatch: val=%v found=%v", val, found)
	}
}

// TestFileStore_AgentPath_CrossSessionIsolation_Delete verifies that a
// Delete from session B with a key that only exists in session A's private
// dir is a no-op (not-found posture) and does not touch A's record.
func TestFileStore_AgentPath_CrossSessionIsolation_Delete(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewFileStore(dir)

	ctxA := WithSessionID(context.Background(), "session-A")
	_ = store.Set(ctxA, "scratch:x", "v", 0)

	ctxB := WithSessionID(context.Background(), "session-B")
	if err := store.Delete(ctxB, "scratch:x"); err != nil {
		t.Fatalf("session-B delete: unexpected error %v", err)
	}

	val, found, _ := store.Get(ctxA, "scratch:x")
	if !found || val != "v" {
		t.Errorf("session-A record deleted by session-B: val=%v found=%v", val, found)
	}
}

// TestFileStore_AgentPath_DefenseInDepth_OverwriteBlocked plants a record
// tagged with session-A at session-B's private path (simulating corruption
// or manual tampering) and verifies that B's Set returns ErrSessionForbidden
// from the SessionID-mismatch guard rather than silently overwriting.
func TestFileStore_AgentPath_DefenseInDepth_OverwriteBlocked(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewFileStore(dir)

	sidB := "session-B"
	plantedPath := store.privatePath(sidB, "scratch", "x")
	if err := os.MkdirAll(filepath.Dir(plantedPath), 0o700); err != nil {
		t.Fatalf("mkdir planted: %v", err)
	}
	now := time.Now()
	planted := fileRecord{
		Key:       "scratch:x",
		Value:     "owned-by-A",
		Namespace: "scratch",
		SessionID: "session-A",
		CreatedAt: now,
		UpdatedAt: now,
	}
	data, _ := json.MarshalIndent(planted, "", "  ")
	if err := os.WriteFile(plantedPath, data, 0o600); err != nil {
		t.Fatalf("write planted: %v", err)
	}

	// B attempts to write to the same slot — the defense-in-depth guard must
	// fire because the on-disk record is owned by a different session.
	ctxB := WithSessionID(context.Background(), sidB)
	err := store.Set(ctxB, "scratch:x", "new-val", 0)
	if !errors.Is(err, ErrSessionForbidden) {
		t.Errorf("Set over mismatched record: err = %v, want ErrSessionForbidden", err)
	}

	// A's planted value must remain intact.
	raw, readErr := os.ReadFile(plantedPath)
	if readErr != nil {
		t.Fatalf("read planted after Set: %v", readErr)
	}
	var after fileRecord
	_ = json.Unmarshal(raw, &after)
	if after.Value != "owned-by-A" || after.SessionID != "session-A" {
		t.Errorf("planted record clobbered: %+v", after)
	}
}

// TestFileStore_Delete_NoSessionOnPrivate_Forbidden verifies the symmetry
// with Set: a caller with no user-path marker and no session_id cannot
// address a private-namespace entry for deletion.
func TestFileStore_Delete_NoSessionOnPrivate_Forbidden(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewFileStore(dir)

	err := store.Delete(context.Background(), "scratch:x")
	if !errors.Is(err, ErrSessionForbidden) {
		t.Errorf("Delete without session on private ns: err = %v, want ErrSessionForbidden", err)
	}
}

func TestFileStore_List_FiltersBySession(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewFileStore(dir)

	ctxA := WithSessionID(context.Background(), "session-A")
	ctxB := WithSessionID(context.Background(), "session-B")
	userCtx := WithUserPath(context.Background())

	_ = store.Set(ctxA, "scratch:x", "x-A", 0)
	_ = store.Set(ctxB, "scratch:y", "y-B", 0)
	_ = store.Set(userCtx, "project:p", "shared", 0)

	// Session A sees its own private entry + shared entry.
	entries, _ := store.List(ctxA, "")
	if len(entries) != 2 {
		t.Errorf("session-A List: got %d entries, want 2", len(entries))
	}
	keys := keySet(entries)
	if !keys["scratch:x"] || !keys["project:p"] {
		t.Errorf("session-A keys = %v, want {scratch:x, project:p}", keys)
	}
	if keys["scratch:y"] {
		t.Errorf("session-A leaked session-B key scratch:y")
	}

	// Session B analogously.
	entries, _ = store.List(ctxB, "")
	keys = keySet(entries)
	if !keys["scratch:y"] || !keys["project:p"] || keys["scratch:x"] {
		t.Errorf("session-B keys = %v; expected scratch:y + project:p only", keys)
	}

	// User path sees only shared.
	entries, _ = store.List(userCtx, "")
	keys = keySet(entries)
	if !keys["project:p"] || keys["scratch:x"] || keys["scratch:y"] {
		t.Errorf("user-path keys = %v; expected project:p only", keys)
	}
}

func TestFileStore_Set_NoSessionOnPrivate_Forbidden(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewFileStore(dir)

	// No session_id, not user-path: private write must fail.
	err := store.Set(context.Background(), "scratch:x", "v", 0)
	if !errors.Is(err, ErrSessionForbidden) {
		t.Errorf("Set without session on private ns: err = %v, want ErrSessionForbidden", err)
	}
}

func TestFileStore_Legacy_PrivateNs_Readable(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewFileStore(dir)

	// Simulate a legacy record written before session binding existed:
	// living in <dir>/scratch/x.json with no session_id field.
	legacyDir := filepath.Join(dir, "scratch")
	if err := os.MkdirAll(legacyDir, 0o700); err != nil {
		t.Fatalf("mkdir legacy: %v", err)
	}
	now := time.Now()
	rec := fileRecord{
		Key:       "scratch:x",
		Value:     "legacy-value",
		Namespace: "scratch",
		CreatedAt: now,
		UpdatedAt: now,
	}
	data, _ := json.MarshalIndent(rec, "", "  ")
	if err := os.WriteFile(filepath.Join(legacyDir, "x.json"), data, 0o600); err != nil {
		t.Fatalf("write legacy: %v", err)
	}

	// Agent path (any session) should be able to read the legacy entry.
	ctxA := WithSessionID(context.Background(), "session-A")
	val, found, err := store.Get(ctxA, "scratch:x")
	if err != nil || !found || val != "legacy-value" {
		t.Errorf("Get legacy: val=%v found=%v err=%v", val, found, err)
	}
}

func TestFileStore_Legacy_PrivateNs_OverwriteBlocked(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewFileStore(dir)

	legacyDir := filepath.Join(dir, "scratch")
	_ = os.MkdirAll(legacyDir, 0o700)
	now := time.Now()
	rec := fileRecord{
		Key:       "scratch:x",
		Value:     "legacy-value",
		Namespace: "scratch",
		CreatedAt: now,
		UpdatedAt: now,
	}
	data, _ := json.MarshalIndent(rec, "", "  ")
	_ = os.WriteFile(filepath.Join(legacyDir, "x.json"), data, 0o600)

	// A session-bound write that would collide with the legacy record
	// must be blocked to prevent accidental stomping.
	ctx := WithSessionID(context.Background(), "session-A")
	err := store.Set(ctx, "scratch:x", "new", 0)
	if !errors.Is(err, ErrSessionForbidden) {
		t.Errorf("legacy overwrite: err = %v, want ErrSessionForbidden", err)
	}
}

func TestFileStore_Shared_UserPath_WriteAndSessionFree(t *testing.T) {
	dir := t.TempDir()
	store, _ := NewFileStore(dir)

	ctx := WithUserPath(context.Background())
	if err := store.Set(ctx, "project:arch", "layered", 0); err != nil {
		t.Fatalf("user Set shared: %v", err)
	}

	// Any session can read it.
	ctxA := WithSessionID(context.Background(), "session-A")
	val, found, _ := store.Get(ctxA, "project:arch")
	if !found || val != "layered" {
		t.Errorf("shared Get from session: val=%v found=%v", val, found)
	}

	// And the stored record must not have been tagged with any sid.
	raw, err := os.ReadFile(filepath.Join(dir, "project", "arch.json"))
	if err != nil {
		t.Fatalf("read shared file: %v", err)
	}
	var rec fileRecord
	_ = json.Unmarshal(raw, &rec)
	if rec.SessionID != "" {
		t.Errorf("shared record tagged with session_id=%q", rec.SessionID)
	}
}

// keySet turns a StoreEntry slice into a set of keys for easier assertions.
func keySet(entries []memory.StoreEntry) map[string]bool {
	out := make(map[string]bool, len(entries))
	for _, e := range entries {
		out[e.Key] = true
	}
	return out
}
