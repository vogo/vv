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
	"errors"
	"log/slog"

	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/session/tree"
)

// maxTreeTitleBytes is just below tree.TitleMaxBytes (200) so a goal/step
// description that the LLM produced can be safely truncated without
// triggering the store's validation reject.
const maxTreeTitleBytes = 180

// maxTreeSummaryBytes mirrors tree.SummaryMaxBytes (2 KiB) for the same
// reason; we leave 48 bytes of headroom for ellipsis and goal-prefixing.
const maxTreeSummaryBytes = 2000

// maybeMirrorPlanToTree writes plan as SessionTree nodes when the
// dispatcher has been wired with a tree store and the writeTree feature
// flag is true. The mirror is best-effort: every error is logged but the
// plan execution path is never blocked.
//
// Effect on the tree:
//   - When no tree exists, a goal root is created from plan.Goal.
//   - When a tree already exists, plan.Goal becomes a subtask under root.
//     This batches multiple plan_task invocations under the same session.
//   - Each plan step becomes a subtask child of the batch node, capturing
//     agent id and step id in metadata for traceability.
//
// Step status is left at "pending" — flipping it to "done" on completion
// is a future iteration; the dispatcher does not currently observe per-step
// completion in a way that's easy to wire here.
func (d *Dispatcher) maybeMirrorPlanToTree(ctx context.Context, plan *Plan, req *schema.RunRequest) {
	if d == nil || d.treeStore == nil || !d.writeTree {
		return
	}
	if plan == nil || len(plan.Steps) == 0 {
		return
	}
	sessionID := ""
	if req != nil {
		sessionID = req.SessionID
	}
	if sessionID == "" {
		// No session id ⇒ no addressable tree. Skip silently.
		return
	}

	parentID, err := d.ensureBatchNode(ctx, sessionID, plan)
	if err != nil {
		slog.Warn("dispatcher: tree mirror skipped",
			"session_id", sessionID, "error", err)
		return
	}

	for _, step := range plan.Steps {
		title := truncateBytes(stepTitle(step), maxTreeTitleBytes)
		summary := truncateBytes(step.Description, maxTreeSummaryBytes)
		md := map[string]any{
			"source":  "dispatcher",
			"agent":   step.Agent,
			"step_id": step.ID,
		}
		if _, addErr := d.treeStore.AddNode(ctx, sessionID, parentID, tree.TreeNode{
			Type:     tree.NodeSubtask,
			Status:   tree.StatusPending,
			Title:    title,
			Summary:  summary,
			Metadata: md,
		}); addErr != nil {
			slog.Warn("dispatcher: tree mirror add step failed",
				"session_id", sessionID, "step_id", step.ID, "error", addErr)
		}
	}
}

// ensureBatchNode creates the tree if missing and returns the parent under
// which the plan steps should be hung. The first plan in a session creates
// a goal root from plan.Goal; subsequent plans hang from a fresh subtask
// node so each plan_task invocation is visually grouped.
func (d *Dispatcher) ensureBatchNode(ctx context.Context, sessionID string, plan *Plan) (string, error) {
	tr, err := d.treeStore.GetTree(ctx, sessionID)
	if errors.Is(err, tree.ErrTreeMissing) {
		root, cerr := d.treeStore.CreateTree(ctx, sessionID, tree.TreeNode{
			Type:    tree.NodeGoal,
			Status:  tree.StatusActive,
			Title:   truncateBytes(planTitle(plan), maxTreeTitleBytes),
			Summary: truncateBytes(plan.Goal, maxTreeSummaryBytes),
			Metadata: map[string]any{
				"source": "dispatcher",
			},
		})
		if cerr != nil {
			return "", cerr
		}
		return root.ID, nil
	}
	if err != nil {
		return "", err
	}

	// Attach a fresh subtask "batch" under root so multi-plan sessions are
	// visually distinguishable. The original goal stays at the root.
	batch, err := d.treeStore.AddNode(ctx, sessionID, tr.RootID, tree.TreeNode{
		Type:    tree.NodeSubtask,
		Status:  tree.StatusActive,
		Title:   truncateBytes(planTitle(plan), maxTreeTitleBytes),
		Summary: truncateBytes(plan.Goal, maxTreeSummaryBytes),
		Metadata: map[string]any{
			"source": "dispatcher",
			"kind":   "plan_batch",
		},
	})
	if err != nil {
		return "", err
	}
	return batch.ID, nil
}

// planTitle picks the human-facing title for the plan's batch / root node.
// It prefers plan.Goal trimmed; falls back to "plan" so the validation
// "title is empty" error never fires on a well-formed plan.
func planTitle(plan *Plan) string {
	if plan != nil && plan.Goal != "" {
		return plan.Goal
	}
	return "plan"
}

// stepTitle produces a non-empty title for a plan step. Description is the
// preferred source; on empty description we fall back to "<agent> step".
func stepTitle(step PlanStep) string {
	if step.Description != "" {
		return step.Description
	}
	if step.Agent != "" {
		return step.Agent + " step"
	}
	return "step"
}

// truncateBytes clamps s to maxBytes on a utf-8 boundary. Used so callers
// who feed unbounded LLM output into the tree never hit the store's hard
// validation cap (TitleMaxBytes / SummaryMaxBytes).
func truncateBytes(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && (s[cut]&0xC0) == 0x80 {
		cut--
	}
	return s[:cut]
}
