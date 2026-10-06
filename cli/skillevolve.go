package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/vogo/vv/skillevolve"
)

const skillEvolutionNotConfigured = "Skill evolution is not configured."

func isSkillCommand(cmd string) bool {
	switch cmd {
	case "/skill-extract", "/skill-proposals", "/skill-approve", "/skill-reject":
		return true
	default:
		return false
	}
}

func (a *App) runSkillCommand(ctx context.Context, parts []string) (string, error) {
	if len(parts) == 0 {
		return skillEvolutionNotConfigured, nil
	}
	switch parts[0] {
	case "/skill-extract":
		sid := ""
		if len(parts) > 1 {
			sid = parts[1]
		}
		return a.handleSkillExtract(ctx, sid)
	case "/skill-proposals":
		return a.handleSkillList(ctx)
	case "/skill-approve":
		if len(parts) < 2 {
			return "Usage: /skill-approve <proposal_id>", nil
		}
		return a.handleSkillApprove(ctx, parts[1])
	case "/skill-reject":
		if len(parts) < 2 {
			return "Usage: /skill-reject <proposal_id>", nil
		}
		return a.handleSkillReject(ctx, parts[1])
	default:
		return "", fmt.Errorf("unknown skill command %s", parts[0])
	}
}

func (a *App) handleSkillExtract(ctx context.Context, sessionID string) (string, error) {
	if a == nil || a.skillEvolve == nil {
		return skillEvolutionNotConfigured, nil
	}
	if sessionID == "" {
		sessionID = a.sessionID
	}
	p, err := a.skillEvolve.Extract(ctx, sessionID)
	if err != nil {
		if ne, ok := err.(*skillevolve.ErrNotEligible); ok {
			return "", fmt.Errorf("not eligible: %s", ne.Eligibility.Reason)
		}
		return "", err
	}
	return formatProposal(p), nil
}

func (a *App) handleSkillList(ctx context.Context) (string, error) {
	if a == nil || a.skillEvolve == nil {
		return skillEvolutionNotConfigured, nil
	}
	list, err := a.skillEvolve.List(ctx)
	if err != nil {
		return "", err
	}
	if len(list) == 0 {
		return "No skill proposals.", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Skill proposals (%d):\n", len(list))
	for _, p := range list {
		b.WriteString(formatProposal(p))
		b.WriteByte('\n')
	}
	return strings.TrimRight(b.String(), "\n"), nil
}

func (a *App) handleSkillApprove(ctx context.Context, id string) (string, error) {
	if a == nil || a.skillEvolve == nil {
		return skillEvolutionNotConfigured, nil
	}
	p, err := a.skillEvolve.Approve(ctx, id)
	if err != nil {
		return "", err
	}
	return formatProposal(p) + "\nregistered, next turn schema updated", nil
}

func (a *App) handleSkillReject(ctx context.Context, id string) (string, error) {
	if a == nil || a.skillEvolve == nil {
		return skillEvolutionNotConfigured, nil
	}
	p, err := a.skillEvolve.Reject(ctx, id)
	if err != nil {
		return "", err
	}
	return formatProposal(p), nil
}

func formatProposal(p skillevolve.Proposal) string {
	var b strings.Builder
	fmt.Fprintf(&b, "id=%s name=%s status=%s", p.ID, p.Extracted.Name, p.Status)
	if p.Extracted.Description != "" {
		fmt.Fprintf(&b, " description=%s", p.Extracted.Description)
	}
	if p.SimilarTo != "" {
		fmt.Fprintf(&b, " similar_to=%s", p.SimilarTo)
	}
	if p.Reason != "" {
		fmt.Fprintf(&b, " reason=%s", p.Reason)
	}
	return b.String()
}
