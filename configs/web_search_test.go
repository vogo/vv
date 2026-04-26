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
	"os"
	"path/filepath"
	"testing"
)

// scenario: provider id normalization is case-fold, trim, and "unknown" → "".
func TestNormalizedWebSearchProvider(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		{"", ""},
		{"tavily", "tavily"},
		{"TAVILY", "tavily"},
		{"  brave  ", "brave"},
		{"serper", ""},
		{"google", ""},
	}
	for _, tc := range cases {
		got := NormalizedWebSearchProvider(tc.in)
		if got != tc.want {
			t.Errorf("NormalizedWebSearchProvider(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

// scenario: IsEnabled requires both a recognized provider and a non-empty key.
func TestWebSearchConfig_IsEnabled(t *testing.T) {
	cases := []struct {
		name string
		cfg  WebSearchConfig
		want bool
	}{
		{"empty", WebSearchConfig{}, false},
		{"key only", WebSearchConfig{APIKey: "x"}, false},
		{"provider only", WebSearchConfig{Provider: "tavily"}, false},
		{"unknown provider with key", WebSearchConfig{Provider: "google", APIKey: "x"}, false},
		{"tavily + key", WebSearchConfig{Provider: "tavily", APIKey: "x"}, true},
		{"brave + key", WebSearchConfig{Provider: "brave", APIKey: "x"}, true},
		{"whitespace key", WebSearchConfig{Provider: "tavily", APIKey: "  "}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.cfg.IsEnabled(); got != tc.want {
				t.Fatalf("IsEnabled = %v, want %v", got, tc.want)
			}
		})
	}
}

// scenario: env vars override the YAML for all four web_search keys.
func TestLoad_WebSearchEnvOverride(t *testing.T) {
	dir := t.TempDir()
	yaml := []byte(`
llm:
  api_key: dummy
tools:
  web_search:
    provider: tavily
    api_key: file-key
    timeout_seconds: 1
    max_results: 3
`)
	path := filepath.Join(dir, "vv.yaml")
	if err := os.WriteFile(path, yaml, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	t.Setenv("VV_WEB_SEARCH_PROVIDER", "brave")
	t.Setenv("VV_WEB_SEARCH_API_KEY", "env-key")
	t.Setenv("VV_WEB_SEARCH_TIMEOUT_SECONDS", "9")
	t.Setenv("VV_WEB_SEARCH_MAX_RESULTS", "7")

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	ws := cfg.Tools.WebSearch
	if ws.Provider != "brave" {
		t.Errorf("provider = %q, want brave", ws.Provider)
	}
	if ws.APIKey != "env-key" {
		t.Errorf("api_key = %q, want env-key", ws.APIKey)
	}
	if ws.TimeoutSeconds != 9 {
		t.Errorf("timeout = %d, want 9", ws.TimeoutSeconds)
	}
	if ws.MaxResults != 7 {
		t.Errorf("max_results = %d, want 7", ws.MaxResults)
	}
	if !ws.IsEnabled() {
		t.Errorf("IsEnabled = false, want true")
	}
}

// scenario: an unknown provider id with a key still loads but disables the
// tool — operators must not have a "broken" tool exposed.
func TestLoad_WebSearchUnknownProviderDisabled(t *testing.T) {
	dir := t.TempDir()
	yaml := []byte(`
llm:
  api_key: dummy
tools:
  web_search:
    provider: serper
    api_key: x
`)
	path := filepath.Join(dir, "vv.yaml")
	if err := os.WriteFile(path, yaml, 0o600); err != nil {
		t.Fatalf("write: %v", err)
	}

	cfg, err := Load(path, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Tools.WebSearch.IsEnabled() {
		t.Fatalf("expected unknown provider to disable the tool")
	}
}
