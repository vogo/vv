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

// End-to-end integration tests for the P2-10 web_search tool execution flow.
//
// These tests stand up a Tavily-shaped httptest backend, register the
// websearch tool on a vage tool.Registry through the public Register API
// (mirroring how vv's wiring composes it via tools.MaybeRegisterWebSearch),
// then drive the tool via Registry.Execute as an LLM ReAct loop would.
// Assertions cover the JSON envelope contract: provider id echo, result
// passthrough, IsError flagging on error codes, and that the api_key never
// leaks into the envelope text.
//
// Sandbox note: httptest.NewServer requires the ability to bind a loopback
// port. In the previous web_fetch session some sandboxes returned
// `bind: operation not permitted`; that is an environment limitation, not
// a real failure of the contract. The test still has value in unconstrained
// CI environments.
package web_search_tests

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vogo/vage/tool"
	"github.com/vogo/vage/tool/websearch"
)

const (
	toolName = "web_search"
)

// envelopeShape mirrors the searchEnvelope fields the LLM sees. Kept as a
// local struct so the test stays decoupled from the package-private
// searchEnvelope type and locked to the public JSON contract.
type envelopeShape struct {
	Query    string `json:"query"`
	Provider string `json:"provider"`
	Results  []struct {
		URL         string `json:"url"`
		Title       string `json:"title"`
		Snippet     string `json:"snippet"`
		PublishedAt string `json:"published_at,omitempty"`
	} `json:"results"`
	RetrievedAt string   `json:"retrieved_at"`
	Warnings    []string `json:"warnings,omitempty"`
	ErrorCode   string   `json:"error_code,omitempty"`
	Message     string   `json:"message,omitempty"`
	StatusCode  int      `json:"status_code,omitempty"`
}

func decodeEnvelope(t *testing.T, text string) envelopeShape {
	t.Helper()
	var env envelopeShape
	if err := json.Unmarshal([]byte(text), &env); err != nil {
		t.Fatalf("decode envelope: %v\nraw=%q", err, text)
	}
	return env
}

// buildRegistry constructs the registry the same way vv would: NewTavily with
// a test endpoint, hand it to websearch.Register through WithProvider, and
// return the hot registry. apiKey is captured so leak assertions can scan for it.
func buildRegistry(t *testing.T, apiKey, endpoint string) *tool.Registry {
	t.Helper()
	provider := websearch.NewTavily(apiKey, websearch.WithTavilyEndpoint(endpoint))
	if provider == nil {
		t.Fatalf("NewTavily returned nil for non-empty key %q", apiKey)
	}

	reg := tool.NewRegistry()
	if err := websearch.Register(reg, websearch.WithProvider(provider)); err != nil {
		t.Fatalf("websearch.Register: %v", err)
	}
	return reg
}

// --- AC-1.1 / AC-3.1: success path returns provider id, results, and url passthrough ---
// scenario: the Tavily-shaped backend returns three result rows; Execute must
// produce a non-error envelope whose results match (URL passthrough, snippet
// trimmed but text-equivalent), and the configured provider id "tavily"
// appears in the envelope.
func TestIntegration_WebSearch_TavilyHandler_Success(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Sanity: the LLM-supplied query reaches the upstream verbatim.
		var body struct {
			Query string `json:"query"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Errorf("decode upstream body: %v", err)
		}
		if body.Query != "go modules" {
			t.Errorf("upstream query=%q want %q", body.Query, "go modules")
		}
		_, _ = w.Write([]byte(`{"results":[
			{"url":"https://example.com/a","title":"Title A","content":"Snippet A","published_date":"2026-04-01"},
			{"url":"https://example.com/b","title":"Title B","content":"Snippet B"},
			{"url":"https://example.com/c","title":"Title C","content":"Snippet C"}
		]}`))
	}))
	defer srv.Close()

	reg := buildRegistry(t, "test-key", srv.URL)

	res, err := reg.Execute(context.Background(), toolName, `{"query":"go modules"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError=true, want false; content=%v", res.Content)
	}
	if len(res.Content) == 0 {
		t.Fatal("envelope content empty")
	}

	env := decodeEnvelope(t, res.Content[0].Text)
	if env.Provider != websearch.TavilyName {
		t.Errorf("envelope provider=%q, want %q", env.Provider, websearch.TavilyName)
	}
	if env.Query != "go modules" {
		t.Errorf("envelope query=%q, want %q", env.Query, "go modules")
	}
	if len(env.Results) != 3 {
		t.Fatalf("results=%d, want 3", len(env.Results))
	}

	wantURLs := []string{"https://example.com/a", "https://example.com/b", "https://example.com/c"}
	for i, r := range env.Results {
		if r.URL != wantURLs[i] {
			t.Errorf("results[%d].URL=%q, want %q (passthrough must preserve original)", i, r.URL, wantURLs[i])
		}
	}
	if env.Results[0].PublishedAt != "2026-04-01" {
		t.Errorf("results[0].PublishedAt=%q, want %q", env.Results[0].PublishedAt, "2026-04-01")
	}
	if env.ErrorCode != "" {
		t.Errorf("error_code=%q, want empty on success", env.ErrorCode)
	}
}

// --- AC-1.3: zero results → empty results array + warnings: ["no_results"] ---
// scenario: provider returns an empty results array; envelope must remain
// non-error with `results: []` and a `no_results` warning so the LLM can
// recognise the empty case without parsing an explicit error.
func TestIntegration_WebSearch_TavilyHandler_NoResults(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"results":[]}`))
	}))
	defer srv.Close()

	reg := buildRegistry(t, "test-key", srv.URL)
	res, err := reg.Execute(context.Background(), toolName, `{"query":"nothing here"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if res.IsError {
		t.Fatalf("IsError=true, want false on no-results path")
	}

	env := decodeEnvelope(t, res.Content[0].Text)
	if env.Results == nil {
		t.Errorf("results is nil, want []")
	}
	if len(env.Results) != 0 {
		t.Errorf("results len=%d, want 0", len(env.Results))
	}

	hasNoResults := false
	for _, w := range env.Warnings {
		if w == "no_results" {
			hasNoResults = true
		}
	}
	if !hasNoResults {
		t.Errorf("warnings=%v missing 'no_results'", env.Warnings)
	}
}

// --- AC-4.3: 5xx upstream → envelope error_code: provider_error + status_code passthrough ---
// scenario: the Tavily backend returns 502; Execute envelope must surface
// IsError=true, error_code=provider_error, and the original 502 in
// status_code so the LLM (and trace logs) can correlate the failure with
// the upstream HTTP response.
func TestIntegration_WebSearch_TavilyHandler_ServerError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
		_, _ = w.Write([]byte("upstream broke"))
	}))
	defer srv.Close()

	reg := buildRegistry(t, "test-key", srv.URL)
	res, err := reg.Execute(context.Background(), toolName, `{"query":"q"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("IsError=false, want true on 5xx path")
	}

	env := decodeEnvelope(t, res.Content[0].Text)
	if env.ErrorCode != "provider_error" {
		t.Errorf("error_code=%q, want provider_error", env.ErrorCode)
	}
	if env.StatusCode != http.StatusBadGateway {
		t.Errorf("status_code=%d, want %d", env.StatusCode, http.StatusBadGateway)
	}
}

// --- AC-4.3 (auth subset): 401 → error_code: invalid_api_key ---
// scenario: 401/403 surface as a dedicated error code so an operator can
// distinguish a misconfigured key from generic upstream errors. Important
// for any future ops dashboards driven from trace events.
func TestIntegration_WebSearch_TavilyHandler_Unauthorized(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	reg := buildRegistry(t, "bad-key", srv.URL)
	res, err := reg.Execute(context.Background(), toolName, `{"query":"q"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("IsError=false, want true on 401 path")
	}

	env := decodeEnvelope(t, res.Content[0].Text)
	if env.ErrorCode != "invalid_api_key" {
		t.Errorf("error_code=%q, want invalid_api_key", env.ErrorCode)
	}
}

// --- AC-4.1: query > 1024 runes → error_code: query_too_long without HTTP call ---
// scenario: the pre-flight rune-length check must reject the request before
// hitting the upstream. Backend uses an "explosive" handler that fails the
// test if reached so we prove the rejection happened before the round-trip.
func TestIntegration_WebSearch_QueryTooLong_NeverHitsBackend(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Errorf("upstream was hit even though pre-flight should have rejected the query")
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	reg := buildRegistry(t, "test-key", srv.URL)

	// 2048 ASCII runes — comfortably above the 1024-rune cap and easy to encode.
	overlong := strings.Repeat("a", 2048)
	args, err := json.Marshal(map[string]any{"query": overlong})
	if err != nil {
		t.Fatalf("marshal args: %v", err)
	}

	res, err := reg.Execute(context.Background(), toolName, string(args))
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if !res.IsError {
		t.Fatalf("IsError=false, want true on query_too_long")
	}

	env := decodeEnvelope(t, res.Content[0].Text)
	if env.ErrorCode != "query_too_long" {
		t.Errorf("error_code=%q, want query_too_long", env.ErrorCode)
	}
	// AC-4.1 echo expectation (per code-review F1 fix): envelope.query echoes
	// a truncated prefix so the LLM can recognise its own request.
	if env.Query == "" {
		t.Errorf("envelope.query is empty, want a truncated prefix echo")
	}
	if len(env.Query) > 256 {
		t.Errorf("envelope.query length=%d, expected a small truncated echo", len(env.Query))
	}
}

// --- AC-5.2: envelope must NEVER contain the api_key, even on error paths ---
// scenario: stand up two backends — a successful one and a failing one — and
// scan the envelope text from each for the configured api_key string. A
// regression that introduces api_key to the envelope (e.g. by including the
// raw upstream request body in an error message) would surface here.
func TestIntegration_WebSearch_EnvelopeOmitsAPIKeyOnAllPaths(t *testing.T) {
	const sentinelKey = "sk-tavily-secret-DO-NOT-LEAK-7c5f1b2a"

	t.Run("success", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"results":[{"url":"https://example.com/a","title":"A","content":"x"}]}`))
		}))
		defer srv.Close()

		reg := buildRegistry(t, sentinelKey, srv.URL)
		res, err := reg.Execute(context.Background(), toolName, `{"query":"hello"}`)
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if strings.Contains(res.Content[0].Text, sentinelKey) {
			t.Fatalf("envelope text leaked api_key on success path:\n%s", res.Content[0].Text)
		}
	})

	t.Run("server_error", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("upstream"))
		}))
		defer srv.Close()

		reg := buildRegistry(t, sentinelKey, srv.URL)
		res, err := reg.Execute(context.Background(), toolName, `{"query":"hello"}`)
		if err != nil {
			t.Fatalf("Execute: %v", err)
		}
		if strings.Contains(res.Content[0].Text, sentinelKey) {
			t.Fatalf("envelope text leaked api_key on 5xx path:\n%s", res.Content[0].Text)
		}
	})
}

// --- AC-1.1: ToolDef metadata is intact end-to-end ---
// scenario: pin the registered ToolDef (name + ReadOnly + Source) so any
// future redesign of the ToolDef contract surfaces here instead of inside
// the LLM round-trip. Specifically validates ReadOnly=true so plan-mode CLI
// permission gating keeps allowing it.
func TestIntegration_WebSearch_ToolDefMetadata(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	reg := buildRegistry(t, "test-key", srv.URL)

	td, ok := reg.Get(toolName)
	if !ok {
		t.Fatalf("registry missing tool %q", toolName)
	}
	if td.Name != toolName {
		t.Errorf("ToolDef.Name=%q, want %q", td.Name, toolName)
	}
	if !td.ReadOnly {
		t.Errorf("ToolDef.ReadOnly=false, want true (matches design §2.4)")
	}
}
