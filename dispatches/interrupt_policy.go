package dispatches

import (
	"context"

	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/interrupt"
	"github.com/vogo/vage/tool/bash"
	"github.com/vogo/vv/configs"
)

// dangerousBashPolicy flags Dangerous bash calls and snapshots the
// fingerprint the host computed from the same rule assembly. It is a
// named type so ResumeInterrupt can type-assert InterruptWitness; wrapping
// it as InterruptPolicyFunc would drop the snapshot.
type dangerousBashPolicy struct {
	classifier  *bash.Classifier
	guardian    *bash.PathGuardian
	fingerprint string
}

// NewDangerousBashPolicy returns an InterruptPolicy that flags bash tool
// calls whose merged classifier+guardian tier is Dangerous. Blocked,
// Caution, Safe, malformed args, and every other tool name are not
// flagged — Blocked stays a hard reject at the permission/bash layer,
// Caution keeps the existing confirm path, and non-bash tools never
// gain a human gate from this policy.
//
// fingerprint is the opaque string from configs.BashPolicyFingerprint
// for the rules this classifier was built from. The policy stores it and
// does not recompute it.
//
// Intercept is side-effect free and must stay cheap: it runs on the ReAct
// hot path once per tool-call iteration.
func NewDangerousBashPolicy(classifier *bash.Classifier, guardian *bash.PathGuardian, fingerprint string) taskagent.InterruptPolicy {
	return &dangerousBashPolicy{
		classifier:  classifier,
		guardian:    guardian,
		fingerprint: fingerprint,
	}
}

// Intercept implements taskagent.InterruptPolicy.
func (p *dangerousBashPolicy) Intercept(_ context.Context, _ string, calls []schema.ToolCall) []string {
	var pending []string
	for _, tc := range calls {
		if tc.Name != "bash" {
			continue
		}
		cls, ok := configs.ClassifyBashArgs(p.classifier, p.guardian, tc.Arguments)
		if !ok || cls.Tier != bash.TierDangerous {
			continue
		}
		pending = append(pending, tc.ID)
	}
	return pending
}

// Witness implements taskagent.InterruptWitness. Flagged ids are exactly
// the ids Intercept returns for the same calls. Classification is
// tier plus rule name; this package's consumer stores the string and
// does not parse it.
func (p *dangerousBashPolicy) Witness(ctx context.Context, sessionID string, calls []schema.ToolCall) interrupt.PolicySnapshot {
	pending := p.Intercept(ctx, sessionID, calls)
	flagged := make(map[string]struct{}, len(pending))
	for _, id := range pending {
		flagged[id] = struct{}{}
	}
	snap := interrupt.PolicySnapshot{
		Fingerprint: p.fingerprint,
		Calls:       make([]interrupt.CallAssessment, 0, len(calls)),
	}
	for _, tc := range calls {
		_, isFlagged := flagged[tc.ID]
		assessment := interrupt.CallAssessment{ToolCallID: tc.ID, Flagged: isFlagged}
		if isFlagged {
			if cls, ok := configs.ClassifyBashArgs(p.classifier, p.guardian, tc.Arguments); ok {
				assessment.Classification = "tier=" + cls.Tier.String() + ";rule=" + cls.Rule
			}
		}
		snap.Calls = append(snap.Calls, assessment)
	}
	return snap
}
