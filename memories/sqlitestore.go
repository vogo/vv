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
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vogo/vage/memory"

	_ "modernc.org/sqlite" // pure-Go SQLite driver; registers "sqlite" driver.
)

// sqliteDBFilename is the fixed DB filename beneath cfg.Memory.Dir.
const sqliteDBFilename = "memory.db"

// sqliteSchemaVersion is the current schema version written to PRAGMA user_version.
// Bump whenever the schema changes; the constructor refuses to open a DB whose
// user_version exceeds this value to prevent a newer vv from silently
// downgrading on-disk data.
const sqliteSchemaVersion = 1

// sqliteSchemaV1 creates the single entries table used by SQLiteStore.
// Compound PRIMARY KEY (namespace, name, session_id) gives us fast point
// lookups and natural physical isolation between session-private records:
// session-A and session-B land in distinct PK slots by construction, so the
// file-system "planted record at another session's path" attack is
// structurally unreachable from the SQL API.
//
// WITHOUT ROWID keeps row layout compact since the PK is always present.
const sqliteSchemaV1 = `
CREATE TABLE IF NOT EXISTS entries (
    namespace  TEXT    NOT NULL,
    name       TEXT    NOT NULL,
    session_id TEXT    NOT NULL DEFAULT '',
    key        TEXT    NOT NULL,
    value      TEXT    NOT NULL,
    created_at INTEGER NOT NULL,
    updated_at INTEGER NOT NULL,
    ttl        INTEGER NOT NULL DEFAULT 0,
    PRIMARY KEY (namespace, name, session_id)
) WITHOUT ROWID;
`

// SQLiteStore persists key-value pairs in a single SQLite database file with
// WAL journalling. It implements memory.Store with the same externally
// observable semantics as FileStore, including session-private isolation,
// legacy-record fallback, and lazy-on-read TTL expiry.
type SQLiteStore struct {
	db          *sql.DB
	dbPath      string
	extraShared map[string]struct{}
}

// Compile-time check: SQLiteStore implements memory.Store.
var _ memory.Store = (*SQLiteStore)(nil)

// NewSQLiteStore opens (or creates) the DB at <dir>/memory.db with WAL mode,
// applies the schema if missing, and returns a store ready for use. The
// parent directory is created at mode 0o700 if it does not exist.
func NewSQLiteStore(dir string, opts ...Option) (*SQLiteStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("sqlitestore: create directory %s: %w", dir, err)
	}

	dbPath := filepath.Join(dir, sqliteDBFilename)
	dsn := buildSQLiteDSN(dbPath)

	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("sqlitestore: open %s: %w", dbPath, err)
	}

	// Bound the pool: WAL permits concurrent readers, and the single-writer
	// serialization is handled by busy_timeout (set in the DSN) queueing
	// colliding writers. 8/4 tracks the defaults used by Ollama / Bluesky.
	db.SetMaxOpenConns(8)
	db.SetMaxIdleConns(4)
	db.SetConnMaxIdleTime(5 * time.Minute)

	// Fail-fast: surface open errors before callers construct agents that
	// would otherwise nil-panic deep inside PersistentMemoryWithStore.
	if err := db.PingContext(context.Background()); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlitestore: ping %s: %w", dbPath, err)
	}

	if err := ensureSQLiteSchema(db); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlitestore: schema %s: %w", dbPath, err)
	}

	// Probe a real query against the entries table so DSN PRAGMAs that apply
	// lazily have fired at least once on the pool.
	if _, err := db.ExecContext(context.Background(), "SELECT 1 FROM entries LIMIT 0"); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("sqlitestore: probe %s: %w", dbPath, err)
	}

	// Tighten permissions on the DB file and its WAL/SHM siblings.
	// Ignore errors silently: if chmod fails (e.g. read-only FS, Windows) the
	// underlying FS is already doing access control at a different layer.
	for _, suffix := range []string{"", "-wal", "-shm"} {
		_ = os.Chmod(dbPath+suffix, 0o600)
	}

	s := &SQLiteStore{db: db, dbPath: dbPath}
	for _, opt := range opts {
		opt.applySQLite(s)
	}
	return s, nil
}

// buildSQLiteDSN builds the modernc.org/sqlite DSN with WAL-mode PRAGMAs
// applied per-connection so every pooled conn inherits the same settings.
func buildSQLiteDSN(dbPath string) string {
	// URL-escape the path so filenames containing "?" / "&" do not break
	// the query string that follows.
	v := url.Values{}
	v.Add("_pragma", "journal_mode(wal)")
	v.Add("_pragma", "synchronous(normal)")
	v.Add("_pragma", "busy_timeout(5000)")
	v.Add("_pragma", "foreign_keys(on)")
	return "file:" + dbPath + "?" + v.Encode()
}

// ensureSQLiteSchema applies the v1 schema on a fresh DB and refuses to open
// a DB whose user_version is newer than what this binary was built against.
func ensureSQLiteSchema(db *sql.DB) error {
	var version int
	if err := db.QueryRowContext(context.Background(), "PRAGMA user_version").Scan(&version); err != nil {
		return fmt.Errorf("read user_version: %w", err)
	}

	switch {
	case version == 0:
		if _, err := db.ExecContext(context.Background(), sqliteSchemaV1); err != nil {
			return fmt.Errorf("apply schema v1: %w", err)
		}
		if _, err := db.ExecContext(
			context.Background(),
			fmt.Sprintf("PRAGMA user_version = %d", sqliteSchemaVersion),
		); err != nil {
			return fmt.Errorf("stamp user_version: %w", err)
		}
	case version == sqliteSchemaVersion:
		// Current schema; nothing to do.
	case version > sqliteSchemaVersion:
		return fmt.Errorf(
			"on-disk schema version %d is newer than supported %d — upgrade vv",
			version, sqliteSchemaVersion,
		)
	default:
		// 0 < version < current — future migrations slot in here.
		return fmt.Errorf("unsupported schema version %d", version)
	}

	return nil
}

// Close closes the underlying *sql.DB. Safe to call multiple times.
func (s *SQLiteStore) Close() error {
	if s == nil || s.db == nil {
		return nil
	}
	err := s.db.Close()
	s.db = nil
	return err
}

func (s *SQLiteStore) Get(ctx context.Context, key string) (any, bool, error) {
	sid := SessionIDFrom(ctx)
	isUser := IsUserPath(ctx)
	ns, name := parseKey(key)
	shared := isShared(ns, s.extraShared)

	// Shared namespaces (or user-path reads of a legacy-private row) live at
	// session_id = ''. For a private namespace, prefer the per-session row
	// and fall back to legacy session_id = '' — identical to FileStore.
	if shared || isUser {
		return s.getRow(ctx, ns, name, "")
	}

	if sid != "" {
		val, found, err := s.getRow(ctx, ns, name, sid)
		if err != nil {
			return nil, false, err
		}
		if found {
			return val, true, nil
		}
	}
	// Legacy fallback: private-namespace rows written before session binding
	// existed carry session_id = '' and must remain readable.
	return s.getRow(ctx, ns, name, "")
}

// getRow fetches exactly one row identified by the compound PK and applies
// lazy TTL expiry.
func (s *SQLiteStore) getRow(ctx context.Context, ns, name, sid string) (any, bool, error) {
	const q = `
		SELECT value, updated_at, ttl
		FROM entries
		WHERE namespace = ? AND name = ? AND session_id = ?
		LIMIT 1`

	var value string
	var updatedNs, ttl int64
	err := s.db.QueryRowContext(ctx, q, ns, name, sid).Scan(&value, &updatedNs, &ttl)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, fmt.Errorf("sqlitestore: get %s:%s: %w", ns, name, err)
	}

	if expired(updatedNs, ttl) {
		// Lazy delete. The updated_at predicate guards against a concurrent
		// Set that refreshed the row between our SELECT and this DELETE:
		// under WAL two readers could both race here, and a writer could
		// slip in; the predicate makes the DELETE a no-op in that case so
		// we never remove a freshly-updated row. The error is swallowed so
		// a benign race doesn't surface as a Get failure.
		_, _ = s.db.ExecContext(ctx,
			`DELETE FROM entries WHERE namespace = ? AND name = ? AND session_id = ? AND updated_at = ?`,
			ns, name, sid, updatedNs)
		return nil, false, nil
	}

	return value, true, nil
}

func (s *SQLiteStore) Set(ctx context.Context, key string, value any, ttl int64) error {
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

		// Guard against overwriting a legacy record (session_id = '') planted
		// in what is now a private namespace. Mirrors FileStore's legacy-slot
		// guard so flipping backend does not change write-eligibility rules.
		var dummy int
		err := s.db.QueryRowContext(ctx,
			`SELECT 1 FROM entries WHERE namespace = ? AND name = ? AND session_id = '' LIMIT 1`,
			ns, name).Scan(&dummy)
		if err == nil {
			return fmt.Errorf("%w: legacy entry in private namespace %q cannot be overwritten",
				ErrSessionForbidden, ns)
		}
		if !errors.Is(err, sql.ErrNoRows) {
			return fmt.Errorf("sqlitestore: legacy probe: %w", err)
		}
	}

	strValue, err := encodeValue(value)
	if err != nil {
		return err
	}

	rowSid := recordSessionID(shared, sid)
	now := time.Now().UnixNano()

	// UPSERT preserves created_at automatically by omitting it from the DO
	// UPDATE list. The compound PK (namespace, name, session_id) means
	// session-A and session-B rows never share a slot, so a write from B
	// cannot clobber A even under pathological concurrency.
	const q = `
		INSERT INTO entries (namespace, name, session_id, key, value, created_at, updated_at, ttl)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(namespace, name, session_id) DO UPDATE SET
			value      = excluded.value,
			key        = excluded.key,
			updated_at = excluded.updated_at,
			ttl        = excluded.ttl`

	if _, err := s.db.ExecContext(
		ctx, q,
		ns, name, rowSid, key, strValue, now, now, ttl,
	); err != nil {
		return fmt.Errorf("sqlitestore: set %s: %w", key, err)
	}

	return nil
}

func (s *SQLiteStore) Delete(ctx context.Context, key string) error {
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
			return fmt.Errorf("%w: no session_id for private namespace %q",
				ErrSessionForbidden, ns)
		}
	}

	// Shared namespaces: delete the session_id='' row (if any).
	if shared {
		if _, err := s.db.ExecContext(ctx,
			`DELETE FROM entries WHERE namespace = ? AND name = ? AND session_id = ''`,
			ns, name); err != nil {
			return fmt.Errorf("sqlitestore: delete %s: %w", key, err)
		}
		return nil
	}

	// Private namespace: prefer the caller's own row; fall back to the
	// legacy session_id='' slot so a delete of a pre-binding record still
	// works. A delete against another session's row is a silent no-op — we
	// simply don't see it with this WHERE clause.
	res, err := s.db.ExecContext(ctx,
		`DELETE FROM entries WHERE namespace = ? AND name = ? AND session_id = ?`,
		ns, name, sid)
	if err != nil {
		return fmt.Errorf("sqlitestore: delete %s: %w", key, err)
	}
	affected, _ := res.RowsAffected()
	if affected > 0 {
		return nil
	}

	// No per-session row. Try the legacy slot.
	if _, err := s.db.ExecContext(ctx,
		`DELETE FROM entries WHERE namespace = ? AND name = ? AND session_id = ''`,
		ns, name); err != nil {
		return fmt.Errorf("sqlitestore: delete legacy %s: %w", key, err)
	}
	return nil
}

func (s *SQLiteStore) List(ctx context.Context, prefix string) ([]memory.StoreEntry, error) {
	sid := SessionIDFrom(ctx)
	isUser := IsUserPath(ctx)

	var (
		rows *sql.Rows
		err  error
	)

	if isUser {
		// User path sees only the shared / legacy-shared slot.
		rows, err = s.db.QueryContext(ctx, `
			SELECT namespace, name, key, value, created_at, updated_at, ttl, session_id
			FROM entries
			WHERE session_id = ''
			ORDER BY key`)
	} else {
		// Agent path sees shared + its own private rows.
		rows, err = s.db.QueryContext(ctx, `
			SELECT namespace, name, key, value, created_at, updated_at, ttl, session_id
			FROM entries
			WHERE session_id = '' OR session_id = ?
			ORDER BY key`, sid)
	}
	if err != nil {
		return nil, fmt.Errorf("sqlitestore: list: %w", err)
	}
	defer func() { _ = rows.Close() }()

	var (
		out      []memory.StoreEntry
		toDelete []sqlitePK
		nowNs    = time.Now().UnixNano()
	)

	for rows.Next() {
		var (
			ns, name, k, v string
			createdNs      int64
			updatedNs      int64
			ttl            int64
			rowSid         string
		)
		if err := rows.Scan(&ns, &name, &k, &v, &createdNs, &updatedNs, &ttl, &rowSid); err != nil {
			return nil, fmt.Errorf("sqlitestore: list scan: %w", err)
		}

		// Visibility rules identical to FileStore's readSharedDir/readPrivateDir:
		// a session_id == '' row in a non-shared namespace is a legacy record.
		// User-path callers still see it (FileStore's long-standing behaviour);
		// agent-path callers see it regardless of their sid.
		shared := isShared(ns, s.extraShared)
		if !shared {
			if rowSid != "" {
				// Private row: only visible to the matching session.
				if isUser || rowSid != sid {
					continue
				}
			}
			// rowSid == "" → legacy-shared, visible to everyone.
		}

		if ttl > 0 && nowNs-updatedNs > ttl*int64(time.Second) {
			// Capture updated_at so the lazy-delete below cannot clobber a
			// concurrent Set that refreshed the row after we read it.
			toDelete = append(toDelete, sqlitePK{
				ns: ns, name: name, sid: rowSid, updatedAt: updatedNs,
			})
			continue
		}

		if prefix != "" && !strings.HasPrefix(k, prefix) {
			continue
		}

		out = append(out, memory.StoreEntry{
			Key:       k,
			Value:     v,
			CreatedAt: time.Unix(0, createdNs),
			TTL:       ttl,
		})
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("sqlitestore: list iterate: %w", err)
	}
	_ = rows.Close()

	// Best-effort lazy expiry. The updated_at guard makes the DELETE a no-op
	// if a concurrent writer refreshed the row after we scanned it, so we
	// can't accidentally remove a freshly-updated entry.
	for _, pk := range toDelete {
		_, _ = s.db.ExecContext(ctx,
			`DELETE FROM entries
			 WHERE namespace = ? AND name = ? AND session_id = ? AND updated_at = ?`,
			pk.ns, pk.name, pk.sid, pk.updatedAt)
	}

	return out, nil
}

// sqlitePK is a compound PK tuple (plus the updated_at we observed) used to
// queue lazy-deletes from List without racing concurrent writers.
type sqlitePK struct {
	ns, name, sid string
	updatedAt     int64
}

func (s *SQLiteStore) Clear(ctx context.Context) error {
	if !IsUserPath(ctx) {
		return fmt.Errorf("%w: Clear is restricted to user path", ErrSessionForbidden)
	}

	if _, err := s.db.ExecContext(ctx, `DELETE FROM entries`); err != nil {
		return fmt.Errorf("sqlitestore: clear: %w", err)
	}
	return nil
}

// expired reports whether a row's TTL has elapsed relative to now.
func expired(updatedNs, ttl int64) bool {
	if ttl <= 0 {
		return false
	}
	return time.Now().UnixNano()-updatedNs > ttl*int64(time.Second)
}
