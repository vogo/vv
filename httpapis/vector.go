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
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/vogo/vage/vector"
)

// writeError sends a uniform JSON error envelope. Mirrors the inline
// {"code","message"} pattern used elsewhere in this package; centralised
// here for vector handlers because they have many short paths.
func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{
		"code":    httpErrorCode(status),
		"message": msg,
	})
}

// httpErrorCode maps a status to a stable string client code.
func httpErrorCode(status int) string {
	switch status {
	case http.StatusBadRequest:
		return "bad_request"
	case http.StatusNotFound:
		return "not_found"
	case http.StatusConflict:
		return "conflict"
	case http.StatusServiceUnavailable:
		return "unavailable"
	case http.StatusBadGateway:
		return "upstream_error"
	case http.StatusNotImplemented:
		return "not_implemented"
	default:
		return "error"
	}
}

// rfc3339OrEmpty formats t as RFC 3339 UTC, returning "" for the zero
// value so JSON consumers can skip the field entirely.
func rfc3339OrEmpty(t time.Time) string {
	if t.IsZero() {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

// newRandomVectorID returns a 16-byte hex id. Used by handleVectorAdd
// when the caller omits the `id` field.
func newRandomVectorID() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

// vectorAddRequest is the body for POST /v1/vector/add.
//
// Either `embedding` is supplied directly (advanced callers, bulk
// ingest from offline pipelines) or `text` is supplied and the server
// embeds it. When both are supplied, `embedding` wins — saves a round
// trip and keeps the contract predictable.
type vectorAddRequest struct {
	ID        string         `json:"id,omitempty"`
	Text      string         `json:"text,omitempty"`
	Embedding []float32      `json:"embedding,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
}

type vectorAddResponse struct {
	ID string `json:"id"`
}

// vectorSearchHit is the JSON projection of a single hit on the search
// endpoint. We render Document inline rather than nested to keep the
// shape flat for typical clients.
type vectorSearchHit struct {
	ID        string         `json:"id"`
	Score     float32        `json:"score"`
	Text      string         `json:"text,omitempty"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt string         `json:"created_at,omitempty"`
}

type vectorSearchResponse struct {
	Hits []vectorSearchHit `json:"hits"`
}

// handleVectorAdd accepts either a pre-computed embedding or text +
// embedder. Returns 201 with the assigned/echoed id. 503 when subsystem
// disabled (caller did not pass a store at registration).
func handleVectorAdd(store vector.VectorStore, embedder vector.Embedder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeError(w, http.StatusServiceUnavailable, "vector subsystem disabled")
			return
		}
		var req vectorAddRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeError(w, http.StatusBadRequest, "invalid json body: "+err.Error())
			return
		}
		text := strings.TrimSpace(req.Text)
		emb := req.Embedding
		if len(emb) == 0 {
			if text == "" {
				writeError(w, http.StatusBadRequest, "either 'text' or 'embedding' is required")
				return
			}
			if embedder == nil {
				writeError(w, http.StatusServiceUnavailable, "embedder disabled — supply 'embedding' explicitly or enable an embedder")
				return
			}
			v, err := embedder.Embed(r.Context(), text)
			if err != nil {
				writeError(w, http.StatusBadGateway, "embedder error: "+err.Error())
				return
			}
			emb = v
		}

		id := strings.TrimSpace(req.ID)
		if id == "" {
			generated, err := newRandomVectorID()
			if err != nil {
				writeError(w, http.StatusInternalServerError, "cannot allocate id: "+err.Error())
				return
			}
			id = generated
		}

		doc := vector.Document{
			ID:        id,
			Text:      text,
			Embedding: emb,
			Metadata:  req.Metadata,
		}
		if err := store.Add(r.Context(), doc); err != nil {
			status, msg := mapVectorError(err)
			writeError(w, status, msg)
			return
		}

		writeJSON(w, http.StatusCreated, vectorAddResponse{ID: id})
	}
}

// handleVectorSearch parses query params, embeds the query, and runs
// store.Search. Query params:
//
//   - q (required) — query text
//   - top_k — int, default 5, hard-capped at 50
//   - min_score — float
//   - metadata — JSON-encoded equality filter object
func handleVectorSearch(store vector.VectorStore, embedder vector.Embedder) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if store == nil {
			writeError(w, http.StatusServiceUnavailable, "vector subsystem disabled")
			return
		}
		if embedder == nil {
			writeError(w, http.StatusServiceUnavailable, "embedder disabled")
			return
		}
		query := strings.TrimSpace(r.URL.Query().Get("q"))
		if query == "" {
			writeError(w, http.StatusBadRequest, "query parameter 'q' is required")
			return
		}

		topK := 0
		if v := r.URL.Query().Get("top_k"); v != "" {
			n, err := strconv.Atoi(v)
			if err != nil || n < 0 {
				writeError(w, http.StatusBadRequest, "top_k must be a non-negative integer")
				return
			}
			topK = n
		}
		const maxTopK = 50
		if topK > maxTopK {
			topK = maxTopK
		}

		var minScore float32
		if v := r.URL.Query().Get("min_score"); v != "" {
			s, err := strconv.ParseFloat(v, 32)
			if err != nil {
				writeError(w, http.StatusBadRequest, "min_score must be a float")
				return
			}
			minScore = float32(s)
		}

		var meta map[string]any
		if v := r.URL.Query().Get("metadata"); v != "" {
			if err := json.Unmarshal([]byte(v), &meta); err != nil {
				writeError(w, http.StatusBadRequest, "metadata must be a JSON object: "+err.Error())
				return
			}
		}

		vec, err := embedder.Embed(r.Context(), query)
		if err != nil {
			writeError(w, http.StatusBadGateway, "embedder error: "+err.Error())
			return
		}
		hits, err := store.Search(r.Context(), vec, vector.SearchOptions{
			TopK:           topK,
			MinScore:       minScore,
			MetadataEquals: meta,
		})
		if err != nil {
			status, msg := mapVectorError(err)
			writeError(w, status, msg)
			return
		}

		out := vectorSearchResponse{Hits: make([]vectorSearchHit, 0, len(hits))}
		for _, h := range hits {
			out.Hits = append(out.Hits, vectorSearchHit{
				ID:        h.Document.ID,
				Score:     h.Score,
				Text:      h.Document.Text,
				Metadata:  h.Document.Metadata,
				CreatedAt: rfc3339OrEmpty(h.Document.CreatedAt),
			})
		}
		writeJSON(w, http.StatusOK, out)
	}
}

// mapVectorError translates vector sentinels into HTTP status codes +
// stable user-facing messages.
func mapVectorError(err error) (int, string) {
	switch {
	case errors.Is(err, vector.ErrEmptyQuery):
		return http.StatusBadRequest, "query/text is empty"
	case errors.Is(err, vector.ErrDimensionMismatch):
		return http.StatusConflict, "dimension mismatch — embedder and store disagree on vector size"
	case errors.Is(err, vector.ErrNotFound):
		return http.StatusNotFound, "not found"
	case errors.Is(err, vector.ErrNotSupported):
		return http.StatusNotImplemented, "operation not supported by the configured backend"
	default:
		return http.StatusBadGateway, err.Error()
	}
}
