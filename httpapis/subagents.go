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
	"strconv"
	"strings"

	"github.com/vogo/vv/sessionlogs"
	"github.com/vogo/vv/setup"
)

// subAgentsResponse is the envelope for GET .../subagents.
type subAgentsResponse struct {
	SessionID string                `json:"session_id"`
	Runs      []sessionlogs.RunMeta `json:"runs"`
}

// subAgentRunResponse is the envelope for GET .../subagents/{agent}/{run}.
type subAgentRunResponse struct {
	SessionID  string               `json:"session_id"`
	AgentID    string               `json:"agent_id"`
	Run        int                  `json:"run"`
	Iterations int                  `json:"iterations"`
	Final      bool                 `json:"final"`
	StopReason string               `json:"stop_reason,omitempty"`
	Messages   []subAgentMessage    `json:"messages,omitempty"`
	Usage      subAgentUsagePayload `json:"usage"`
}

type subAgentMessage struct {
	Role    string `json:"role"`
	Content string `json:"content,omitempty"`
	AgentID string `json:"agent_id,omitempty"`
}

type subAgentUsagePayload struct {
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
}

// transcriptUnavailable writes the 503 shared by both sub-agent routes.
// The sub-agent transcripts exist only when the session subsystem is on
// AND the wired checkpoint backend is the transcript store, so "not
// configured" is a server-state answer, not a missing resource.
func transcriptUnavailable(w http.ResponseWriter) {
	writeJSON(w, http.StatusServiceUnavailable, map[string]string{
		"code":    "subagent_transcripts_disabled",
		"message": "sub-agent transcripts are not configured (session subsystem off, or a non-transcript checkpoint store is wired)",
	})
}

// handleListSubAgents implements GET /v1/sessions/{id}/subagents.
//
// Returns one entry per delegated dispatch — the answer to "where did
// the specialist's work go". Replaces GET .../children, which keyed off
// Session.ParentID and therefore always returned an empty list.
func handleListSubAgents(initResult *setup.InitResult) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		if id == "" {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"code": "bad_request", "message": "session id is empty",
			})

			return
		}

		store := initResult.TranscriptStore()
		if store == nil {
			transcriptUnavailable(w)

			return
		}

		runs, err := store.ListRuns(r.Context(), id)
		if err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"code": "error", "message": err.Error(),
			})

			return
		}

		if runs == nil {
			runs = []sessionlogs.RunMeta{}
		}

		writeJSON(w, http.StatusOK, subAgentsResponse{SessionID: id, Runs: runs})
	}
}

// handleGetSubAgentRun implements
// GET /v1/sessions/{id}/subagents/{agent}/{run}, returning the final
// state of one dispatch.
func handleGetSubAgentRun(initResult *setup.InitResult) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		id := strings.TrimSpace(r.PathValue("id"))
		agentID := strings.TrimSpace(r.PathValue("agent"))

		run, err := strconv.Atoi(strings.TrimSpace(r.PathValue("run")))
		if id == "" || agentID == "" || err != nil || run <= 0 {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"code": "bad_request", "message": "session id, agent id and a positive run number are required",
			})

			return
		}

		store := initResult.TranscriptStore()
		if store == nil {
			transcriptUnavailable(w)

			return
		}

		cp, err := store.LoadRun(r.Context(), id, agentID, run)
		if err != nil {
			if errors.Is(err, sessionlogs.ErrRunNotFound) {
				writeJSON(w, http.StatusNotFound, map[string]string{
					"code": "not_found", "message": "sub-agent run not found",
				})

				return
			}

			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"code": "error", "message": err.Error(),
			})

			return
		}

		msgs := make([]subAgentMessage, 0, len(cp.Messages))
		for _, m := range cp.Messages {
			msgs = append(msgs, subAgentMessage{
				Role:    string(m.Role()),
				Content: m.Text(),
				AgentID: m.AgentID,
			})
		}

		writeJSON(w, http.StatusOK, subAgentRunResponse{
			SessionID:  id,
			AgentID:    cp.AgentID,
			Run:        run,
			Iterations: cp.Sequence,
			Final:      cp.Final,
			StopReason: string(cp.StopReason),
			Messages:   msgs,
			Usage: subAgentUsagePayload{
				PromptTokens:     cp.Usage.PromptTokens,
				CompletionTokens: cp.Usage.CompletionTokens,
				TotalTokens:      cp.Usage.TotalTokens,
			},
		})
	}
}
