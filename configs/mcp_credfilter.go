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
	"log/slog"
	"regexp"

	"github.com/vogo/vage/security/credscrub"
)

// BuildMCPCredentialScanner builds a credscrub.Scanner from the config.
// Returns nil when the feature is disabled. Invalid user-supplied regexes
// are logged and skipped; they do not fail construction.
func BuildMCPCredentialScanner(cfg MCPCredentialFilterConfig) *credscrub.Scanner {
	if !cfg.IsEnabled() {
		return nil
	}

	action := mapAction(cfg.Action)

	rules := credscrub.DefaultRules()
	for _, p := range cfg.ExtraPatterns {
		re, err := regexp.Compile(p)
		if err != nil {
			slog.Warn("vv: invalid mcp_credential_filter.extra_patterns entry, skipping",
				"pattern", p, "error", err)

			continue
		}
		rules = append(rules, credscrub.Rule{
			Name:    "custom",
			Type:    "custom",
			Pattern: re,
		})
	}

	allow := credscrub.DefaultAllowlist()
	for _, p := range cfg.Allowlist {
		re, err := regexp.Compile(p)
		if err != nil {
			slog.Warn("vv: invalid mcp_credential_filter.allowlist entry, skipping",
				"pattern", p, "error", err)

			continue
		}
		allow = append(allow, re)
	}

	return credscrub.NewScanner(credscrub.Config{
		Rules:        rules,
		Allowlist:    allow,
		Action:       action,
		MaxScanBytes: cfg.MaxScanBytes,
	})
}

func mapAction(s string) credscrub.Action {
	switch s {
	case "log":
		return credscrub.ActionLog
	case "block":
		return credscrub.ActionBlock
	case "redact", "":
		return credscrub.ActionRedact
	default:
		slog.Warn("vv: unknown mcp_credential_filter.action, falling back to redact",
			"action", s)

		return credscrub.ActionRedact
	}
}
