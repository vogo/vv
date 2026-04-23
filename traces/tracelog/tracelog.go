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

package tracelog

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"sync"

	"github.com/vogo/vage/hook"
	"github.com/vogo/vage/schema"
)

const (
	defaultBufferSize   = 1024
	defaultMaxFileBytes = 64 << 20 // 64 MiB

	sessionIDMaxLen = 128

	dirPerm  = 0o700
	filePerm = 0o600
)

// Config controls the JSONL hook's disk layout and buffering. Fields map 1:1
// to configs.TraceConfig but take no pointers — the caller has already
// decided whether the hook should exist.
type Config struct {
	// BaseDir is the root directory for trace files. Trace files land at
	// <BaseDir>/<projectHash>/<sessionID>.jsonl. Must be a non-empty absolute
	// path; a relative path is resolved against the working directory.
	BaseDir string

	// WorkingDir seeds the project hash bucket. Empty → "default".
	WorkingDir string

	// MaxFileBytes triggers size-based rotation (`<sid>.jsonl` →
	// `<sid>.1.jsonl` → …). 0 disables rotation; negative values are coerced
	// to the default.
	MaxFileBytes int64

	// BufferSize caps the event channel capacity. 0 / negative → default.
	BufferSize int
}

// JSONLHook implements vage hook.AsyncHook by writing each event as a single
// JSON line to a per-session file under <baseDir>/<projectHash>/. It is safe
// for a single hook.Manager to register; do not share instances across
// managers.
type JSONLHook struct {
	baseDir      string
	maxFileBytes int64
	ch           chan schema.Event

	files map[string]*sessionFile // consumer-goroutine exclusive; no lock
	wg    sync.WaitGroup

	stopOnce sync.Once
}

type sessionFile struct {
	f       *os.File
	written int64
	part    int
}

// Compile-time: JSONLHook satisfies hook.AsyncHook.
var _ hook.AsyncHook = (*JSONLHook)(nil)

// New constructs a JSONLHook. It resolves and creates the project-scoped
// directory eagerly (so configuration errors surface at startup, not on the
// first event) and allocates the event channel. Start must be called before
// the hook receives any events.
func New(cfg Config) (*JSONLHook, error) {
	if cfg.BaseDir == "" {
		return nil, fmt.Errorf("tracelog: BaseDir is required")
	}

	base, err := filepath.Abs(cfg.BaseDir)
	if err != nil {
		return nil, fmt.Errorf("tracelog: abs base dir: %w", err)
	}

	projectDir := filepath.Join(base, ProjectHash(cfg.WorkingDir))
	if err := os.MkdirAll(projectDir, dirPerm); err != nil {
		return nil, fmt.Errorf("tracelog: mkdir %q: %w", projectDir, err)
	}

	maxBytes := cfg.MaxFileBytes
	if maxBytes < 0 {
		maxBytes = defaultMaxFileBytes
	}

	bufSize := cfg.BufferSize
	if bufSize <= 0 {
		bufSize = defaultBufferSize
	}

	return &JSONLHook{
		baseDir:      projectDir,
		maxFileBytes: maxBytes,
		ch:           make(chan schema.Event, bufSize),
		files:        make(map[string]*sessionFile),
	}, nil
}

// BaseDir returns the resolved project-scoped directory. Useful for tests
// and diagnostics.
func (h *JSONLHook) BaseDir() string { return h.baseDir }

// EventChan implements hook.AsyncHook.
func (h *JSONLHook) EventChan() chan<- schema.Event { return h.ch }

// Filter implements hook.AsyncHook. Returning nil subscribes to every event
// type — the on-disk trace is deliberately full-firehose.
func (h *JSONLHook) Filter() []string { return nil }

// Start launches the consumer goroutine that drains the event channel.
// The ctx is not retained — the consumer exits when the channel is closed
// by Stop.
func (h *JSONLHook) Start(_ context.Context) error {
	h.wg.Add(1)

	go h.consume()

	return nil
}

// Stop closes the event channel (causing the consumer to drain and exit),
// waits for the consumer, and flushes every open file. It is safe to call
// more than once.
func (h *JSONLHook) Stop(_ context.Context) error {
	h.stopOnce.Do(func() {
		close(h.ch)
		h.wg.Wait()

		for sid, sf := range h.files {
			if err := sf.f.Sync(); err != nil {
				slog.Warn("tracelog: sync on stop", "sid", sid, "err", err)
			}

			if err := sf.f.Close(); err != nil {
				slog.Warn("tracelog: close on stop", "sid", sid, "err", err)
			}
		}

		h.files = nil
	})

	return nil
}

func (h *JSONLHook) consume() {
	defer h.wg.Done()

	for ev := range h.ch {
		h.writeEvent(ev)
	}
}

func (h *JSONLHook) writeEvent(ev schema.Event) {
	line, err := json.Marshal(ev)
	if err != nil {
		slog.Warn("tracelog: marshal event", "type", ev.Type, "err", err)
		return
	}

	line = append(line, '\n')

	sid := sanitizeSessionID(ev.SessionID)

	sf, err := h.ensureFile(sid)
	if err != nil {
		slog.Warn("tracelog: open session file", "sid", sid, "err", err)
		return
	}

	if h.maxFileBytes > 0 && sf.written+int64(len(line)) > h.maxFileBytes {
		if rerr := h.rollSession(sid); rerr != nil {
			slog.Warn("tracelog: rotate", "sid", sid, "err", rerr)
			return
		}

		sf, err = h.ensureFile(sid)
		if err != nil {
			slog.Warn("tracelog: reopen after rotate", "sid", sid, "err", err)
			return
		}
	}

	n, werr := sf.f.Write(line)
	if werr != nil {
		slog.Warn("tracelog: write", "sid", sid, "err", werr)
		return
	}

	sf.written += int64(n)
}

// ensureFile returns the current sessionFile for sid, opening it on first
// access. part is carried across rotations.
func (h *JSONLHook) ensureFile(sid string) (*sessionFile, error) {
	if sf, ok := h.files[sid]; ok {
		return sf, nil
	}

	sf, err := h.openSession(sid, 0)
	if err != nil {
		return nil, err
	}

	h.files[sid] = sf

	return sf, nil
}

// rollSession flushes + closes the current file for sid and opens the next
// part. It mutates h.files in place.
func (h *JSONLHook) rollSession(sid string) error {
	sf, ok := h.files[sid]
	if !ok {
		// No open file to rotate — nothing to do.
		return nil
	}

	if err := sf.f.Sync(); err != nil {
		slog.Warn("tracelog: sync before rotate", "sid", sid, "err", err)
	}

	if err := sf.f.Close(); err != nil {
		slog.Warn("tracelog: close before rotate", "sid", sid, "err", err)
	}

	delete(h.files, sid)

	next, err := h.openSession(sid, sf.part+1)
	if err != nil {
		return err
	}

	h.files[sid] = next

	return nil
}

func (h *JSONLHook) openSession(sid string, part int) (*sessionFile, error) {
	name := sid + ".jsonl"
	if part > 0 {
		name = fmt.Sprintf("%s.%d.jsonl", sid, part)
	}

	path := filepath.Join(h.baseDir, name)

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, filePerm)
	if err != nil {
		return nil, fmt.Errorf("open %q: %w", path, err)
	}

	// A file reopened after a crash may already have bytes; start accounting
	// from its current size so rotation doesn't double-count pre-existing
	// content.
	info, err := f.Stat()
	if err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("stat %q: %w", path, err)
	}

	return &sessionFile{f: f, written: info.Size(), part: part}, nil
}

var sidPattern = regexp.MustCompile(`[^a-zA-Z0-9._-]`)

// sanitizeSessionID maps an arbitrary session id to a filesystem-safe token.
// Empty input becomes "default"; everything outside [A-Za-z0-9._-] is
// replaced with '_'. Capped at 128 chars — most filesystems allow 255, but
// the session id is not the only component of the file name (rotation
// appends ".<N>.jsonl") so we leave headroom.
func sanitizeSessionID(sid string) string {
	if sid == "" {
		return "default"
	}

	s := sidPattern.ReplaceAllString(sid, "_")

	if len(s) > sessionIDMaxLen {
		s = s[:sessionIDMaxLen]
	}

	return s
}
