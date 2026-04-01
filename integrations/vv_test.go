package integrations

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/setup"
)

// TestIntegration_VV_Init verifies that the full initialization pipeline
// (config loading → LLM client → memory → agents) completes successfully
// using the real ~/.vv/vv.yaml configuration.
func TestIntegration_VV_Init(t *testing.T) {
	configPath := configs.DefaultPath()
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Skipf("config file %s not found, skipping integration test", configPath)
	}

	initResult, err := setup.InitFromFile(configPath, true, nil)
	if err != nil {
		t.Fatalf("setup.Init: %v", err)
	}

	if initResult.Config == nil {
		t.Fatal("expected non-nil Config")
	}

	if initResult.LLMClient == nil {
		t.Fatal("expected non-nil LLMClient")
	}

	if initResult.SetupResult == nil {
		t.Fatal("expected non-nil SetupResult")
	}

	if initResult.SetupResult.Dispatcher == nil {
		t.Fatal("expected non-nil Dispatcher")
	}

	if initResult.SetupResult.Dispatcher.ID() != "orchestrator" {
		t.Errorf("Dispatcher ID = %q, want %q", initResult.SetupResult.Dispatcher.ID(), "orchestrator")
	}

	// Verify all dispatchable agents are available.
	for _, id := range []string{"coder", "researcher", "reviewer", "chat"} {
		a := initResult.SetupResult.Agent(id)
		if a == nil {
			t.Errorf("expected agent %q to be created", id)
		}
	}

	t.Logf("init OK: provider=%s model=%s agents=%d",
		initResult.Config.LLM.Provider,
		initResult.Config.LLM.Model,
		len(initResult.SetupResult.Agents()),
	)
}

// TestIntegration_VV_RunPrompt performs a full end-to-end test: load real config,
// initialize all components, and execute a fixed prompt through the dispatcher.
// Requires a valid ~/.vv/vv.yaml with a working LLM API key.
func TestIntegration_VV_RunPrompt(t *testing.T) {
	configPath := configs.DefaultPath()
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Skipf("config file %s not found, skipping integration test", configPath)
	}

	// Check for API key via env or config.
	if os.Getenv("VV_LLM_API_KEY") == "" {
		cfg, err := configs.Load(configPath, true)
		if err != nil || configs.NeedsSetup(cfg) {
			t.Skip("no LLM API key available, skipping integration test")
		}
	}

	initResult, err := setup.InitFromFile(configPath, true, nil)
	if err != nil {
		t.Fatalf("setup.Init: %v", err)
	}

	// Execute a simple prompt through the dispatcher.
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	req := &schema.RunRequest{
		Messages: []schema.Message{
			schema.NewUserMessage("Reply with exactly: hello vv"),
		},
	}

	resp, err := initResult.SetupResult.Dispatcher.Run(ctx, req)
	if err != nil {
		t.Fatalf("Dispatcher.Run: %v", err)
	}

	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one response message")
	}

	// Log the response for manual verification.
	for i, msg := range resp.Messages {
		t.Logf("response[%d] agent=%s role=%s text=%s",
			i, msg.AgentID, msg.Role, msg.Content.Text())
	}
}

// TestIntegration_VV_RunSubAgent tests running a specific sub-agent (chat)
// directly, bypassing the dispatcher's classify/explore phases.
func TestIntegration_VV_RunSubAgent(t *testing.T) {
	configPath := configs.DefaultPath()
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		t.Skipf("config file %s not found, skipping integration test", configPath)
	}

	if os.Getenv("VV_LLM_API_KEY") == "" {
		cfg, err := configs.Load(configPath, true)
		if err != nil || configs.NeedsSetup(cfg) {
			t.Skip("no LLM API key available, skipping integration test")
		}
	}

	initResult, err := setup.InitFromFile(configPath, true, nil)
	if err != nil {
		t.Fatalf("setup.Init: %v", err)
	}

	chatAgent := initResult.SetupResult.Agent("chat")
	if chatAgent == nil {
		t.Fatal("chat agent not found")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	req := &schema.RunRequest{
		Messages: []schema.Message{
			schema.NewUserMessage("What is 2+3? Reply with just the number."),
		},
	}

	resp, err := chatAgent.Run(ctx, req)
	if err != nil {
		t.Fatalf("chat.Run: %v", err)
	}

	if len(resp.Messages) == 0 {
		t.Fatal("expected at least one response message")
	}

	for i, msg := range resp.Messages {
		t.Logf("chat response[%d] role=%s text=%s", i, msg.Role, msg.Content.Text())
	}
}
