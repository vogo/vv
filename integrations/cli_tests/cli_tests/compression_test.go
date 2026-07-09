package cli_tests

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/largemodel"
	"github.com/vogo/vage/memory"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/tool"
	vvcli "github.com/vogo/vv/cli"
	"github.com/vogo/vv/configs"
)

// =============================================================================
// Integration Test: End-to-End Proactive Compression via CompactIfNeeded
// =============================================================================

// TestIntegration_Compression_ProactiveCompactEndToEnd verifies that the
// proactive compression path works end-to-end: when a conversation's estimated
// token count exceeds the threshold (derived from ModelMaxContextTokens and
// CompressionThreshold), CompactIfNeeded triggers compaction and produces a
// smaller history with a summary message.
//
// This simulates the CLI's invokeAgent path where:
// 1. Token estimate is checked against threshold.
// 2. CompactIfNeeded is called.
// 3. History is replaced with compressed version.
func TestIntegration_Compression_ProactiveCompactEndToEnd(t *testing.T) {
	// Configure small context window and low threshold to trigger compaction easily.
	threshold := 0.5
	contextCfg := configs.ContextConfig{
		ModelMaxContextTokens: 2000,
		CompressionThreshold:  &threshold,
		ToolOutputMaxTokens:   2000,
		ProtectedTurns:        2,
	}

	compactor := memory.NewConversationCompactor(compressionMockSummarizer(), contextCfg.ProtectedTurns)

	// Build a conversation that exceeds the threshold.
	// Threshold = 2000 * 0.9 (safety) * 0.5 = 900 tokens.
	history := []schema.Message{
		{Message: aimodel.Message{Role: aimodel.RoleSystem, Content: aimodel.NewTextContent("You are a helpful assistant.")}},
	}

	// Add turns until we exceed the threshold. Each turn ~100 tokens.
	for i := 1; i <= 20; i++ {
		history = append(
			history,
			schema.NewUserMessage(fmt.Sprintf("User message %d: %s", i, strings.Repeat("x", 200))),
		)
		history = append(history, schema.Message{
			Message: aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent(fmt.Sprintf("Response %d: %s", i, strings.Repeat("y", 200))),
			},
		})
	}

	// Calculate threshold as the CLI does.
	safetyMargin := 0.10
	effectiveMax := float64(contextCfg.ModelMaxContextTokens) * (1.0 - safetyMargin)
	compactionThreshold := int(effectiveMax * contextCfg.EffectiveCompressionThreshold())

	estimatedTokens := compactor.EstimateTokens(history)
	t.Logf("Estimated tokens: %d, Threshold: %d", estimatedTokens, compactionThreshold)

	if estimatedTokens <= compactionThreshold {
		t.Fatalf("test setup error: estimated tokens (%d) should exceed threshold (%d)", estimatedTokens, compactionThreshold)
	}

	// Simulate proactive compaction path.
	compressed, newTokens, compacted, err := memory.CompactIfNeeded(
		context.Background(), compactor, history, compactionThreshold,
	)
	if err != nil {
		t.Fatalf("CompactIfNeeded: %v", err)
	}

	if !compacted {
		t.Fatal("expected compaction to occur")
	}

	// Verify history length decreased.
	if len(compressed) >= len(history) {
		t.Errorf("compressed count (%d) should be < original (%d)", len(compressed), len(history))
	}

	// Verify token count decreased.
	if newTokens >= estimatedTokens {
		t.Errorf("new tokens (%d) should be < original (%d)", newTokens, estimatedTokens)
	}

	// Verify a summary message is present in the compressed history.
	foundSummary := false
	for _, m := range compressed {
		if m.Metadata != nil {
			if c, ok := m.Metadata["compressed"].(bool); ok && c {
				foundSummary = true
				if m.Role != aimodel.RoleSystem {
					t.Errorf("summary role = %q, want system", m.Role)
				}
				break
			}
		}
	}
	if !foundSummary {
		t.Fatal("no summary message found in compressed history")
	}

	// Verify system prompt is preserved.
	if compressed[0].Content.Text() != "You are a helpful assistant." {
		t.Error("system prompt should be preserved")
	}

	// Verify protected turns are preserved (last 2 user/assistant pairs).
	lastUserIdx := -1
	for i := len(compressed) - 1; i >= 0; i-- {
		if compressed[i].Role == aimodel.RoleUser {
			lastUserIdx = i
			break
		}
	}
	if lastUserIdx < 0 {
		t.Fatal("no user message found in compressed history")
	}
	if !strings.Contains(compressed[lastUserIdx].Content.Text(), "User message 20") {
		t.Errorf("last protected user message should be from turn 20, got: %q", compressed[lastUserIdx].Content.Text())
	}
}

// =============================================================================
// Integration Test: No compression when below threshold
// =============================================================================

// TestIntegration_Compression_NoCompactBelowThreshold verifies that
// CompactIfNeeded does NOT trigger compaction when the estimated token count
// is below the threshold, returning the original history unchanged.
func TestIntegration_Compression_NoCompactBelowThreshold(t *testing.T) {
	contextCfg := configs.ContextConfig{
		ModelMaxContextTokens: 128000,
		ProtectedTurns:        4,
	}

	compactor := memory.NewConversationCompactor(compressionMockSummarizer(), contextCfg.ProtectedTurns)

	// Small conversation well below threshold.
	history := []schema.Message{
		{Message: aimodel.Message{Role: aimodel.RoleSystem, Content: aimodel.NewTextContent("System prompt.")}},
		schema.NewUserMessage("Hello"),
		{Message: aimodel.Message{Role: aimodel.RoleAssistant, Content: aimodel.NewTextContent("Hi there!")}},
	}

	safetyMargin := 0.10
	effectiveMax := float64(contextCfg.ModelMaxContextTokens) * (1.0 - safetyMargin)
	compactionThreshold := int(effectiveMax * contextCfg.EffectiveCompressionThreshold())

	result, tokens, compacted, err := memory.CompactIfNeeded(
		context.Background(), compactor, history, compactionThreshold,
	)
	if err != nil {
		t.Fatalf("CompactIfNeeded: %v", err)
	}

	if compacted {
		t.Error("should not compact below threshold")
	}
	if len(result) != len(history) {
		t.Errorf("message count changed: got %d, want %d", len(result), len(history))
	}
	if tokens == 0 {
		t.Error("tokens should be non-zero")
	}
}

// =============================================================================
// Integration Test: CLI App construction with compactor
// =============================================================================

// TestIntegration_Compression_CLIAppWithCompactor verifies that the CLI App
// can be constructed with a ConversationCompactor, validating the updated
// constructor signature and basic initialization.
func TestIntegration_Compression_CLIAppWithCompactor(t *testing.T) {
	orchestrator := &stubStreamAgent{id: "orchestrator", response: "test response"}
	compactor := memory.NewConversationCompactor(compressionMockSummarizer(), 4)

	cfg := &configs.Config{
		Mode: "cli",
		LLM:  configs.LLMConfig{Model: "test-model", Provider: "openai", APIKey: "test-key"},
		Context: configs.ContextConfig{
			ModelMaxContextTokens: 128000,
			ProtectedTurns:        4,
			ToolOutputMaxTokens:   8000,
		},
	}

	app := vvcli.New(orchestrator, cfg, nil, nil, compactor)
	if app == nil {
		t.Fatal("cli.New returned nil with compactor")
	}
}

// =============================================================================
// Integration Test: CLI App construction without compactor (backward compat)
// =============================================================================

// TestIntegration_Compression_CLIAppWithoutCompactor verifies that the CLI App
// can still be constructed with a nil compactor, maintaining backward
// compatibility for configurations that don't enable compression.
func TestIntegration_Compression_CLIAppWithoutCompactor(t *testing.T) {
	orchestrator := &stubStreamAgent{id: "orchestrator", response: "test response"}

	cfg := &configs.Config{
		Mode: "cli",
		LLM:  configs.LLMConfig{Model: "test-model", Provider: "openai", APIKey: "test-key"},
	}

	app := vvcli.New(orchestrator, cfg, nil, nil, nil)
	if app == nil {
		t.Fatal("cli.New returned nil with nil compactor")
	}
}

// =============================================================================
// Integration Test: ContextConfig defaults and effective threshold
// =============================================================================

// TestIntegration_Compression_ContextConfigDefaults verifies that ContextConfig
// provides correct defaults when loaded from an empty config, and that
// EffectiveCompressionThreshold returns 0.8 for nil pointer.
func TestIntegration_Compression_ContextConfigDefaults(t *testing.T) {
	cfg := configs.ContextConfig{}
	// Before applyDefaults, fields are zero.
	// EffectiveCompressionThreshold should return 0.8 for nil pointer.
	if cfg.EffectiveCompressionThreshold() != 0.8 {
		t.Errorf("EffectiveCompressionThreshold = %f, want 0.8", cfg.EffectiveCompressionThreshold())
	}

	// With explicit threshold set.
	threshold := 0.5
	cfg.CompressionThreshold = &threshold
	if cfg.EffectiveCompressionThreshold() != 0.5 {
		t.Errorf("EffectiveCompressionThreshold = %f, want 0.5", cfg.EffectiveCompressionThreshold())
	}

	// With zero threshold (explicitly set to 0).
	zero := 0.0
	cfg.CompressionThreshold = &zero
	if cfg.EffectiveCompressionThreshold() != 0.0 {
		t.Errorf("EffectiveCompressionThreshold = %f, want 0.0", cfg.EffectiveCompressionThreshold())
	}
}

// =============================================================================
// Integration Test: TruncatingToolRegistry integration
// =============================================================================

// TestIntegration_Compression_TruncatingToolRegistryWithRealTools verifies
// that TruncatingToolRegistry correctly wraps a real tool registry, truncating
// large outputs while passing through small outputs unchanged.
func TestIntegration_Compression_TruncatingToolRegistryWithRealTools(t *testing.T) {
	// Create a mock registry with a tool that returns large output.
	inner := &mockToolRegistry{
		tools: map[string]schema.ToolDef{
			"large_tool": {Name: "large_tool", Description: "returns large output"},
			"small_tool": {Name: "small_tool", Description: "returns small output"},
		},
		executeFn: func(_ context.Context, name, _ string) (schema.ToolResult, error) {
			switch name {
			case "large_tool":
				return schema.TextResult("tc1", strings.Repeat("x", 50000)), nil
			case "small_tool":
				return schema.TextResult("tc2", "short result"), nil
			default:
				return schema.ToolResult{}, fmt.Errorf("unknown tool: %s", name)
			}
		},
	}

	tr := tool.NewTruncatingToolRegistry(inner, 2000)

	// Large tool output should be truncated.
	result, err := tr.Execute(context.Background(), "large_tool", "{}")
	if err != nil {
		t.Fatalf("Execute large_tool: %v", err)
	}
	if len(result.Content) == 0 {
		t.Fatal("expected content in result")
	}
	if len(result.Content[0].Text) >= 50000 {
		t.Error("large tool output should be truncated")
	}
	if !strings.Contains(result.Content[0].Text, "[truncated:") {
		t.Error("truncated output should contain truncation marker")
	}

	// Small tool output should pass through unchanged.
	result2, err := tr.Execute(context.Background(), "small_tool", "{}")
	if err != nil {
		t.Fatalf("Execute small_tool: %v", err)
	}
	if result2.Content[0].Text != "short result" {
		t.Errorf("small tool output should be unchanged, got %q", result2.Content[0].Text)
	}

	// List should delegate.
	list := tr.List()
	if len(list) != 2 {
		t.Errorf("List() = %d tools, want 2", len(list))
	}
}

// =============================================================================
// Integration Test: IsContextOverflowError detection
// =============================================================================

// TestIntegration_Compression_OverflowDetection verifies that
// IsContextOverflowError correctly identifies context overflow errors from
// various API error formats, which is critical for the reactive compression
// path.
func TestIntegration_Compression_OverflowDetection(t *testing.T) {
	tests := []struct {
		name     string
		err      error
		expected bool
	}{
		{
			name:     "nil error returns false",
			err:      nil,
			expected: false,
		},
		{
			name:     "generic error returns false",
			err:      fmt.Errorf("network timeout"),
			expected: false,
		},
		{
			name: "API error 413 returns true",
			err: &aimodel.APIError{
				StatusCode: 413,
				Message:    "payload too large",
			},
			expected: true,
		},
		{
			name: "API error context_length_exceeded returns true",
			err: &aimodel.APIError{
				StatusCode: 400,
				Code:       "context_length_exceeded",
			},
			expected: true,
		},
		{
			name: "API error with maximum context length message returns true",
			err: &aimodel.APIError{
				StatusCode: 400,
				Code:       "invalid_request_error",
				Message:    "This model's Maximum context length is 200000 tokens.",
			},
			expected: true,
		},
		{
			name: "API error request_too_large code returns true",
			err: &aimodel.APIError{
				StatusCode: 400,
				Code:       "request_too_large",
			},
			expected: true,
		},
		{
			name: "unrelated API error returns false",
			err: &aimodel.APIError{
				StatusCode: 400,
				Code:       "invalid_request",
				Message:    "Invalid JSON in request body",
			},
			expected: false,
		},
		{
			name:     "wrapped non-API error with context_length_exceeded returns true",
			err:      fmt.Errorf("stream error: context_length_exceeded"),
			expected: true,
		},
		{
			name: "wrapped API error returns true",
			err: fmt.Errorf("request failed: %w", &aimodel.APIError{
				StatusCode: 413,
				Message:    "too large",
			}),
			expected: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := largemodel.IsContextOverflowError(tt.err)
			if got != tt.expected {
				t.Errorf("IsContextOverflowError() = %v, want %v", got, tt.expected)
			}
		})
	}
}

// =============================================================================
// Integration Test: Emergency compaction simulation
// =============================================================================

// TestIntegration_Compression_EmergencyCompactSimulation simulates the
// emergency compaction flow: when a context overflow error is detected, a new
// compactor with protectedTurns=1 is created and used to aggressively compress
// the history before retry.
func TestIntegration_Compression_EmergencyCompactSimulation(t *testing.T) {
	// Original compactor with protectedTurns=4.
	originalCompactor := memory.NewConversationCompactor(compressionMockSummarizer(), 4)

	// Build large conversation.
	history := []schema.Message{
		{Message: aimodel.Message{Role: aimodel.RoleSystem, Content: aimodel.NewTextContent("System prompt.")}},
	}
	for i := 1; i <= 15; i++ {
		history = append(
			history,
			schema.NewUserMessage(fmt.Sprintf("Q%d: %s", i, strings.Repeat("x", 80))),
		)
		history = append(history, schema.Message{
			Message: aimodel.Message{
				Role:    aimodel.RoleAssistant,
				Content: aimodel.NewTextContent(fmt.Sprintf("A%d: %s", i, strings.Repeat("y", 120))),
			},
		})
	}

	// First attempt: normal compaction.
	normalResult, normalTokens, err := originalCompactor.Compact(context.Background(), history)
	if err != nil {
		t.Fatalf("normal Compact: %v", err)
	}

	// Simulate overflow error detection.
	overflowErr := &aimodel.APIError{
		StatusCode: 400,
		Code:       "context_length_exceeded",
		Message:    "maximum context length exceeded",
	}
	if !largemodel.IsContextOverflowError(overflowErr) {
		t.Fatal("overflow error should be detected")
	}

	// Emergency compaction: use protectedTurns=1 for aggressive compression.
	emergencyCompactor := memory.NewConversationCompactor(originalCompactor.Summarizer(), 1)
	emergencyResult, emergencyTokens, err := emergencyCompactor.Compact(context.Background(), history)
	if err != nil {
		t.Fatalf("emergency Compact: %v", err)
	}

	// Emergency should be more aggressive than normal.
	if len(emergencyResult) >= len(normalResult) {
		t.Errorf("emergency result (%d msgs) should be smaller than normal (%d msgs)",
			len(emergencyResult), len(normalResult))
	}
	if emergencyTokens >= normalTokens {
		t.Errorf("emergency tokens (%d) should be less than normal (%d)",
			emergencyTokens, normalTokens)
	}

	// Verify the emergency result has the expected structure:
	// system + summary + 1 user + 1 assistant = 4 messages.
	if len(emergencyResult) != 4 {
		t.Errorf("emergency result should have 4 messages, got %d", len(emergencyResult))
	}

	t.Logf("Normal: %d msgs (%d tokens), Emergency: %d msgs (%d tokens)",
		len(normalResult), normalTokens, len(emergencyResult), emergencyTokens)
}

// =============================================================================
// Integration Test: Token estimation consistency
// =============================================================================

// TestIntegration_Compression_TokenEstimationConsistency verifies that
// EstimateTextTokens and DefaultTokenEstimator produce consistent results,
// and that the running token estimate maintained by the CLI stays in sync
// with the compactor's EstimateTokens.
func TestIntegration_Compression_TokenEstimationConsistency(t *testing.T) {
	compactor := memory.NewConversationCompactor(compressionMockSummarizer(), 2)

	// Build history incrementally, tracking tokens as the CLI would.
	var history []schema.Message
	runningTotal := 0

	msgs := []schema.Message{
		schema.NewUserMessage("Hello, how are you?"),
		{Message: aimodel.Message{Role: aimodel.RoleAssistant, Content: aimodel.NewTextContent("I'm doing well, thanks!")}},
		schema.NewUserMessage("Can you help me with Go code?"),
		{Message: aimodel.Message{Role: aimodel.RoleAssistant, Content: aimodel.NewTextContent("Of course! What do you need?")}},
	}

	for _, msg := range msgs {
		history = append(history, msg)
		runningTotal += memory.DefaultTokenEstimator(msg)
	}

	// Running total should match compactor's EstimateTokens.
	compactorEstimate := compactor.EstimateTokens(history)
	if runningTotal != compactorEstimate {
		t.Errorf("running total (%d) != compactor estimate (%d)", runningTotal, compactorEstimate)
	}

	// Verify EstimateTextTokens matches DefaultTokenEstimator for text content.
	for _, msg := range msgs {
		textEstimate := memory.EstimateTextTokens(msg.Content.Text())
		msgEstimate := memory.DefaultTokenEstimator(msg)
		if textEstimate != msgEstimate {
			t.Errorf("EstimateTextTokens(%q) = %d, DefaultTokenEstimator = %d",
				msg.Content.Text(), textEstimate, msgEstimate)
		}
	}
}
