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

	"github.com/vogo/vage/session/tree"
)

// newTreeMux mounts every tree route on a fresh ServeMux and returns it
// alongside the underlying store. Tests prefer this over httpHarness for
// speed (no actual server boot, no port allocation).
func newTreeMux(t *testing.T) (*http.ServeMux, tree.SessionTreeStore) {
	t.Helper()
	store := tree.NewMapTreeStore(tree.WithMapPromoter(tree.NoopPromoter{}))
	mux := http.NewServeMux()
	mux.HandleFunc("GET /v1/sessions/{id}/tree", handleGetTree(store))
	mux.HandleFunc("POST /v1/sessions/{id}/tree", handleCreateTree(store))
	mux.HandleFunc("DELETE /v1/sessions/{id}/tree", handleDeleteTree(store))
	mux.HandleFunc("POST /v1/sessions/{id}/tree/nodes", handleAddNode(store))
	mux.HandleFunc("PATCH /v1/sessions/{id}/tree/nodes/{nid}", handleUpdateNode(store))
	mux.HandleFunc("DELETE /v1/sessions/{id}/tree/nodes/{nid}", handleDeleteNode(store))
	mux.HandleFunc("POST /v1/sessions/{id}/tree/cursor", handleSetCursor(store))
	mux.HandleFunc("POST /v1/sessions/{id}/tree/promote/{nid}", handlePromote(store))
	return mux, store
}

func doJSON(t *testing.T, mux http.Handler, method, path, body string) *httptest.ResponseRecorder {
	t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	return w
}

func decodeJSON(t *testing.T, w *httptest.ResponseRecorder, dst any) {
	t.Helper()
	if err := json.Unmarshal(w.Body.Bytes(), dst); err != nil {
		t.Fatalf("decode: %v\nbody=%s", err, w.Body.String())
	}
}

// TestTreeHTTP_CreateGetAddPatchDelete walks the full happy-path lifecycle
// in one test: create root → list → add child → patch child → delete child.
// Single test keeps the per-step state grokkable.
func TestTreeHTTP_CreateGetAddPatchDelete(t *testing.T) {
	mux, _ := newTreeMux(t)

	// 1. POST /tree creates root
	w := doJSON(t, mux, "POST", "/v1/sessions/sess-1/tree",
		`{"title":"ship login","summary":"deliver MVP","status":"active"}`)
	if w.Code != http.StatusCreated {
		t.Fatalf("create root: code=%d body=%s", w.Code, w.Body.String())
	}
	var root treeNodeResponse
	decodeJSON(t, w, &root)
	if root.Type != "goal" || root.Title != "ship login" {
		t.Errorf("root mismatch: %+v", root)
	}

	// 2. GET /tree returns the tree
	w = doJSON(t, mux, "GET", "/v1/sessions/sess-1/tree", "")
	if w.Code != http.StatusOK {
		t.Fatalf("get tree: code=%d", w.Code)
	}
	var tr treeResponse
	decodeJSON(t, w, &tr)
	if len(tr.Nodes) != 1 || tr.Nodes[0].ID != root.ID {
		t.Errorf("tree state unexpected: %+v", tr)
	}

	// 3. POST /nodes adds a child
	body := `{"parent_id":"` + root.ID + `","node":{"title":"build handler","status":"pending"}}`
	w = doJSON(t, mux, "POST", "/v1/sessions/sess-1/tree/nodes", body)
	if w.Code != http.StatusCreated {
		t.Fatalf("add node: code=%d body=%s", w.Code, w.Body.String())
	}
	var child treeNodeResponse
	decodeJSON(t, w, &child)
	if child.Parent != root.ID {
		t.Errorf("child parent = %q, want %q", child.Parent, root.ID)
	}

	// 4. PATCH child to mark done
	w = doJSON(t, mux, "PATCH", "/v1/sessions/sess-1/tree/nodes/"+child.ID,
		`{"status":"done"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("patch: code=%d body=%s", w.Code, w.Body.String())
	}
	var patched treeNodeResponse
	decodeJSON(t, w, &patched)
	if patched.Status != "done" {
		t.Errorf("patched status = %q, want done", patched.Status)
	}

	// 5. DELETE child
	w = doJSON(t, mux, "DELETE", "/v1/sessions/sess-1/tree/nodes/"+child.ID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("delete: code=%d", w.Code)
	}
}

// TestTreeHTTP_CursorAndPromote checks the side-channel ops.
func TestTreeHTTP_CursorAndPromote(t *testing.T) {
	mux, store := newTreeMux(t)
	ctx := context.Background()

	root, err := store.CreateTree(ctx, "s2", tree.TreeNode{
		Type: tree.NodeGoal, Title: "g", Status: tree.StatusActive,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	for range 2 {
		_, _ = store.AddNode(ctx, "s2", root.ID, tree.TreeNode{
			Type: tree.NodeSubtask, Title: "step", Status: tree.StatusPending,
		})
	}

	// SetCursor
	w := doJSON(t, mux, "POST", "/v1/sessions/s2/tree/cursor",
		`{"node_id":"`+root.ID+`"}`)
	if w.Code != http.StatusOK {
		t.Fatalf("cursor: code=%d body=%s", w.Code, w.Body.String())
	}

	// Promote
	w = doJSON(t, mux, "POST", "/v1/sessions/s2/tree/promote/"+root.ID, "")
	if w.Code != http.StatusOK {
		t.Fatalf("promote: code=%d body=%s", w.Code, w.Body.String())
	}
	var promoted treeNodeResponse
	decodeJSON(t, w, &promoted)
	// Children should now be promoted; verify via store.
	tr, _ := store.GetTree(ctx, "s2")
	for _, cid := range tr.Nodes[root.ID].Children {
		if !tr.Nodes[cid].Promoted {
			t.Errorf("child %s not promoted after HTTP promote", cid)
		}
	}
}

// TestTreeHTTP_IncludePromoted confirms the query flag plumbs through
// GetTreeView so default GET hides folded nodes and ?include_promoted=1
// surfaces them.
func TestTreeHTTP_IncludePromoted(t *testing.T) {
	mux, store := newTreeMux(t)
	ctx := context.Background()
	root, _ := store.CreateTree(ctx, "s3", tree.TreeNode{Type: tree.NodeGoal, Title: "g", Status: tree.StatusActive})
	_, _ = store.AddNode(ctx, "s3", root.ID, tree.TreeNode{Type: tree.NodeSubtask, Title: "c1", Status: tree.StatusPending})
	_, _ = store.PromoteNode(ctx, "s3", root.ID)

	// default: promoted dropped
	w := doJSON(t, mux, "GET", "/v1/sessions/s3/tree", "")
	var def treeResponse
	decodeJSON(t, w, &def)
	if len(def.Nodes) != 1 {
		t.Errorf("default GET returned %d nodes; want 1 (promoted hidden)", len(def.Nodes))
	}

	// with flag: promoted included
	w = doJSON(t, mux, "GET", "/v1/sessions/s3/tree?include_promoted=1", "")
	var full treeResponse
	decodeJSON(t, w, &full)
	if len(full.Nodes) != 2 {
		t.Errorf("?include_promoted GET returned %d nodes; want 2", len(full.Nodes))
	}
}

// TestTreeHTTP_Errors covers the error mapping: missing tree → 404, bad body
// → 400, deleting root → 400.
func TestTreeHTTP_Errors(t *testing.T) {
	mux, _ := newTreeMux(t)

	// 404 missing tree
	w := doJSON(t, mux, "GET", "/v1/sessions/none/tree", "")
	if w.Code != http.StatusNotFound {
		t.Errorf("missing tree code=%d, want 404", w.Code)
	}

	// 400 bad body on create
	w = doJSON(t, mux, "POST", "/v1/sessions/x/tree", `{not json`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("bad body code=%d, want 400", w.Code)
	}

	// 400 invalid title (empty)
	w = doJSON(t, mux, "POST", "/v1/sessions/x/tree", `{}`)
	if w.Code != http.StatusBadRequest {
		t.Errorf("missing title code=%d, want 400", w.Code)
	}
}

// TestTreeHTTP_DeleteTree wipes the entire tree via DELETE.
func TestTreeHTTP_DeleteTree(t *testing.T) {
	mux, store := newTreeMux(t)
	ctx := context.Background()
	_, _ = store.CreateTree(ctx, "s4", tree.TreeNode{Type: tree.NodeGoal, Title: "g", Status: tree.StatusActive})

	w := doJSON(t, mux, "DELETE", "/v1/sessions/s4/tree", "")
	if w.Code != http.StatusOK {
		t.Errorf("delete tree code=%d, want 200", w.Code)
	}
	if _, err := store.GetTree(ctx, "s4"); err == nil {
		t.Errorf("tree still exists after DELETE")
	}
}
