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
	"testing"
	"time"

	"github.com/vogo/vage/session"
)

func newChildrenStore(t *testing.T) session.SessionStore {
	t.Helper()
	store := session.NewMapSessionStore()
	ctx := context.Background()
	now := time.Now()

	parent := &session.Session{
		ID: "parent", State: session.StateActive,
		CreatedAt: now, UpdatedAt: now,
	}
	if err := store.Create(ctx, parent); err != nil {
		t.Fatalf("seed parent: %v", err)
	}

	for _, cid := range []string{"child-a", "child-b"} {
		c := &session.Session{
			ID: cid, ParentID: "parent", AgentID: "sub",
			State:     session.StateActive,
			CreatedAt: now, UpdatedAt: now,
		}
		if err := store.Create(ctx, c); err != nil {
			t.Fatalf("seed %s: %v", cid, err)
		}
	}

	// An unrelated session that must NOT show up.
	if err := store.Create(ctx, &session.Session{
		ID: "solo", State: session.StateActive,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed solo: %v", err)
	}
	return store
}

func TestHandleListChildren_OK(t *testing.T) {
	store := newChildrenStore(t)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/parent/children", nil)
	r = withPathValue(r, "id", "parent")
	handleListChildren(store)(rr, r)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var resp sessionListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Sessions) != 2 {
		t.Fatalf("len = %d, want 2", len(resp.Sessions))
	}
	for _, s := range resp.Sessions {
		if s.ParentID != "parent" {
			t.Errorf("ParentID = %q, want parent", s.ParentID)
		}
	}
}

func TestHandleListChildren_ParentNotFound(t *testing.T) {
	store := newChildrenStore(t)
	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/missing/children", nil)
	r = withPathValue(r, "id", "missing")
	handleListChildren(store)(rr, r)

	if rr.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", rr.Code)
	}
}

func TestHandleListChildren_ParentExistsNoChildren(t *testing.T) {
	store := session.NewMapSessionStore()
	ctx := context.Background()
	now := time.Now()
	if err := store.Create(ctx, &session.Session{
		ID: "lonely", State: session.StateActive,
		CreatedAt: now, UpdatedAt: now,
	}); err != nil {
		t.Fatalf("seed: %v", err)
	}

	rr := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodGet, "/v1/sessions/lonely/children", nil)
	r = withPathValue(r, "id", "lonely")
	handleListChildren(store)(rr, r)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rr.Code, rr.Body.String())
	}
	var resp sessionListResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(resp.Sessions) != 0 {
		t.Errorf("len = %d, want 0", len(resp.Sessions))
	}
}
