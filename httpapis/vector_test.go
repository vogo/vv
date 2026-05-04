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
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/vogo/vage/vector"
)

// vectorFixture wires the in-process MapVectorStore + HashEmbedder so
// the handlers exercise the full Embed -> Add -> Search -> render flow
// without external dependencies.
func vectorFixture(t *testing.T) (*vector.MapVectorStore, *vector.HashEmbedder) {
	t.Helper()
	return vector.NewMapVectorStore(), vector.NewHashEmbedder(64)
}

func doVectorJSON(t *testing.T, h http.HandlerFunc, method, target string, body any) *http.Response {
	t.Helper()
	var buf bytes.Buffer
	if body != nil {
		if err := json.NewEncoder(&buf).Encode(body); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	req := httptest.NewRequest(method, target, &buf)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	h(rr, req)
	return rr.Result()
}

func decodeBody(t *testing.T, resp *http.Response, out any) {
	t.Helper()
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		t.Fatalf("decode body: %v", err)
	}
}

func TestVectorAdd_HappyPath(t *testing.T) {
	store, emb := vectorFixture(t)
	resp := doVectorJSON(t, handleVectorAdd(store, emb), http.MethodPost, "/v1/vector/add",
		vectorAddRequest{ID: "doc-1", Text: "alpha topic body content"})

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d, want 201", resp.StatusCode)
	}
	var got vectorAddResponse
	decodeBody(t, resp, &got)
	if got.ID != "doc-1" {
		t.Errorf("id round-trip wrong: %q", got.ID)
	}
	if store.Len() != 1 {
		t.Errorf("Len = %d, want 1", store.Len())
	}
}

func TestVectorAdd_AutoIDWhenOmitted(t *testing.T) {
	store, emb := vectorFixture(t)
	resp := doVectorJSON(t, handleVectorAdd(store, emb), http.MethodPost, "/v1/vector/add",
		vectorAddRequest{Text: "alpha topic body content"})

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
	var got vectorAddResponse
	decodeBody(t, resp, &got)
	if got.ID == "" {
		t.Error("expected auto-allocated id")
	}
}

func TestVectorAdd_ExplicitEmbeddingShortCircuitsEmbedder(t *testing.T) {
	store, _ := vectorFixture(t)
	// embedder=nil — handler must not call it because embedding is provided.
	resp := doVectorJSON(t, handleVectorAdd(store, nil), http.MethodPost, "/v1/vector/add",
		vectorAddRequest{ID: "x", Text: "ignored body", Embedding: []float32{0.1, 0.2}})

	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("status = %d", resp.StatusCode)
	}
}

func TestVectorAdd_RequiresStore(t *testing.T) {
	_, emb := vectorFixture(t)
	resp := doVectorJSON(t, handleVectorAdd(nil, emb), http.MethodPost, "/v1/vector/add",
		vectorAddRequest{Text: "x"})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestVectorAdd_RequiresEmbedderWhenNoEmbedding(t *testing.T) {
	store, _ := vectorFixture(t)
	resp := doVectorJSON(t, handleVectorAdd(store, nil), http.MethodPost, "/v1/vector/add",
		vectorAddRequest{Text: "alpha"})
	if resp.StatusCode != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", resp.StatusCode)
	}
}

func TestVectorAdd_RequiresTextOrEmbedding(t *testing.T) {
	store, emb := vectorFixture(t)
	resp := doVectorJSON(t, handleVectorAdd(store, emb), http.MethodPost, "/v1/vector/add",
		vectorAddRequest{ID: "x"})
	if resp.StatusCode != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", resp.StatusCode)
	}
}

func TestVectorAdd_InvalidJSON(t *testing.T) {
	store, emb := vectorFixture(t)
	req := httptest.NewRequest(http.MethodPost, "/v1/vector/add", strings.NewReader("not json"))
	rr := httptest.NewRecorder()
	handleVectorAdd(store, emb)(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestVectorSearch_HappyPath(t *testing.T) {
	store, emb := vectorFixture(t)

	// Seed via Add path.
	for i, text := range []string{"alpha shared keyword tokens here", "beta shared keyword tokens here", "gamma orthogonal content"} {
		v, _ := emb.Embed(context.Background(), text)
		_ = store.Add(context.Background(), vector.Document{
			ID:   "doc-" + string(rune('0'+i)),
			Text: text, Embedding: v,
		})
	}

	target := "/v1/vector/search?" + url.Values{
		"q":     {"alpha shared keyword tokens here"},
		"top_k": {"2"},
	}.Encode()
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rr := httptest.NewRecorder()
	handleVectorSearch(store, emb)(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body=%s", rr.Code, rr.Body.String())
	}
	var got vectorSearchResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Hits) != 2 {
		t.Fatalf("hits = %d, want 2", len(got.Hits))
	}
	if got.Hits[0].ID != "doc-0" {
		t.Errorf("top hit = %q, want doc-0", got.Hits[0].ID)
	}
	if got.Hits[0].Text == "" {
		t.Error("text not propagated")
	}
}

func TestVectorSearch_RequiresQuery(t *testing.T) {
	store, emb := vectorFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/vector/search", nil)
	rr := httptest.NewRecorder()
	handleVectorSearch(store, emb)(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestVectorSearch_BadTopK(t *testing.T) {
	store, emb := vectorFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/vector/search?q=x&top_k=-1", nil)
	rr := httptest.NewRecorder()
	handleVectorSearch(store, emb)(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestVectorSearch_RequiresStore(t *testing.T) {
	_, emb := vectorFixture(t)
	req := httptest.NewRequest(http.MethodGet, "/v1/vector/search?q=x", nil)
	rr := httptest.NewRecorder()
	handleVectorSearch(nil, emb)(rr, req)
	if rr.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", rr.Code)
	}
}

func TestVectorSearch_BadMetadataJSON(t *testing.T) {
	store, emb := vectorFixture(t)
	target := "/v1/vector/search?q=x&metadata=" + url.QueryEscape("{not json")
	req := httptest.NewRequest(http.MethodGet, target, nil)
	rr := httptest.NewRecorder()
	handleVectorSearch(store, emb)(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestMapVectorError_DimensionMismatch(t *testing.T) {
	status, _ := mapVectorError(vector.ErrDimensionMismatch)
	if status != http.StatusConflict {
		t.Errorf("dimension mismatch -> %d, want 409", status)
	}
}

func TestMapVectorError_NotFound(t *testing.T) {
	status, _ := mapVectorError(vector.ErrNotFound)
	if status != http.StatusNotFound {
		t.Errorf("not found -> %d, want 404", status)
	}
}

func TestMapVectorError_NotSupported(t *testing.T) {
	status, _ := mapVectorError(vector.ErrNotSupported)
	if status != http.StatusNotImplemented {
		t.Errorf("not supported -> %d, want 501", status)
	}
}
