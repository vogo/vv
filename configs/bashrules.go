package configs

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"

	"github.com/vogo/vage/tool/bash"
)

// BuildBashClassifier returns a bash command classifier composed of the
// default rule library plus any valid user extensions from cfg. Invalid user
// regex patterns are logged and skipped. Returns nil when the feature is
// disabled in config.
func BuildBashClassifier(cfg BashRulesConfig) *bash.Classifier {
	if !cfg.IsEnabled() {
		return nil
	}

	rules := make([]bash.Rule, 0, len(cfg.UserBlocked)+len(cfg.UserDangerous)+len(cfg.UserSafe))

	// User rules come first within each tier so their Name surfaces on ties.
	// A default higher-tier rule still wins because Classify picks the worst tier.
	rules = append(rules, compileUserRules(cfg.UserBlocked, bash.TierBlocked, "user-blocked")...)
	rules = append(rules, compileUserRules(cfg.UserDangerous, bash.TierDangerous, "user-dangerous")...)
	rules = append(rules, compileUserRules(cfg.UserSafe, bash.TierSafe, "user-safe")...)
	rules = append(rules, bash.DefaultRules()...)

	return bash.NewClassifier(rules)
}

// ClassifyBashArgs evaluates classifier and path guardian against a bash
// tool's JSON arguments (`{"command":"..."}`) and returns the higher-Tier
// classification. Returns (zero, false) when neither is configured or args
// are malformed. Shared by the permission executor and the interrupt policy
// so the two gates cannot disagree on the same invocation.
func ClassifyBashArgs(c *bash.Classifier, g *bash.PathGuardian, args string) (bash.Classification, bool) {
	if c == nil && g == nil {
		return bash.Classification{}, false
	}

	var parsed struct {
		Command string `json:"command"`
	}

	if err := json.Unmarshal([]byte(args), &parsed); err != nil || parsed.Command == "" {
		return bash.Classification{}, false
	}

	var best bash.Classification
	initialized := false

	if c != nil {
		best = c.Classify(parsed.Command)
		initialized = true
	}

	if g != nil {
		gCls := g.Classify(parsed.Command)
		if !initialized || gCls.Tier > best.Tier {
			best = gCls
		}
	}

	return best, true
}

func compileUserRules(patterns []string, tier bash.Tier, namePrefix string) []bash.Rule {
	if len(patterns) == 0 {
		return nil
	}

	out := make([]bash.Rule, 0, len(patterns))

	for i, pat := range patterns {
		re, err := regexp.Compile(pat)
		if err != nil {
			slog.Warn("vv: invalid bash_rules regex; skipping",
				"tier", tier.String(),
				"pattern", pat,
				"error", err)

			continue
		}

		out = append(out, bash.Rule{
			Name:    fmt.Sprintf("%s-%d", namePrefix, i+1),
			Tier:    tier,
			Pattern: re,
			Reason:  "user-configured rule",
		})
	}

	return out
}
