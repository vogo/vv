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

package cli

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/vogo/vage/session/tree"
)

// TestPrintTree_NilStore covers the "subsystem disabled" message — the
// caller in main.go relies on the concrete error to exit non-zero.
func TestPrintTree_NilStore(t *testing.T) {
	var buf bytes.Buffer
	if err := PrintTree(context.Background(), nil, "x", false, &buf); err == nil {
		t.Errorf("PrintTree(nil store) returned nil error")
	}
}

// TestPrintTree_MissingTree confirms a missing tree prints a friendly
// message rather than surfacing the sentinel error.
func TestPrintTree_MissingTree(t *testing.T) {
	store := tree.NewMapTreeStore(tree.WithMapPromoter(tree.NoopPromoter{}))
	var buf bytes.Buffer
	if err := PrintTree(context.Background(), store, "absent", false, &buf); err != nil {
		t.Fatalf("PrintTree: %v", err)
	}
	if !strings.Contains(buf.String(), "no tree") {
		t.Errorf("missing-tree output = %q; want contains 'no tree'", buf.String())
	}
}

// TestPrintTree_RendersTree exercises the happy path with a populated tree.
func TestPrintTree_RendersTree(t *testing.T) {
	store := tree.NewMapTreeStore(tree.WithMapPromoter(tree.NoopPromoter{}))
	ctx := context.Background()
	root, err := store.CreateTree(ctx, "s1", tree.TreeNode{
		Type: tree.NodeGoal, Title: "ship login", Status: tree.StatusActive,
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	if _, err := store.AddNode(ctx, "s1", root.ID, tree.TreeNode{
		Type: tree.NodeSubtask, Title: "build handler", Status: tree.StatusPending,
	}); err != nil {
		t.Fatalf("add: %v", err)
	}

	var buf bytes.Buffer
	if err := PrintTree(ctx, store, "s1", false, &buf); err != nil {
		t.Fatalf("PrintTree: %v", err)
	}
	out := buf.String()
	for _, want := range []string{"ship login", "build handler", "Session Tree"} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, out)
		}
	}
}
