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
	"errors"
	"net/http"
	"strings"

	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/checkpoint"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/setup"
)

// resumeResponse is the JSON envelope returned by POST .../resume on
// success. Mirrors the in-memory schema.RunResponse projection used by
// the existing /run endpoints so clients that read both routes get a
// consistent shape.
type resumeResponse struct {
	SessionID  string `json:"session_id"`
	AgentID    string `json:"agent_id,omitempty"`
	StopReason string `json:"stop_reason,omitempty"`
	Duration   int64  `json:"duration_ms,omitempty"`
	// Messages is the assistant's final transcript suffix produced by the
	// resumed loop. Callers may need full message bodies for replay or
	// audit, so we surface them rather than just the last text.
	Messages []resumeMessage `json:"messages,omitempty"`
	// Usage carries cumulative token counts for the resumed segment.
	PromptTokens     int `json:"prompt_tokens,omitempty"`
	CompletionTokens int `json:"completion_tokens,omitempty"`
	TotalTokens      int `json:"total_tokens,omitempty"`
}

// resumeMessage is the trimmed projection of schema.Message used in the
// HTTP envelope. Only the fields a client needs for rendering / audit
// are exposed; raw aimodel.Content is flattened to text.
type resumeMessage struct {
	Role    string `json:"role"`
	Content string `json:"content,omitempty"`
	AgentID string `json:"agent_id,omitempty"`
}

// handleResumeSession implements POST /v1/sessions/{id}/resume. It loads
// the latest non-final checkpoint for the session, resolves the
// originating TaskAgent through initResult.ResumeAgent, and re-drives
// the ReAct loop to completion.
//
// HTTP status mapping:
//   - 200: resume completed; body = resumeResponse
//   - 400: empty session id (path param empty)
//   - 404: ErrCheckpointNotFound or ErrAgentNotFound
//   - 409: ErrAlreadyFinal (cp is terminal; start a new session)
//   - 422: cp.AgentID resolves to a non-TaskAgent (Router/Workflow/Custom)
//   - 501: ?stream=1 — streaming resume is not yet implemented (sync path
//     is the only supported shape; matches vage TaskAgent.Resume's
//     contract, which returns RunResponse, not RunStream)
//   - 503: session subsystem disabled OR IterationStore unset (setup wiring)
//   - 500: any other error from Load / Resume
func handleResumeSession(initResult *setup.InitResult) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		sid := strings.TrimSpace(r.PathValue("id"))
		if sid == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"code": "bad_request", "message": "session id is empty",
			})
			return
		}

		// Streaming form: 501 Not Implemented with a clear next-step
		// message. The Resume() entry point on TaskAgent is sync today;
		// supporting SSE here would require a ResumeStream method that
		// has not yet been added to vage.
		if r.URL.Query().Get("stream") == "1" {
			writeJSON(w, http.StatusNotImplemented, map[string]string{
				"code":    "not_implemented",
				"message": "streaming resume is not yet supported; omit ?stream=1 for sync resume",
			})
			return
		}

		// 503 paths — these are setup-time properties; no point asking
		// the user to retry.
		if initResult == nil || initResult.SessionStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"code":    "session_disabled",
				"message": setup.ErrSessionDisabled.Error(),
			})
			return
		}
		if initResult.IterationStore == nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{
				"code":    "iteration_store_missing",
				"message": setup.ErrNoIterationStore.Error(),
			})
			return
		}

		cp, err := initResult.IterationStore.Load(r.Context(), sid, "")
		switch {
		case errors.Is(err, checkpoint.ErrCheckpointNotFound):
			writeJSON(w, http.StatusNotFound, map[string]string{
				"code":    "checkpoint_not_found",
				"message": "no checkpoints exist for this session",
			})
			return
		case err != nil:
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"code": "load_failed", "message": err.Error(),
			})
			return
		}

		if cp.Final {
			writeJSON(w, http.StatusConflict, map[string]string{
				"code":    "already_final",
				"message": "session is already finalized; start a new session (fork support is not yet available)",
			})
			return
		}

		a, err := initResult.ResumeAgent(cp.AgentID)
		if err != nil {
			writeJSON(w, http.StatusNotFound, map[string]string{
				"code": "agent_not_found", "message": err.Error(),
			})
			return
		}

		ta, ok := a.(*taskagent.Agent)
		if !ok {
			writeJSON(w, http.StatusUnprocessableEntity, map[string]string{
				"code":    "agent_not_resumable",
				"message": "checkpoint references an agent kind that does not support Resume",
			})
			return
		}

		resp, err := ta.Resume(r.Context(), sid)
		if err != nil {
			if errors.Is(err, checkpoint.ErrAlreadyFinal) {
				// Race between Load and Resume — rare under concurrent
				// runs, surface the same 409 as the pre-check above.
				writeJSON(w, http.StatusConflict, map[string]string{
					"code":    "already_final",
					"message": "session became final during resume; start a new session",
				})
				return
			}
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"code": "resume_failed", "message": err.Error(),
			})
			return
		}

		// Successful resume — bump ResumeCount via the metrics hook.
		// Errors are logged inside the hook; we intentionally do not
		// fail the request when the metrics write fails because the
		// resume itself succeeded.
		if hook := initResult.MetricsHook; hook != nil {
			_ = hook.RecordResume(r.Context(), sid)
		}

		writeJSON(w, http.StatusOK, toResumeResponse(sid, cp.AgentID, resp))
	}
}

// toResumeResponse flattens a schema.RunResponse into the JSON envelope
// returned by POST .../resume. Token usage is collapsed to the three
// canonical counters so callers do not need to know the aimodel.Usage
// shape; the regular /run endpoints surface the full Usage struct, so
// this projection is intentionally narrow and resume-specific.
func toResumeResponse(sessionID, agentID string, resp *schema.RunResponse) resumeResponse {
	out := resumeResponse{SessionID: sessionID, AgentID: agentID}
	if resp == nil {
		return out
	}
	out.StopReason = string(resp.StopReason)
	out.Duration = resp.Duration
	if resp.Usage != nil {
		out.PromptTokens = resp.Usage.PromptTokens
		out.CompletionTokens = resp.Usage.CompletionTokens
		out.TotalTokens = resp.Usage.TotalTokens
	}
	for _, m := range resp.Messages {
		out.Messages = append(out.Messages, resumeMessage{
			Role:    string(m.Role),
			Content: m.Content.Text(),
			AgentID: m.AgentID,
		})
	}
	return out
}
