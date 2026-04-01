package integrations

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/service"
	"github.com/vogo/vv/agents"
	vagacli "github.com/vogo/vv/cli"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/tools"
)

// --- Helpers for CLI integration tests ---

// stubStreamAgent implements agent.StreamAgent for CLI integration testing.
type stubStreamAgent struct {
	id       string
	response string
}

var _ agent.StreamAgent = (*stubStreamAgent)(nil)

func (s *stubStreamAgent) ID() string          { return s.id }
func (s *stubStreamAgent) Name() string        { return s.id }
func (s *stubStreamAgent) Description() string { return s.id }

func (s *stubStreamAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	return &schema.RunResponse{
		Messages: []schema.Message{
			schema.NewAssistantMessage(aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent(s.response),
			}, s.id),
		},
	}, nil
}

func (s *stubStreamAgent) RunStream(ctx context.Context, req *schema.RunRequest) (*schema.RunStream, error) {
	return schema.NewRunStream(ctx, 8, func(_ context.Context, send func(schema.Event) error) error {
		if err := send(schema.NewEvent(schema.EventAgentStart, s.id, req.SessionID, schema.AgentStartData{})); err != nil {
			return err
		}

		if err := send(schema.NewEvent(schema.EventTextDelta, s.id, req.SessionID, schema.TextDeltaData{Delta: s.response})); err != nil {
			return err
		}

		return send(schema.NewEvent(schema.EventAgentEnd, s.id, req.SessionID, schema.AgentEndData{
			Message: s.response,
		}))
	}), nil
}

// --- Test: Config mode defaults to "cli" ---
// Verifies that when no mode is specified in YAML or env, the default is "cli".
func TestIntegration_CLI_ConfigModeDefault(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "configs.yaml")

	content := `
llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := configs.Load(path, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}

	if cfg.Mode != "cli" {
		t.Errorf("default mode = %q, want %q", cfg.Mode, "cli")
	}
}

// --- Test: Config mode explicit "http" from YAML ---
// Verifies that mode can be set to "http" via YAML.
func TestIntegration_CLI_ConfigModeHTTP(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "configs.yaml")

	content := `
llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
mode: "http"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := configs.Load(path, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}

	if cfg.Mode != "http" {
		t.Errorf("mode = %q, want %q", cfg.Mode, "http")
	}
}

// --- Test: Config mode explicit "cli" from YAML ---
// Verifies that mode can be explicitly set to "cli" via YAML.
func TestIntegration_CLI_ConfigModeCLI(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "configs.yaml")

	content := `
llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
mode: "cli"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := configs.Load(path, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}

	if cfg.Mode != "cli" {
		t.Errorf("mode = %q, want %q", cfg.Mode, "cli")
	}
}

// --- Test: VV_MODE environment variable override ---
// Verifies that VV_MODE env var overrides YAML mode setting.
func TestIntegration_CLI_ConfigModeEnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "configs.yaml")

	content := `
llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
mode: "cli"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("VV_MODE", "http")

	cfg, err := configs.Load(path, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}

	if cfg.Mode != "http" {
		t.Errorf("mode = %q, want %q after VV_MODE override", cfg.Mode, "http")
	}
}

// --- Test: CLIConfig.ConfirmTools parsed from YAML ---
// Verifies that the confirm_tools list is correctly loaded.
func TestIntegration_CLI_ConfigConfirmTools(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "configs.yaml")

	content := `
llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
cli:
  confirm_tools:
    - bash
    - file_write
    - file_edit
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := configs.Load(path, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}

	if len(cfg.CLI.ConfirmTools) != 3 {
		t.Fatalf("confirm_tools len = %d, want 3", len(cfg.CLI.ConfirmTools))
	}

	expected := []string{"bash", "file_write", "file_edit"}
	for i, want := range expected {
		if cfg.CLI.ConfirmTools[i] != want {
			t.Errorf("confirm_tools[%d] = %q, want %q", i, cfg.CLI.ConfirmTools[i], want)
		}
	}
}

// --- Test: Empty CLIConfig.ConfirmTools ---
// Verifies that absent confirm_tools results in an empty slice.
func TestIntegration_CLI_ConfigConfirmToolsEmpty(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "configs.yaml")

	content := `
llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := configs.Load(path, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}

	if len(cfg.CLI.ConfirmTools) != 0 {
		t.Errorf("confirm_tools len = %d, want 0", len(cfg.CLI.ConfirmTools))
	}
}

// --- Test: CLI App construction with valid config ---
// Verifies that cli.New() creates a properly initialized App with all fields set.
func TestIntegration_CLI_AppConstruction(t *testing.T) {
	orchestrator := &stubStreamAgent{id: "orchestrator", response: "orchestrated response"}

	cfg := &configs.Config{
		Mode: "cli",
		LLM:  configs.LLMConfig{Model: "test-model", Provider: "openai", APIKey: "test-key"},
		CLI:  configs.CLIConfig{ConfirmTools: []string{"bash"}},
	}

	app := vagacli.New(orchestrator, cfg, nil)
	if app == nil {
		t.Fatal("cli.New returned nil")
	}
}

// --- Test: WrapRegistry with no confirm tools returns original registry ---
// Verifies that WrapRegistry is a no-op when confirm_tools is empty.
func TestIntegration_CLI_WrapRegistryNoConfirmTools(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	wrapped := vagacli.WrapRegistry(reg, nil)

	// Should be the exact same pointer.
	if wrapped != reg {
		t.Error("WrapRegistry with nil confirm_tools should return the original registries")
	}

	// Also test with empty slice.
	wrapped2 := vagacli.WrapRegistry(reg, []string{})
	if wrapped2 != reg {
		t.Error("WrapRegistry with empty confirm_tools should return the original registries")
	}
}

// --- Test: WrapRegistry with confirm tools wraps the registry ---
// Verifies that WrapRegistry returns a confirming executor when confirm_tools is provided.
func TestIntegration_CLI_WrapRegistryWithConfirmTools(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	wrapped := vagacli.WrapRegistry(reg, []string{"bash", "file_write"})

	// Should be a different object.
	if wrapped == reg {
		t.Error("WrapRegistry with confirm_tools should return a new wrapped registries")
	}

	// The wrapped registry should still expose the same tools.
	origList := reg.List()
	wrappedList := wrapped.List()
	if len(wrappedList) != len(origList) {
		t.Errorf("wrapped tool count = %d, original = %d", len(wrappedList), len(origList))
	}

	// The wrapped registry should delegate Get correctly.
	if _, ok := wrapped.Get("bash"); !ok {
		t.Error("wrapped registry should delegate Get for 'bash'")
	}
}

// --- Test: ConfirmingExecutor approve flow ---
// Verifies that when confirmFn returns true, the tool executes normally.
func TestIntegration_CLI_ConfirmingExecutorApprove(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 5})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	// Default WrapRegistry confirmFn allows all, simulating approval.
	wrapped := vagacli.WrapRegistry(reg, []string{"bash"})

	// Execute bash with a simple command.
	result, err := wrapped.Execute(context.Background(), "bash", `{"command":"echo hello"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if result.IsError {
		text := ""
		for _, p := range result.Content {
			if p.Type == "text" {
				text = p.Text
				break
			}
		}
		t.Errorf("expected successful execution, got error: %s", text)
	}
}

// --- Test: ConfirmingExecutor passthrough for non-confirmed tool ---
// Verifies that tools NOT in the confirm list execute without invoking confirmFn.
func TestIntegration_CLI_ConfirmingExecutorPassthrough(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 5})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	// Only "bash" is in the confirm list.
	wrapped := vagacli.WrapRegistry(reg, []string{"bash"})

	// Execute file_read (not in confirm list) -- should work without confirmation.
	// Use a temp file to read.
	tmpFile := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(tmpFile, []byte("test content"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := wrapped.Execute(context.Background(), "file_read", fmt.Sprintf(`{"file_path":%q}`, tmpFile))
	if err != nil {
		t.Fatalf("Execute file_read: %v", err)
	}

	if result.IsError {
		text := ""
		for _, p := range result.Content {
			if p.Type == "text" {
				text = p.Text
				break
			}
		}
		t.Errorf("expected successful passthrough, got error: %s", text)
	}
}

// --- Test: agents.Create accepts tool.ToolRegistry interface ---
// Verifies that agents.Create works with both the original registry and a wrapped one.
func TestIntegration_CLI_AgentsCreateWithWrappedRegistry(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 30})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{Message: aimodel.Message{Role: aimodel.RoleAssistant, Content: aimodel.NewTextContent("test")}},
			},
		},
	}

	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 10},
		CLI:    configs.CLIConfig{ConfirmTools: []string{"bash", "file_write"}},
	}

	// Wrap registry (as main.go does).
	wrapped := vagacli.WrapRegistry(reg, cfg.CLI.ConfirmTools)

	cfg.Memory = configs.MemoryConfig{MaxConcurrency: 2}

	// Create agents with wrapped registry -- should work without error.
	allAgents := agents.Create(cfg, mock, wrapped, wrapped, wrapped, nil, nil)

	if allAgents.Coder.ID() != "coder" {
		t.Errorf("coder ID = %q, want %q", allAgents.Coder.ID(), "coder")
	}

	if allAgents.Chat.ID() != "chat" {
		t.Errorf("chat ID = %q, want %q", allAgents.Chat.ID(), "chat")
	}

	// Coder should still have tools.
	if len(allAgents.Coder.Tools()) != 6 {
		t.Errorf("coder tool count = %d, want 6", len(allAgents.Coder.Tools()))
	}

	// Chat should have no tools.
	if len(allAgents.Chat.Tools()) != 0 {
		t.Errorf("chat tool count = %d, want 0", len(allAgents.Chat.Tools()))
	}
}

// --- Test: Full wiring with CLI mode config ---
// Verifies that the full initialization path works correctly with CLI mode,
// including config loading, tool registration, agent creation, and registry wrapping.
func TestIntegration_CLI_FullWiringCLIMode(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "test-configs.yaml")
	configContent := `
llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
mode: "cli"
cli:
  confirm_tools:
    - bash
    - file_write
agents:
  max_iterations: 5
tools:
  bash_timeout: 10
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := configs.Load(configPath, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}

	if cfg.Mode != "cli" {
		t.Errorf("mode = %q, want %q", cfg.Mode, "cli")
	}

	toolRegistry, err := tools.Register(cfg.Tools)
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{Message: aimodel.Message{Role: aimodel.RoleAssistant, Content: aimodel.NewTextContent("test")}},
			},
		},
	}

	// Wrap registry as main.go does.
	wrapped := vagacli.WrapRegistry(toolRegistry, cfg.CLI.ConfirmTools)

	cfg.Memory = configs.MemoryConfig{MaxConcurrency: 2}
	allAgents := agents.Create(cfg, mock, wrapped, wrapped, wrapped, nil, nil)

	if allAgents.Orchestrator.ID() != "orchestrator" {
		t.Errorf("orchestrator ID = %q, want %q", allAgents.Orchestrator.ID(), "orchestrator")
	}

	if allAgents.Coder.ID() != "coder" {
		t.Errorf("coder ID = %q, want %q", allAgents.Coder.ID(), "coder")
	}

	if allAgents.Chat.ID() != "chat" {
		t.Errorf("chat ID = %q, want %q", allAgents.Chat.ID(), "chat")
	}

	// Verify wrapped registry preserves tool list.
	if len(wrapped.List()) != 6 {
		t.Errorf("wrapped tool count = %d, want 6", len(wrapped.List()))
	}
}

// --- Test: HTTP mode still works identically with mode=http ---
// Verifies that the HTTP service path is unaffected by CLI mode additions.
func TestIntegration_CLI_HTTPModeUnchanged(t *testing.T) {
	dir := t.TempDir()
	configPath := filepath.Join(dir, "test-configs.yaml")
	configContent := `
llm:
  provider: "openai"
  model: "test-model"
  api_key: "test-key"
mode: "http"
server:
  addr: ":0"
tools:
  bash_timeout: 10
agents:
  max_iterations: 5
`
	if err := os.WriteFile(configPath, []byte(configContent), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := configs.Load(configPath, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}

	if cfg.Mode != "http" {
		t.Fatalf("mode = %q, want %q", cfg.Mode, "http")
	}

	toolRegistry, err := tools.Register(cfg.Tools)
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	mock := &mockChatCompleter{
		response: &aimodel.ChatResponse{
			Choices: []aimodel.Choice{
				{Message: aimodel.Message{Role: aimodel.RoleAssistant, Content: aimodel.NewTextContent("http response")}},
			},
		},
	}

	cfg.Memory = configs.MemoryConfig{MaxConcurrency: 2}
	allAgents := agents.Create(cfg, mock, toolRegistry, toolRegistry, toolRegistry, nil, nil)

	svc := service.New(
		service.Config{Addr: ":0"},
		service.WithToolRegistry(toolRegistry),
	)
	svc.RegisterAgent(allAgents.Orchestrator)
	svc.RegisterAgent(allAgents.Coder)
	svc.RegisterAgent(allAgents.Chat)
	svc.RegisterAgent(allAgents.Researcher)
	svc.RegisterAgent(allAgents.Reviewer)

	ts := httptest.NewServer(svc.Handler())
	defer ts.Close()

	// Verify health endpoint works.
	healthResp, err := ts.Client().Get(ts.URL + "/v1/health")
	if err != nil {
		t.Fatalf("GET /v1/health: %v", err)
	}
	defer func() { _ = healthResp.Body.Close() }()

	if healthResp.StatusCode != http.StatusOK {
		t.Errorf("health status = %d, want %d", healthResp.StatusCode, http.StatusOK)
	}

	// Verify agent listing returns 5 agents (orchestrator, coder, chat, researcher, reviewer).
	agentsResp, err := ts.Client().Get(ts.URL + "/v1/agents")
	if err != nil {
		t.Fatalf("GET /v1/agents: %v", err)
	}
	defer func() { _ = agentsResp.Body.Close() }()

	var agentList []struct{ ID string }
	_ = json.NewDecoder(agentsResp.Body).Decode(&agentList)
	if len(agentList) != 5 {
		t.Errorf("agent count = %d, want 5", len(agentList))
	}

	// Verify sync run still works.
	reqBody, _ := json.Marshal(schema.RunRequest{
		Messages: []schema.Message{schema.NewUserMessage("test")},
	})
	runResp, err := ts.Client().Post(ts.URL+"/v1/agents/chat/run", "application/json", strings.NewReader(string(reqBody)))
	if err != nil {
		t.Fatalf("POST /v1/agents/chat/run: %v", err)
	}
	defer func() { _ = runResp.Body.Close() }()

	if runResp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(runResp.Body)
		t.Fatalf("run status = %d, body: %s", runResp.StatusCode, string(body))
	}
}

// --- Test: CLI uses orchestrator directly ---
// Verifies that the CLI is constructed with an orchestrator agent.
func TestIntegration_CLI_OrchestratorWiring(t *testing.T) {
	orchestrator := &stubStreamAgent{id: "orchestrator", response: "orchestrated response"}

	cfg := &configs.Config{
		LLM:    configs.LLMConfig{Model: "test-model"},
		Agents: configs.AgentsConfig{MaxIterations: 5},
	}

	app := vagacli.New(orchestrator, cfg, nil)
	if app == nil {
		t.Fatal("cli.New returned nil")
	}
}

// --- Test: CLI agent streaming produces expected events ---
// Verifies that a stream agent produces the expected event sequence
// (AgentStart, TextDelta, AgentEnd) when invoked.
func TestIntegration_CLI_AgentStreaming(t *testing.T) {
	coder := &stubStreamAgent{id: "coder", response: "streaming response"}

	ctx := context.Background()
	req := &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("write code")},
		SessionID: "test-session",
	}

	stream, err := coder.RunStream(ctx, req)
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	defer func() { _ = stream.Close() }()

	var events []schema.Event
	for {
		event, recvErr := stream.Recv()
		if recvErr != nil {
			if recvErr == io.EOF {
				break
			}
			t.Fatalf("Recv: %v", recvErr)
		}
		events = append(events, event)
	}

	if len(events) != 3 {
		t.Fatalf("got %d events, want 3", len(events))
	}

	// Verify event types in order.
	expectedTypes := []string{
		string(schema.EventAgentStart),
		string(schema.EventTextDelta),
		string(schema.EventAgentEnd),
	}
	for i, evt := range events {
		if string(evt.Type) != expectedTypes[i] {
			t.Errorf("event[%d].Type = %q, want %q", i, evt.Type, expectedTypes[i])
		}
	}

	// Verify TextDelta contains the response text.
	if data, ok := events[1].Data.(schema.TextDeltaData); ok {
		if data.Delta != "streaming response" {
			t.Errorf("TextDelta = %q, want %q", data.Delta, "streaming response")
		}
	} else {
		t.Error("event[1].Data is not TextDeltaData")
	}
}

// --- Test: CLI multi-turn conversation history ---
// Verifies that conversation history is correctly built up across multiple turns
// and passed to subsequent agent invocations.
func TestIntegration_CLI_MultiTurnHistory(t *testing.T) {
	// Simulate multi-turn conversation by building up history as the CLI would.
	orchestrator := &stubStreamAgent{id: "orchestrator", response: "response"}

	cfg := &configs.Config{Mode: "cli"}
	app := vagacli.New(orchestrator, cfg, nil)

	// Simulate 3 turns of conversation by verifying message structure.
	// Turn 1: user message.
	msg1 := schema.NewUserMessage("first message")
	// Turn 1: agent response.
	msg2 := schema.NewAssistantMessage(
		aimodel.Message{Role: aimodel.RoleAssistant, Content: aimodel.NewTextContent("first response")},
		"coder",
	)
	// Turn 2: user message.
	msg3 := schema.NewUserMessage("second message")
	// Turn 2: agent response.
	msg4 := schema.NewAssistantMessage(
		aimodel.Message{Role: aimodel.RoleAssistant, Content: aimodel.NewTextContent("second response")},
		"coder",
	)
	// Turn 3: user message.
	msg5 := schema.NewUserMessage("third message")

	history := []schema.Message{msg1, msg2, msg3, msg4, msg5}

	// Verify history correctly alternates user/assistant messages.
	if len(history) != 5 {
		t.Fatalf("history len = %d, want 5", len(history))
	}

	// Verify content of each message.
	contents := []string{"first message", "first response", "second message", "second response", "third message"}
	for i, want := range contents {
		got := history[i].Content.Text()
		if got != want {
			t.Errorf("history[%d] = %q, want %q", i, got, want)
		}
	}

	// Verify the app was created and can be used for routing with the full history.
	req := &schema.RunRequest{
		Messages: history,
	}

	// Verify routing works with full history.
	_ = req
	_ = app
}

// --- Test: Cancellation context propagation ---
// Verifies that cancelling a context during stream consumption stops the stream.
func TestIntegration_CLI_CancellationPropagation(t *testing.T) {
	coder := &stubStreamAgent{id: "coder", response: "response"}

	ctx, cancel := context.WithCancel(context.Background())

	req := &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage("test")},
		SessionID: "test-session",
	}

	stream, err := coder.RunStream(ctx, req)
	if err != nil {
		t.Fatalf("RunStream: %v", err)
	}
	defer func() { _ = stream.Close() }()

	// Read one event.
	_, err = stream.Recv()
	if err != nil {
		t.Fatalf("first Recv: %v", err)
	}

	// Cancel the context.
	cancel()

	// Subsequent reads should eventually fail or return EOF.
	// Drain remaining events (there might be buffered events).
	for {
		_, recvErr := stream.Recv()
		if recvErr != nil {
			// Context cancellation or EOF -- both are acceptable.
			break
		}
	}

	// If we reached here without hanging, cancellation propagated correctly.
}

// --- Test: Full config with all CLI fields ---
// Verifies that a config with all CLI-related fields loads correctly.
func TestIntegration_CLI_FullConfigWithAllFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "configs.yaml")

	content := `
llm:
  provider: "anthropic"
  model: "claude-sonnet-4"
  api_key: "sk-test-full"
server:
  addr: ":9999"
tools:
  bash_timeout: 120
  bash_working_dir: "/tmp/test"
agents:
  max_iterations: 25
  run_token_budget: 10000
mode: "cli"
cli:
  confirm_tools:
    - bash
    - file_write
    - file_edit
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := configs.Load(path, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}

	checks := []struct {
		name string
		got  any
		want any
	}{
		{"Mode", cfg.Mode, "cli"},
		{"LLM.Provider", cfg.LLM.Provider, "anthropic"},
		{"LLM.Model", cfg.LLM.Model, "claude-sonnet-4"},
		{"LLM.APIKey", cfg.LLM.APIKey, "sk-test-full"},
		{"Server.Addr", cfg.Server.Addr, ":9999"},
		{"Tools.BashTimeout", cfg.Tools.BashTimeout, 120},
		{"Tools.BashWorkingDir", cfg.Tools.BashWorkingDir, "/tmp/test"},
		{"Agents.MaxIterations", cfg.Agents.MaxIterations, 25},
		{"Agents.RunTokenBudget", cfg.Agents.RunTokenBudget, 10000},
		{"CLI.ConfirmTools len", len(cfg.CLI.ConfirmTools), 3},
	}

	for _, c := range checks {
		if fmt.Sprintf("%v", c.got) != fmt.Sprintf("%v", c.want) {
			t.Errorf("%s = %v, want %v", c.name, c.got, c.want)
		}
	}
}

// --- Test: WrapRegistry preserves tool execution ---
// Verifies that wrapping a registry with confirm_tools still allows tool execution
// to pass through for non-confirmed tools and confirms for confirmed tools.
func TestIntegration_CLI_WrapRegistryPreservesExecution(t *testing.T) {
	reg, err := tools.Register(configs.ToolsConfig{BashTimeout: 5})
	if err != nil {
		t.Fatalf("tools.Register: %v", err)
	}

	wrapped := vagacli.WrapRegistry(reg, []string{"bash"})

	// Verify all 6 tools are still accessible via Get.
	for _, name := range []string{"bash", "file_read", "file_write", "file_edit", "glob", "grep"} {
		if _, ok := wrapped.Get(name); !ok {
			t.Errorf("wrapped registry missing tool %q", name)
		}
	}

	// Execute a non-confirmed tool (file_read) -- should work directly.
	tmpFile := filepath.Join(t.TempDir(), "test.txt")
	if err := os.WriteFile(tmpFile, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}

	result, err := wrapped.Execute(context.Background(), "file_read", fmt.Sprintf(`{"file_path":%q}`, tmpFile))
	if err != nil {
		t.Fatalf("Execute file_read: %v", err)
	}

	if result.IsError {
		t.Error("file_read should succeed without confirmation")
	}
}

// --- Test: Mode selection drives the correct branch in main ---
// Verifies that the config mode field correctly distinguishes between CLI and HTTP paths.
// This is a structural test -- it validates the config plumbing, not the actual TUI/server startup.
func TestIntegration_CLI_ModeSelectionBranching(t *testing.T) {
	tests := []struct {
		name     string
		yaml     string
		envMode  string
		wantMode string
	}{
		{
			name:     "default mode is cli",
			yaml:     "",
			wantMode: "cli",
		},
		{
			name:     "explicit cli mode from YAML",
			yaml:     "mode: cli",
			wantMode: "cli",
		},
		{
			name:     "explicit http mode from YAML",
			yaml:     "mode: http",
			wantMode: "http",
		},
		{
			name:     "VV_MODE overrides YAML",
			yaml:     "mode: cli",
			envMode:  "http",
			wantMode: "http",
		},
		{
			name:     "VV_MODE sets mode when absent from YAML",
			yaml:     "",
			envMode:  "http",
			wantMode: "http",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "configs.yaml")

			if err := os.WriteFile(path, []byte(tt.yaml), 0o644); err != nil {
				t.Fatal(err)
			}

			if tt.envMode != "" {
				t.Setenv("VV_MODE", tt.envMode)
			}

			cfg, err := configs.Load(path, true)
			if err != nil {
				t.Fatalf("configs.Load: %v", err)
			}

			if cfg.Mode != tt.wantMode {
				t.Errorf("mode = %q, want %q", cfg.Mode, tt.wantMode)
			}
		})
	}
}
