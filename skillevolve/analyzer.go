package skillevolve

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/checkpoint"
	"github.com/vogo/vv/sessionlogs"
)

const (
	userTaskMaxBytes      = 2048
	lastAssistantMaxBytes = 2048
	systemExcerptMaxBytes = 1024
	spawnSpecMaxBytes     = 2048
)

// Analyze loads the session transcript and computes SessionFeatures.
// Illegal ids wrap checkpoint.ErrInvalidArgument; missing transcripts wrap
// checkpoint.ErrCheckpointNotFound.
func Analyze(ctx context.Context, logs *sessionlogs.Store, sessionID string) (SessionFeatures, error) {
	if logs == nil {
		return SessionFeatures{}, fmt.Errorf("%w: transcript store is nil", checkpoint.ErrInvalidArgument)
	}

	cp, err := logs.Load(ctx, sessionID, "")
	if err != nil {
		return SessionFeatures{}, fmt.Errorf("analyze session %q: %w", sessionID, err)
	}

	metas, err := logs.List(ctx, sessionID)
	if err != nil {
		return SessionFeatures{}, fmt.Errorf("analyze session %q list: %w", sessionID, err)
	}

	feat := SessionFeatures{
		SessionID: sessionID,
		Eligibility: Eligibility{
			Final:      cp.Final,
			StopReason: cp.StopReason,
			Turns:      len(metas),
		},
	}

	calls, errs := countTools(cp.Messages)
	feat.Eligibility.ToolCalls = calls
	feat.Eligibility.ToolErrors = errs
	if calls > 0 {
		feat.Eligibility.SuccessRate = float64(calls-errs) / float64(calls)
	}

	feat.ToolSequence = collectToolSequence(cp.Messages, sessionlogs.DefaultPrimaryAgentID)
	feat.SpawnedSpecs = collectSpawnedSpecs(cp.Messages)
	feat.ActivatedSkills = collectActivatedSkills(cp.Messages)
	feat.UserTask = truncateRunes(firstText(cp.Messages, schema.RoleUser), userTaskMaxBytes)
	feat.LastAssistant = truncateRunes(lastText(cp.Messages, schema.RoleAssistant), lastAssistantMaxBytes)
	feat.SystemExcerpt = truncateRunes(firstText(cp.Messages, schema.RoleSystem), systemExcerptMaxBytes)

	runs, err := logs.ListRuns(ctx, sessionID)
	if err != nil {
		return feat, nil
	}
	for _, run := range runs {
		rcp, loadErr := logs.LoadRun(ctx, sessionID, run.AgentID, run.Run)
		if loadErr != nil || rcp == nil {
			continue
		}
		feat.ToolSequence = append(feat.ToolSequence, collectToolSequence(rcp.Messages, run.AgentID)...)
		feat.SpawnedSpecs = append(feat.SpawnedSpecs, collectSpawnedSpecs(rcp.Messages)...)
	}

	return feat, nil
}

// Eligible applies the transcript qualification gate to already-computed features.
func Eligible(feat SessionFeatures, minTurns int, minSuccess float64) Eligibility {
	e := feat.Eligibility

	if !e.Final {
		e.OK = false
		e.Reason = ReasonNotFinal
		return e
	}
	if e.StopReason != schema.StopReasonComplete {
		e.OK = false
		e.Reason = ReasonStopReason
		return e
	}
	if e.Turns < minTurns {
		e.OK = false
		e.Reason = ReasonTooFewTurns
		return e
	}
	if e.ToolCalls < 1 {
		e.OK = false
		e.Reason = ReasonNoToolCalls
		return e
	}
	if e.SuccessRate < minSuccess {
		e.OK = false
		e.Reason = ReasonToolSuccessRate
		return e
	}
	e.OK = true
	e.Reason = ""
	return e
}

func countTools(msgs []schema.Message) (calls, errs int) {
	for _, msg := range msgs {
		if msg.Role() == schema.RoleAssistant {
			calls += len(msg.ToolCalls())
		}
		if msg.Role() == schema.RoleTool {
			for _, part := range msg.Parts() {
				if part.Type == schema.MessagePartToolResult && part.IsError {
					errs++
				}
			}
		}
	}
	return calls, errs
}

func collectToolSequence(msgs []schema.Message, agentID string) []ToolStep {
	type pending struct {
		name string
		args string
	}
	byID := map[string]pending{}
	var steps []ToolStep

	for _, msg := range msgs {
		if msg.Role() == schema.RoleAssistant {
			for _, tc := range msg.ToolCalls() {
				byID[tc.ID] = pending{name: tc.Name, args: tc.Arguments}
				steps = append(steps, ToolStep{Name: tc.Name, AgentID: agentID, Success: true, Args: tc.Arguments})
			}
		}
		if msg.Role() == schema.RoleTool {
			for _, part := range msg.Parts() {
				if part.Type != schema.MessagePartToolResult {
					continue
				}
				p, ok := byID[part.ToolCallID]
				if !ok {
					continue
				}
				for i := range steps {
					if steps[i].Name == p.name && steps[i].Args == p.args && steps[i].Success {
						if part.IsError {
							steps[i].Success = false
						}
						break
					}
				}
			}
		}
	}
	return steps
}

func collectSpawnedSpecs(msgs []schema.Message) []string {
	var out []string
	for _, msg := range msgs {
		if msg.Role() != schema.RoleAssistant {
			continue
		}
		for _, tc := range msg.ToolCalls() {
			if tc.Name != "spawn_worker" {
				continue
			}
			raw := tc.Arguments
			if len(raw) > spawnSpecMaxBytes {
				raw = raw[:spawnSpecMaxBytes]
			}
			if json.Valid([]byte(raw)) || raw != "" {
				out = append(out, raw)
			}
		}
	}
	return out
}

func collectActivatedSkills(msgs []schema.Message) []string {
	var out []string
	seen := map[string]bool{}
	for _, msg := range msgs {
		if msg.Role() != schema.RoleAssistant {
			continue
		}
		for _, tc := range msg.ToolCalls() {
			if tc.Name != "use_skill" {
				continue
			}
			var parsed struct {
				Skill string `json:"skill"`
			}
			if err := json.Unmarshal([]byte(tc.Arguments), &parsed); err != nil {
				continue
			}
			id := strings.TrimSpace(parsed.Skill)
			if id == "" || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func firstText(msgs []schema.Message, role schema.Role) string {
	for _, msg := range msgs {
		if msg.Role() == role {
			return msg.Text()
		}
	}
	return ""
}

func lastText(msgs []schema.Message, role schema.Role) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role() == role {
			return msgs[i].Text()
		}
	}
	return ""
}

func truncateRunes(s string, maxBytes int) string {
	if maxBytes <= 0 || len(s) <= maxBytes {
		return s
	}
	// Cut on byte boundary then drop a possible incomplete trailing rune.
	cut := s[:maxBytes]
	for len(cut) > 0 && cut[len(cut)-1]&0xc0 == 0x80 {
		cut = cut[:len(cut)-1]
	}
	return cut
}
