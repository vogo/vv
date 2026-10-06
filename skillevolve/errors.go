package skillevolve

import (
	"fmt"
)

// ErrNotEligible is returned when a session fails the transcript qualification gate.
type ErrNotEligible struct {
	Eligibility Eligibility
}

func (e *ErrNotEligible) Error() string {
	if e == nil {
		return "session is not eligible for skill extraction"
	}
	if e.Eligibility.Reason != "" {
		return "session is not eligible: " + e.Eligibility.Reason
	}
	return "session is not eligible for skill extraction"
}

// ErrNotPending is returned when Approve/Reject is called on a non-pending proposal.
type ErrNotPending struct {
	ID     string
	Status ProposalStatus
}

func (e *ErrNotPending) Error() string {
	if e == nil {
		return "proposal is not pending"
	}
	return fmt.Sprintf("proposal %q is %s, not pending", e.ID, e.Status)
}

// ErrProposalNotFound is returned when a proposal id is unknown.
type ErrProposalNotFound struct {
	ID string
}

func (e *ErrProposalNotFound) Error() string {
	if e == nil {
		return "proposal not found"
	}
	return fmt.Sprintf("proposal %q not found", e.ID)
}

// ErrExtractFailed wraps an extractor / LLM failure.
type ErrExtractFailed struct {
	Err error
}

func (e *ErrExtractFailed) Error() string {
	if e == nil || e.Err == nil {
		return "skill extract failed"
	}
	return "skill extract failed: " + e.Err.Error()
}

func (e *ErrExtractFailed) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

// ErrInvalidProposalID is returned for a malformed proposal identifier.
type ErrInvalidProposalID struct {
	ID string
}

func (e *ErrInvalidProposalID) Error() string {
	if e == nil {
		return "invalid proposal id"
	}
	return fmt.Sprintf("invalid proposal id %q", e.ID)
}

// ErrSkillExists is returned when WriteSkillMD finds an existing skill directory.
type ErrSkillExists struct {
	Name string
}

func (e *ErrSkillExists) Error() string {
	if e == nil {
		return "skill exists"
	}
	return fmt.Sprintf("skill exists: %s", e.Name)
}
