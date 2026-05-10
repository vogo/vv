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

	"github.com/vogo/vage/session"
)

// handleListChildren implements GET /v1/sessions/{id}/children.
//
// Returns every session whose ParentID equals {id}. The parent itself
// must exist (a 404 is more useful than an empty list when the caller
// has the wrong id) — but a parent that exists with zero dispatches
// returns 200 + empty list.
func handleListChildren(store session.SessionStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")

		// Parent existence check first — gives a stable 404 contract.
		if _, err := store.Get(r.Context(), id); err != nil {
			if errors.Is(err, session.ErrSessionNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{
					"code":    "not_found",
					"message": "session not found",
				})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"code":    "error",
				"message": err.Error(),
			})
			return
		}

		kids, err := session.ListChildren(r.Context(), store, id)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"code":    "error",
				"message": err.Error(),
			})
			return
		}

		out := sessionListResponse{
			Sessions: make([]sessionMetaResponse, len(kids)),
		}
		for i, c := range kids {
			out.Sessions[i] = toMetaResponse(c)
		}
		writeJSON(w, http.StatusOK, out)
	}
}
