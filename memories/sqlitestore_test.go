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
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"testing"
	"time"
)

// newSQLiteTestStore opens a fresh SQLiteStore rooted at t.TempDir() and
// registers a Close on test teardown.
func newSQLiteTestStore(t *testing.T, opts ...Option) *SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	s, err := NewSQLiteStore(dir, opts...)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

// rowCount counts all rows in entries (used to verify physical deletion /
// Clear semantics).
func rowCount(t *testing.T, s *SQLiteStore) int {
	t.Helper()
	var n int
	if err := s.db.QueryRow(`SELECT COUNT(*) FROM entries`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	return n
}

func TestSQLiteStore_SetAndGet(t *testing.T) {
	s := newSQLiteTestStore(t)
	ctx := context.Background()

	if err := s.Set(ctx, "project:conventions", "Use gofumpt", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	if n := rowCount(t, s); n != 1 {
		t.Fatalf("row count = %d, want 1", n)
	}

	val, found, err := s.Get(ctx, "project:conventions")
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

func TestSQLiteStore_GetNotFound(t *testing.T) {
	s := newSQLiteTestStore(t)

	_, found, err := s.Get(context.Background(), "nonexistent:key")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if found {
		t.Error("expected key to not be found")
	}
}

func TestSQLiteStore_Delete(t *testing.T) {
	s := newSQLiteTestStore(t)
	ctx := context.Background()

	_ = s.Set(ctx, "project:key", "value", 0)
	if err := s.Delete(ctx, "project:key"); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	_, found, err := s.Get(ctx, "project:key")
	if err != nil {
		t.Fatalf("Get after delete: %v", err)
	}
	if found {
		t.Error("expected key to be deleted")
	}
	if n := rowCount(t, s); n != 0 {
		t.Errorf("row count after delete = %d, want 0", n)
	}
}

func TestSQLiteStore_DeleteNonExistent(t *testing.T) {
	s := newSQLiteTestStore(t)
	// Deleting a non-existent key in a shared namespace is a no-op.
	if err := s.Delete(context.Background(), "project:missing"); err != nil {
		t.Fatalf("Delete nonexistent shared: %v", err)
	}
}

func TestSQLiteStore_List(t *testing.T) {
	s := newSQLiteTestStore(t)
	ctx := context.Background()

	_ = s.Set(ctx, "project:conventions", "gofumpt", 0)
	_ = s.Set(ctx, "project:architecture", "layered", 0)
	_ = s.Set(ctx, "user:preferences", "dark mode", 0)

	entries, err := s.List(ctx, "")
	if err != nil {
		t.Fatalf("List: %v", err)
	}
	if len(entries) != 3 {
		t.Errorf("List all: got %d entries, want 3", len(entries))
	}

	entries, err = s.List(ctx, "project")
	if err != nil {
		t.Fatalf("List with prefix: %v", err)
	}
	if len(entries) != 2 {
		t.Errorf("List project: got %d entries, want 2", len(entries))
	}

	entries, err = s.List(ctx, "nonexistent")
	if err != nil {
		t.Fatalf("List nonexistent: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("List nonexistent: got %d entries, want 0", len(entries))
	}
}

func TestSQLiteStore_Clear(t *testing.T) {
	s := newSQLiteTestStore(t)
	ctx := context.Background()

	_ = s.Set(ctx, "project:conventions", "gofumpt", 0)
	_ = s.Set(ctx, "user:preferences", "dark mode", 0)

	userCtx := WithUserPath(ctx)
	if err := s.Clear(userCtx); err != nil {
		t.Fatalf("Clear: %v", err)
	}

	entries, err := s.List(userCtx, "")
	if err != nil {
		t.Fatalf("List after clear: %v", err)
	}
	if len(entries) != 0 {
		t.Errorf("List after clear: got %d entries, want 0", len(entries))
	}
	if n := rowCount(t, s); n != 0 {
		t.Errorf("row count after clear = %d, want 0", n)
	}
}

func TestSQLiteStore_Clear_NonUserPathForbidden(t *testing.T) {
	s := newSQLiteTestStore(t)
	err := s.Clear(context.Background())
	if !errors.Is(err, ErrSessionForbidden) {
		t.Errorf("Clear without user path: err = %v, want ErrSessionForbidden", err)
	}
}

func TestSQLiteStore_UpdatePreservesCreatedAt(t *testing.T) {
	s := newSQLiteTestStore(t)
	ctx := context.Background()

	_ = s.Set(ctx, "project:key", "value1", 0)

	entries1, _ := s.List(ctx, "project:key")
	if len(entries1) == 0 {
		t.Fatal("expected entry")
	}
	createdAt1 := entries1[0].CreatedAt

	// Sleep a tick so the UPSERT's updated_at strictly differs from created_at.
	time.Sleep(2 * time.Millisecond)
	_ = s.Set(ctx, "project:key", "value2", 0)

	entries2, _ := s.List(ctx, "project:key")
	if len(entries2) == 0 {
		t.Fatal("expected entry after update")
	}
	if !entries2[0].CreatedAt.Equal(createdAt1) {
		t.Errorf("CreatedAt changed after update: %v != %v", entries2[0].CreatedAt, createdAt1)
	}

	val, _, _ := s.Get(ctx, "project:key")
	if val != "value2" {
		t.Errorf("value after update = %q, want %q", val, "value2")
	}
}

func TestSQLiteStore_DefaultNamespace(t *testing.T) {
	s := newSQLiteTestStore(t)
	ctx := context.Background()

	// Key without namespace should use "default".
	_ = s.Set(ctx, "simplekey", "value", 0)

	val, found, err := s.Get(ctx, "simplekey")
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

func TestSQLiteStore_WithSharedNamespaces_Extends(t *testing.T) {
	s := newSQLiteTestStore(t, WithSharedNamespaces("team"))
	ctx := WithUserPath(context.Background())

	if err := s.Set(ctx, "team:charter", "ship it", 0); err != nil {
		t.Fatalf("Set on configured shared namespace: %v", err)
	}
	val, found, err := s.Get(ctx, "team:charter")
	if err != nil || !found || val != "ship it" {
		t.Errorf("Get team:charter: val=%v found=%v err=%v", val, found, err)
	}
}

// --- Session-binding parity tests (mirror filestore_test.go US-1..US-7) ---

func TestSQLiteStore_UserPath_PrivateWrite_Forbidden(t *testing.T) {
	s := newSQLiteTestStore(t)

	ctx := WithUserPath(context.Background())
	err := s.Set(ctx, "scratch:x", "v", 0)
	if !errors.Is(err, ErrSessionForbidden) {
		t.Errorf("user-path Set on private ns: err = %v, want ErrSessionForbidden", err)
	}

	err = s.Delete(ctx, "scratch:x")
	if !errors.Is(err, ErrSessionForbidden) {
		t.Errorf("user-path Delete on private ns: err = %v, want ErrSessionForbidden", err)
	}
}

func TestSQLiteStore_AgentPath_PrivateRoundTrip(t *testing.T) {
	s := newSQLiteTestStore(t)

	ctxA := WithSessionID(context.Background(), "session-A")
	if err := s.Set(ctxA, "scratch:x", "v", 0); err != nil {
		t.Fatalf("Set session-A: %v", err)
	}

	// Physical isolation: the row lives at (namespace='scratch', name='x',
	// session_id='session-A').
	var sid, ns, name string
	err := s.db.QueryRow(`
		SELECT namespace, name, session_id FROM entries
		WHERE namespace = 'scratch' AND name = 'x' AND session_id = 'session-A'
	`).Scan(&ns, &name, &sid)
	if err != nil {
		t.Fatalf("expected private row (scratch, x, session-A): %v", err)
	}
	if ns != "scratch" || name != "x" || sid != "session-A" {
		t.Errorf("row = (%q, %q, %q), want (scratch, x, session-A)", ns, name, sid)
	}

	val, found, err := s.Get(ctxA, "scratch:x")
	if err != nil || !found || val != "v" {
		t.Errorf("Get session-A: val=%v found=%v err=%v", val, found, err)
	}
}

func TestSQLiteStore_AgentPath_CrossSessionRead_NotFound(t *testing.T) {
	s := newSQLiteTestStore(t)

	ctxA := WithSessionID(context.Background(), "session-A")
	_ = s.Set(ctxA, "scratch:x", "private", 0)

	ctxB := WithSessionID(context.Background(), "session-B")
	val, found, err := s.Get(ctxB, "scratch:x")
	if err != nil {
		t.Fatalf("Get session-B: %v", err)
	}
	if found {
		t.Errorf("session-B sees session-A entry: val=%v", val)
	}
}

func TestSQLiteStore_AgentPath_CrossSessionIsolation_Overwrite(t *testing.T) {
	s := newSQLiteTestStore(t)

	ctxA := WithSessionID(context.Background(), "session-A")
	_ = s.Set(ctxA, "scratch:x", "v-A", 0)

	ctxB := WithSessionID(context.Background(), "session-B")
	if err := s.Set(ctxB, "scratch:x", "v-B", 0); err != nil {
		t.Fatalf("Set session-B on its own private slot: %v", err)
	}

	// Verify A still sees A's value.
	val, found, _ := s.Get(ctxA, "scratch:x")
	if !found || val != "v-A" {
		t.Errorf("session-A value tampered: val=%v found=%v", val, found)
	}
	val, found, _ = s.Get(ctxB, "scratch:x")
	if !found || val != "v-B" {
		t.Errorf("session-B value mismatch: val=%v found=%v", val, found)
	}
}

func TestSQLiteStore_AgentPath_CrossSessionIsolation_Delete(t *testing.T) {
	s := newSQLiteTestStore(t)

	ctxA := WithSessionID(context.Background(), "session-A")
	_ = s.Set(ctxA, "scratch:x", "v", 0)

	ctxB := WithSessionID(context.Background(), "session-B")
	if err := s.Delete(ctxB, "scratch:x"); err != nil {
		t.Fatalf("session-B delete: unexpected error %v", err)
	}

	val, found, _ := s.Get(ctxA, "scratch:x")
	if !found || val != "v" {
		t.Errorf("session-A record deleted by session-B: val=%v found=%v", val, found)
	}
}

// TestSQLiteStore_AgentPath_StructuralIsolation documents the stronger
// invariant the SQL schema provides: the per-session file-planting attack
// FileStore defends against is unreachable here because the PK
// (namespace, name, session_id) physically separates each session's slot.
// Session-B's Set can only land at session_id='session-B'; it cannot possibly
// overwrite a session_id='session-A' row.
func TestSQLiteStore_AgentPath_StructuralIsolation(t *testing.T) {
	s := newSQLiteTestStore(t)

	ctxA := WithSessionID(context.Background(), "session-A")
	if err := s.Set(ctxA, "scratch:x", "owned-by-A", 0); err != nil {
		t.Fatalf("Set session-A: %v", err)
	}

	ctxB := WithSessionID(context.Background(), "session-B")
	if err := s.Set(ctxB, "scratch:x", "new-val", 0); err != nil {
		t.Fatalf("Set session-B on its own slot: %v", err)
	}

	// A's row must be intact.
	var val, rowSid string
	err := s.db.QueryRow(`
		SELECT value, session_id FROM entries
		WHERE namespace = 'scratch' AND name = 'x' AND session_id = 'session-A'
	`).Scan(&val, &rowSid)
	if err != nil {
		t.Fatalf("expected A's row to exist: %v", err)
	}
	if val != "owned-by-A" || rowSid != "session-A" {
		t.Errorf("A's row tampered: val=%q sid=%q", val, rowSid)
	}

	// And there should be exactly two distinct rows (A and B) at that key.
	var n int
	if err := s.db.QueryRow(`
		SELECT COUNT(*) FROM entries WHERE namespace = 'scratch' AND name = 'x'
	`).Scan(&n); err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 2 {
		t.Errorf("row count for scratch:x = %d, want 2 (one per session)", n)
	}
}

func TestSQLiteStore_Delete_NoSessionOnPrivate_Forbidden(t *testing.T) {
	s := newSQLiteTestStore(t)
	err := s.Delete(context.Background(), "scratch:x")
	if !errors.Is(err, ErrSessionForbidden) {
		t.Errorf("Delete without session on private ns: err = %v, want ErrSessionForbidden", err)
	}
}

func TestSQLiteStore_List_FiltersBySession(t *testing.T) {
	s := newSQLiteTestStore(t)

	ctxA := WithSessionID(context.Background(), "session-A")
	ctxB := WithSessionID(context.Background(), "session-B")
	userCtx := WithUserPath(context.Background())

	_ = s.Set(ctxA, "scratch:x", "x-A", 0)
	_ = s.Set(ctxB, "scratch:y", "y-B", 0)
	_ = s.Set(userCtx, "project:p", "shared", 0)

	// Session A sees its own private entry + shared entry.
	entries, _ := s.List(ctxA, "")
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
	entries, _ = s.List(ctxB, "")
	keys = keySet(entries)
	if !keys["scratch:y"] || !keys["project:p"] || keys["scratch:x"] {
		t.Errorf("session-B keys = %v; expected scratch:y + project:p only", keys)
	}

	// User path sees only shared.
	entries, _ = s.List(userCtx, "")
	keys = keySet(entries)
	if !keys["project:p"] || keys["scratch:x"] || keys["scratch:y"] {
		t.Errorf("user-path keys = %v; expected project:p only", keys)
	}
}

func TestSQLiteStore_Set_NoSessionOnPrivate_Forbidden(t *testing.T) {
	s := newSQLiteTestStore(t)
	err := s.Set(context.Background(), "scratch:x", "v", 0)
	if !errors.Is(err, ErrSessionForbidden) {
		t.Errorf("Set without session on private ns: err = %v, want ErrSessionForbidden", err)
	}
}

func TestSQLiteStore_Legacy_PrivateNs_Readable(t *testing.T) {
	s := newSQLiteTestStore(t)

	// Simulate a legacy record by inserting directly with session_id=''
	// (what a pre-binding vv would have written).
	now := time.Now().UnixNano()
	if _, err := s.db.Exec(`
		INSERT INTO entries (namespace, name, session_id, key, value, created_at, updated_at, ttl)
		VALUES ('scratch', 'x', '', 'scratch:x', 'legacy-value', ?, ?, 0)
	`, now, now); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}

	// Agent path (any session) should be able to read the legacy entry.
	ctxA := WithSessionID(context.Background(), "session-A")
	val, found, err := s.Get(ctxA, "scratch:x")
	if err != nil || !found || val != "legacy-value" {
		t.Errorf("Get legacy: val=%v found=%v err=%v", val, found, err)
	}
}

func TestSQLiteStore_Legacy_PrivateNs_OverwriteBlocked(t *testing.T) {
	s := newSQLiteTestStore(t)

	now := time.Now().UnixNano()
	if _, err := s.db.Exec(`
		INSERT INTO entries (namespace, name, session_id, key, value, created_at, updated_at, ttl)
		VALUES ('scratch', 'x', '', 'scratch:x', 'legacy-value', ?, ?, 0)
	`, now, now); err != nil {
		t.Fatalf("seed legacy: %v", err)
	}

	// A session-bound write on the same (ns, name) must be rejected to
	// prevent accidentally stomping a legacy record.
	ctx := WithSessionID(context.Background(), "session-A")
	err := s.Set(ctx, "scratch:x", "new", 0)
	if !errors.Is(err, ErrSessionForbidden) {
		t.Errorf("legacy overwrite: err = %v, want ErrSessionForbidden", err)
	}
}

func TestSQLiteStore_Shared_UserPath_WriteAndSessionFree(t *testing.T) {
	s := newSQLiteTestStore(t)

	ctx := WithUserPath(context.Background())
	if err := s.Set(ctx, "project:arch", "layered", 0); err != nil {
		t.Fatalf("user Set shared: %v", err)
	}

	// Any session can read it.
	ctxA := WithSessionID(context.Background(), "session-A")
	val, found, _ := s.Get(ctxA, "project:arch")
	if !found || val != "layered" {
		t.Errorf("shared Get from session: val=%v found=%v", val, found)
	}

	// Stored session_id must be empty for shared writes.
	var rowSid string
	if err := s.db.QueryRow(`
		SELECT session_id FROM entries WHERE namespace = 'project' AND name = 'arch'
	`).Scan(&rowSid); err != nil {
		t.Fatalf("read stored row: %v", err)
	}
	if rowSid != "" {
		t.Errorf("shared record tagged with session_id=%q", rowSid)
	}
}

// --- SQLite-specific tests ---

func TestSQLiteStore_WALEnabled(t *testing.T) {
	s := newSQLiteTestStore(t)

	var mode string
	if err := s.db.QueryRow(`PRAGMA journal_mode`).Scan(&mode); err != nil {
		t.Fatalf("PRAGMA journal_mode: %v", err)
	}
	if mode != "wal" {
		t.Errorf("journal_mode = %q, want wal", mode)
	}

	var sync int
	if err := s.db.QueryRow(`PRAGMA synchronous`).Scan(&sync); err != nil {
		t.Fatalf("PRAGMA synchronous: %v", err)
	}
	if sync != 1 {
		t.Errorf("synchronous = %d, want 1 (NORMAL)", sync)
	}

	var busy int
	if err := s.db.QueryRow(`PRAGMA busy_timeout`).Scan(&busy); err != nil {
		t.Fatalf("PRAGMA busy_timeout: %v", err)
	}
	if busy != 5000 {
		t.Errorf("busy_timeout = %d, want 5000", busy)
	}
}

func TestSQLiteStore_UserVersion(t *testing.T) {
	s := newSQLiteTestStore(t)

	var v int
	if err := s.db.QueryRow(`PRAGMA user_version`).Scan(&v); err != nil {
		t.Fatalf("PRAGMA user_version: %v", err)
	}
	if v != sqliteSchemaVersion {
		t.Errorf("user_version = %d, want %d", v, sqliteSchemaVersion)
	}
}

func TestSQLiteStore_ConcurrentWrites(t *testing.T) {
	s := newSQLiteTestStore(t)

	const (
		sessions  = 4
		perWriter = 50
	)

	var wg sync.WaitGroup
	errs := make(chan error, sessions*perWriter)

	for i := range sessions {
		sid := "session-" + string(rune('A'+i))
		ctx := WithSessionID(context.Background(), sid)
		wg.Go(func() {
			for j := range perWriter {
				key := "scratch:k" + string(rune('0'+j/10)) + string(rune('0'+j%10))
				if err := s.Set(ctx, key, "v", 0); err != nil {
					errs <- err
					return
				}
			}
		})
	}

	wg.Wait()
	close(errs)
	for err := range errs {
		t.Errorf("concurrent Set: %v", err)
	}

	if n := rowCount(t, s); n != sessions*perWriter {
		t.Errorf("row count = %d, want %d", n, sessions*perWriter)
	}
}

func TestSQLiteStore_FailFast_OnCorruptDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, sqliteDBFilename)
	// Seed the path with non-SQLite garbage.
	if err := os.WriteFile(dbPath, []byte("not a sqlite database"), 0o600); err != nil {
		t.Fatalf("seed garbage: %v", err)
	}

	if _, err := NewSQLiteStore(dir); err == nil {
		t.Fatal("expected NewSQLiteStore to fail on corrupt DB; got nil")
	}
}

func TestSQLiteStore_TTLExpiry_DeletesRow(t *testing.T) {
	s := newSQLiteTestStore(t)
	ctx := context.Background()

	// TTL = 1 second.
	if err := s.Set(ctx, "project:ephemeral", "v", 1); err != nil {
		t.Fatalf("Set: %v", err)
	}
	// Wait past the TTL window.
	time.Sleep(1200 * time.Millisecond)

	_, found, err := s.Get(ctx, "project:ephemeral")
	if err != nil {
		t.Fatalf("Get after expiry: %v", err)
	}
	if found {
		t.Error("expected TTL-expired entry to be gone on Get")
	}

	if n := rowCount(t, s); n != 0 {
		t.Errorf("row count after TTL expiry = %d, want 0 (physically deleted)", n)
	}
}

func TestSQLiteStore_Close_Idempotent(t *testing.T) {
	s := newSQLiteTestStore(t)
	if err := s.Close(); err != nil {
		t.Fatalf("first Close: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("second Close: %v", err)
	}
}

func TestSQLiteStore_ReopenPersists(t *testing.T) {
	dir := t.TempDir()

	s1, err := NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore (first): %v", err)
	}
	if err := s1.Set(context.Background(), "project:k", "v", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}
	if err := s1.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	s2, err := NewSQLiteStore(dir)
	if err != nil {
		t.Fatalf("NewSQLiteStore (reopen): %v", err)
	}
	defer func() { _ = s2.Close() }()

	val, found, err := s2.Get(context.Background(), "project:k")
	if err != nil || !found || val != "v" {
		t.Errorf("reopen Get: val=%v found=%v err=%v", val, found, err)
	}
}

// TestSQLiteStore_FilePermissions verifies AC-1.1: the DB, -wal, and -shm
// sidecar files must all be 0o600, and the parent directory must be 0o700.
// Skipped on Windows where POSIX permission bits are not meaningful.
func TestSQLiteStore_FilePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX file permissions not meaningful on Windows")
	}

	dir := t.TempDir()
	// The tempdir on macOS/Linux typically comes back at 0o700 already, but
	// tighten to guarantee the store's MkdirAll(0o700) is exercised.
	if err := os.Chmod(dir, 0o755); err != nil {
		t.Fatalf("chmod parent dir: %v", err)
	}
	// NewSQLiteStore calls MkdirAll with 0o700, which does NOT change an
	// already-existing dir's mode. So we exercise the "new child directory"
	// branch by asking the store to create a fresh subdir.
	subDir := filepath.Join(dir, "memory")

	s, err := NewSQLiteStore(subDir)
	if err != nil {
		t.Fatalf("NewSQLiteStore: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })

	// Trigger at least one WAL write so the -wal / -shm sidecars exist.
	ctx := WithSessionID(context.Background(), "session-A")
	if err := s.Set(ctx, "scratch:x", "v", 0); err != nil {
		t.Fatalf("Set: %v", err)
	}

	// Parent directory must be 0o700.
	info, err := os.Stat(subDir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if mode := info.Mode().Perm(); mode != 0o700 {
		t.Errorf("parent dir mode = %#o, want 0o700", mode)
	}

	// Each of the three DB files must be 0o600.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		path := filepath.Join(subDir, sqliteDBFilename+suffix)
		fi, err := os.Stat(path)
		if err != nil {
			// -wal / -shm may not exist if checkpoint already folded them
			// back into the main file; only -wal / -shm are skippable.
			if suffix == "" {
				t.Fatalf("stat %s: %v", path, err)
			}
			t.Logf("sidecar %s not present (may be checkpointed): %v", path, err)
			continue
		}
		if mode := fi.Mode().Perm(); mode != 0o600 {
			t.Errorf("%s mode = %#o, want 0o600", path, mode)
		}
	}
}
