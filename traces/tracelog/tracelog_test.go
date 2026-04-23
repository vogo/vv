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
	"bufio"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/vogo/vage/schema"
)

func newTestHook(t *testing.T, cfg Config) *JSONLHook {
	t.Helper()

	if cfg.BaseDir == "" {
		cfg.BaseDir = t.TempDir()
	}

	h, err := New(cfg)
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if err := h.Start(context.Background()); err != nil {
		t.Fatalf("Start: %v", err)
	}

	return h
}

// readLines returns the newline-delimited contents of path.
func readLines(t *testing.T, path string) []string {
	t.Helper()

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %q: %v", path, err)
	}

	defer func() { _ = f.Close() }()

	var lines []string

	sc := bufio.NewScanner(f)
	for sc.Scan() {
		lines = append(lines, sc.Text())
	}

	if err := sc.Err(); err != nil {
		t.Fatalf("scan %q: %v", path, err)
	}

	return lines
}

// decodedEvent mirrors schema.Event with Data as raw JSON. Unmarshaling into
// the real schema.Event fails because its Data field is a sealed interface —
// marshal works, unmarshal does not. Downstream consumers (P2-14/P3-5) will
// dispatch on Type and decode Data into the matching typed struct; tests do
// not need that here.
type decodedEvent struct {
	Type      string          `json:"type"`
	AgentID   string          `json:"agent_id,omitempty"`
	SessionID string          `json:"session_id,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data,omitempty"`
}

// Test_HappyPath — 5 events, one session, file contains 5 valid JSON lines
// with the required fields.
func Test_HappyPath(t *testing.T) {
	h := newTestHook(t, Config{WorkingDir: "/proj/x"})

	sid := "sess-1"
	for range 5 {
		h.EventChan() <- schema.NewEvent(
			schema.EventAgentStart,
			"agent-a",
			sid,
			schema.AgentStartData{},
		)
	}

	if err := h.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	path := filepath.Join(h.BaseDir(), sid+".jsonl")

	lines := readLines(t, path)
	if len(lines) != 5 {
		t.Fatalf("got %d lines, want 5", len(lines))
	}

	for i, line := range lines {
		var ev decodedEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			t.Fatalf("line %d unmarshal: %v (%q)", i, err, line)
		}

		if ev.Type == "" || ev.SessionID == "" || ev.Timestamp.IsZero() {
			t.Fatalf("line %d missing required field: %+v", i, ev)
		}
	}
}

// Test_MultipleSessionsGetSeparateFiles — routes per session id.
func Test_MultipleSessionsGetSeparateFiles(t *testing.T) {
	h := newTestHook(t, Config{WorkingDir: "/proj/x"})

	h.EventChan() <- schema.NewEvent(schema.EventAgentStart, "a", "sid-A", schema.AgentStartData{})
	h.EventChan() <- schema.NewEvent(schema.EventAgentStart, "a", "sid-B", schema.AgentStartData{})
	h.EventChan() <- schema.NewEvent(schema.EventAgentEnd, "a", "sid-A", schema.AgentEndData{Duration: 1})

	if err := h.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if lines := readLines(t, filepath.Join(h.BaseDir(), "sid-A.jsonl")); len(lines) != 2 {
		t.Fatalf("sid-A: got %d lines, want 2", len(lines))
	}

	if lines := readLines(t, filepath.Join(h.BaseDir(), "sid-B.jsonl")); len(lines) != 1 {
		t.Fatalf("sid-B: got %d lines, want 1", len(lines))
	}
}

// Test_EmptySessionFallsBackToDefault — blank session id lands in default.jsonl.
func Test_EmptySessionFallsBackToDefault(t *testing.T) {
	h := newTestHook(t, Config{})

	h.EventChan() <- schema.NewEvent(schema.EventAgentStart, "a", "", schema.AgentStartData{})

	if err := h.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	if lines := readLines(t, filepath.Join(h.BaseDir(), "default.jsonl")); len(lines) != 1 {
		t.Fatalf("default.jsonl: got %d lines, want 1", len(lines))
	}
}

// Test_SessionIDSanitization — unsafe characters get replaced.
func Test_SessionIDSanitization(t *testing.T) {
	cases := []struct {
		in   string
		want string
	}{
		{"", "default"},
		{"simple", "simple"},
		{"abc/../etc", "abc_.._etc"},
		{"with space", "with_space"},
		{"slash/inside", "slash_inside"},
		{"tabs\t\n", "tabs__"},
		{"ok-chars_1.2", "ok-chars_1.2"},
	}

	for _, tc := range cases {
		if got := sanitizeSessionID(tc.in); got != tc.want {
			t.Errorf("sanitizeSessionID(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}

	long := strings.Repeat("x", 300)
	if got := sanitizeSessionID(long); len(got) != sessionIDMaxLen {
		t.Errorf("long id: len=%d, want %d", len(got), sessionIDMaxLen)
	}
}

// Test_FileRotation — MaxFileBytes triggers rollover to <sid>.1.jsonl.
func Test_FileRotation(t *testing.T) {
	// Each AgentEndData with a short message marshals to ~150–200 bytes
	// depending on timestamp precision. Keep the budget small so two events
	// are enough to force rotation.
	h := newTestHook(t, Config{MaxFileBytes: 150})

	for i := range 5 {
		h.EventChan() <- schema.NewEvent(
			schema.EventAgentEnd,
			"coder",
			"sid",
			schema.AgentEndData{Duration: int64(i), Message: "m"},
		)
	}

	if err := h.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	entries, err := os.ReadDir(h.BaseDir())
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	var parts []string
	for _, e := range entries {
		parts = append(parts, e.Name())
	}

	if len(parts) < 2 {
		t.Fatalf("expected at least one rotation (>= 2 files), got %v", parts)
	}

	// Every file we produced should be valid JSONL.
	for _, name := range parts {
		lines := readLines(t, filepath.Join(h.BaseDir(), name))
		if len(lines) == 0 {
			t.Errorf("empty rotated file %q", name)
			continue
		}

		for i, line := range lines {
			var ev decodedEvent
			if err := json.Unmarshal([]byte(line), &ev); err != nil {
				t.Errorf("%s:%d invalid JSON: %v", name, i, err)
			}
		}
	}
}

// Test_RotationDisabledKeepsSingleFile — MaxFileBytes=0 never rotates.
func Test_RotationDisabledKeepsSingleFile(t *testing.T) {
	h := newTestHook(t, Config{MaxFileBytes: 0})

	for i := range 20 {
		h.EventChan() <- schema.NewEvent(
			schema.EventAgentEnd,
			"coder",
			"sid",
			schema.AgentEndData{Duration: int64(i), Message: strings.Repeat("x", 200)},
		)
	}

	if err := h.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	entries, err := os.ReadDir(h.BaseDir())
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	if len(entries) != 1 {
		names := make([]string, 0, len(entries))
		for _, e := range entries {
			names = append(names, e.Name())
		}

		t.Fatalf("rotation disabled but got multiple files: %v", names)
	}

	lines := readLines(t, filepath.Join(h.BaseDir(), entries[0].Name()))
	if len(lines) != 20 {
		t.Fatalf("got %d lines, want 20", len(lines))
	}
}

// Test_StopIsIdempotent — two Stops do not panic and do not deadlock.
func Test_StopIsIdempotent(t *testing.T) {
	h := newTestHook(t, Config{})

	h.EventChan() <- schema.NewEvent(schema.EventAgentStart, "a", "s", schema.AgentStartData{})

	if err := h.Stop(context.Background()); err != nil {
		t.Fatalf("first Stop: %v", err)
	}

	done := make(chan struct{})
	go func() {
		_ = h.Stop(context.Background())
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("second Stop deadlocked")
	}
}

// Test_ProjectHash_Stable — same cwd in, same hash out; different cwd in,
// different hash out.
func Test_ProjectHash_Stable(t *testing.T) {
	a := ProjectHash("/a/b/c")
	b := ProjectHash("/a/b/c")
	c := ProjectHash("/different/path")

	if a == "" {
		t.Fatal("empty hash for non-empty input")
	}

	if a != b {
		t.Fatalf("not deterministic: %q vs %q", a, b)
	}

	if a == c {
		t.Fatalf("collision for distinct paths: %q", a)
	}

	if ProjectHash("") != "default" {
		t.Fatalf(`empty input should return "default"`)
	}

	for _, r := range a {
		isLower := r >= 'a' && r <= 'z'
		isDigit := r >= '0' && r <= '9'

		if !isLower && !isDigit {
			t.Fatalf("hash contains non-base32 char %q (full: %q)", r, a)
		}
	}
}

// Test_FilePermissions — 0600 files under 0700 directories.
func Test_FilePermissions(t *testing.T) {
	if os.Getenv("CI_SKIP_PERM") != "" {
		t.Skip("skipping permission test in restricted environment")
	}

	h := newTestHook(t, Config{})

	h.EventChan() <- schema.NewEvent(schema.EventAgentStart, "a", "s", schema.AgentStartData{})

	if err := h.Stop(context.Background()); err != nil {
		t.Fatalf("Stop: %v", err)
	}

	info, err := os.Stat(h.BaseDir())
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}

	if got := info.Mode().Perm(); got&0o077 != 0 {
		t.Errorf("dir mode = %o, want no access for group/other", got)
	}

	finfo, err := os.Stat(filepath.Join(h.BaseDir(), "s.jsonl"))
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}

	if got := finfo.Mode().Perm(); got&0o077 != 0 {
		t.Errorf("file mode = %o, want no access for group/other", got)
	}
}

// Test_New_RejectsEmptyBaseDir — misconfig surfaces at construction.
func Test_New_RejectsEmptyBaseDir(t *testing.T) {
	if _, err := New(Config{}); err == nil {
		t.Fatal("expected error for empty BaseDir")
	}
}
