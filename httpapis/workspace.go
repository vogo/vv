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

package httpapis

import (
	"errors"
	"net/http"
	"time"

	"github.com/vogo/vage/workspace"
)

// noteListItem is the JSON projection of workspace.NoteInfo for the index
// endpoint. UpdatedAt is rendered in RFC 3339 to match other endpoints
// (sessions, events).
type noteListItem struct {
	Name      string `json:"name"`
	Bytes     int    `json:"bytes"`
	UpdatedAt string `json:"updated_at"`
}

type noteListResponse struct {
	Notes []noteListItem `json:"notes"`
}

// handleGetPlan implements GET /v1/sessions/{id}/workspace/plan.
//
// Returns plan.md as text/markdown. A missing plan returns 404 (rather
// than empty 200) so clients can distinguish "no workspace yet" from
// "empty plan deliberately written".
func handleGetPlan(ws workspace.Workspace) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		plan, err := ws.ReadPlan(r.Context(), id)
		if writeWorkspaceErr(w, err) {
			return
		}
		if plan == "" {
			writeJSON(w, http.StatusNotFound, map[string]string{
				"code":    "not_found",
				"message": "no plan recorded for session",
			})
			return
		}

		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = w.Write([]byte(plan))
	}
}

// handleListNotes implements GET /v1/sessions/{id}/workspace/notes.
//
// Returns the index of notes (name, bytes, updated_at) ordered by recency.
func handleListNotes(ws workspace.Workspace) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		notes, err := ws.ListNotes(r.Context(), id)
		if writeWorkspaceErr(w, err) {
			return
		}

		out := noteListResponse{Notes: make([]noteListItem, len(notes))}
		for i, n := range notes {
			out.Notes[i] = noteListItem{
				Name:      n.Name,
				Bytes:     n.Bytes,
				UpdatedAt: n.UpdatedAt.UTC().Format(time.RFC3339),
			}
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// handleGetNote implements GET /v1/sessions/{id}/workspace/notes/{name}.
//
// Returns the note body as text/markdown. Invalid names return 400; missing
// notes return 404.
func handleGetNote(ws workspace.Workspace) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		name := r.PathValue("name")

		body, err := ws.ReadNote(r.Context(), id, name)
		if writeWorkspaceErr(w, err) {
			return
		}
		if body == "" {
			writeJSON(w, http.StatusNotFound, map[string]string{
				"code":    "not_found",
				"message": "no note with that name",
			})
			return
		}

		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}
}

// writeWorkspaceErr translates a workspace error into the appropriate HTTP
// response and returns true when the response was written. err == nil
// returns false ("continue").
func writeWorkspaceErr(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, workspace.ErrInvalidName), errors.Is(err, workspace.ErrInvalidSession):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request", "message": err.Error()})
	case errors.Is(err, workspace.ErrTooLarge), errors.Is(err, workspace.ErrTooManyNotes):
		writeJSON(w, http.StatusRequestEntityTooLarge, map[string]string{"code": "too_large", "message": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "error", "message": err.Error()})
	}
	return true
}
