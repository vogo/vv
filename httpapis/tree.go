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
	"time"

	"github.com/vogo/vage/session/tree"
)

// treeResponse is the JSON projection of a tree.SessionTree for the GET
// endpoint. Nodes are returned as an array (root-first) to keep ordering
// stable across language clients that don't preserve map iteration order.
type treeResponse struct {
	SessionID string             `json:"session_id"`
	RootID    string             `json:"root_id,omitempty"`
	Cursor    string             `json:"cursor,omitempty"`
	Nodes     []treeNodeResponse `json:"nodes"`
	UpdatedAt string             `json:"updated_at"`
}

// treeNodeResponse mirrors tree.TreeNode with timestamps formatted to RFC
// 3339. Promoted_at is omitted when zero so the on-the-wire shape matches
// the on-disk format.
type treeNodeResponse struct {
	ID          string         `json:"id"`
	Type        string         `json:"type"`
	Status      string         `json:"status"`
	Title       string         `json:"title"`
	Summary     string         `json:"summary,omitempty"`
	ContentRef  string         `json:"content_ref,omitempty"`
	EmbeddingID string         `json:"embedding_id,omitempty"`
	Evidence    []string       `json:"evidence,omitempty"`
	Supersedes  []string       `json:"supersedes,omitempty"`
	Pinned      bool           `json:"pinned,omitempty"`
	Promoted    bool           `json:"promoted,omitempty"`
	PromotedAt  string         `json:"promoted_at,omitempty"`
	Parent      string         `json:"parent,omitempty"`
	Children    []string       `json:"children,omitempty"`
	Depth       int            `json:"depth"`
	CreatedAt   string         `json:"created_at"`
	UpdatedAt   string         `json:"updated_at"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

// createTreeRequest is the body for POST /v1/sessions/{id}/tree (create root)
// and is also reused inside addNodeRequest for the child payload.
type createTreeRequest struct {
	Type     string         `json:"type,omitempty"`
	Title    string         `json:"title"`
	Summary  string         `json:"summary,omitempty"`
	Status   string         `json:"status,omitempty"`
	Pinned   bool           `json:"pinned,omitempty"`
	Metadata map[string]any `json:"metadata,omitempty"`
}

func (c createTreeRequest) toNode() tree.TreeNode {
	return tree.TreeNode{
		Type:     tree.NodeType(c.Type),
		Status:   tree.NodeStatus(c.Status),
		Title:    c.Title,
		Summary:  c.Summary,
		Pinned:   c.Pinned,
		Metadata: c.Metadata,
	}
}

type addNodeRequest struct {
	ParentID string            `json:"parent_id"`
	Node     createTreeRequest `json:"node"`
}

type updateNodeRequest struct {
	Title      *string         `json:"title,omitempty"`
	Summary    *string         `json:"summary,omitempty"`
	Status     *string         `json:"status,omitempty"`
	Pinned     *bool           `json:"pinned,omitempty"`
	ContentRef *string         `json:"content_ref,omitempty"`
	Metadata   *map[string]any `json:"metadata,omitempty"`
}

type setCursorRequest struct {
	NodeID string `json:"node_id"`
}

func toTreeNodeResponse(n *tree.TreeNode) treeNodeResponse {
	out := treeNodeResponse{
		ID:          n.ID,
		Type:        string(n.Type),
		Status:      string(n.Status),
		Title:       n.Title,
		Summary:     n.Summary,
		ContentRef:  n.ContentRef,
		EmbeddingID: n.EmbeddingID,
		Evidence:    n.Evidence,
		Supersedes:  n.Supersedes,
		Pinned:      n.Pinned,
		Promoted:    n.Promoted,
		Parent:      n.Parent,
		Children:    n.Children,
		Depth:       n.Depth,
		CreatedAt:   n.CreatedAt.UTC().Format(time.RFC3339),
		UpdatedAt:   n.UpdatedAt.UTC().Format(time.RFC3339),
		Metadata:    n.Metadata,
	}
	if !n.PromotedAt.IsZero() {
		out.PromotedAt = n.PromotedAt.UTC().Format(time.RFC3339)
	}
	return out
}

func toTreeResponse(t *tree.SessionTree) treeResponse {
	out := treeResponse{
		SessionID: t.SessionID,
		RootID:    t.RootID,
		Cursor:    t.Cursor,
		UpdatedAt: t.UpdatedAt.UTC().Format(time.RFC3339),
	}
	out.Nodes = make([]treeNodeResponse, 0, len(t.Nodes))
	// Emit root first so clients can render BFS without an extra walk.
	if root, ok := t.Nodes[t.RootID]; ok {
		out.Nodes = append(out.Nodes, toTreeNodeResponse(root))
	}
	for id, n := range t.Nodes {
		if id == t.RootID {
			continue
		}
		out.Nodes = append(out.Nodes, toTreeNodeResponse(n))
	}
	return out
}

// handleGetTree implements GET /v1/sessions/{id}/tree.
//
// Query params: include_promoted=1 returns promoted (folded) nodes too.
// Default view drops promoted subtrees for compactness — matching the
// SessionTreeSource render in the LLM prompt.
func handleGetTree(store tree.SessionTreeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		opts := tree.ViewOptions{
			IncludePromoted: parseBoolQuery(r.URL.Query().Get("include_promoted")),
		}
		t, err := store.GetTreeView(r.Context(), id, opts)
		if writeTreeErr(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, toTreeResponse(t))
	}
}

// handleCreateTree implements POST /v1/sessions/{id}/tree, creating the
// root node from the request body.
//
// Default 'type' is goal — the only legitimate value for a root in the
// current model. Clients can still override it but the store will reject
// anything other than goal at the type-validation step.
func handleCreateTree(store tree.SessionTreeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var req createTreeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request", "message": "invalid request body"})
			return
		}
		node := req.toNode()
		if node.Type == "" {
			node.Type = tree.NodeGoal
		}
		root, err := store.CreateTree(r.Context(), id, node)
		if writeTreeErr(w, err) {
			return
		}
		writeJSON(w, http.StatusCreated, toTreeNodeResponse(root))
	}
}

// handleAddNode implements POST /v1/sessions/{id}/tree/nodes.
//
// Default 'type' is subtask — the most common shape for an LLM-driven plan
// step. Clients pin a different type explicitly.
func handleAddNode(store tree.SessionTreeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var req addNodeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request", "message": "invalid request body"})
			return
		}
		node := req.Node.toNode()
		if node.Type == "" {
			node.Type = tree.NodeSubtask
		}
		n, err := store.AddNode(r.Context(), id, req.ParentID, node)
		if writeTreeErr(w, err) {
			return
		}
		writeJSON(w, http.StatusCreated, toTreeNodeResponse(n))
	}
}

// handleUpdateNode implements PATCH /v1/sessions/{id}/tree/nodes/{nid}.
//
// PATCH semantics: omitted fields keep their current values. The server
// reads the current node and merges supplied pointers in.
func handleUpdateNode(store tree.SessionTreeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		nid := r.PathValue("nid")

		var req updateNodeRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request", "message": "invalid request body"})
			return
		}

		t, err := store.GetTree(r.Context(), id)
		if writeTreeErr(w, err) {
			return
		}
		cur, ok := t.Nodes[nid]
		if !ok {
			writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found", "message": "node not found"})
			return
		}

		next := *cur
		if req.Title != nil {
			next.Title = *req.Title
		}
		if req.Summary != nil {
			next.Summary = *req.Summary
		}
		if req.Status != nil {
			next.Status = tree.NodeStatus(*req.Status)
		}
		if req.Pinned != nil {
			next.Pinned = *req.Pinned
		}
		if req.ContentRef != nil {
			next.ContentRef = *req.ContentRef
		}
		if req.Metadata != nil {
			next.Metadata = *req.Metadata
		}
		// Type/Parent must not be sent to UpdateNode (they are immutable).
		next.Type = ""
		next.Parent = ""

		out, err := store.UpdateNode(r.Context(), id, next)
		if writeTreeErr(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, toTreeNodeResponse(out))
	}
}

// handleDeleteNode implements DELETE /v1/sessions/{id}/tree/nodes/{nid}.
func handleDeleteNode(store tree.SessionTreeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		nid := r.PathValue("nid")
		err := store.DeleteNode(r.Context(), id, nid)
		if writeTreeErr(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

// handleSetCursor implements POST /v1/sessions/{id}/tree/cursor.
func handleSetCursor(store tree.SessionTreeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var req setCursorRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request", "message": "invalid request body"})
			return
		}
		err := store.SetCursor(r.Context(), id, req.NodeID)
		if writeTreeErr(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"cursor": req.NodeID})
	}
}

// handlePromote implements POST /v1/sessions/{id}/tree/promote/{nid}.
func handlePromote(store tree.SessionTreeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		nid := r.PathValue("nid")
		out, err := store.PromoteNode(r.Context(), id, nid)
		if writeTreeErr(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, toTreeNodeResponse(out))
	}
}

// handleDeleteTree implements DELETE /v1/sessions/{id}/tree.
func handleDeleteTree(store tree.SessionTreeStore) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		err := store.DeleteTree(r.Context(), id)
		if writeTreeErr(w, err) {
			return
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "deleted"})
	}
}

// writeTreeErr maps a SessionTreeStore error into the appropriate HTTP
// response, returning true when a response was emitted (so the caller can
// early-return).
func writeTreeErr(w http.ResponseWriter, err error) bool {
	if err == nil {
		return false
	}
	switch {
	case errors.Is(err, tree.ErrInvalidArgument), errors.Is(err, tree.ErrImmutableField):
		writeJSON(w, http.StatusBadRequest, map[string]string{"code": "bad_request", "message": err.Error()})
	case errors.Is(err, tree.ErrNotFound), errors.Is(err, tree.ErrTreeMissing):
		writeJSON(w, http.StatusNotFound, map[string]string{"code": "not_found", "message": err.Error()})
	case errors.Is(err, tree.ErrAlreadyExists):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "conflict", "message": err.Error()})
	case errors.Is(err, tree.ErrHasChildren):
		writeJSON(w, http.StatusConflict, map[string]string{"code": "conflict", "message": err.Error()})
	case errors.Is(err, tree.ErrTreeFull):
		writeJSON(w, http.StatusUnprocessableEntity, map[string]string{"code": "tree_full", "message": err.Error()})
	default:
		writeJSON(w, http.StatusInternalServerError, map[string]string{"code": "error", "message": err.Error()})
	}
	return true
}

// parseBoolQuery interprets a query-string flag. Empty / "0" / "false" /
// "no" → false; anything else → true. Aligned with the convention already
// used elsewhere in this package (see e.g. include_promoted).
func parseBoolQuery(raw string) bool {
	if raw == "" {
		return false
	}
	if b, err := strconv.ParseBool(raw); err == nil {
		return b
	}
	// Tolerate "1" / "yes" / etc. without erroring.
	switch raw {
	case "1", "yes", "y", "on":
		return true
	}
	return false
}
