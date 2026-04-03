package httpapis

import (
	"encoding/json"
	"math"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/vogo/vv/costtracker"
)

func TestCostEnrichMiddleware_SyncResponse(t *testing.T) {
	// Mock handler that returns a JSON response with usage.
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
			"messages": [],
			"usage": {"prompt_tokens": 1000, "completion_tokens": 500, "total_tokens": 1500, "cache_read_tokens": 200},
			"duration_ms": 5000
		}`))
	})

	lookup := func(_ string) *costtracker.Pricing {
		return &costtracker.Pricing{
			InputPerMTokens:  3.0,
			OutputPerMTokens: 15.0,
			CachePerMTokens:  0.3,
		}
	}

	enriched := costEnrichMiddleware(handler, lookup)

	req := httptest.NewRequest(http.MethodPost, "/v1/agents/test/run", nil)
	w := httptest.NewRecorder()
	enriched.ServeHTTP(w, req)

	var result map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	costData, ok := result["estimated_cost_usd"]
	if !ok {
		t.Fatal("estimated_cost_usd not present in response")
	}

	var cost float64
	if err := json.Unmarshal(costData, &cost); err != nil {
		t.Fatalf("unmarshal cost: %v", err)
	}

	if cost <= 0 {
		t.Errorf("cost = %f, want > 0", cost)
	}

	// Expected: (800/1M)*3.0 + (500/1M)*15.0 + (200/1M)*0.3
	expected := float64(800)/1_000_000*3.0 +
		float64(500)/1_000_000*15.0 +
		float64(200)/1_000_000*0.3

	if math.Abs(cost-expected) > 1e-9 {
		t.Errorf("cost = %f, want %f", cost, expected)
	}
}

func TestCostEnrichMiddleware_NoPricing(t *testing.T) {
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"messages":[],"usage":{"prompt_tokens":100,"completion_tokens":50,"total_tokens":150}}`))
	})

	lookup := func(_ string) *costtracker.Pricing {
		return nil
	}

	enriched := costEnrichMiddleware(handler, lookup)

	req := httptest.NewRequest(http.MethodPost, "/v1/agents/test/run", nil)
	w := httptest.NewRecorder()
	enriched.ServeHTTP(w, req)

	var result map[string]json.RawMessage
	if err := json.Unmarshal(w.Body.Bytes(), &result); err != nil {
		t.Fatalf("unmarshal response: %v", err)
	}

	if _, ok := result["estimated_cost_usd"]; ok {
		t.Error("estimated_cost_usd should not be present when no pricing available")
	}
}

func TestCostEnrichMiddleware_NonRunEndpoints_PassThrough(t *testing.T) {
	called := false
	handler := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(`{"status":"ok"}`))
	})

	lookup := func(_ string) *costtracker.Pricing {
		return &costtracker.Pricing{InputPerMTokens: 1.0, OutputPerMTokens: 2.0}
	}

	enriched := costEnrichMiddleware(handler, lookup)

	req := httptest.NewRequest(http.MethodGet, "/v1/agents", nil)
	w := httptest.NewRecorder()
	enriched.ServeHTTP(w, req)

	if !called {
		t.Error("handler was not called for non-run endpoint")
	}
}

func TestInjectCostIntoJSON_NoUsage(t *testing.T) {
	body := []byte(`{"messages":[],"duration_ms":1000}`)
	lookup := func(_ string) *costtracker.Pricing {
		return &costtracker.Pricing{InputPerMTokens: 1.0, OutputPerMTokens: 2.0}
	}

	result := injectCostIntoJSON(body, lookup)

	var raw map[string]json.RawMessage
	if err := json.Unmarshal(result, &raw); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if _, ok := raw["estimated_cost_usd"]; ok {
		t.Error("estimated_cost_usd should not be present when no usage in response")
	}
}
