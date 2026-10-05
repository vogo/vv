package dispatches

import (
	"context"

	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/tool/bash"
	"github.com/vogo/vv/configs"
)

// NewDangerousBashPolicy returns an InterruptPolicy that flags bash tool
// calls whose merged classifier+guardian tier is Dangerous. Blocked,
// Caution, Safe, malformed args, and every other tool name are not
// flagged — Blocked stays a hard reject at the permission/bash layer,
// Caution keeps the existing confirm path, and non-bash tools never
// gain a human gate from this policy.
//
// Intercept is side-effect free and must stay cheap: it runs on the ReAct
// hot path once per tool-call iteration.
func NewDangerousBashPolicy(classifier *bash.Classifier, guardian *bash.PathGuardian) taskagent.InterruptPolicy {
	return taskagent.InterruptPolicyFunc(func(_ context.Context, _ string, calls []schema.ToolCall) []string {
		var pending []string
		for _, tc := range calls {
			if tc.Name != "bash" {
				continue
			}
			cls, ok := configs.ClassifyBashArgs(classifier, guardian, tc.Arguments)
			if !ok || cls.Tier != bash.TierDangerous {
				continue
			}
			pending = append(pending, tc.ID)
		}
		return pending
	})
}
