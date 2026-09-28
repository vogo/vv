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
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/session"
)

const rotateSession = "sess-rotate"

func newRotatingStore(t *testing.T, maxBytes int64) (*rotatingSessionStore, string) {
	t.Helper()

	root := t.TempDir()

	inner, err := session.NewFileSessionStore(root)
	if err != nil {
		t.Fatalf("NewFileSessionStore: %v", err)
	}

	if err := inner.Create(context.Background(), session.New(rotateSession)); err != nil {
		t.Fatalf("Create: %v", err)
	}

	return newRotatingSessionStore(inner, root, maxBytes), root
}

func appendEvents(t *testing.T, store session.SessionStore, n int) {
	t.Helper()

	ctx := context.Background()

	for i := range n {
		ev := schema.NewEvent(schema.EventToolCallStart, "primary", rotateSession, schema.ToolCallStartData{
			ToolCallID: fmt.Sprintf("call-%03d", i),
			ToolName:   "bash",
			Arguments:  strings.Repeat("x", 200),
		})

		if err := store.AppendEvent(ctx, rotateSession, ev); err != nil {
			t.Fatalf("AppendEvent %d: %v", i, err)
		}
	}
}

func TestRotatingSessionStore_RotatesAndStitchesReadsBack(t *testing.T) {
	store, root := newRotatingStore(t, 1024)

	const total = 40
	appendEvents(t, store, total)

	dir := filepath.Join(root, rotateSession)

	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	segments := 0

	for _, e := range entries {
		if _, ok := eventSegmentIndex(e.Name()); ok {
			segments++
		}
	}

	if segments == 0 {
		t.Fatal("no rotated segment produced; the size bound never engaged")
	}

	// Rotation must not cost a single event on the read path.
	got, err := store.ListEvents(context.Background(), rotateSession)
	if err != nil {
		t.Fatalf("ListEvents: %v", err)
	}

	if len(got) != total {
		t.Fatalf("ListEvents returned %d events, want %d (rotated segments were dropped)", len(got), total)
	}

	// ...and must preserve append order across the segment boundary.
	// Data decodes as nil on both halves (schema.Event.Data is an
	// interface with unexported markers), so ordering is checked on the
	// timestamps, which do survive.
	for i := 1; i < len(got); i++ {
		if got[i].Timestamp.Before(got[i-1].Timestamp) {
			t.Fatalf("event %d predates event %d: rotated segments were stitched out of order", i, i-1)
		}
	}

	if got[0].Data != nil {
		t.Error("rotated segments must present the same nil-Data shape as the live log, or the two halves of one log behave differently")
	}

	raw, err := os.ReadFile(filepath.Join(dir, eventsFilePrefix+"1"+eventsFileSuffix))
	if err != nil {
		t.Fatalf("read first segment: %v", err)
	}

	if !strings.Contains(string(raw), "call-000") {
		t.Error("the oldest events must live in the first rotated segment")
	}
}

func TestRotatingSessionStore_NoRotationBelowThreshold(t *testing.T) {
	store, root := newRotatingStore(t, 1<<20)

	appendEvents(t, store, 5)

	entries, err := os.ReadDir(filepath.Join(root, rotateSession))
	if err != nil {
		t.Fatalf("ReadDir: %v", err)
	}

	for _, e := range entries {
		if _, ok := eventSegmentIndex(e.Name()); ok {
			t.Errorf("unexpected rotated segment %q below the threshold", e.Name())
		}
	}

	got, err := store.ListEvents(context.Background(), rotateSession)
	if err != nil || len(got) != 5 {
		t.Errorf("ListEvents = %d events (err=%v), want 5", len(got), err)
	}
}

func TestRotatingSessionStore_UnknownSessionKeepsNotFound(t *testing.T) {
	store, _ := newRotatingStore(t, 1024)

	if _, err := store.ListEvents(context.Background(), "absent"); err == nil {
		t.Error("ListEvents on an unknown session must keep the store's not-found contract")
	}
}

func TestEventSegmentIndex(t *testing.T) {
	cases := map[string]struct {
		n  int
		ok bool
	}{
		"events.1.jsonl":  {1, true},
		"events.42.jsonl": {42, true},
		"events.jsonl":    {0, false}, // the live log is not a segment
		"events.0.jsonl":  {0, false},
		"events.-1.jsonl": {0, false},
		"events.x.jsonl":  {0, false},
		"messages.jsonl":  {0, false},
	}

	for name, want := range cases {
		n, ok := eventSegmentIndex(name)
		if n != want.n || ok != want.ok {
			t.Errorf("eventSegmentIndex(%q) = (%d,%v), want (%d,%v)", name, n, ok, want.n, want.ok)
		}
	}
}
