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

package setup

import (
	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/session"
	"github.com/vogo/vv/configs"
)

// controlPlaneEvents is the events.jsonl whitelist used by
// session.event_persist=control (the default).
//
// The list is a whitelist rather than a blocklist so a new event type
// added upstream defaults to "not persisted" — a silently growing log is
// worse than a missing line, and adding the type here is a one-liner.
//
// Three types are deliberately absent because their payload already has
// an owner file, and writing them here would store the same bytes twice
// (see doc/domains/core/session/storage-redesign.md § 3):
//
//   - schema.EventTextDelta  — per-token stream chunks; the assembled
//     text lives in messages.jsonl. This alone was 75% of events.jsonl.
//   - schema.EventToolResult — the tool body lives in messages.jsonl as
//     a tool message; tool_call_start/end keep the call metadata here.
//   - schema.EventContextBuilt — byte-equivalent to build_reports/*.json,
//     which (unlike an event) can be deserialised back.
var controlPlaneEvents = []string{
	// Agent / loop lifecycle.
	schema.EventAgentStart,
	schema.EventAgentEnd,
	schema.EventIterationStart,
	schema.EventError,

	// Tool calls (metadata only — the result body is in messages.jsonl).
	schema.EventToolCallStart,
	schema.EventToolCallEnd,

	// LLM calls (counts only, no prompt bodies).
	schema.EventLLMCallStart,
	schema.EventLLMCallEnd,
	schema.EventLLMCallError,

	// Orchestration.
	schema.EventPhaseStart,
	schema.EventPhaseEnd,
	schema.EventSubAgentStart,
	schema.EventSubAgentEnd,

	// Budget.
	schema.EventTokenBudgetExhausted,
	schema.EventBudgetWarn,
	schema.EventBudgetExceeded,

	// Skills.
	schema.EventSkillDiscover,
	schema.EventSkillActivate,
	schema.EventSkillDeactivate,
	schema.EventSkillResourceLoad,

	// Safety / interaction.
	schema.EventGuardCheck,
	schema.EventMCPCredentialDetected,
	schema.EventPendingInteraction,

	// Progress + persistence pointers.
	schema.EventTodoUpdate,
	schema.EventCheckpointWritten,
	schema.EventContextEdited,

	// Workspace and tree mutations.
	schema.EventWorkspacePlanUpdated,
	schema.EventWorkspaceNoteWritten,
	schema.EventWorkspaceScratchWritten,
	schema.EventWorkspaceArtifactWritten,
	schema.EventSessionTreeUpdated,
	schema.EventSessionTreePromotionStarted,
	schema.EventSessionTreePromotionCompleted,
	schema.EventSessionTreePromotionFailed,
}

// ControlPlaneEvents returns a copy of the default events.jsonl
// whitelist. Exported for tests and for operators inspecting the
// effective configuration.
func ControlPlaneEvents() []string {
	out := make([]string, len(controlPlaneEvents))
	copy(out, controlPlaneEvents)

	return out
}

// sessionHookOptions translates cfg.Session.EventPersist into SessionHook
// options.
//
// "none" keeps a single event type — agent_start, one line per run —
// rather than dropping everything: SessionHook's auto-create is what
// materialises meta.json in HTTP mode (CLI calls TouchSession itself),
// so a hook that never fires would leave sessions with a transcript but
// no metadata record. One ~120-byte line per run is the cheapest way to
// keep that invariant.
func sessionHookOptions(cfg *configs.Config) []session.Option {
	switch cfg.Session.EffectiveEventPersist() {
	case configs.EventPersistAll:
		return nil
	case configs.EventPersistNone:
		return []session.Option{session.WithFilter(schema.EventAgentStart)}
	default:
		return []session.Option{session.WithFilter(controlPlaneEvents...)}
	}
}
