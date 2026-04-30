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

package dispatches

import (
	"context"
	"testing"

	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/session/tree"
)

func newTreeStore() tree.SessionTreeStore {
	return tree.NewMapTreeStore(tree.WithMapPromoter(tree.NoopPromoter{}))
}

// TestMirrorPlan_Disabled covers the zero-cost default path: store unset
// or feature flag off ⇒ no tree state created.
func TestMirrorPlan_Disabled(t *testing.T) {
	store := newTreeStore()
	d := &Dispatcher{} // store nil, writeTree false
	plan := &Plan{Goal: "ship login", Steps: []PlanStep{{ID: "1", Description: "add handler", Agent: "coder"}}}
	d.maybeMirrorPlanToTree(context.Background(), plan, &schema.RunRequest{SessionID: "s1"})
	if _, err := store.GetTree(context.Background(), "s1"); err == nil {
		t.Errorf("expected ErrTreeMissing when feature disabled")
	}
}

// TestMirrorPlan_FirstPlanCreatesRoot verifies the bootstrap path: an empty
// session gets a goal root + N subtask children matching plan.Steps.
func TestMirrorPlan_FirstPlanCreatesRoot(t *testing.T) {
	store := newTreeStore()
	d := &Dispatcher{treeStore: store, writeTree: true}
	plan := &Plan{
		Goal: "ship login",
		Steps: []PlanStep{
			{ID: "1", Description: "add handler", Agent: "coder"},
			{ID: "2", Description: "add tests", Agent: "coder", DependsOn: []string{"1"}},
		},
	}
	d.maybeMirrorPlanToTree(context.Background(), plan, &schema.RunRequest{SessionID: "s1"})

	tr, err := store.GetTree(context.Background(), "s1")
	if err != nil {
		t.Fatalf("GetTree: %v", err)
	}
	root := tr.Nodes[tr.RootID]
	if root.Type != tree.NodeGoal {
		t.Errorf("root type = %q, want goal", root.Type)
	}
	if len(root.Children) != 2 {
		t.Fatalf("root.Children = %v, want 2", root.Children)
	}
	for _, cid := range root.Children {
		c := tr.Nodes[cid]
		if c.Type != tree.NodeSubtask {
			t.Errorf("step type = %q, want subtask", c.Type)
		}
		if c.Metadata["agent"] != "coder" {
			t.Errorf("step agent metadata = %v, want coder", c.Metadata["agent"])
		}
	}
}

// TestMirrorPlan_SecondPlanBatches asserts a fresh plan invocation under an
// existing tree creates a new batch node rather than overwriting the goal.
func TestMirrorPlan_SecondPlanBatches(t *testing.T) {
	store := newTreeStore()
	d := &Dispatcher{treeStore: store, writeTree: true}
	first := &Plan{Goal: "first goal", Steps: []PlanStep{{ID: "1", Description: "x", Agent: "coder"}}}
	second := &Plan{Goal: "second goal", Steps: []PlanStep{{ID: "1", Description: "y", Agent: "coder"}}}
	ctx := context.Background()
	d.maybeMirrorPlanToTree(ctx, first, &schema.RunRequest{SessionID: "s1"})
	d.maybeMirrorPlanToTree(ctx, second, &schema.RunRequest{SessionID: "s1"})

	tr, _ := store.GetTree(ctx, "s1")
	root := tr.Nodes[tr.RootID]
	// First plan attaches its 1 step directly under root; the second plan
	// adds a batch node alongside, so root has 2 children: the original
	// step (leaf) and the batch (with the second plan's step under it).
	if len(root.Children) != 2 {
		t.Fatalf("want first-plan-step + second-plan-batch under root, got %d children", len(root.Children))
	}
	var batch *tree.TreeNode
	for _, cid := range root.Children {
		c := tr.Nodes[cid]
		if c.Metadata != nil && c.Metadata["kind"] == "plan_batch" {
			batch = c
			break
		}
	}
	if batch == nil {
		t.Fatalf("no plan_batch node found under root")
	}
	if len(batch.Children) != 1 {
		t.Errorf("batch has %d children, want 1", len(batch.Children))
	}
}

// TestMirrorPlan_NoSession exercises the silent skip when SessionID is empty.
func TestMirrorPlan_NoSession(t *testing.T) {
	store := newTreeStore()
	d := &Dispatcher{treeStore: store, writeTree: true}
	plan := &Plan{Goal: "x", Steps: []PlanStep{{ID: "1", Description: "y", Agent: "coder"}}}
	d.maybeMirrorPlanToTree(context.Background(), plan, &schema.RunRequest{}) // no SessionID
	// no panic, no state — verify zero trees exist
	if _, err := store.GetTree(context.Background(), ""); err == nil {
		t.Errorf("did not expect a tree to be created without SessionID")
	}
}
