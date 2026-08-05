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
	"log/slog"
	"os"
	"path/filepath"
	"time"
)

// sessionActivityFiles are the per-session files whose modification time
// stands for "when was this session last touched".
//
// The session directory's own mtime is not usable: appending to an
// existing file does not update it, so a session written to daily would
// still look untouched since creation.
var sessionActivityFiles = []string{
	"messages.jsonl",
	"events.jsonl",
	"meta.json",
	"state.json",
	"metrics.json",
}

// sweepExpiredSessions deletes session directories under root whose last
// activity predates now-days. Called once at startup when
// session.retention_days is set.
//
// Deleting the user's history is destructive, so the sweep is
// conservative in every ambiguous case: a directory whose age cannot be
// determined is kept, and every failure is logged and skipped rather
// than escalated. Because the whole session — transcripts, workspace,
// tree — lives under one directory, a single RemoveAll leaves no
// orphaned state (constitution § 4).
func sweepExpiredSessions(root string, days int) {
	if root == "" || days <= 0 {
		return
	}

	cutoff := time.Now().AddDate(0, 0, -days)

	entries, err := os.ReadDir(root)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("vv: session retention sweep failed to read root", "dir", root, "error", err)
		}

		return
	}

	removed := 0

	for _, e := range entries {
		if !e.IsDir() {
			continue
		}

		dir := filepath.Join(root, e.Name())

		last, ok := sessionLastActivity(dir)
		if !ok || !last.Before(cutoff) {
			continue
		}

		if err := os.RemoveAll(dir); err != nil {
			slog.Warn("vv: session retention sweep failed to remove", "dir", dir, "error", err)

			continue
		}

		removed++
	}

	if removed > 0 {
		slog.Info("vv: session retention sweep removed expired sessions",
			"count", removed, "older_than_days", days, "dir", root)
	}
}

// sessionLastActivity returns the newest mtime among a session's
// activity files. ok is false when none of them exist, which keeps
// unrecognised directories out of the sweep.
func sessionLastActivity(dir string) (time.Time, bool) {
	var (
		newest time.Time
		found  bool
	)

	for _, name := range sessionActivityFiles {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil {
			continue
		}

		found = true

		if info.ModTime().After(newest) {
			newest = info.ModTime()
		}
	}

	return newest, found
}
