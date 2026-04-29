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
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/session"
)

// HTTP session-endpoint limits.
const (
	httpSessionListDefaultLimit = 50
	httpSessionListMaxLimit     = 200
	httpEventListDefaultLimit   = 1000
	httpEventListMaxLimit       = 5000
)

// sessionMetaResponse is the JSON projection of a session.Session for list /
// get responses.
type sessionMetaResponse struct {
	ID        string         `json:"id"`
	AgentID   string         `json:"agent_id,omitempty"`
	UserID    string         `json:"user_id,omitempty"`
	Title     string         `json:"title,omitempty"`
	State     string         `json:"state"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt string         `json:"created_at"`
	UpdatedAt string         `json:"updated_at"`
}

func toMetaResponse(s *session.Session) sessionMetaResponse {
	return sessionMetaResponse{
		ID:        s.ID,
		AgentID:   s.AgentID,
		UserID:    s.UserID,
		Title:     s.Title,
		State:     string(s.State),
		Metadata:  s.Metadata,
		CreatedAt: s.CreatedAt.UTC().Format("2006-01-02T15:04:05Z"),
		UpdatedAt: s.UpdatedAt.UTC().Format("2006-01-02T15:04:05Z"),
	}
}

type sessionListResponse struct {
	Sessions []sessionMetaResponse `json:"sessions"`
}

type sessionDetailResponse struct {
	sessionMetaResponse
	State map[string]any `json:"state,omitempty"`
}

type eventListResponse struct {
	Events []schema.Event `json:"events"`
}

type sessionPatchRequest struct {
	Title    *string         `json:"title,omitempty"`
	State    *string         `json:"state,omitempty"`
	Metadata *map[string]any `json:"metadata,omitempty"`
}

// handleListSessions implements GET /v1/sessions.
//
// Query params: user_id, agent_id, state, limit, offset.
// Filter values use AND semantics; unset fields are wildcards.
func handleListSessions(store session.SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()

		filter := session.SessionFilter{
			UserID:  q.Get("user_id"),
			AgentID: q.Get("agent_id"),
			State:   session.SessionState(q.Get("state")),
		}

		limit, ok := parsePositiveInt(q.Get("limit"), httpSessionListDefaultLimit, httpSessionListMaxLimit)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request", "message": "invalid limit"})
			return
		}
		filter.Limit = limit

		if v := q.Get("offset"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request", "message": "invalid offset"})
				return
			}
			filter.Offset = n
		}

		sessions, err := store.List(r.Context(), filter)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "error", "message": err.Error()})
			return
		}

		resp := sessionListResponse{Sessions: make([]sessionMetaResponse, len(sessions))}
		for i, s := range sessions {
			resp.Sessions[i] = toMetaResponse(s)
		}

		writeJSON(w, http.StatusOK, resp)
	}
}

// handleGetSession implements GET /v1/sessions/{id}.
// Returns full meta + state KV; 404 when the id is unknown.
func handleGetSession(store session.SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		s, err := store.Get(r.Context(), id)
		if writeSessionErr(w, err) {
			return
		}

		state, err := store.ListState(r.Context(), id)
		if writeSessionErr(w, err) {
			return
		}

		resp := sessionDetailResponse{
			sessionMetaResponse: toMetaResponse(s),
			State:               state,
		}
		writeJSON(w, http.StatusOK, resp)
	}
}

// handleListEvents implements GET /v1/sessions/{id}/events.
//
// Query params: type (comma-separated), limit. Returns the most recent
// `limit` events matching the type filter, in append order.
func handleListEvents(store session.SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		limit, ok := parsePositiveInt(r.URL.Query().Get("limit"), httpEventListDefaultLimit, httpEventListMaxLimit)
		if !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request", "message": "invalid limit"})
			return
		}

		typeFilter := parseTypeFilter(r.URL.Query().Get("type"))

		events, err := store.ListEvents(r.Context(), id)
		if writeSessionErr(w, err) {
			return
		}

		filtered := events
		if len(typeFilter) > 0 {
			// Allocate a fresh slice rather than aliasing events: store
			// implementations are documented to return a slice the caller
			// owns, but an explicit copy keeps that contract one-way only and
			// avoids a subtle dependency on iteration order matching write
			// order.
			filtered = make([]schema.Event, 0, len(events))
			for _, e := range events {
				if _, match := typeFilter[e.Type]; match {
					filtered = append(filtered, e)
				}
			}
		}

		// Tail-truncate to at most `limit` events so callers always see the
		// most recent activity. List endpoints with offset support belong to a
		// follow-up if pagination becomes necessary.
		if len(filtered) > limit {
			filtered = filtered[len(filtered)-limit:]
		}

		writeJSON(w, http.StatusOK, eventListResponse{Events: filtered})
	}
}

// handleDeleteSession implements DELETE /v1/sessions/{id}. The underlying
// store treats Delete as idempotent, so a missing id still returns 200.
func handleDeleteSession(store session.SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		if err := store.Delete(r.Context(), id); writeSessionErr(w, err) {
			return
		}

		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

// handlePatchSession implements PATCH /v1/sessions/{id}.
//
// Accepted body fields (all optional): title, state, metadata. Unset fields
// are preserved. Metadata semantics = full replacement of the map; merge is
// out of scope for the MVP.
func handlePatchSession(store session.SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		var patch sessionPatchRequest
		if err := json.NewDecoder(r.Body).Decode(&patch); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request", "message": "invalid request body"})
			return
		}

		s, err := store.Get(r.Context(), id)
		if writeSessionErr(w, err) {
			return
		}

		if patch.Title != nil {
			s.Title = *patch.Title
		}
		if patch.State != nil {
			next := session.SessionState(*patch.State)
			if !validSessionState(next) {
				writeJSON(w, http.StatusBadRequest, map[string]string{
					"code":    "bad_request",
					"message": "invalid state; must be one of active/paused/completed/failed",
				})
				return
			}
			s.State = next
		}
		if patch.Metadata != nil {
			s.Metadata = *patch.Metadata
		}

		if uerr := store.Update(r.Context(), s); writeSessionErr(w, uerr) {
			return
		}

		// Refetch so the response carries the store-refreshed UpdatedAt.
		fresh, err := store.Get(r.Context(), id)
		if writeSessionErr(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, toMetaResponse(fresh))
	}
}

// writeSessionErr translates a session-store error into the appropriate HTTP
// response and returns true when the response was written. err == nil means
// "no error; continue" and returns false.
func writeSessionErr(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, session.ErrSessionNotFound):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found", "message": err.Error()})
	case errors.Is(err, session.ErrInvalidArgument):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request", "message": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "error", "message": err.Error()})
	}
	return true
}

func validSessionState(s session.SessionState) bool {
	switch s {
	case session.StateActive, session.StatePaused, session.StateCompleted, session.StateFailed:
		return true
	}
	return false
}

// parsePositiveInt parses a positive integer from raw, returning (n, true)
// on success. Empty string returns (defaultVal, true). Out-of-range or
// non-numeric returns (0, false).
func parsePositiveInt(raw string, defaultVal, maxVal int) (int, bool) {
	if raw == "" {
		return defaultVal, true
	}
	n, err := strconv.Atoi(raw)
	if err != nil || n <= 0 {
		return 0, false
	}
	if n > maxVal {
		n = maxVal
	}
	return n, true
}

// parseTypeFilter splits a comma-separated event type filter into a set.
// Empty input returns nil (= no filter).
func parseTypeFilter(raw string) map[string]struct{} {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	parts := strings.Split(raw, ",")
	out := make(map[string]struct{}, len(parts))
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		out[p] = struct{}{}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
