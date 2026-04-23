package agents_tests

import (
	"testing"

	"github.com/vogo/vv/agents"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/tools"
)

// --- Test 1a: Coder has all 6 tools (bash, read, write, edit, glob, grep) ---
// Test cases:
//   - Coder agent has exactly 6 tools registered
//   - All expected tool names are present: bash, read, write, edit, glob, grep
func TestIntegration_Agents_CoderHasTools(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	readOnlyReg, err := tools.RegisterReadOnly(configs.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.RegisterReadOnly: %v", err)
	}

	reviewReg, err := tools.RegisterReviewTools(configs.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.RegisterReviewTools: %v", err)
	}

	mock := &mockChatCompleter{}
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
	}

	allAgents := agents.Create(cfg, mock, reg, readOnlyReg, reviewReg, nil, nil)
	toolList := allAgents.Coder.Tools()

	if len(toolList) != 6 {
		t.Fatalf("coder has %d tools, want 6", len(toolList))
	}

	toolNames := make(map[string]bool)
	for _, td := range toolList {
		toolNames[td.Name] = true
	}

	for _, name := range []string{"bash", "read", "write", "edit", "glob", "grep"} {
		if !toolNames[name] {
			t.Errorf("coder missing tool %q", name)
		}
	}
}

// --- Test 1b: Chat agent has no tools ---
// Test cases:
//   - Chat agent has zero tools registered
func TestIntegration_Agents_ChatHasNoTools(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	readOnlyReg, err := tools.RegisterReadOnly(configs.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.RegisterReadOnly: %v", err)
	}

	reviewReg, err := tools.RegisterReviewTools(configs.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.RegisterReviewTools: %v", err)
	}

	mock := &mockChatCompleter{}
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
	}

	allAgents := agents.Create(cfg, mock, reg, readOnlyReg, reviewReg, nil, nil)
	toolList := allAgents.Chat.Tools()

	if len(toolList) != 0 {
		t.Errorf("chat has %d tools, want 0", len(toolList))
	}
}

// --- Test 2: Researcher agent has exactly read-only tools (read, glob, grep) and no write/edit/bash ---
// Test cases:
//   - Researcher agent has exactly 3 tools
//   - All expected tools present: read, glob, grep
//   - Write/edit/bash tools are NOT present
func TestIntegration_Agents_ResearcherHasReadOnlyTools(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	readOnlyReg, err := tools.RegisterReadOnly(configs.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.RegisterReadOnly: %v", err)
	}

	reviewReg, err := tools.RegisterReviewTools(configs.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.RegisterReviewTools: %v", err)
	}

	mock := &mockChatCompleter{}
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
	}

	allAgents := agents.Create(cfg, mock, reg, readOnlyReg, reviewReg, nil, nil)
	toolList := allAgents.Researcher.Tools()

	if len(toolList) != 3 {
		t.Fatalf("researcher has %d tools, want 3", len(toolList))
	}

	toolNames := make(map[string]bool)
	for _, td := range toolList {
		toolNames[td.Name] = true
	}

	for _, name := range []string{"read", "glob", "grep"} {
		if !toolNames[name] {
			t.Errorf("researcher missing tool %q", name)
		}
	}

	// Verify researcher does NOT have write/edit/bash.
	for _, name := range []string{"bash", "write", "edit"} {
		if toolNames[name] {
			t.Errorf("researcher should not have tool %q", name)
		}
	}
}

// --- Test 3: Reviewer agent has read + bash tools (read, glob, grep, bash) but not write/edit ---
// Test cases:
//   - Reviewer agent has exactly 4 tools
//   - All expected tools present: bash, read, glob, grep
//   - Write/edit tools are NOT present
func TestIntegration_Agents_ReviewerHasCorrectTools(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	readOnlyReg, err := tools.RegisterReadOnly(configs.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.RegisterReadOnly: %v", err)
	}

	reviewReg, err := tools.RegisterReviewTools(configs.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.RegisterReviewTools: %v", err)
	}

	mock := &mockChatCompleter{}
	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		Memory: configs.MemoryConfig{MaxConcurrency: 2},
	}

	allAgents := agents.Create(cfg, mock, reg, readOnlyReg, reviewReg, nil, nil)
	toolList := allAgents.Reviewer.Tools()

	if len(toolList) != 4 {
		t.Fatalf("reviewer has %d tools, want 4", len(toolList))
	}

	toolNames := make(map[string]bool)
	for _, td := range toolList {
		toolNames[td.Name] = true
	}

	for _, name := range []string{"bash", "read", "glob", "grep"} {
		if !toolNames[name] {
			t.Errorf("reviewer missing tool %q", name)
		}
	}

	// Verify reviewer does NOT have write/edit.
	for _, name := range []string{"write", "edit"} {
		if toolNames[name] {
			t.Errorf("reviewer should not have tool %q", name)
		}
	}
}
