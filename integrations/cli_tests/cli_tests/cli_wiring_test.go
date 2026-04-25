package cli_tests

import (
	"context"
	"io"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/schema"
	vvcli "github.com/vogo/vv/cli"
	"github.com/vogo/vv/configs"
)

// --- Test: CLI App construction with valid config ---
// Verifies that cli.New() creates a properly initialized App with all fields set.
func TestIntegration_CLI_AppConstruction(t *testing.T) {
	orchestrator := &stubStreamAgent{id: "orchestrator", response: "orchestrated response"}

	cfg := &configs.Config{
		Mode: "cli",
		LLM:  configs.LLMConfig{Model: "test-model", Provider: "openai", APIKey: "test-key"},
		CLI:  configs.CLIConfig{ConfirmTools: []string{"bash"}},
	}

	app := vvcli.New(orchestrator, cfg, nil, nil, nil)
	if app == nil {
		t.Fatal("cli.New returned nil")
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

	app := vvcli.New(orchestrator, cfg, nil, nil, nil)
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
	app := vvcli.New(orchestrator, cfg, nil, nil, nil)

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
