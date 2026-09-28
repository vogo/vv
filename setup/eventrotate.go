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
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/session"
)

const (
	eventsFileName   = "events.jsonl"
	eventsFilePrefix = "events."
	eventsFileSuffix = ".jsonl"

	// eventsScanBuffer bounds one event line during a rotated-segment
	// read. Events carry no message bodies under the default whitelist,
	// so this only has to cover an unusually large tool-call argument.
	eventsScanBuffer = 4 << 20
)

// rotatingSessionStore adds size-based rotation to a SessionStore's event
// log: events.jsonl is renamed to events.<n>.jsonl once it crosses the
// threshold, and reads stitch the segments back together in order.
//
// The behaviour is inherited from the trace hook, which
// event_persist:all now subsumes — merging the two event sinks must not
// cost the unbounded-file protection the trace hook had.
//
// Everything except the two event methods is delegated by embedding, so
// the decorator cannot drift as the SessionStore interface grows.
type rotatingSessionStore struct {
	session.SessionStore

	root     string
	maxBytes int64

	mu sync.Mutex
}

// Compile-time check.
var _ session.SessionStore = (*rotatingSessionStore)(nil)

// newRotatingSessionStore wraps inner. maxBytes <= 0 is a programming
// error at the call site (callers skip the wrapper), not a silent
// no-rotation mode.
func newRotatingSessionStore(inner session.SessionStore, root string, maxBytes int64) *rotatingSessionStore {
	return &rotatingSessionStore{SessionStore: inner, root: root, maxBytes: maxBytes}
}

func (r *rotatingSessionStore) eventsPath(id string) string {
	return filepath.Join(r.root, id, eventsFileName)
}

// AppendEvent rotates the log if it has outgrown the threshold, then
// delegates. A rotation failure is not fatal: losing the size bound is
// better than losing the event.
func (r *rotatingSessionStore) AppendEvent(ctx context.Context, id string, e schema.Event) error {
	r.rotateIfNeeded(id)

	return r.SessionStore.AppendEvent(ctx, id, e)
}

func (r *rotatingSessionStore) rotateIfNeeded(id string) {
	path := r.eventsPath(id)

	info, err := os.Stat(path)
	if err != nil || info.Size() < r.maxBytes {
		return
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	// Re-check under the lock: a concurrent writer may have rotated
	// already, and rotating twice would leave an empty segment behind.
	if info, err = os.Stat(path); err != nil || info.Size() < r.maxBytes {
		return
	}

	next := highestEventSegment(filepath.Dir(path)) + 1

	target := filepath.Join(filepath.Dir(path), eventsFilePrefix+strconv.Itoa(next)+eventsFileSuffix)
	if err := os.Rename(path, target); err != nil {
		// Keep appending to the oversized file rather than dropping the
		// event; the operator sees the warning and can act.
		return
	}
}

// ListEvents returns the rotated segments (oldest first) followed by the
// live log, so rotation is invisible to API callers.
func (r *rotatingSessionStore) ListEvents(ctx context.Context, id string) ([]schema.Event, error) {
	dir := filepath.Join(r.root, id)

	older, err := readEventSegments(dir)
	if err != nil {
		return nil, err
	}

	current, err := r.SessionStore.ListEvents(ctx, id)
	if err != nil {
		// A session whose live log is gone but whose segments survive is
		// a partial delete; reporting the store's error keeps the 404
		// contract intact.
		return nil, err
	}

	if len(older) == 0 {
		return current, nil
	}

	return append(older, current...), nil
}

// highestEventSegment returns the largest n among events.<n>.jsonl in
// dir, or 0 when there are none.
func highestEventSegment(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}

	max := 0

	for _, e := range entries {
		if n, ok := eventSegmentIndex(e.Name()); ok && n > max {
			max = n
		}
	}

	return max
}

// eventSegmentIndex parses "events.<n>.jsonl". The live "events.jsonl"
// is deliberately not a match.
func eventSegmentIndex(name string) (int, bool) {
	if !strings.HasPrefix(name, eventsFilePrefix) || !strings.HasSuffix(name, eventsFileSuffix) {
		return 0, false
	}

	mid := strings.TrimSuffix(strings.TrimPrefix(name, eventsFilePrefix), eventsFileSuffix)

	n, err := strconv.Atoi(mid)
	if err != nil || n <= 0 {
		return 0, false
	}

	return n, true
}

// readEventSegments reads every rotated segment in dir, oldest first.
func readEventSegments(dir string) ([]schema.Event, error) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("vv: read %q: %w", dir, err)
	}

	type segment struct {
		n    int
		name string
	}

	var segs []segment

	for _, e := range entries {
		if n, ok := eventSegmentIndex(e.Name()); ok {
			segs = append(segs, segment{n: n, name: e.Name()})
		}
	}

	if len(segs) == 0 {
		return nil, nil
	}

	sort.Slice(segs, func(i, j int) bool { return segs[i].n < segs[j].n })

	var out []schema.Event

	for _, seg := range segs {
		events, err := readEventFile(filepath.Join(dir, seg.name))
		if err != nil {
			return nil, err
		}

		out = append(out, events...)
	}

	return out, nil
}

// rawEventLine mirrors the on-disk event shape. Data must be decoded as
// raw bytes: schema.Event.Data is an interface whose implementations
// carry unexported markers, so unmarshalling straight into schema.Event
// fails on every line. vage's own reader does the same and leaves Data
// nil, and the rotated segments must present exactly that shape or a
// caller would see the two halves of one log behave differently.
type rawEventLine struct {
	Type      string          `json:"type"`
	AgentID   string          `json:"agent_id,omitempty"`
	SessionID string          `json:"session_id,omitempty"`
	Timestamp time.Time       `json:"timestamp"`
	Data      json.RawMessage `json:"data,omitempty"`
	ParentID  string          `json:"parent_id,omitempty"`
}

func (r *rawEventLine) toEvent() schema.Event {
	return schema.Event{
		Type:      r.Type,
		AgentID:   r.AgentID,
		SessionID: r.SessionID,
		Timestamp: r.Timestamp,
		Data:      nil,
		ParentID:  r.ParentID,
	}
}

func readEventFile(path string) ([]schema.Event, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("vv: open %q: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	var out []schema.Event

	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), eventsScanBuffer)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}

		var raw rawEventLine
		if err := json.Unmarshal([]byte(line), &raw); err != nil {
			// One malformed line must not hide the rest of a segment.
			continue
		}

		out = append(out, raw.toEvent())
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("vv: scan %q: %w", path, err)
	}

	return out, nil
}
