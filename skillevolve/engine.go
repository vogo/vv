package skillevolve

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/vogo/vage/hook"
	"github.com/vogo/vage/skill"
	"github.com/vogo/vv/sessionlogs"
)

const (
	eventProposed   = "skill.proposed"
	eventRegistered = "skill.registered"
	eventRejected   = "skill.rejected"
)

// Engine orchestrates extract → dedup → queue → approve/reject.
type Engine struct {
	mu         sync.Mutex
	Logs       *sessionlogs.Store
	Extractor  Extractor
	Deduper    Deduper
	Queue      *FileQueue
	Registrar  *Registrar
	Hooks      *hook.Manager
	MinTurns   int
	MinSuccess float64
	Vage       skill.Registry
}

// Extract analyzes a session, extracts a skill, dedups, and enqueues a proposal.
func (e *Engine) Extract(ctx context.Context, sessionID string) (Proposal, error) {
	if e == nil {
		return Proposal{}, fmt.Errorf("skill evolution is not configured")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	feat, err := Analyze(ctx, e.Logs, sessionID)
	if err != nil {
		return Proposal{}, err
	}
	elig := Eligible(feat, e.MinTurns, e.MinSuccess)
	feat.Eligibility = elig
	if !elig.OK {
		return Proposal{}, &ErrNotEligible{Eligibility: elig}
	}

	draft, err := e.Extractor.Extract(ctx, feat)
	if err != nil {
		return Proposal{}, &ErrExtractFailed{Err: err}
	}
	draft, err = normalizeExtracted(draft, feat)
	if err != nil {
		return Proposal{}, &ErrExtractFailed{Err: err}
	}

	p := Proposal{
		SessionID: sessionID,
		Extracted: draft,
		Status:    StatusPending,
	}
	if e.Deduper != nil {
		if id, score, ok := e.Deduper.FindDuplicate(ctx, draft, listedFromRegistry(e.Vage)); ok {
			p.Status = StatusDuplicate
			p.SimilarTo = id
			p.Similarity = score
			p.Reason = "duplicate"
		}
	}

	saved, err := e.Queue.Put(p)
	if err != nil {
		return Proposal{}, err
	}
	dispatchSkillEvent(ctx, e.Hooks, sessionID, eventProposed, saved.ID, draft.Name, string(saved.Status))
	return saved, nil
}

// List returns queued proposals, newest first.
func (e *Engine) List(_ context.Context) ([]Proposal, error) {
	if e == nil || e.Queue == nil {
		return nil, fmt.Errorf("skill evolution is not configured")
	}
	return e.Queue.List()
}

// Approve writes SKILL.md, registers, and marks the proposal approved.
func (e *Engine) Approve(ctx context.Context, proposalID string) (Proposal, error) {
	if e == nil {
		return Proposal{}, fmt.Errorf("skill evolution is not configured")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p, err := e.Queue.Get(proposalID)
	if err != nil {
		return Proposal{}, err
	}
	if p.Status != StatusPending {
		return p, &ErrNotPending{ID: proposalID, Status: p.Status}
	}
	if e.Registrar == nil {
		return p, fmt.Errorf("skill registrar is not configured")
	}
	if _, err := e.Registrar.Register(ctx, p.Extracted); err != nil {
		return p, err
	}
	updated, err := e.Queue.UpdateStatus(proposalID, StatusApproved)
	if err != nil {
		slog.Error("vv: skill registered but proposal status update failed", "id", proposalID, "error", err)
		return p, err
	}
	dispatchSkillEvent(ctx, e.Hooks, p.SessionID, eventRegistered, updated.ID, p.Extracted.Name, string(StatusApproved))
	return updated, nil
}

// Reject marks a pending proposal rejected.
func (e *Engine) Reject(ctx context.Context, proposalID string) (Proposal, error) {
	if e == nil {
		return Proposal{}, fmt.Errorf("skill evolution is not configured")
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	p, err := e.Queue.Get(proposalID)
	if err != nil {
		return Proposal{}, err
	}
	if p.Status != StatusPending {
		return p, &ErrNotPending{ID: proposalID, Status: p.Status}
	}
	updated, err := e.Queue.UpdateStatus(proposalID, StatusRejected)
	if err != nil {
		return p, err
	}
	dispatchSkillEvent(ctx, e.Hooks, p.SessionID, eventRejected, updated.ID, p.Extracted.Name, string(StatusRejected))
	return updated, nil
}
