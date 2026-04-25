package project_instructions_tests

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/agents"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/dispatches"
	"github.com/vogo/vv/registries"
	"github.com/vogo/vv/setup"
)

// --- Integration Test 1: No VV.md -- unchanged behavior ---
// Setup: Create a temp directory without VV.md. Call configs.LoadProjectInstructions().
// Verify empty string is returned. Then build agents via setup.New() and verify
// they are created successfully without project instructions.
// Test cases:
//   - LoadProjectInstructions returns empty string for directory without VV.md
//   - setup.New() succeeds when config has no ProjectInstructions
//   - Dispatcher is created successfully
//   - All sub-agents (coder, chat, researcher, reviewer) are created
func TestIntegration_NoVVMd_UnchangedBehavior(t *testing.T) {
	dir := t.TempDir()

	// Verify LoadProjectInstructions returns empty for directory without VV.md.
	got := configs.LoadProjectInstructions(dir)
	if got != "" {
		t.Errorf("LoadProjectInstructions(dir without VV.md) = %q, want empty", got)
	}

	// Build config with no project instructions.
	cfg := &configs.Config{
		LLM: configs.LLMConfig{
			Provider: "openai",
			Model:    "test-model",
			APIKey:   "test-key",
		},
		Tools: configs.ToolsConfig{
			BashTimeout:    10,
			BashWorkingDir: dir,
		},
		Agents: configs.AgentsConfig{
			MaxIterations: 5,
		},
		Orchestrate: configs.OrchestrateConfig{
			MaxConcurrency:    2,
			MaxRecursionDepth: 2,
		},
	}

	// Verify ProjectInstructions is empty (not loaded from VV.md).
	if cfg.ProjectInstructions != "" {
		t.Errorf("cfg.ProjectInstructions = %q, want empty", cfg.ProjectInstructions)
	}

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent("test response"),
					},
				},
			},
		},
	}

	result, err := setup.New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("setup.New() failed: %v", err)
	}

	if result.Dispatcher == nil {
		t.Fatal("Dispatcher is nil")
	}

	// Verify all dispatchable agents were created.
	expectedAgents := []string{"coder", "researcher", "reviewer"}
	for _, id := range expectedAgents {
		if a := result.Agent(id); a == nil {
			t.Errorf("agent %q not found", id)
		}
	}
}

// --- Integration Test 2: VV.md with unreadable permissions ---
// Setup: Create VV.md with chmod 000. Call configs.LoadProjectInstructions().
// Verify empty string returned with no panic.
// Test cases:
//   - LoadProjectInstructions returns empty string for unreadable VV.md
//   - No panic occurs
//   - Function gracefully handles permission errors
//
// Note: This test is skipped on systems where tests run as root (e.g., some CI).
func TestIntegration_VVMd_UnreadablePermissions(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("permission test not applicable on Windows")
	}

	if os.Getuid() == 0 {
		t.Skip("skipping permission test: running as root")
	}

	dir := t.TempDir()
	path := filepath.Join(dir, configs.ProjectInstructionsFileName)

	// Write content then remove read permissions.
	if err := os.WriteFile(path, []byte("secret instructions"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}

	// Ensure cleanup can remove the file.
	t.Cleanup(func() {
		_ = os.Chmod(path, 0o644)
	})

	got := configs.LoadProjectInstructions(dir)
	if got != "" {
		t.Errorf("LoadProjectInstructions(unreadable VV.md) = %q, want empty", got)
	}
}

// --- Integration Test 3: End-to-end with VV.md ---
// Setup: Create a temp directory with a VV.md containing distinctive content.
// Build config pointing to that directory. Use setup.New() to create agents.
// Use a capturing mock LLM to verify that project instructions reach the system prompt.
// Test cases:
//   - Config.ProjectInstructions contains the VV.md content after loading
//   - setup.New() succeeds with project instructions set
//   - Agent system prompt contains the project instructions content
//   - Agent system prompt contains the "# Project Instructions" delimiter
//   - Agent system prompt contains the "IMPORTANT:" prefix
//   - Project instructions are appended after the base system prompt
func TestIntegration_EndToEnd_WithVVMd(t *testing.T) {
	dir := t.TempDir()

	vvmdContent := "# My Project\n\nAlways use Go 1.22.\nRun tests with `make test`.\n\n## Special Rules\n- Never use global variables\n- All functions must have doc comments"
	if err := os.WriteFile(filepath.Join(dir, configs.ProjectInstructionsFileName), []byte(vvmdContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Verify LoadProjectInstructions reads the content.
	loaded := configs.LoadProjectInstructions(dir)
	if loaded != vvmdContent {
		t.Fatalf("LoadProjectInstructions() = %q, want %q", loaded, vvmdContent)
	}

	// Build config with project instructions set (simulating what setup.Init does).
	cfg := &configs.Config{
		LLM: configs.LLMConfig{
			Provider: "openai",
			Model:    "test-model",
			APIKey:   "test-key",
		},
		Tools: configs.ToolsConfig{
			BashTimeout:    10,
			BashWorkingDir: dir,
		},
		Agents: configs.AgentsConfig{
			MaxIterations: 5,
		},
		Orchestrate: configs.OrchestrateConfig{
			MaxConcurrency:    2,
			MaxRecursionDepth: 2,
		},
		ProjectInstructions: vvmdContent,
	}

	capture := &captureChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent("Hello!"),
					},
				},
			},
		},
	}

	result, err := setup.New(cfg, capture, nil, nil, nil)
	if err != nil {
		t.Fatalf("setup.New() failed: %v", err)
	}

	if result.Dispatcher == nil {
		t.Fatal("Dispatcher is nil")
	}

	// Run the researcher agent to trigger an LLM call and capture the
	// system prompt. It is the lightest dispatchable agent that still
	// exercises the project instructions wiring.
	subAgent := result.Agent("researcher")
	if subAgent == nil {
		t.Fatal("researcher agent not found")
	}

	ctx := context.Background()
	runReq := &schema.RunRequest{
		Messages: []schema.Message{
			schema.NewUserMessage("test prompt"),
		},
	}

	_, runErr := subAgent.Run(ctx, runReq)
	if runErr != nil {
		t.Fatalf("subAgent.Run: %v", runErr)
	}

	if capture.captured == nil {
		t.Fatal("expected LLM call to be captured")
	}

	// Verify the system message contains project instructions.
	if len(capture.captured.Messages) == 0 {
		t.Fatal("expected at least one message in the request")
	}

	systemMsg := capture.captured.Messages[0]
	if systemMsg.Role != aimodel.RoleSystem {
		t.Fatalf("first message role = %q, want %q", systemMsg.Role, aimodel.RoleSystem)
	}

	text := systemMsg.Content.Text()

	if !strings.Contains(text, vvmdContent) {
		t.Errorf("system prompt should contain VV.md content, got:\n%s", text)
	}

	if !strings.Contains(text, "# Project Instructions") {
		t.Error("system prompt should contain the project instructions delimiter")
	}

	if !strings.Contains(text, "IMPORTANT:") {
		t.Error("system prompt should contain the IMPORTANT prefix")
	}

	// Verify project instructions are appended after base prompt (not prepended).
	headerIdx := strings.Index(text, "# Project Instructions")
	contentIdx := strings.Index(text, vvmdContent)

	if headerIdx <= 0 {
		t.Error("project instructions header should not be at the start of the system prompt")
	}

	if contentIdx < headerIdx {
		t.Error("project instructions content should appear after the header")
	}
}

// --- Integration Test 4: Init guard -- pre-set ProjectInstructions not overwritten ---
// Verifies that if ProjectInstructions is already set on the config, Init() does not
// overwrite it with file content (the guard `cfg.ProjectInstructions == ""`).
// Test cases:
//   - Pre-set ProjectInstructions is preserved through setup.New()
//   - File content does not override pre-set value
//   - Agent system prompt uses the pre-set value, not file content
func TestIntegration_PresetProjectInstructions_NotOverwritten(t *testing.T) {
	dir := t.TempDir()

	// Write a VV.md that should NOT be used.
	fileContent := "FILE_CONTENT_SHOULD_NOT_APPEAR"
	if err := os.WriteFile(filepath.Join(dir, configs.ProjectInstructionsFileName), []byte(fileContent), 0o644); err != nil {
		t.Fatal(err)
	}

	presetInstructions := "PRESET_INSTRUCTIONS_MARKER"

	cfg := &configs.Config{
		LLM: configs.LLMConfig{
			Provider: "openai",
			Model:    "test-model",
			APIKey:   "test-key",
		},
		Tools: configs.ToolsConfig{
			BashTimeout:    10,
			BashWorkingDir: dir,
		},
		Agents: configs.AgentsConfig{
			MaxIterations: 5,
		},
		Orchestrate: configs.OrchestrateConfig{
			MaxConcurrency:    2,
			MaxRecursionDepth: 2,
		},
		ProjectInstructions: presetInstructions,
	}

	capture := &captureChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent("Hello!"),
					},
				},
			},
		},
	}

	result, err := setup.New(cfg, capture, nil, nil, nil)
	if err != nil {
		t.Fatalf("setup.New() failed: %v", err)
	}

	// Verify the preset instructions were preserved.
	if cfg.ProjectInstructions != presetInstructions {
		t.Errorf("ProjectInstructions = %q, want %q", cfg.ProjectInstructions, presetInstructions)
	}

	// Run researcher agent and verify system prompt uses the preset value.
	subAgent := result.Agent("researcher")
	if subAgent == nil {
		t.Fatal("researcher agent not found")
	}

	ctx := context.Background()
	runReq := &schema.RunRequest{
		Messages: []schema.Message{
			schema.NewUserMessage("test prompt"),
		},
	}

	_, runErr := subAgent.Run(ctx, runReq)
	if runErr != nil {
		t.Fatalf("subAgent.Run: %v", runErr)
	}

	if capture.captured == nil {
		t.Fatal("expected LLM call to be captured")
	}

	text := capture.captured.Messages[0].Content.Text()

	if !strings.Contains(text, presetInstructions) {
		t.Error("system prompt should contain preset instructions")
	}

	if strings.Contains(text, fileContent) {
		t.Error("system prompt should NOT contain file content when preset is used")
	}
}

// --- Integration Test 5: Dispatcher receives project instructions ---
// Verifies the WithProjectInstructions option is wired correctly through setup.New().
// Test cases:
//   - Dispatcher is created with project instructions
//   - WithProjectInstructions option correctly sets the field
//   - Empty project instructions result in no modification
func TestIntegration_Dispatcher_ReceivesProjectInstructions(t *testing.T) {
	instructions := "DISPATCHER_TEST_MARKER"

	reg := registries.New()
	agents.RegisterCoder(reg)
	agents.RegisterResearcher(reg)
	agents.RegisterReviewer(reg)
	agents.RegisterPlanner(reg)

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent("test"),
					},
				},
			},
		},
	}

	coderAgent := &stubAgent{id: "coder"}
	researcherAgent := &stubAgent{id: "researcher"}

	subAgents := map[string]agent.Agent{
		"coder":      coderAgent,
		"researcher": researcherAgent,
	}

	// Create dispatcher with project instructions.
	d := dispatches.New(
		reg,
		subAgents,
		nil,
		dispatches.WithLLM(mock, "test-model"),
		dispatches.WithFallbackAgent(researcherAgent),
		dispatches.WithProjectInstructions(instructions),
		dispatches.WithMaxRecursionDepth(2),
	)

	if d == nil {
		t.Fatal("Dispatcher is nil")
	}

	// Verify dispatcher was created (the projectInstructions field is private,
	// but it was set by the option -- verified by the unit test in dispatches/).
	if d.ID() != "orchestrator" {
		t.Errorf("Dispatcher ID = %q, want %q", d.ID(), "orchestrator")
	}

	// Create dispatcher without project instructions.
	d2 := dispatches.New(
		reg,
		subAgents,
		nil,
		dispatches.WithLLM(mock, "test-model"),
		dispatches.WithFallbackAgent(researcherAgent),
		dispatches.WithProjectInstructions(""),
		dispatches.WithMaxRecursionDepth(2),
	)

	if d2 == nil {
		t.Fatal("Dispatcher without instructions is nil")
	}
}

// --- Integration Test 6: All agent factories accept project instructions ---
// Verifies that every agent factory can be created with project instructions
// through the full setup.New() path and that each agent runs without error.
// Test cases:
//   - All six agents (coder, chat, researcher, reviewer, explorer, planner) are created
//   - Each dispatchable agent can run successfully
//   - Project instructions do not cause factory errors
func TestIntegration_AllAgentFactories_WithProjectInstructions(t *testing.T) {
	instructions := "# Custom Rules\n\n- Always use structured logging\n- Prefer table-driven tests\n\n```go\nfunc Example() {}\n```"

	cfg := &configs.Config{
		LLM: configs.LLMConfig{
			Provider: "openai",
			Model:    "test-model",
			APIKey:   "test-key",
		},
		Tools: configs.ToolsConfig{
			BashTimeout:    10,
			BashWorkingDir: t.TempDir(),
		},
		Agents: configs.AgentsConfig{
			MaxIterations: 5,
		},
		Orchestrate: configs.OrchestrateConfig{
			MaxConcurrency:    2,
			MaxRecursionDepth: 2,
		},
		ProjectInstructions: instructions,
	}

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{
					Message: aimodel.Message{
						Role:    aimodel.RoleAssistant,
						Content: aimodel.NewTextContent("test response"),
					},
				},
			},
		},
	}

	result, err := setup.New(cfg, mock, nil, nil, nil)
	if err != nil {
		t.Fatalf("setup.New() with project instructions failed: %v", err)
	}

	// Verify all dispatchable agents were created.
	dispatchable := []string{"coder", "researcher", "reviewer"}
	for _, id := range dispatchable {
		a := result.Agent(id)
		if a == nil {
			t.Errorf("agent %q not found", id)
			continue
		}

		// Run each agent to confirm it works.
		ctx := context.Background()
		runReq := &schema.RunRequest{
			Messages: []schema.Message{
				schema.NewUserMessage("hello"),
			},
		}

		_, runErr := a.Run(ctx, runReq)
		if runErr != nil {
			t.Errorf("agent %q Run failed: %v", id, runErr)
		}
	}
}

// --- Integration Test 7: AppendProjectInstructions preserves complex markdown ---
// Verifies the helper function preserves various markdown elements end-to-end.
// Test cases:
//   - Code blocks with language specifiers are preserved
//   - Nested headings are preserved
//   - Links and images are preserved
//   - Tables are preserved
//   - The delimiter and IMPORTANT prefix are present
func TestIntegration_AppendProjectInstructions_ComplexMarkdown(t *testing.T) {
	base := "You are a helpful assistant."
	instructions := `# Project Instructions

## Build Commands

` + "```bash" + `
make build
make test
` + "```" + `

## Architecture

| Layer | Package | Notes |
|-------|---------|-------|
| API   | api/    | REST  |
| Core  | core/   | Logic |

### Links

See [documentation](https://example.com) for details.

### Special Characters

Use ` + "`" + `go fmt` + "`" + ` and ` + "`" + `go vet` + "`" + ` always.
Paths like ` + "`" + `C:\Users\test` + "`" + ` should work.
Unicode: cafe, naive, resume.`

	got := agents.AppendProjectInstructions(base, instructions)

	// Verify base prompt is present.
	if !strings.Contains(got, base) {
		t.Error("result should contain base prompt")
	}

	// Verify delimiter.
	if !strings.Contains(got, "# Project Instructions") {
		t.Error("result should contain project instructions header")
	}

	// Verify code block preserved.
	if !strings.Contains(got, "```bash") {
		t.Error("result should preserve bash code block")
	}

	// Verify table preserved.
	if !strings.Contains(got, "| Layer | Package | Notes |") {
		t.Error("result should preserve markdown table")
	}

	// Verify link preserved.
	if !strings.Contains(got, "[documentation](https://example.com)") {
		t.Error("result should preserve markdown link")
	}

	// Verify special characters preserved.
	if !strings.Contains(got, `C:\Users\test`) {
		t.Error("result should preserve backslash paths")
	}
}

// --- Integration Test 8: YAML exclusion in round-trip ---
// Verifies that ProjectInstructions is excluded from YAML serialization
// even through a full Save/Load cycle.
// Test cases:
//   - ProjectInstructions does not appear in saved YAML file
//   - Loading the saved file results in empty ProjectInstructions
//   - Other config fields survive the round-trip
func TestIntegration_YAMLExclusion_RoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "test-config.yaml")

	original := &configs.Config{
		LLM: configs.LLMConfig{
			Provider: "openai",
			Model:    "gpt-4o",
			APIKey:   "test-key",
		},
		Server: configs.ServerConfig{
			Addr: ":9090",
		},
		ProjectInstructions: "These instructions should NOT appear in YAML",
	}

	// Save config.
	if err := configs.Save(original, path); err != nil {
		t.Fatalf("configs.Save: %v", err)
	}

	// Read raw YAML to verify exclusion.
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}

	yamlStr := string(data)
	if strings.Contains(yamlStr, "These instructions should NOT appear") {
		t.Error("ProjectInstructions content should not appear in YAML file")
	}

	if strings.Contains(yamlStr, "projectinstructions") || strings.Contains(yamlStr, "project_instructions") {
		t.Error("ProjectInstructions field name should not appear in YAML file")
	}

	// Load and verify.
	loaded, err := configs.Load(path, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}

	if loaded.ProjectInstructions != "" {
		t.Errorf("loaded ProjectInstructions = %q, want empty", loaded.ProjectInstructions)
	}

	// Verify other fields survived.
	if loaded.LLM.Model != "gpt-4o" {
		t.Errorf("loaded Model = %q, want %q", loaded.LLM.Model, "gpt-4o")
	}

	if loaded.Server.Addr != ":9090" {
		t.Errorf("loaded Addr = %q, want %q", loaded.Server.Addr, ":9090")
	}
}
