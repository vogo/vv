/*
 * Licensed to the Apache Software Foundation (ASF) under one or more
 * contributor license agreements.  See the NOTICE file distributed with
 * this work for additional information regarding copyright ownership.
 * The ASF licenses this file to You under the Apache License, Version 2.0
 * (the "License"); you may not use this file except in compliance with
 * the License.  You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package configs

import (
	"testing"

	"github.com/vogo/vage/security/credscrub"
)

func TestMCPCredentialFilterConfig_IsEnabled_DefaultTrue(t *testing.T) {
	var c MCPCredentialFilterConfig
	if !c.IsEnabled() {
		t.Error("default config should be enabled")
	}
}

func TestMCPCredentialFilterConfig_IsEnabled_ExplicitFalse(t *testing.T) {
	f := false
	c := MCPCredentialFilterConfig{Enabled: &f}

	if c.IsEnabled() {
		t.Error("explicit false should disable")
	}
}

func TestBuildMCPCredentialScanner_Disabled(t *testing.T) {
	f := false
	if got := BuildMCPCredentialScanner(MCPCredentialFilterConfig{Enabled: &f}); got != nil {
		t.Error("disabled config should return nil scanner")
	}
}

func TestBuildMCPCredentialScanner_DefaultAction(t *testing.T) {
	s := BuildMCPCredentialScanner(MCPCredentialFilterConfig{})
	if s == nil {
		t.Fatal("default config should produce scanner")
	}

	if got := s.Action(); got != credscrub.ActionRedact {
		t.Errorf("default action should be redact, got %q", got)
	}
}

func TestBuildMCPCredentialScanner_UnknownActionFallsBack(t *testing.T) {
	s := BuildMCPCredentialScanner(MCPCredentialFilterConfig{Action: "notavalidaction"})
	if s == nil {
		t.Fatal("expected scanner even on unknown action")
	}

	if got := s.Action(); got != credscrub.ActionRedact {
		t.Errorf("unknown action should fall back to redact, got %q", got)
	}
}

func TestBuildMCPCredentialScanner_ExtraPatterns(t *testing.T) {
	s := BuildMCPCredentialScanner(MCPCredentialFilterConfig{
		ExtraPatterns: []string{`ACME-[0-9]{8}`},
	})

	r := s.ScanText("token=ACME-12345678")
	if len(r.Hits) == 0 {
		t.Fatal("expected extra pattern to match")
	}

	found := false
	for _, h := range r.Hits {
		if h.Type == "custom" {
			found = true

			break
		}
	}

	if !found {
		t.Errorf("expected type=custom hit; got %+v", r.Hits)
	}
}

func TestBuildMCPCredentialScanner_InvalidExtraPatternSkipped(t *testing.T) {
	// Invalid regex should be logged and skipped, not panic or fail construction.
	s := BuildMCPCredentialScanner(MCPCredentialFilterConfig{
		ExtraPatterns: []string{"["}, // syntax error
	})
	if s == nil {
		t.Fatal("invalid pattern should not prevent construction")
	}
}

func TestBuildMCPCredentialScanner_UserAllowlist(t *testing.T) {
	s := BuildMCPCredentialScanner(MCPCredentialFilterConfig{
		Allowlist: []string{`^AKIA[0-9A-Z]{16}$`}, // allow AWS keys entirely (test shape only)
	})

	r := s.ScanText("AKIAIOSFODNN7EXAMPLE")
	for _, h := range r.Hits {
		if h.Type == "aws_access_key" {
			t.Errorf("user allowlist should have suppressed aws_access_key; got %+v", h)
		}
	}
}
