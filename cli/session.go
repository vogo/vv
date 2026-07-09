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
	"context"
	"errors"
	"fmt"
	"io"
	"sort"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/vogo/vage/session"
)

// SessionResumeMode classifies how cli.PrepareSessionID resolved a
// user-supplied --session value. Run() consults it to decide what banner to
// show on TUI startup.
type SessionResumeMode int

// Resume modes returned by PrepareSessionID.
const (
	// SessionResumeNew means the caller did not request a specific id; one was
	// minted via session.GenerateID.
	SessionResumeNew SessionResumeMode = iota

	// SessionResumeExisting means the requested id was found in the store and
	// the loaded Session is returned for banner rendering.
	SessionResumeExisting

	// SessionResumeNotFound means the requested id is well-formed but absent
	// from the store. The caller should still bind the id; SessionHook
	// auto-creates on the first AppendEvent.
	SessionResumeNotFound
)

// SessionListLimit caps the number of rows printed by `vv --session list`.
const SessionListLimit = 20

// PrepareSessionID resolves the user-supplied --session value against store
// and returns the id to bind to the App together with the resolved mode and
// (for an existing id) the loaded Session metadata. An invalid id surfaces as
// an error so the caller can exit before the TUI starts.
//
// Validation happens at the boundary so the same error message reaches the
// user regardless of which SessionStore backend is in play (MapStore.Get does
// not call validateID, FileStore.Get does — without this guard a stray `..`
// would silently bind as a "not found" id on MapStore).
//
// store may be a SessionStore or any narrower interface; only Get is used.
func PrepareSessionID(ctx context.Context, store session.SessionMetaStore, want string) (string, SessionResumeMode, *session.Session, error) {
	want = strings.TrimSpace(want)
	if want == "" {
		return session.GenerateID(), SessionResumeNew, nil, nil
	}

	if !session.IDPattern.MatchString(want) || want == "." || want == ".." {
		return "", SessionResumeNew, nil, fmt.Errorf("invalid session id %q", want)
	}

	s, err := store.Get(ctx, want)
	switch {
	case err == nil:
		return s.ID, SessionResumeExisting, s, nil
	case errors.Is(err, session.ErrSessionNotFound):
		return want, SessionResumeNotFound, nil, nil
	default:
		// Includes ErrInvalidArgument and any backend I/O failures.
		return "", SessionResumeNew, nil, fmt.Errorf("resolve session %q: %w", want, err)
	}
}

// PrintSessionList writes a table of the most recent sessions to w, sorted by
// UpdatedAt descending. Event counts are best-effort; ListEvents errors per
// row do not fail the whole call.
func PrintSessionList(ctx context.Context, store session.SessionStore, w io.Writer) error {
	sessions, err := store.List(ctx, session.SessionFilter{Limit: SessionListLimit})
	if err != nil {
		return fmt.Errorf("list sessions: %w", err)
	}

	sort.Slice(sessions, func(i, j int) bool {
		return sessions[i].UpdatedAt.After(sessions[j].UpdatedAt)
	})

	if len(sessions) == 0 {
		_, _ = fmt.Fprintln(w, "(no sessions yet)")
		return nil
	}

	tw := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	_, _ = fmt.Fprintln(tw, "ID\tAGENT\tTITLE\tSTATE\tEVENTS\tUPDATED")

	for _, s := range sessions {
		count := -1
		if events, lerr := store.ListEvents(ctx, s.ID); lerr == nil {
			count = len(events)
		}
		_, _ = fmt.Fprintf(
			tw, "%s\t%s\t%s\t%s\t%s\t%s\n",
			s.ID,
			dashIfEmpty(s.AgentID),
			dashIfEmpty(s.Title),
			string(s.State),
			formatCount(count),
			s.UpdatedAt.Local().Format("2006-01-02 15:04"),
		)
	}

	return tw.Flush()
}

// TouchSession refreshes the UpdatedAt timestamp on the session metadata so
// resume-listings reflect activity. SessionHook only writes events (not meta),
// so without an explicit Update the meta UpdatedAt stays at creation time.
//
// When the id is absent from the store, TouchSession creates a fresh
// Session{State=Active, AgentID=agentID}. Errors are returned to the caller —
// they do not block the TUI from running but should be surfaced for diagnostics.
func TouchSession(ctx context.Context, store session.SessionMetaStore, sessionID, agentID string) error {
	if sessionID == "" {
		return nil
	}

	s, err := store.Get(ctx, sessionID)
	if errors.Is(err, session.ErrSessionNotFound) {
		now := time.Now()
		seed := &session.Session{
			ID:        sessionID,
			AgentID:   agentID,
			State:     session.StateActive,
			CreatedAt: now,
			UpdatedAt: now,
		}
		if cerr := store.Create(ctx, seed); cerr != nil && !errors.Is(cerr, session.ErrSessionExists) {
			return fmt.Errorf("create session %q: %w", sessionID, cerr)
		}
		return nil
	}
	if err != nil {
		return fmt.Errorf("get session %q: %w", sessionID, err)
	}

	if agentID != "" && s.AgentID == "" {
		s.AgentID = agentID
	}

	if uerr := store.Update(ctx, s); uerr != nil {
		return fmt.Errorf("update session %q: %w", sessionID, uerr)
	}

	return nil
}

func dashIfEmpty(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

func formatCount(n int) string {
	if n < 0 {
		return "?"
	}
	return fmt.Sprintf("%d", n)
}
