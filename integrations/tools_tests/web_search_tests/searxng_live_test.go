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

// Live SearXNG integration test. Skipped unless an opt-in env var is set —
// vanilla `go test` runs in any environment must remain hermetic, so the
// network-dependent path is only enabled when the operator explicitly points
// at a working SearXNG instance.
//
// To run:
//
//	export VV_WEBSEARCH_LIVE_SEARXNG_URL=http://10.225.32.180/search
//	# optional: when the instance has SearXNG's limiter enabled, supply a
//	# browser-style User-Agent so requests are not rejected with 429.
//	export VV_WEBSEARCH_LIVE_SEARXNG_UA='Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/120.0.0.0 Safari/537.36'
//	go test ./integrations/tools_tests/web_search_tests -run SearXNG_Live -v
//
// Failure modes documented in the spec (changes/.../websearch-searxng/spec.md):
//   - 429 Too Many Requests → instance settings.yml `search.formats` does
//     not include `json`, OR `server.limiter` is on and the supplied UA is
//     flagged as a bot. Fix the instance config and rerun.
//   - 401 / 403 → the gateway in front of the instance requires an api key
//     not yet supplied via `VV_WEBSEARCH_LIVE_SEARXNG_API_KEY`.
package web_search_tests

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/vogo/vage/tool"
	"github.com/vogo/vage/tool/websearch"
)

const liveSearXNGURLEnv = "VV_WEBSEARCH_LIVE_SEARXNG_URL"

// liveEnvelope is the agent-facing envelope shape emitted by Registry.Execute.
// Local copy of the relevant fields so we don't have to import internal types.
type liveEnvelope struct {
	Query      string `json:"query"`
	Provider   string `json:"provider"`
	Results    []any  `json:"results"`
	Warnings   []string
	ErrorCode  string `json:"error_code,omitempty"`
	Message    string `json:"message,omitempty"`
	StatusCode int    `json:"status_code,omitempty"`
}

func TestSearXNG_Live_RealInstance(t *testing.T) {
	endpoint := strings.TrimSpace(os.Getenv(liveSearXNGURLEnv))
	if endpoint == "" {
		t.Skipf("set %s=<searxng-url> to enable live integration test", liveSearXNGURLEnv)
	}

	opts := []websearch.SearXNGOption{}
	if ua := strings.TrimSpace(os.Getenv("VV_WEBSEARCH_LIVE_SEARXNG_UA")); ua != "" {
		opts = append(opts, websearch.WithSearXNGUserAgent(ua))
	}
	if key := strings.TrimSpace(os.Getenv("VV_WEBSEARCH_LIVE_SEARXNG_API_KEY")); key != "" {
		opts = append(opts, websearch.WithSearXNGAPIKey(key))
	}
	if lang := strings.TrimSpace(os.Getenv("VV_WEBSEARCH_LIVE_SEARXNG_LANGUAGE")); lang != "" {
		opts = append(opts, websearch.WithSearXNGLanguage(lang))
	}

	provider := websearch.NewSearXNG(endpoint, opts...)
	if provider == nil {
		t.Fatalf("NewSearXNG returned nil for endpoint=%q", endpoint)
	}

	reg := tool.NewRegistry()
	if err := websearch.Register(
		reg,
		websearch.WithProvider(provider),
		websearch.WithTimeout(15*time.Second),
		websearch.WithDefaultMaxResults(5),
	); err != nil {
		t.Fatalf("websearch.Register: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	res, err := reg.Execute(ctx, toolName, `{"query":"golang generics"}`)
	if err != nil {
		t.Fatalf("Execute returned go error: %v", err)
	}
	if len(res.Content) == 0 {
		t.Fatal("empty content")
	}

	var env liveEnvelope
	if uerr := json.Unmarshal([]byte(res.Content[0].Text), &env); uerr != nil {
		t.Fatalf("decode envelope: %v\nbody=%s", uerr, res.Content[0].Text)
	}

	if env.Provider != websearch.SearXNGName {
		t.Errorf("provider=%q, want %q", env.Provider, websearch.SearXNGName)
	}

	// Surface diagnostic for spec write-back: tests that fail because the
	// instance needs limiter / formats tweaks should print a clear hint
	// rather than just IsError=true.
	if res.IsError {
		t.Fatalf("upstream returned error envelope: code=%q message=%q status=%d\n"+
			"hint: ensure SearXNG settings.yml has `search.formats: [html, json]` and either disable `server.limiter` or whitelist the supplied User-Agent",
			env.ErrorCode, env.Message, env.StatusCode)
	}

	if len(env.Results) == 0 {
		t.Fatalf("no results returned for live query — query may be filtered or instance has no engines enabled")
	}
	t.Logf("live SearXNG returned %d results", len(env.Results))
}
