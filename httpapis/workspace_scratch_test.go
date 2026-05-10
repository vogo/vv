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
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vogo/vage/workspace"
)

func newScratchHTTPWorkspace(t *testing.T) workspace.Workspace {
	t.Helper()
	ws, err := workspace.NewFileWorkspace(t.TempDir())
	if err != nil {
		t.Fatalf("NewFileWorkspace: %v", err)
	}
	return ws
}

func TestHandleListScratch_EmptySlot(t *testing.T) {
	ws := newScratchHTTPWorkspace(t)

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess/workspace/scratch/child", nil)
	r = withPathValue(r, "id", "sess")
	r = withPathValue(r, "slot", "child")
	handleListScratch(ws)(rr, r)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var resp scratchListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Slot != "child" {
		t.Errorf("slot = %q, want child", resp.Slot)
	}
	if len(resp.Entries) != 0 {
		t.Errorf("entries = %d, want 0", len(resp.Entries))
	}
}

func TestHandleListScratch_WithEntries(t *testing.T) {
	ws := newScratchHTTPWorkspace(t)
	ctx := context.Background()
	_ = ws.WriteScratch(ctx, "sess", "child", "first", "1")
	_ = ws.WriteScratch(ctx, "sess", "child", "second", "22")

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess/workspace/scratch/child", nil)
	r = withPathValue(r, "id", "sess")
	r = withPathValue(r, "slot", "child")
	handleListScratch(ws)(rr, r)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var resp scratchListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Entries) != 2 {
		t.Fatalf("entries = %d, want 2", len(resp.Entries))
	}
	for _, e := range resp.Entries {
		if e.UpdatedAt == "" {
			t.Errorf("UpdatedAt empty for %q", e.Name)
		}
	}
}

func TestHandleListScratch_InvalidSlot(t *testing.T) {
	ws := newScratchHTTPWorkspace(t)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess/workspace/scratch/..", nil)
	r = withPathValue(r, "id", "sess")
	r = withPathValue(r, "slot", "..")
	handleListScratch(ws)(rr, r)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}

func TestHandleGetScratch_RoundTrip(t *testing.T) {
	ws := newScratchHTTPWorkspace(t)
	_ = ws.WriteScratch(context.Background(), "sess", "child", "alpha", "# hello")

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess/workspace/scratch/child/alpha", nil)
	r = withPathValue(r, "id", "sess")
	r = withPathValue(r, "slot", "child")
	r = withPathValue(r, "name", "alpha")
	handleGetScratch(ws)(rr, r)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if got := rr.Body.String(); got != "# hello" {
		t.Errorf("body = %q, want '# hello'", got)
	}
	if ct := rr.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/markdown") {
		t.Errorf("Content-Type = %q, want text/markdown", ct)
	}
}

func TestHandleGetScratch_Missing404(t *testing.T) {
	ws := newScratchHTTPWorkspace(t)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess/workspace/scratch/child/missing", nil)
	r = withPathValue(r, "id", "sess")
	r = withPathValue(r, "slot", "child")
	r = withPathValue(r, "name", "missing")
	handleGetScratch(ws)(rr, r)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404; body=%s", rr.Code, rr.Body.String())
	}
}

func TestHandleGetArtifact_RoundTrip(t *testing.T) {
	ws := newScratchHTTPWorkspace(t)
	_, _ = ws.WriteArtifact(context.Background(), "sess", "report.bin", []byte("\x00\x01\x02 binary"))

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess/workspace/artifacts/report.bin", nil)
	r = withPathValue(r, "id", "sess")
	r = withPathValue(r, "name", "report.bin")
	handleGetArtifact(ws)(rr, r)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	if got := rr.Body.String(); got != "\x00\x01\x02 binary" {
		t.Errorf("body = %q", got)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("Content-Type = %q", ct)
	}
}

func TestHandleGetArtifact_Missing404(t *testing.T) {
	ws := newScratchHTTPWorkspace(t)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess/workspace/artifacts/missing.txt", nil)
	r = withPathValue(r, "id", "sess")
	r = withPathValue(r, "name", "missing.txt")
	handleGetArtifact(ws)(rr, r)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestHandleGetArtifact_InvalidName400(t *testing.T) {
	ws := newScratchHTTPWorkspace(t)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/sess/workspace/artifacts/..", nil)
	r = withPathValue(r, "id", "sess")
	r = withPathValue(r, "name", "..")
	handleGetArtifact(ws)(rr, r)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400", rr.Code)
	}
}
