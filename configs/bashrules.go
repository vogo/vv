package configs

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"regexp"
	"sort"
	"strings"

	"github.com/vogo/vage/tool/bash"
)

// BuildBashClassifier returns a bash command classifier composed of the
// default rule library plus any valid user extensions from cfg. Invalid user
// regex patterns are logged and skipped. Returns nil when the feature is
// disabled in config.
func BuildBashClassifier(cfg BashRulesConfig) *bash.Classifier {
	c, _ := BuildBashClassifierWithFingerprint(cfg, nil, "", false)
	return c
}

// BuildBashClassifierWithFingerprint assembles rules once and returns both
// the classifier and the fingerprint of that same slice. Callers outside
// this package cannot see assembleBashRules, so this is the way to keep
// the two from drifting. A disabled config returns a nil classifier and
// an empty fingerprint. allowedDirs and workingDir are ignored when
// guardianOn is false.
func BuildBashClassifierWithFingerprint(cfg BashRulesConfig, allowedDirs []string, workingDir string, guardianOn bool) (*bash.Classifier, string) {
	if !cfg.IsEnabled() {
		return nil, ""
	}
	rules := assembleBashRules(cfg)
	return bash.NewClassifier(rules), BashPolicyFingerprint(rules, allowedDirs, workingDir, guardianOn)
}

// assembleBashRules is the single assembly of user rules plus DefaultRules.
// BuildBashClassifier and BashPolicyFingerprint must share this result so
// the frozen fingerprint describes the classifier that will actually run.
// Disabled config yields nil. Invalid user patterns are skipped.
func assembleBashRules(cfg BashRulesConfig) []bash.Rule {
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

	return rules
}

// BashPolicyFingerprint is the opaque SHA-256 hex of the bash rules and,
// when guardianOn is true, the sorted allow-list plus working directory.
// When guardianOn is false the directory arguments are ignored and the
// material records guardian=off, because a nil guardian does not consult
// them. Command text and secrets are not part of the material.
func BashPolicyFingerprint(rules []bash.Rule, allowedDirs []string, workingDir string, guardianOn bool) string {
	sum := sha256.Sum256([]byte(bashPolicyMaterial(rules, allowedDirs, workingDir, guardianOn)))
	return hex.EncodeToString(sum[:])
}

func bashPolicyMaterial(rules []bash.Rule, allowedDirs []string, workingDir string, guardianOn bool) string {
	var b strings.Builder
	for _, r := range rules {
		pattern := ""
		if r.Pattern != nil {
			pattern = r.Pattern.String()
		}
		fmt.Fprintf(&b, "rule\t%s\t%s\t%s\t%s\n", r.Name, r.Tier.String(), pattern, r.Reason)
	}
	if !guardianOn {
		b.WriteString("guardian=off\n")
		return b.String()
	}
	dirs := append([]string(nil), allowedDirs...)
	sort.Strings(dirs)
	b.WriteString("guardian=on\n")
	for _, d := range dirs {
		fmt.Fprintf(&b, "dir\t%s\n", d)
	}
	fmt.Fprintf(&b, "workdir\t%s\n", workingDir)
	return b.String()
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
