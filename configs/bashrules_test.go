package configs

import (
	"testing"

	"github.com/vogo/vage/tool/bash"
)

func TestBashRulesConfig_IsEnabled_DefaultTrue(t *testing.T) {
	var c BashRulesConfig
	if !c.IsEnabled() {
		t.Error("default config should be enabled")
	}
}

func TestBashRulesConfig_IsEnabled_ExplicitFalse(t *testing.T) {
	f := false
	c := BashRulesConfig{Enabled: &f}

	if c.IsEnabled() {
		t.Error("explicit false should disable")
	}
}

func TestBuildBashClassifier_Disabled(t *testing.T) {
	f := false
	if got := BuildBashClassifier(BashRulesConfig{Enabled: &f}); got != nil {
		t.Error("disabled config should return nil classifier")
	}
}

func TestBuildBashClassifier_DefaultsOnly(t *testing.T) {
	c := BuildBashClassifier(BashRulesConfig{})
	if c == nil {
		t.Fatal("enabled config should return a non-nil classifier")
	}

	if got := c.Classify("rm -rf /").Tier; got != bash.TierBlocked {
		t.Errorf("defaults should block rm -rf /, got %s", got)
	}
}

func TestBuildBashClassifier_UserExtensions(t *testing.T) {
	c := BuildBashClassifier(BashRulesConfig{
		UserBlocked:   []string{`\bterraform\s+destroy\b`},
		UserDangerous: []string{`\bcustom-dangerous\b`},
		UserSafe:      []string{`^bundle\s+exec\s`},
	})
	if c == nil {
		t.Fatal("classifier should be non-nil")
	}

	if got := c.Classify("terraform destroy").Tier; got != bash.TierBlocked {
		t.Errorf("user-blocked should apply, got %s", got)
	}

	if got := c.Classify("custom-dangerous foo").Tier; got != bash.TierDangerous {
		t.Errorf("user-dangerous should apply, got %s", got)
	}

	if got := c.Classify("bundle exec rspec").Tier; got != bash.TierSafe {
		t.Errorf("user-safe should apply, got %s", got)
	}
}

func TestBuildBashClassifier_InvalidRegexSkipped(t *testing.T) {
	c := BuildBashClassifier(BashRulesConfig{
		UserBlocked: []string{
			`[invalid(regex`,     // malformed; should be skipped with a warning
			`\bvalid-pattern\b`, // valid; should load
		},
	})
	if c == nil {
		t.Fatal("classifier should still load when some user patterns are invalid")
	}

	if got := c.Classify("valid-pattern here").Tier; got != bash.TierBlocked {
		t.Errorf("valid user pattern should load even when a neighbour is invalid, got %s", got)
	}
}
