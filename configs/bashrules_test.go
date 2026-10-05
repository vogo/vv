package configs

import (
	"strings"
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
			`[invalid(regex`,    // malformed; should be skipped with a warning
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

func TestClassifyBashArgs(t *testing.T) {
	c := bash.NewClassifier(bash.DefaultRules())

	cls, ok := ClassifyBashArgs(c, nil, `{"command":"rm -rf ./dist"}`)
	if !ok || cls.Tier != bash.TierDangerous {
		t.Fatalf("dangerous cmd: ok=%v tier=%s", ok, cls.Tier)
	}

	if _, ok := ClassifyBashArgs(nil, nil, `{"command":"rm -rf ./dist"}`); ok {
		t.Fatal("nil classifier+guardian must not classify")
	}

	if _, ok := ClassifyBashArgs(c, nil, `not-json`); ok {
		t.Fatal("malformed args must not classify")
	}

	cls, ok = ClassifyBashArgs(c, nil, `{"command":"ls"}`)
	if !ok || cls.Tier != bash.TierSafe {
		t.Fatalf("ls: ok=%v tier=%s", ok, cls.Tier)
	}
}

func TestBashPolicyFingerprint_SharesAssemblyAndIgnoresDirsWhenGuardianOff(t *testing.T) {
	cfg := BashRulesConfig{}
	rules := assembleBashRules(cfg)
	material := bashPolicyMaterial(rules, []string{"/tmp/secret"}, "/work", false)
	if !strings.Contains(material, "recursive-rm") {
		t.Fatal("fingerprint material must include a default rule name")
	}
	if strings.Contains(material, "/tmp/secret") || strings.Contains(material, "guardian=on") {
		t.Fatalf("guardian off must ignore directories: %s", material)
	}
	if !strings.Contains(material, "guardian=off") {
		t.Fatal("guardian off material missing guardian=off")
	}

	_, fp := BuildBashClassifierWithFingerprint(cfg, []string{"/b", "/a"}, "/work", true)
	if fp != BashPolicyFingerprint(rules, []string{"/a", "/b"}, "/work", true) {
		t.Fatal("classifier fingerprint must match the same assembled rules, directory order ignored")
	}
	if fp == BashPolicyFingerprint(rules, nil, "", false) {
		t.Fatal("turning the guardian on must change the fingerprint")
	}

	user := BashRulesConfig{UserDangerous: []string{`\beval\b`}}
	if BashPolicyFingerprint(assembleBashRules(user), nil, "", false) == BashPolicyFingerprint(rules, nil, "", false) {
		t.Fatal("a user dangerous rule must change the fingerprint")
	}
}
