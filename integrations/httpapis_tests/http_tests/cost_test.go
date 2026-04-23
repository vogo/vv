package http_tests

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/vogo/vv/traces/costtraces"
)

// Test 7a: HTTP cost enrichment middleware for sync responses.
// Sends a sync run request through the cost enrichment middleware
// and verifies estimated_cost_usd appears in the response.
func TestIntegration_HTTP_CostEnrichment_SyncRun(t *testing.T) {
	// Create a mock handler that returns a response with usage data.
	mockHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"messages": [{"role": "assistant", "content": "Hello"}],
			"model": "claude-sonnet-4",
			"usage": {
				"prompt_tokens": 1000,
				"completion_tokens": 500,
				"total_tokens": 1500,
				"cache_read_tokens": 200
			}
		}`))
	})

	pricingLookup := func(model string) *costtraces.Pricing {
		return costtraces.LookupPricing(model, nil)
	}

	// Wrap with cost enrichment middleware.
	handler := costEnrichTestMiddleware(mockHandler, pricingLookup)

	// Create a request that looks like a run endpoint.
	req := httptest.NewRequest("POST", "/v1/agents/chat/run", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	resp := rec.Result()
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want %d", resp.StatusCode, http.StatusOK)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}

	var result map[string]json.RawMessage
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Verify estimated_cost_usd was injected.
	costData, ok := result["estimated_cost_usd"]
	if !ok {
		t.Fatal("response missing 'estimated_cost_usd' field")
	}

	var cost float64
	if err := json.Unmarshal(costData, &cost); err != nil {
		t.Fatalf("unmarshal cost: %v", err)
	}

	if cost <= 0 {
		t.Errorf("estimated_cost_usd = %f, want > 0", cost)
	}

	// Verify the cost is reasonable for claude-sonnet-4 pricing:
	// (800/1M)*3.0 + (500/1M)*15.0 + (200/1M)*0.3 = 0.0024 + 0.0075 + 0.00006 = 0.00996
	expected := float64(800)/1_000_000*3.0 +
		float64(500)/1_000_000*15.0 +
		float64(200)/1_000_000*0.3

	if cost < expected*0.9 || cost > expected*1.1 {
		t.Errorf("estimated_cost_usd = %f, expected approximately %f", cost, expected)
	}
}

// Test 7b: HTTP cost enrichment middleware does not inject cost when no pricing.
// Verifies that estimated_cost_usd is not added when the model is unknown.
func TestIntegration_HTTP_CostEnrichment_NoPricing(t *testing.T) {
	mockHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"messages": [{"role": "assistant", "content": "Hello"}],
			"model": "unknown-model-xyz",
			"usage": {
				"prompt_tokens": 1000,
				"completion_tokens": 500,
				"total_tokens": 1500
			}
		}`))
	})

	pricingLookup := func(model string) *costtraces.Pricing {
		return costtraces.LookupPricing(model, nil)
	}

	handler := costEnrichTestMiddleware(mockHandler, pricingLookup)
	req := httptest.NewRequest("POST", "/v1/agents/chat/run", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body, _ := io.ReadAll(rec.Result().Body)

	var result map[string]json.RawMessage
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Should NOT have estimated_cost_usd because model is unknown.
	if _, ok := result["estimated_cost_usd"]; ok {
		t.Error("response should not have 'estimated_cost_usd' for unknown model")
	}
}

// Test 7c: HTTP cost enrichment for streaming responses.
// Sends a stream request and verifies a usage SSE event is emitted
// after the stream ends with correct totals.
func TestIntegration_HTTP_CostEnrichment_Stream(t *testing.T) {
	// Create a mock handler that returns SSE events.
	mockHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		flusher, _ := w.(http.Flusher)

		events := []struct {
			event string
			data  string
		}{
			{"agent_start", `{"type":"agent_start"}`},
			{"llm_call_end", `{"type":"llm_call_end","data":{"model":"claude-sonnet-4","prompt_tokens":800,"completion_tokens":400,"total_tokens":1200,"cache_read_tokens":150}}`},
			{"llm_call_end", `{"type":"llm_call_end","data":{"model":"claude-sonnet-4","prompt_tokens":700,"completion_tokens":300,"total_tokens":1000,"cache_read_tokens":100}}`},
			{"agent_end", `{"type":"agent_end","data":{"duration_ms":5000}}`},
		}

		for _, e := range events {
			_, _ = fmt.Fprintf(w, "event: %s\ndata: %s\n\n", e.event, e.data)
			flusher.Flush()
		}
	})

	pricingLookup := func(model string) *costtraces.Pricing {
		return costtraces.LookupPricing(model, nil)
	}

	handler := costEnrichTestMiddleware(mockHandler, pricingLookup)
	req := httptest.NewRequest("POST", "/v1/agents/chat/stream", bytes.NewReader([]byte(`{}`)))
	req.Header.Set("Content-Type", "application/json")

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body := rec.Body.String()

	// Verify the original events were written through.
	if !strings.Contains(body, "event: agent_start") {
		t.Error("response should contain agent_start event")
	}

	if !strings.Contains(body, "event: agent_end") {
		t.Error("response should contain agent_end event")
	}

	// Verify a usage event was appended at the end.
	if !strings.Contains(body, "event: usage") {
		t.Error("response should contain final usage event")
	}

	// Extract the usage event data.
	usageLine := ""
	lines := strings.Split(body, "\n")

	for i, line := range lines {
		if line == "event: usage" && i+1 < len(lines) {
			if after, ok := strings.CutPrefix(lines[i+1], "data: "); ok {
				usageLine = after
			}
		}
	}

	if usageLine == "" {
		t.Fatal("could not extract usage event data")
	}

	var usage costtraces.Usage
	if err := json.Unmarshal([]byte(usageLine), &usage); err != nil {
		t.Fatalf("unmarshal usage: %v", err)
	}

	// Total across 2 llm_call_end events: 800+700=1500 input, 400+300=700 output, 150+100=250 cached.
	if usage.InputTokens != 1500 {
		t.Errorf("InputTokens = %d, want 1500", usage.InputTokens)
	}

	if usage.OutputTokens != 700 {
		t.Errorf("OutputTokens = %d, want 700", usage.OutputTokens)
	}

	if usage.CacheReadTokens != 250 {
		t.Errorf("CacheReadTokens = %d, want 250", usage.CacheReadTokens)
	}

	if usage.TotalTokens != 2200 {
		t.Errorf("TotalTokens = %d, want 2200", usage.TotalTokens)
	}

	// CallCount is 1 because the middleware accumulates token counts from
	// individual llm_call_end events into a single tracker.Add() call at the end.
	if usage.CallCount != 1 {
		t.Errorf("CallCount = %d, want 1", usage.CallCount)
	}

	// EstimatedCostUSD should be present since claude-sonnet-4 has pricing.
	if usage.EstimatedCostUSD == nil {
		t.Error("EstimatedCostUSD = nil, want non-nil")
	}
}

// Test 7d: Non-run/stream endpoints pass through unmodified.
func TestIntegration_HTTP_CostEnrichment_Passthrough(t *testing.T) {
	mockHandler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	pricingLookup := func(model string) *costtraces.Pricing {
		return costtraces.LookupPricing(model, nil)
	}

	handler := costEnrichTestMiddleware(mockHandler, pricingLookup)
	req := httptest.NewRequest("GET", "/v1/health", nil)

	rec := httptest.NewRecorder()
	handler.ServeHTTP(rec, req)

	body, _ := io.ReadAll(rec.Result().Body)

	var result map[string]string
	if err := json.Unmarshal(body, &result); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if result["status"] != "ok" {
		t.Errorf("status = %q, want %q", result["status"], "ok")
	}

	// Should not have estimated_cost_usd.
	if _, ok := result["estimated_cost_usd"]; ok {
		t.Error("non-run endpoint should not have estimated_cost_usd")
	}
}
