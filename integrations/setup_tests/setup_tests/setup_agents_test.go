package setup_tests

import (
	"testing"

	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/registries"
	"github.com/vogo/vv/setup"
)

// --- Test: setup.New() creates all agents with correct IDs ---
// Verifies that setup.New() produces a Result with all expected dispatchable agents
// and a working Dispatcher.
// Test cases:
//   - Result.Dispatcher is non-nil and has ID "orchestrator"
//   - All 4 dispatchable agents are created: coder, researcher, reviewer, chat
//   - Each agent has the correct ID
//   - Result.Agents() returns exactly 4 agents (sorted by ID)
func TestIntegration_SetupNew_AllAgentsCreated(t *testing.T) {
	mock := &mockChatCompleter{}
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}

	result, err := setup.New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("setup.New: %v", err)
	}

	if result.Dispatcher == nil {
		t.Fatal("expected non-nil Dispatcher")
	}

	if result.Dispatcher.ID() != "orchestrator" {
		t.Errorf("Dispatcher ID = %q, want %q", result.Dispatcher.ID(), "orchestrator")
	}

	// Verify the 3 dispatchable agents (chat removed in M6 G2).
	for _, id := range []string{"coder", "researcher", "reviewer"} {
		a := result.Agent(id)
		if a == nil {
			t.Errorf("expected agent %q to be created", id)
		} else if a.ID() != id {
			t.Errorf("agent ID = %q, want %q", a.ID(), id)
		}
	}

	agents := result.Agents()
	if len(agents) != 3 {
		t.Fatalf("Agents() = %d, want 3", len(agents))
	}

	// Verify sorted order.
	for i := 1; i < len(agents); i++ {
		if agents[i-1].ID() >= agents[i].ID() {
			t.Errorf("Agents() not sorted: %q >= %q", agents[i-1].ID(), agents[i].ID())
		}
	}
}

// --- Test: setup.New() coder has correct tool count (ProfileFull + todo_write) ---
// Verifies that the coder agent built through setup.New() has all 6 tools from
// ProfileFull plus todo_write which setup registers on every tool-carrying
// agent.
// Test cases:
//   - Coder agent has exactly 7 tools
//   - All expected tool names are present: bash, read, write, edit, glob, grep, todo_write
func TestIntegration_SetupNew_CoderHasFullTools(t *testing.T) {
	mock := &mockChatCompleter{}
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}

	result, err := setup.New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("setup.New: %v", err)
	}

	coderAgent := result.Agent("coder")
	if coderAgent == nil {
		t.Fatal("coder agent not found")
	}

	coder, ok := coderAgent.(*taskagent.Agent)
	if !ok {
		t.Fatalf("coder is %T, want *taskagent.Agent", coderAgent)
	}

	toolList := coder.Tools()
	if len(toolList) != 7 {
		t.Fatalf("coder has %d tools, want 7", len(toolList))
	}

	toolNames := make(map[string]bool)
	for _, td := range toolList {
		toolNames[td.Name] = true
	}

	for _, name := range []string{"bash", "read", "write", "edit", "glob", "grep", "todo_write"} {
		if !toolNames[name] {
			t.Errorf("coder missing tool %q", name)
		}
	}
}

// --- Test: setup.New() researcher has read-only tools plus todo_write ---
// Test cases:
//   - Researcher agent has exactly 4 tools (ProfileReadOnly + todo_write)
//   - Expected tools: read, glob, grep, todo_write
//   - Write/edit/bash tools are NOT present
func TestIntegration_SetupNew_ResearcherHasReadOnlyTools(t *testing.T) {
	mock := &mockChatCompleter{}
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}

	result, err := setup.New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("setup.New: %v", err)
	}

	researcherAgent := result.Agent("researcher")
	if researcherAgent == nil {
		t.Fatal("researcher agent not found")
	}

	researcher, ok := researcherAgent.(*taskagent.Agent)
	if !ok {
		t.Fatalf("researcher is %T, want *taskagent.Agent", researcherAgent)
	}

	toolList := researcher.Tools()
	if len(toolList) != 4 {
		t.Fatalf("researcher has %d tools, want 4", len(toolList))
	}

	toolNames := make(map[string]bool)
	for _, td := range toolList {
		toolNames[td.Name] = true
	}

	for _, name := range []string{"read", "glob", "grep", "todo_write"} {
		if !toolNames[name] {
			t.Errorf("researcher missing tool %q", name)
		}
	}

	for _, name := range []string{"bash", "write", "edit"} {
		if toolNames[name] {
			t.Errorf("researcher should not have tool %q", name)
		}
	}
}

// --- Test: setup.New() reviewer has review tools plus todo_write ---
// Test cases:
//   - Reviewer agent has exactly 5 tools (ProfileReview + todo_write)
//   - Expected tools: bash, read, glob, grep, todo_write
//   - Write/edit tools are NOT present
func TestIntegration_SetupNew_ReviewerHasReviewTools(t *testing.T) {
	mock := &mockChatCompleter{}
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		Tools:  configs.ToolsConfig{BashTimeout: 10},
	}

	result, err := setup.New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("setup.New: %v", err)
	}

	reviewerAgent := result.Agent("reviewer")
	if reviewerAgent == nil {
		t.Fatal("reviewer agent not found")
	}

	reviewer, ok := reviewerAgent.(*taskagent.Agent)
	if !ok {
		t.Fatalf("reviewer is %T, want *taskagent.Agent", reviewerAgent)
	}

	toolList := reviewer.Tools()
	if len(toolList) != 5 {
		t.Fatalf("reviewer has %d tools, want 5", len(toolList))
	}

	toolNames := make(map[string]bool)
	for _, td := range toolList {
		toolNames[td.Name] = true
	}

	for _, name := range []string{"bash", "read", "glob", "grep", "todo_write"} {
		if !toolNames[name] {
			t.Errorf("reviewer missing tool %q", name)
		}
	}

	for _, name := range []string{"write", "edit"} {
		if toolNames[name] {
			t.Errorf("reviewer should not have tool %q", name)
		}
	}
}

// --- Test: ToolProfile.BuildRegistry produces correct tool counts ---
// Verifies tool profile -> registry mapping at the integration level.
// Test cases:
//   - ProfileFull produces 6 tools
//   - ProfileReadOnly produces 3 tools
//   - ProfileReview produces 4 tools
//   - ProfileNone produces 0 tools
func TestIntegration_ToolProfileBuildRegistryCounts(t *testing.T) {
	toolsCfg := configs.ToolsConfig{BashTimeout: 10}

	tests := []struct {
		name    string
		profile registries.ToolProfile
		want    int
	}{
		{"full", registries.ProfileFull, 6},
		{"read-only", registries.ProfileReadOnly, 3},
		{"review", registries.ProfileReview, 4},
		{"none", registries.ProfileNone, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reg, err := tt.profile.BuildRegistry(toolsCfg)
			if err != nil {
				t.Fatalf("BuildRegistry: %v", err)
			}

			got := len(reg.List())
			if got != tt.want {
				t.Errorf("%s profile: %d tools, want %d", tt.name, got, tt.want)
			}
		})
	}
}
