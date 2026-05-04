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
	"context"
	"errors"
	"fmt"
	"io"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/checkpoint"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/setup"
)

// ErrAgentNotResumable is returned when the resolved agent is not a
// *taskagent.Agent — only TaskAgents support per-iteration checkpoint /
// Resume. Other agent shapes (Router/Workflow/Custom) write no
// checkpoints, so reaching this path indicates a checkpoint produced by
// a TaskAgent that has since been replaced with a non-resumable kind.
//
// The "not found" / "session disabled" / "no iteration store" sentinels
// are hosted in vv/setup so the HTTP layer can reference the same
// values without depending on cli (which carries TUI deps).
var ErrAgentNotResumable = errors.New("vv: resolved agent does not support Resume")

// RunResume continues the most recent ReAct iteration of a previous run
// that was interrupted before its terminal checkpoint. The function:
//
//  1. validates the session subsystem and IterationStore are wired;
//  2. loads the latest checkpoint for sessionID (Load with id="");
//  3. resolves the originating agent by checkpoint AgentID:
//     - "primary" → Dispatcher.Primary()
//     - any other id → Result.Agent(id) (dispatchable sub-agents)
//  4. type-asserts the agent to *taskagent.Agent (only TaskAgents
//     persist checkpoints) and calls its Resume(ctx, sessionID);
//  5. renders the final assistant message to stdout and a one-line
//     stats summary to stderr.
//
// Errors are surfaced verbatim except ErrAlreadyFinal / ErrCheckpointNotFound,
// which are wrapped with user-facing guidance: the original error remains
// in the chain (errors.Is recognises it) and the wrapper text tells the
// human what to do next.
func RunResume(
	ctx context.Context,
	initResult *setup.InitResult,
	sessionID string,
	stdout io.Writer,
	stderr io.Writer,
) error {
	if initResult == nil {
		return fmt.Errorf("vv: init result is nil")
	}
	if initResult.SessionStore == nil {
		return setup.ErrSessionDisabled
	}
	if initResult.IterationStore == nil {
		return setup.ErrNoIterationStore
	}
	if sessionID == "" {
		return fmt.Errorf("vv: --resume requires a non-empty session id")
	}

	cp, err := initResult.IterationStore.Load(ctx, sessionID, "")
	switch {
	case errors.Is(err, checkpoint.ErrCheckpointNotFound):
		return fmt.Errorf("vv: no checkpoints found for session %q (start a new session with `vv` or `vv --session %s`): %w",
			sessionID, sessionID, err)
	case err != nil:
		return fmt.Errorf("vv: load checkpoint: %w", err)
	}

	if cp.Final {
		return fmt.Errorf("vv: session %q already finalized; start a new session (fork support is not yet available): %w",
			sessionID, checkpoint.ErrAlreadyFinal)
	}

	a, err := initResult.ResumeAgent(cp.AgentID)
	if err != nil {
		return err
	}

	ta, ok := a.(*taskagent.Agent)
	if !ok {
		return fmt.Errorf("%w: agent_id=%q kind=%T", ErrAgentNotResumable, cp.AgentID, a)
	}

	_, _ = fmt.Fprintf(stderr,
		"[resume] session=%s agent=%s checkpoint_seq=%d iteration=%d messages=%d\n",
		sessionID, cp.AgentID, cp.Sequence, cp.Iteration, len(cp.Messages))

	resp, err := ta.Resume(ctx, sessionID)
	if err != nil {
		if errors.Is(err, checkpoint.ErrAlreadyFinal) {
			// Race: the cp transitioned to final between Load and Resume
			// (rare, but possible under concurrent runs). Surface the
			// same friendly guidance as the pre-check above.
			return fmt.Errorf("vv: session %q became final during resume; start a new session: %w",
				sessionID, err)
		}
		return fmt.Errorf("vv: resume: %w", err)
	}

	// Successful resume — bump ResumeCount in metrics. Failures are
	// logged inside the hook and intentionally ignored here so a
	// transient store hiccup cannot mask a real Resume return value.
	if hook := initResult.MetricsHook; hook != nil {
		_ = hook.RecordResume(ctx, sessionID)
	}

	renderResumeResponse(stdout, stderr, resp)
	return nil
}

// renderResumeResponse prints the final assistant text to stdout and a
// one-line summary to stderr. Mirrors the trailing-summary shape used by
// RunPrompt so the two non-interactive paths look familiar to the same
// user.
func renderResumeResponse(stdout, stderr io.Writer, resp *schema.RunResponse) {
	if resp == nil {
		return
	}

	final := lastAssistantText(resp.Messages)
	if final != "" {
		_, _ = fmt.Fprintln(stdout, final)
	}

	stats := execStats{
		DurationMs: resp.Duration,
	}
	if resp.Usage != nil {
		stats.PromptTokens = resp.Usage.PromptTokens
		stats.CompletionTokens = resp.Usage.CompletionTokens
	}

	_, _ = fmt.Fprintln(stderr)
	_, _ = fmt.Fprintf(stderr, "[done] %s stop=%s\n", buildStatsLine(stats), resp.StopReason)
}

// lastAssistantText returns the text body of the last assistant message
// in msgs, or "" when the response has no assistant text (tool-only
// terminus, error path, or empty).
func lastAssistantText(msgs []schema.Message) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		m := msgs[i]
		if m.Role != aimodel.RoleAssistant {
			continue
		}
		text := m.Content.Text()
		if text != "" {
			return text
		}
	}
	return ""
}
