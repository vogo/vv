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

package setup

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// seedSession creates a session directory whose activity files carry the
// given age.
func seedSession(t *testing.T, root, id string, age time.Duration, files ...string) string {
	t.Helper()

	dir := filepath.Join(root, id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	if len(files) == 0 {
		files = []string{"meta.json", "messages.jsonl"}
	}

	stamp := time.Now().Add(-age)

	for _, name := range files {
		path := filepath.Join(dir, name)
		if err := os.WriteFile(path, []byte("{}\n"), 0o600); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}

		if err := os.Chtimes(path, stamp, stamp); err != nil {
			t.Fatalf("chtimes %s: %v", path, err)
		}
	}

	return dir
}

func TestSweepExpiredSessions_RemovesOnlyStaleSessions(t *testing.T) {
	root := t.TempDir()

	stale := seedSession(t, root, "old", 40*24*time.Hour)
	fresh := seedSession(t, root, "new", 2*24*time.Hour)

	sweepExpiredSessions(root, 30)

	if _, err := os.Stat(stale); !os.IsNotExist(err) {
		t.Errorf("stale session survived the sweep (err=%v)", err)
	}

	if _, err := os.Stat(fresh); err != nil {
		t.Errorf("fresh session was deleted: %v", err)
	}
}

// TestSweepExpiredSessions_UsesNewestActivityFile guards the reason the
// sweep does not look at directory mtime: appending to events.jsonl
// leaves both the directory and meta.json untouched, so an actively used
// session would look ancient.
func TestSweepExpiredSessions_UsesNewestActivityFile(t *testing.T) {
	root := t.TempDir()

	dir := seedSession(t, root, "active", 90*24*time.Hour, "meta.json")

	recent := filepath.Join(dir, "events.jsonl")
	if err := os.WriteFile(recent, []byte("{}\n"), 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	sweepExpiredSessions(root, 30)

	if _, err := os.Stat(dir); err != nil {
		t.Errorf("a session with recent event activity must survive: %v", err)
	}
}

func TestSweepExpiredSessions_SkipsUnrecognisedDirectories(t *testing.T) {
	root := t.TempDir()

	// No activity file at all: age is unknowable, so it must be kept.
	dir := filepath.Join(root, "mystery")
	if err := os.MkdirAll(filepath.Join(dir, "workspace"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	old := time.Now().Add(-365 * 24 * time.Hour)
	if err := os.Chtimes(dir, old, old); err != nil {
		t.Fatalf("chtimes: %v", err)
	}

	sweepExpiredSessions(root, 1)

	if _, err := os.Stat(dir); err != nil {
		t.Errorf("a directory of unknown age must be kept, not guessed at: %v", err)
	}
}

func TestSweepExpiredSessions_DisabledAndMissingRoot(t *testing.T) {
	root := t.TempDir()
	dir := seedSession(t, root, "old", 400*24*time.Hour)

	// 0 days means "keep everything".
	sweepExpiredSessions(root, 0)

	if _, err := os.Stat(dir); err != nil {
		t.Errorf("retention_days=0 must delete nothing: %v", err)
	}

	// A missing root is not an error.
	sweepExpiredSessions(filepath.Join(root, "nope"), 30)
	sweepExpiredSessions("", 30)
}

func TestSessionLastActivity_ReportsUnknown(t *testing.T) {
	root := t.TempDir()

	if _, ok := sessionLastActivity(filepath.Join(root, "absent")); ok {
		t.Error("a missing directory must report an unknown age")
	}

	dir := seedSession(t, root, "known", time.Hour, "state.json")

	if _, ok := sessionLastActivity(dir); !ok {
		t.Error("state.json alone must be enough to date a session")
	}
}
