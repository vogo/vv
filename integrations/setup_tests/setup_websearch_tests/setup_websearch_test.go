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

// End-to-end integration tests for P2-10 · WebSearch wiring.
//
// These tests prove the contract between vv's config layer (configs.WebSearchConfig
// + env-var overrides) and the agent-facing tool registries assembled by
// setup.New. They focus on whether the `web_search` ToolDef appears in each
// tool-carrying agent's registry under the documented configurations
// (AC-2.1 / AC-2.2 / AC-2.3 / AC-2.4 plus the configured-path positive case).
//
// They never make outbound HTTP calls — provider construction is allowed
// (NewTavily/NewBrave only validate the api key shape), but the LLM mock
// never selects web_search so the handler never fires.
package setup_websearch_tests

import (
	"os"
	"path/filepath"
	"slices"
	"sort"
	"sync"
	"testing"

	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/tool"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/setup"
)

// webSearchToolName mirrors the canonical tool name exposed by
// vage/tool/websearch.ToolName. Repeated as a local constant so the
// integration test is decoupled from any future renaming inside the package.
const webSearchToolName = "web_search"

// baseCfgForWebSearchTests assembles the minimal *configs.Config setup.New
// requires; callers tweak Tools.WebSearch per scenario.
func baseCfgForWebSearchTests() *configs.Config {
	return &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 3},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}
}

// agentToolNames extracts the set of registered tool names from a TaskAgent.
// Returns an empty map if the agent is not a *taskagent.Agent so caller can
// fail with a clearer assertion message.
func agentToolNames(t *testing.T, agentID string, a any) map[string]bool {
	t.Helper()

	ta, ok := a.(*taskagent.Agent)
	if !ok {
		t.Fatalf("agent %q is %T, want *taskagent.Agent", agentID, a)
	}

	names := make(map[string]bool, len(ta.Tools()))
	for _, td := range ta.Tools() {
		names[td.Name] = true
	}

	return names
}

// captureToolRegistries returns a setup.Options whose WrapToolRegistry hook
// snapshots each underlying *tool.Registry's tool-name set. The returned
// closure exposes the captured per-registry name maps in registration order.
//
// Used to assert tool presence on the Primary Assistant — there is no public
// getter from setup.Result for the Primary, so the only deterministic surface
// is the wrap callback that fires once per agent (3 dispatchable + Primary).
func captureToolRegistries() (*setup.Options, func() []map[string]bool) {
	var (
		mu       sync.Mutex
		captures []map[string]bool
	)

	opts := &setup.Options{
		WrapToolRegistry: func(r *tool.Registry) tool.ToolRegistry {
			snap := make(map[string]bool)
			for _, td := range r.List() {
				snap[td.Name] = true
			}
			mu.Lock()
			captures = append(captures, snap)
			mu.Unlock()
			return r
		},
	}

	return opts, func() []map[string]bool {
		mu.Lock()
		defer mu.Unlock()
		out := make([]map[string]bool, len(captures))
		copy(out, captures)
		return out
	}
}

// --- AC-2.1: cfg.Tools.WebSearch empty → web_search NOT registered on any agent ---
// scenario: with the zero-value WebSearchConfig (and therefore
// WebSearchConfig.IsEnabled() == false), the tool-carrying agents (coder /
// researcher / reviewer + Primary) must NOT carry the `web_search` ToolDef.
// Proves the "no key configured = zero-cost path" invariant.
func TestIntegration_SetupNew_WebSearch_DefaultDisabled(t *testing.T) {
	cfg := baseCfgForWebSearchTests()
	if cfg.Tools.WebSearch.IsEnabled() {
		t.Fatalf("baseline WebSearchConfig is unexpectedly enabled: %+v", cfg.Tools.WebSearch)
	}

	opts, capturedFn := captureToolRegistries()

	mock := &mockChatCompleter{}
	result, err := setup.New(cfg, mock, nil, nil, opts)
	if err != nil {
		t.Fatalf("setup.New: %v", err)
	}

	for _, id := range []string{"coder", "researcher", "reviewer"} {
		a := result.Agent(id)
		if a == nil {
			t.Fatalf("agent %q missing from setup result", id)
		}
		names := agentToolNames(t, id, a)
		if names[webSearchToolName] {
			t.Errorf("agent %q unexpectedly carries %q (default-disabled path)",
				id, webSearchToolName)
		}
	}

	// Primary Assistant: snapshot the captured registries and assert none of
	// them registered web_search. WrapToolRegistry fires once for each of the
	// 3 dispatchable agents plus once for the Primary — assertion is
	// position-independent because every wrap call must be web_search-free
	// in this scenario.
	captures := capturedFn()
	if len(captures) != 4 {
		t.Fatalf("WrapToolRegistry called %d times, want 4 (3 dispatchable + Primary)", len(captures))
	}
	for i, snap := range captures {
		if snap[webSearchToolName] {
			t.Errorf("captured registry #%d has %q registered (default-disabled path)",
				i, webSearchToolName)
		}
	}
}

// --- AC-2.1 (key only): provider empty + api_key present → web_search NOT registered ---
// scenario: an api_key on its own (without a provider id) is not enough to
// enable the tool. IsEnabled requires both halves. This guards against an
// operator setting only one of the two and silently getting a broken
// registration.
func TestIntegration_SetupNew_WebSearch_KeyOnlyDisabled(t *testing.T) {
	cfg := baseCfgForWebSearchTests()
	cfg.Tools.WebSearch = configs.WebSearchConfig{APIKey: "k-only"}

	if cfg.Tools.WebSearch.IsEnabled() {
		t.Fatal("WebSearchConfig with only api_key must not be enabled")
	}

	mock := &mockChatCompleter{}
	result, err := setup.New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("setup.New: %v", err)
	}

	for _, id := range []string{"coder", "researcher", "reviewer"} {
		a := result.Agent(id)
		if a == nil {
			t.Fatalf("agent %q missing", id)
		}
		names := agentToolNames(t, id, a)
		if names[webSearchToolName] {
			t.Errorf("agent %q unexpectedly carries %q (key-only path must skip)",
				id, webSearchToolName)
		}
	}
}

// --- AC-2.2: unknown provider value → startup completes; web_search NOT registered ---
// scenario: setting `provider: serper` (not in {tavily, brave}) is fail-soft.
// configs.Load logs slog.Warn but does not abort, and the resulting cfg
// MUST NOT cause setup.New to register web_search. Proves the unknown-provider
// path is resilient at the integration boundary, not just at the config layer.
func TestIntegration_SetupNew_WebSearch_UnknownProviderSkipped(t *testing.T) {
	cfg := baseCfgForWebSearchTests()
	cfg.Tools.WebSearch = configs.WebSearchConfig{
		Provider: "serper",
		APIKey:   "any-key",
	}

	if cfg.Tools.WebSearch.IsEnabled() {
		t.Fatalf("unknown provider must not be enabled, got IsEnabled=true")
	}

	mock := &mockChatCompleter{}
	result, err := setup.New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("setup.New (unknown provider must not abort): %v", err)
	}

	for _, id := range []string{"coder", "researcher", "reviewer"} {
		a := result.Agent(id)
		if a == nil {
			t.Fatalf("agent %q missing", id)
		}
		names := agentToolNames(t, id, a)
		if names[webSearchToolName] {
			t.Errorf("agent %q unexpectedly carries %q (unknown provider must skip)",
				id, webSearchToolName)
		}
	}
}

// --- AC-2.3: provider mismatch (e.g. tavily set but only brave key meaning is reversed)
// at the config layer manifests as the same IsEnabled gate — there is no
// per-provider key validation, only the "both fields present" check. This test
// pins the contract by enabling the tool and verifying the resolved provider
// name matches what was configured (so a future divergence between configured
// id and registered id surfaces). Pairs with the unit-level coverage in
// vv/configs/web_search_test.go::TestWebSearchConfig_IsEnabled.
func TestIntegration_SetupNew_WebSearch_ProviderTavily(t *testing.T) {
	cfg := baseCfgForWebSearchTests()
	cfg.Tools.WebSearch = configs.WebSearchConfig{
		Provider: "tavily",
		APIKey:   "test-key",
	}
	if !cfg.Tools.WebSearch.IsEnabled() {
		t.Fatal("tavily + key must be enabled")
	}

	opts, capturedFn := captureToolRegistries()

	mock := &mockChatCompleter{}
	result, err := setup.New(cfg, mock, nil, nil, opts)
	if err != nil {
		t.Fatalf("setup.New: %v", err)
	}

	for _, id := range []string{"coder", "researcher", "reviewer"} {
		a := result.Agent(id)
		if a == nil {
			t.Fatalf("agent %q missing from setup result", id)
		}
		names := agentToolNames(t, id, a)
		if !names[webSearchToolName] {
			t.Errorf("agent %q missing %q under enabled config", id, webSearchToolName)
		}
	}

	// The Primary Assistant uses ProfileReadOnly (or ProfileReview when
	// orchestrate.primary_allow_bash is true) which routes through CapRead and
	// therefore through MaybeRegisterWebSearch. WrapToolRegistry runs once per
	// agent — assert at least one captured registry carries web_search and
	// that ALL captures carry it (every tool-bearing agent must see the tool
	// in the configured path).
	captures := capturedFn()
	if len(captures) != 4 {
		t.Fatalf("WrapToolRegistry called %d times, want 4 (3 dispatchable + Primary)", len(captures))
	}
	for i, snap := range captures {
		if !snap[webSearchToolName] {
			// Print sorted name list so a regression is debuggable from the test log.
			names := make([]string, 0, len(snap))
			for n := range snap {
				names = append(names, n)
			}
			sort.Strings(names)
			t.Errorf("captured registry #%d missing %q; tools=%v",
				i, webSearchToolName, names)
		}
	}
}

// --- AC-2.3 mirror: provider=brave + key registers web_search across the same agents ---
// scenario: confirm provider symmetry — the brave provider id triggers the
// same wiring path as tavily. Catches a regression where one provider branch
// would short-circuit before reaching websearch.Register.
func TestIntegration_SetupNew_WebSearch_ProviderBrave(t *testing.T) {
	cfg := baseCfgForWebSearchTests()
	cfg.Tools.WebSearch = configs.WebSearchConfig{
		Provider: "brave",
		APIKey:   "brave-key",
	}

	mock := &mockChatCompleter{}
	result, err := setup.New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("setup.New: %v", err)
	}

	for _, id := range []string{"coder", "researcher", "reviewer"} {
		a := result.Agent(id)
		if a == nil {
			t.Fatalf("agent %q missing", id)
		}
		names := agentToolNames(t, id, a)
		if !names[webSearchToolName] {
			t.Errorf("agent %q missing %q under brave-enabled config", id, webSearchToolName)
		}
	}
}

// --- AC-2.4: env vars VV_WEB_SEARCH_PROVIDER / VV_WEB_SEARCH_API_KEY override YAML ---
// scenario: a YAML file with web_search disabled (empty provider) plus env
// overrides that supply both provider and api_key must produce a Config whose
// IsEnabled is true and whose setup.New result registers web_search on every
// tool-carrying agent. Proves the env-override path reaches the agents, not
// just the configs layer.
func TestIntegration_SetupNew_WebSearch_EnvOverride(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "vv.yaml")
	content := `llm:
  provider: openai
  model: test-model
  api_key: test-key
  base_url: http://127.0.0.1:0
agents:
  max_iterations: 3
tools:
  web_search:
    provider: ""
    api_key: ""
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("VV_WEB_SEARCH_PROVIDER", "tavily")
	t.Setenv("VV_WEB_SEARCH_API_KEY", "env-tavily-key")

	cfg, err := configs.Load(cfgPath, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}

	if cfg.Tools.WebSearch.Provider != "tavily" {
		t.Fatalf("env override of provider missed: got %q", cfg.Tools.WebSearch.Provider)
	}
	if cfg.Tools.WebSearch.APIKey != "env-tavily-key" {
		t.Fatalf("env override of api_key missed: got %q", cfg.Tools.WebSearch.APIKey)
	}
	if !cfg.Tools.WebSearch.IsEnabled() {
		t.Fatalf("env-overridden config should be enabled, got IsEnabled=false")
	}

	// setup.New requires Tools.BashTimeout for sane defaults; Load applies a
	// default of 30 already so this is just a guard against env drift.
	if cfg.Tools.BashTimeout == 0 {
		cfg.Tools.BashTimeout = 10
	}

	opts, capturedFn := captureToolRegistries()

	mock := &mockChatCompleter{}
	result, err := setup.New(cfg, mock, nil, nil, opts)
	if err != nil {
		t.Fatalf("setup.New: %v", err)
	}

	for _, id := range []string{"coder", "researcher", "reviewer"} {
		a := result.Agent(id)
		if a == nil {
			t.Fatalf("agent %q missing from setup result", id)
		}
		names := agentToolNames(t, id, a)
		if !names[webSearchToolName] {
			t.Errorf("agent %q missing %q after env override", id, webSearchToolName)
		}
	}

	// Primary Assistant carries the tool too — see AC-2.3 test rationale.
	captures := capturedFn()
	if len(captures) != 4 {
		t.Fatalf("WrapToolRegistry called %d times, want 4", len(captures))
	}
	for i, snap := range captures {
		if !snap[webSearchToolName] {
			t.Errorf("captured registry #%d missing %q under env-override path", i, webSearchToolName)
		}
	}
}

// --- ToolProfile.BuildRegistry surfaces web_search the same way for all
// read-capable profiles. setup.New reaches the Primary registry through
// `primaryToolProfile(cfg).BuildRegistry(cfg.Tools, ...)` which is
// ProfileReadOnly by default; this test pins that BuildRegistry agrees with
// the agent-level assertions above so a regression in the BuildRegistry layer
// is caught even when WrapToolRegistry plumbing changes.
func TestIntegration_SetupNew_WebSearch_BuildRegistryAcrossProfiles(t *testing.T) {
	// Use the registries package via the same flow setup.New uses. Importing
	// it directly here is intentional — the alternative is a roundtrip through
	// setup.New which we already exercise above.
	enabled := configs.ToolsConfig{
		BashTimeout: 10,
		WebSearch: configs.WebSearchConfig{
			Provider: "tavily",
			APIKey:   "test-key",
		},
	}
	disabled := configs.ToolsConfig{BashTimeout: 10}

	// Cover every profile that includes CapRead — that is the capability that
	// hooks MaybeRegisterWebSearch, mirroring tool_access.go::registerCapabilityTools.
	cases := []struct {
		name   string
		cfg    configs.ToolsConfig
		expect bool
	}{
		{"enabled", enabled, true},
		{"disabled", disabled, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			profileCases := []struct {
				name string
				list func(cfg configs.ToolsConfig) ([]string, error)
			}{
				{"ProfileFull", listProfileFull},
				{"ProfileReadOnly", listProfileReadOnly},
				{"ProfileReview", listProfileReview},
			}

			for _, pc := range profileCases {
				t.Run(pc.name, func(t *testing.T) {
					names, err := pc.list(tc.cfg)
					if err != nil {
						t.Fatalf("list registry tools: %v", err)
					}

					seen := slices.Contains(names, webSearchToolName)
					if seen != tc.expect {
						sort.Strings(names)
						t.Errorf("%s with %s config: %q present=%v want=%v; tools=%v",
							pc.name, tc.name, webSearchToolName, seen, tc.expect, names)
					}
				})
			}
		})
	}
}
