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
	"net/http"
	"time"

	"github.com/vogo/vage/workspace"
)

// scratchListResponse is the JSON envelope for GET .../scratch/{slot}.
// Slot is echoed back so a client that hits a generic listing endpoint
// can confirm the response matches the request.
type scratchListResponse struct {
	Slot    string         `json:"slot"`
	Entries []noteListItem `json:"entries"`
}

// handleListScratch implements GET /v1/sessions/{id}/workspace/scratch/{slot}.
//
// Returns the index of entries (name, bytes, updated_at) ordered by recency.
// A missing slot returns 200 + empty list so a client polling a child
// agent's slot before the agent writes anything sees the same shape it
// will see after.
func handleListScratch(ws workspace.Workspace) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		slot := r.PathValue("slot")

		entries, err := ws.ListScratch(r.Context(), id, slot)
		if writeWorkspaceErr(w, err) {
			return
		}

		out := scratchListResponse{
			Slot:    slot,
			Entries: make([]noteListItem, len(entries)),
		}
		for i, e := range entries {
			out.Entries[i] = noteListItem{
				Name:      e.Name,
				Bytes:     e.Bytes,
				UpdatedAt: e.UpdatedAt.UTC().Format(time.RFC3339),
			}
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// handleGetScratch implements GET /v1/sessions/{id}/workspace/scratch/{slot}/{name}.
//
// Returns the entry body as text/markdown. Invalid id/slot/name → 400;
// missing entry → 404.
func handleGetScratch(ws workspace.Workspace) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		slot := r.PathValue("slot")
		name := r.PathValue("name")

		body, err := ws.ReadScratch(r.Context(), id, slot, name)
		if writeWorkspaceErr(w, err) {
			return
		}
		if body == "" {
			writeJSON(w, http.StatusNotFound, map[string]string{
				"code":    "not_found",
				"message": "no scratch entry with that name in slot",
			})
			return
		}

		w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
		_, _ = w.Write([]byte(body))
	}
}

// handleGetArtifact implements GET /v1/sessions/{id}/workspace/artifacts/{name}.
//
// Returns the artifact body as application/octet-stream — artifacts are
// arbitrary bytes (diffs, logs, reports), not necessarily markdown.
// Invalid id/name → 400; missing artifact → 404.
func handleGetArtifact(ws workspace.Workspace) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		name := r.PathValue("name")

		body, err := ws.ReadArtifact(r.Context(), id, name)
		if writeWorkspaceErr(w, err) {
			return
		}
		if body == nil {
			writeJSON(w, http.StatusNotFound, map[string]string{
				"code":    "not_found",
				"message": "no artifact with that name",
			})
			return
		}

		w.Header().Set("Content-Type", "application/octet-stream")
		_, _ = w.Write(body)
	}
}
