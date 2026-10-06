package skillevolve

import (
	"context"
	"time"

	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/skill"
)

// Eligibility is the transcript-only qualification gate for skill extraction.
type Eligibility struct {
	OK          bool
	Final       bool // latest checkpoint Final; required true by Eligible
	StopReason  schema.StopReason
	Turns       int // main-chain checkpoint count = len(Store.List)
	ToolCalls   int
	ToolErrors  int
	SuccessRate float64 // (ToolCalls-ToolErrors)/ToolCalls; 0 when ToolCalls==0
	Reason      string  // stable English short code when not OK
}

// ToolStep is one observed tool invocation, main-chain or sub-agent.
type ToolStep struct {
	Name    string
	AgentID string
	Success bool
	Args    string
}

// SessionFeatures is the analyzer output handed to an Extractor.
type SessionFeatures struct {
	SessionID       string
	Eligibility     Eligibility
	ToolSequence    []ToolStep
	SpawnedSpecs    []string
	UserTask        string
	LastAssistant   string
	SystemExcerpt   string
	ActivatedSkills []string
}

// ExtractedSkill is the normalized extraction result, never AllowedTools.
type ExtractedSkill struct {
	Name          string            `json:"name"`
	Description   string            `json:"description"`
	Instructions  string            `json:"instructions"`
	ObservedTools []string          `json:"observed_tools,omitempty"`
	Metadata      map[string]string `json:"metadata,omitempty"`
}

// Extractor turns session features into a skill draft.
type Extractor interface {
	Extract(ctx context.Context, feat SessionFeatures) (ExtractedSkill, error)
}

// ExtractorFunc adapts a function into an Extractor.
type ExtractorFunc func(ctx context.Context, feat SessionFeatures) (ExtractedSkill, error)

// Extract implements Extractor.
func (f ExtractorFunc) Extract(ctx context.Context, feat SessionFeatures) (ExtractedSkill, error) {
	return f(ctx, feat)
}

// ProposalStatus is the disk-queue lifecycle of one extraction.
type ProposalStatus string

const (
	StatusPending   ProposalStatus = "pending"
	StatusApproved  ProposalStatus = "approved"
	StatusRejected  ProposalStatus = "rejected"
	StatusDuplicate ProposalStatus = "duplicate"
)

// Proposal is one queued extraction, stored as JSON under skill_dir/.proposals/.
type Proposal struct {
	ID         string         `json:"id"`
	Status     ProposalStatus `json:"status"`
	SessionID  string         `json:"session_id"`
	Extracted  ExtractedSkill `json:"extracted"`
	SimilarTo  string         `json:"similar_to,omitempty"`
	Similarity float32        `json:"similarity,omitempty"`
	Reason     string         `json:"reason,omitempty"`
	CreatedAt  time.Time      `json:"created_at"`
	DecidedAt  time.Time      `json:"decided_at,omitempty"`
}

// Deduper finds an already-registered skill similar to an extraction.
type Deduper interface {
	FindDuplicate(ctx context.Context, extracted ExtractedSkill, listed []skill.Def) (id string, score float32, ok bool)
}

// SchemaRefresher overlays use_skill / spawn_worker ToolDef enums after a
// file skill is registered. Implemented by setup.skillSchemaRefresher.
type SchemaRefresher interface {
	Refresh() error
}

// Unstable eligibility reason codes (stable for tests and HTTP bodies).
const (
	ReasonNotFinal        = "not_final"
	ReasonStopReason      = "stop_reason"
	ReasonTooFewTurns     = "too_few_turns"
	ReasonNoToolCalls     = "no_tool_calls"
	ReasonToolSuccessRate = "tool_success_rate"
)
