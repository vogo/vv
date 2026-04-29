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

package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/vogo/vage/session"
)

func newTestStore(t *testing.T) session.SessionStore {
	t.Helper()
	return session.NewMapSessionStore()
}

func TestPrepareSessionID_NewMintsFresh(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	id, mode, prev, err := PrepareSessionID(ctx, store, "")
	if err != nil {
		t.Fatalf("PrepareSessionID: %v", err)
	}
	if mode != SessionResumeNew {
		t.Errorf("mode = %v, want SessionResumeNew", mode)
	}
	if prev != nil {
		t.Errorf("prev = %v, want nil for new session", prev)
	}
	if id == "" {
		t.Error("id is empty; expected GenerateID-minted value")
	}
}

func TestPrepareSessionID_Existing(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	want := "alpha-001"
	if err := store.Create(ctx, &session.Session{
		ID:        want,
		AgentID:   "coder",
		Title:     "first",
		State:     session.StateActive,
		CreatedAt: time.Now(),
		UpdatedAt: time.Now(),
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	id, mode, prev, err := PrepareSessionID(ctx, store, want)
	if err != nil {
		t.Fatalf("PrepareSessionID: %v", err)
	}
	if mode != SessionResumeExisting {
		t.Errorf("mode = %v, want SessionResumeExisting", mode)
	}
	if id != want {
		t.Errorf("id = %q, want %q", id, want)
	}
	if prev == nil || prev.AgentID != "coder" {
		t.Errorf("prev meta not loaded correctly: %+v", prev)
	}
}

func TestPrepareSessionID_NotFound(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	id, mode, prev, err := PrepareSessionID(ctx, store, "no-such-id")
	if err != nil {
		t.Fatalf("PrepareSessionID: %v", err)
	}
	if mode != SessionResumeNotFound {
		t.Errorf("mode = %v, want SessionResumeNotFound", mode)
	}
	if id != "no-such-id" {
		t.Errorf("id = %q, want %q", id, "no-such-id")
	}
	if prev != nil {
		t.Errorf("prev = %v, want nil for not-found case", prev)
	}
}

func TestPrepareSessionID_Invalid(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	// Path-traversal style id is rejected by validateID.
	_, _, _, err := PrepareSessionID(ctx, store, "..")
	if err == nil {
		t.Fatal("expected error for invalid id")
	}
}

func TestTouchSession_CreatesWhenMissing(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	if err := TouchSession(ctx, store, "fresh-id", "researcher"); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}

	got, err := store.Get(ctx, "fresh-id")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if got.AgentID != "researcher" {
		t.Errorf("AgentID = %q, want %q", got.AgentID, "researcher")
	}
	if got.State != session.StateActive {
		t.Errorf("State = %q, want active", got.State)
	}
}

func TestTouchSession_RefreshesUpdatedAt(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	old := time.Now().Add(-1 * time.Hour)
	if err := store.Create(ctx, &session.Session{
		ID:        "stale",
		State:     session.StateActive,
		CreatedAt: old,
		UpdatedAt: old,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	if err := TouchSession(ctx, store, "stale", ""); err != nil {
		t.Fatalf("TouchSession: %v", err)
	}

	got, err := store.Get(ctx, "stale")
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if !got.UpdatedAt.After(old) {
		t.Errorf("UpdatedAt = %v, want fresh > %v", got.UpdatedAt, old)
	}
}

func TestPrintSessionList_Empty(t *testing.T) {
	store := newTestStore(t)
	var buf bytes.Buffer
	if err := PrintSessionList(context.Background(), store, &buf); err != nil {
		t.Fatalf("PrintSessionList: %v", err)
	}
	if !strings.Contains(buf.String(), "no sessions yet") {
		t.Errorf("expected 'no sessions yet' message, got: %q", buf.String())
	}
}

func TestPrintSessionList_SortedByUpdatedAtDesc(t *testing.T) {
	ctx := context.Background()
	store := newTestStore(t)

	now := time.Now()
	for i, id := range []string{"old", "newer", "newest"} {
		if err := store.Create(ctx, &session.Session{
			ID:        id,
			Title:     id,
			State:     session.StateActive,
			CreatedAt: now,
			UpdatedAt: now.Add(time.Duration(i) * time.Minute),
		}); err != nil {
			t.Fatalf("seed %s: %v", id, err)
		}
	}

	var buf bytes.Buffer
	if err := PrintSessionList(ctx, store, &buf); err != nil {
		t.Fatalf("PrintSessionList: %v", err)
	}

	out := buf.String()
	// Find the offsets of the data rows. tabwriter pads with spaces, not
	// tabs, so locate by id token at the start of a line.
	lines := strings.Split(out, "\n")
	pos := map[string]int{}
	for i, l := range lines {
		switch {
		case strings.HasPrefix(l, "newest "):
			pos["newest"] = i
		case strings.HasPrefix(l, "newer "):
			pos["newer"] = i
		case strings.HasPrefix(l, "old "):
			pos["old"] = i
		}
	}
	if len(pos) != 3 {
		t.Fatalf("expected 3 data rows, got %v in:\n%s", pos, out)
	}
	if pos["newest"] >= pos["newer"] || pos["newer"] >= pos["old"] {
		t.Errorf("rows not sorted UpdatedAt desc; pos=%v\n%s", pos, out)
	}
}
