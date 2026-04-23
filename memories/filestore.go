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
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vogo/vage/memory"
)

// sessionDirName is the subdirectory under the store root that holds
// session-private records, laid out as <dir>/<sessionDirName>/<sid>/<ns>__<key>.json.
const sessionDirName = "session"

// FileStore persists key-value pairs as individual JSON files in a directory.
// Keys are sanitized to safe filenames. The store is not safe for concurrent
// use; wrap with PersistentMemory (which uses syncMemory) for thread safety.
type FileStore struct {
	dir         string
	extraShared map[string]struct{}
}

// fileRecord is the JSON-serialized format of a stored entry.
type fileRecord struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	Namespace string    `json:"namespace"`
	SessionID string    `json:"session_id,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	TTL       int64     `json:"ttl"`
}

// Compile-time check: FileStore implements memory.Store.
var _ memory.Store = (*FileStore)(nil)

// Option configures a memory Store. It is applied in both FileStore and
// SQLiteStore constructors so backend-swap is a config flip rather than a
// code change.
type Option interface {
	applyFile(*FileStore)
	applySQLite(*SQLiteStore)
}

// sharedNamespacesOpt carries the extra-shared namespace names.
type sharedNamespacesOpt struct {
	names []string
}

func (o sharedNamespacesOpt) applyFile(s *FileStore) {
	if s.extraShared == nil {
		s.extraShared = make(map[string]struct{}, len(o.names))
	}
	for _, n := range o.names {
		if n != "" {
			s.extraShared[n] = struct{}{}
		}
	}
}

func (o sharedNamespacesOpt) applySQLite(s *SQLiteStore) {
	if s.extraShared == nil {
		s.extraShared = make(map[string]struct{}, len(o.names))
	}
	for _, n := range o.names {
		if n != "" {
			s.extraShared[n] = struct{}{}
		}
	}
}

// WithSharedNamespaces adds extra namespace names to the shared allowlist.
// Entries in these namespaces are treated as cross-session; writes from any
// context path skip session_id binding.
func WithSharedNamespaces(names ...string) Option {
	return sharedNamespacesOpt{names: names}
}

// NewFileStore creates a new FileStore rooted at dir.
// The directory is created if it does not exist.
func NewFileStore(dir string, opts ...Option) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("filestore: create directory %s: %w", dir, err)
	}
	s := &FileStore{dir: dir}
	for _, opt := range opts {
		opt.applyFile(s)
	}
	return s, nil
}

// encodeValue normalises a caller-supplied value to the stored TEXT form.
// Strings pass through verbatim; everything else is JSON-marshalled. Used by
// both FileStore and SQLiteStore so the on-disk representation stays
// identical across backends.
func encodeValue(value any) (string, error) {
	if s, ok := value.(string); ok {
		return s, nil
	}
	b, err := json.Marshal(value)
	if err != nil {
		return "", fmt.Errorf("memories: marshal value: %w", err)
	}
	return string(b), nil
}

// parseKey splits a key into namespace and name.
// Format: "namespace:key" -> ("namespace", "key").
// If no colon, namespace is "default".
func parseKey(key string) (namespace, name string) {
	if before, after, ok := strings.Cut(key, ":"); ok {
		return before, after
	}
	return "default", key
}

// sanitize replaces characters that are unsafe in filenames.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			return r
		default:
			return '_'
		}
	}, s)
}

// sharedPath returns the filesystem path for an entry in a shared namespace.
func (s *FileStore) sharedPath(ns, name string) string {
	return filepath.Join(s.dir, sanitize(ns), sanitize(name)+".json")
}

// privatePath returns the filesystem path for a session-private entry.
func (s *FileStore) privatePath(sid, ns, name string) string {
	fname := sanitize(ns) + "__" + sanitize(name) + ".json"
	return filepath.Join(s.dir, sessionDirName, sanitize(sid), fname)
}

// resolveReadPath picks the path to read for the given (ns, name, sid).
// For shared namespaces, returns the shared path. For private namespaces,
// prefers the per-session private path; falls back to the legacy shared
// layout so entries written before session binding was introduced remain
// readable.
func (s *FileStore) resolveReadPath(ns, name, sid string, shared bool) (path string, legacy bool) {
	if shared {
		return s.sharedPath(ns, name), false
	}
	if sid != "" {
		p := s.privatePath(sid, ns, name)
		if _, err := os.Stat(p); err == nil {
			return p, false
		}
	}
	return s.sharedPath(ns, name), true
}

func (s *FileStore) Get(ctx context.Context, key string) (any, bool, error) {
	sid := SessionIDFrom(ctx)
	isUser := IsUserPath(ctx)
	ns, name := parseKey(key)
	shared := isShared(ns, s.extraShared)

	fp, legacyFallback := s.resolveReadPath(ns, name, sid, shared)

	data, err := os.ReadFile(fp)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("filestore: read %s: %w", key, err)
	}

	var rec fileRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, false, fmt.Errorf("filestore: unmarshal %s: %w", key, err)
	}

	if !shared {
		if rec.SessionID != "" {
			if isUser || rec.SessionID != sid {
				slog.Warn("memories: session mismatch on get",
					"namespace", ns, "key", name,
					"expected_session", rec.SessionID,
					"got_session", sid, "user_path", isUser)
				return nil, false, nil
			}
		}
		// rec.SessionID == "" in a private namespace means a legacy entry;
		// we allow the read. legacyFallback just records that it came from
		// the shared-layout path for diagnostics.
		_ = legacyFallback
	}

	if rec.TTL > 0 && time.Since(rec.UpdatedAt) > time.Duration(rec.TTL)*time.Second {
		_ = os.Remove(fp)
		return nil, false, nil
	}

	return rec.Value, true, nil
}

func (s *FileStore) Set(ctx context.Context, key string, value any, ttl int64) error {
	sid := SessionIDFrom(ctx)
	isUser := IsUserPath(ctx)
	ns, name := parseKey(key)
	shared := isShared(ns, s.extraShared)

	if !shared {
		if isUser {
			return fmt.Errorf("%w: user path cannot write private namespace %q",
				ErrSessionForbidden, ns)
		}
		if sid == "" {
			return fmt.Errorf("%w: no session_id for private namespace %q",
				ErrSessionForbidden, ns)
		}
	}

	var fp string
	if shared {
		fp = s.sharedPath(ns, name)
	} else {
		fp = s.privatePath(sid, ns, name)
	}

	if err := os.MkdirAll(filepath.Dir(fp), 0o700); err != nil {
		return fmt.Errorf("filestore: create record dir: %w", err)
	}

	now := time.Now()
	createdAt := now

	// Read existing for createdAt preservation and cross-session overwrite guard.
	if existing, err := os.ReadFile(fp); err == nil {
		var old fileRecord
		if json.Unmarshal(existing, &old) == nil {
			createdAt = old.CreatedAt
			if !shared && old.SessionID != "" && old.SessionID != sid {
				return fmt.Errorf("%w: overwrite blocked (owner=%s, caller=%s)",
					ErrSessionForbidden, old.SessionID, sid)
			}
		}
	}

	// Also guard against a legacy record sitting in the shared layout path
	// for a private namespace: block overwrite from any session.
	if !shared {
		legacy := s.sharedPath(ns, name)
		if legacy != fp {
			if existing, err := os.ReadFile(legacy); err == nil {
				var old fileRecord
				if json.Unmarshal(existing, &old) == nil && old.SessionID == "" {
					return fmt.Errorf("%w: legacy entry in private namespace %q cannot be overwritten",
						ErrSessionForbidden, ns)
				}
			}
		}
	}

	strValue, err := encodeValue(value)
	if err != nil {
		return err
	}

	rec := fileRecord{
		Key:       key,
		Value:     strValue,
		Namespace: ns,
		SessionID: recordSessionID(shared, sid),
		CreatedAt: createdAt,
		UpdatedAt: now,
		TTL:       ttl,
	}

	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("filestore: marshal record: %w", err)
	}

	if err := os.WriteFile(fp, data, 0o600); err != nil {
		return fmt.Errorf("filestore: write %s: %w", key, err)
	}

	return nil
}

// recordSessionID returns the SessionID to stamp on a record: empty for
// shared or user-path writes, the caller sid for private writes.
func recordSessionID(shared bool, sid string) string {
	if shared {
		return ""
	}
	return sid
}

func (s *FileStore) Delete(ctx context.Context, key string) error {
	sid := SessionIDFrom(ctx)
	isUser := IsUserPath(ctx)
	ns, name := parseKey(key)
	shared := isShared(ns, s.extraShared)

	if !shared {
		if isUser {
			return fmt.Errorf("%w: user path cannot delete private namespace %q",
				ErrSessionForbidden, ns)
		}
		if sid == "" {
			// Symmetric with Set: a non-user caller with no session identity
			// cannot address a private-namespace entry for mutation.
			return fmt.Errorf("%w: no session_id for private namespace %q",
				ErrSessionForbidden, ns)
		}
	}

	fp, _ := s.resolveReadPath(ns, name, sid, shared)

	if !shared {
		data, err := os.ReadFile(fp)
		if err != nil {
			if os.IsNotExist(err) {
				return nil
			}
			return fmt.Errorf("filestore: read for delete %s: %w", key, err)
		}
		var rec fileRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			return fmt.Errorf("filestore: unmarshal for delete %s: %w", key, err)
		}
		if rec.SessionID != "" && rec.SessionID != sid {
			return fmt.Errorf("%w: delete blocked (owner=%s, caller=%s)",
				ErrSessionForbidden, rec.SessionID, sid)
		}
		// rec.SessionID == "" in a private namespace = legacy shared;
		// deletion is permitted here because the up-front guard already
		// rejected the (!shared && sid == "" && !isUser) case.
	}

	if err := os.Remove(fp); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("filestore: delete %s: %w", key, err)
	}
	return nil
}

func (s *FileStore) List(ctx context.Context, prefix string) ([]memory.StoreEntry, error) {
	sid := SessionIDFrom(ctx)
	isUser := IsUserPath(ctx)
	var entries []memory.StoreEntry

	rootEntries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return entries, nil
		}
		return nil, fmt.Errorf("filestore: read dir: %w", err)
	}

	for _, topEntry := range rootEntries {
		if !topEntry.IsDir() {
			continue
		}
		name := topEntry.Name()

		if name == sessionDirName {
			// Session-private area: only walk the caller's own sid.
			if isUser || sid == "" {
				continue
			}
			sidDir := filepath.Join(s.dir, sessionDirName, sanitize(sid))
			items, err := s.readPrivateDir(sidDir, sid, prefix)
			if err != nil {
				return nil, err
			}
			entries = append(entries, items...)
			continue
		}

		// Shared / legacy namespace directory.
		nsPath := filepath.Join(s.dir, name)
		items, err := s.readSharedDir(nsPath, sid, isUser, prefix)
		if err != nil {
			return nil, err
		}
		entries = append(entries, items...)
	}

	return entries, nil
}

// readPrivateDir lists session-private records belonging to sid.
func (s *FileStore) readPrivateDir(sidDir, sid, prefix string) ([]memory.StoreEntry, error) {
	files, err := os.ReadDir(sidDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("filestore: read session dir: %w", err)
	}

	var out []memory.StoreEntry
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(sidDir, f.Name()))
		if err != nil {
			continue
		}
		var rec fileRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			continue
		}
		// Defensive: drop anything whose recorded session_id doesn't match.
		if rec.SessionID != sid {
			continue
		}
		if rec.TTL > 0 && time.Since(rec.UpdatedAt) > time.Duration(rec.TTL)*time.Second {
			_ = os.Remove(filepath.Join(sidDir, f.Name()))
			continue
		}
		if prefix != "" && !strings.HasPrefix(rec.Key, prefix) {
			continue
		}
		out = append(out, memory.StoreEntry{
			Key:       rec.Key,
			Value:     rec.Value,
			CreatedAt: rec.CreatedAt,
			TTL:       rec.TTL,
		})
	}
	return out, nil
}

// readSharedDir lists entries in a shared (or legacy) namespace directory.
// For a legacy directory (ns not in the shared allowlist), only records
// with matching session_id (or empty session_id = legacy shared) are
// returned; user-path callers see only the empty-sid entries.
func (s *FileStore) readSharedDir(nsDir, sid string, isUser bool, prefix string) ([]memory.StoreEntry, error) {
	files, err := os.ReadDir(nsDir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("filestore: read ns dir %s: %w", nsDir, err)
	}

	ns := filepath.Base(nsDir)
	shared := isShared(ns, s.extraShared)

	var out []memory.StoreEntry
	for _, f := range files {
		if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
			continue
		}
		data, err := os.ReadFile(filepath.Join(nsDir, f.Name()))
		if err != nil {
			continue
		}
		var rec fileRecord
		if err := json.Unmarshal(data, &rec); err != nil {
			continue
		}

		if !shared {
			// Legacy entry in a private namespace path.
			// Only expose to matching session or empty-sid (legacy shared).
			if rec.SessionID != "" && rec.SessionID != sid {
				continue
			}
			if rec.SessionID == "" {
				// legacy shared entry — visible to user path and any sid
			} else if isUser {
				continue
			}
		}

		if rec.TTL > 0 && time.Since(rec.UpdatedAt) > time.Duration(rec.TTL)*time.Second {
			_ = os.Remove(filepath.Join(nsDir, f.Name()))
			continue
		}
		if prefix != "" && !strings.HasPrefix(rec.Key, prefix) {
			continue
		}
		out = append(out, memory.StoreEntry{
			Key:       rec.Key,
			Value:     rec.Value,
			CreatedAt: rec.CreatedAt,
			TTL:       rec.TTL,
		})
	}
	return out, nil
}

func (s *FileStore) Clear(ctx context.Context) error {
	if !IsUserPath(ctx) {
		return fmt.Errorf("%w: Clear is restricted to user path", ErrSessionForbidden)
	}

	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("filestore: read dir for clear: %w", err)
	}

	for _, entry := range entries {
		p := filepath.Join(s.dir, entry.Name())
		if err := os.RemoveAll(p); err != nil {
			return fmt.Errorf("filestore: clear %s: %w", p, err)
		}
	}

	return nil
}
