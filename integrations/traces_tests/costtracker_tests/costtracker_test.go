package costtraces_tests

import (
	"encoding/json"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/traces/costtraces"
)

// Test 4a: Cost tracker accumulation across multiple Add calls.
// Creates a Tracker with known pricing, calls Add() multiple times,
// and verifies cumulative totals in Snapshot().
func TestIntegration_CostTracker_Accumulation(t *testing.T) {
	pricing := &costtraces.Pricing{
		InputPerMTokens:  3.0,
		OutputPerMTokens: 15.0,
		CachePerMTokens:  0.3,
	}
	tracker := costtraces.New("claude-sonnet-4", pricing)

	// First call: 1000 input (200 cached), 500 output.
	tracker.Add(1000, 500, 200)

	// Second call: 2000 input (300 cached), 1000 output.
	tracker.Add(2000, 1000, 300)

	// Third call: 500 input (0 cached), 250 output.
	tracker.Add(500, 250, 0)

	snap := tracker.Snapshot()

	if snap.InputTokens != 3500 {
		t.Errorf("InputTokens = %d, want 3500", snap.InputTokens)
	}

	if snap.OutputTokens != 1750 {
		t.Errorf("OutputTokens = %d, want 1750", snap.OutputTokens)
	}

	if snap.CacheReadTokens != 500 {
		t.Errorf("CacheReadTokens = %d, want 500", snap.CacheReadTokens)
	}

	if snap.TotalTokens != 5250 {
		t.Errorf("TotalTokens = %d, want 5250 (3500+1750)", snap.TotalTokens)
	}

	if snap.CallCount != 3 {
		t.Errorf("CallCount = %d, want 3", snap.CallCount)
	}

	if snap.EstimatedCostUSD == nil {
		t.Fatal("EstimatedCostUSD = nil, want non-nil")
	}
}

// Test 4b: Cache-read tokens are not double-charged.
// Verifies that cost of 1000 input tokens with 200 cached uses:
// (800 * inputRate + 200 * cacheRate + 500 * outputRate)
// NOT: (1000 * inputRate + 200 * cacheRate + 500 * outputRate).
func TestIntegration_CostTracker_NoCacheDoubleCharge(t *testing.T) {
	pricing := &costtraces.Pricing{
		InputPerMTokens:  3.0,
		OutputPerMTokens: 15.0,
		CachePerMTokens:  0.3,
	}
	tracker := costtraces.New("test-model", pricing)

	tracker.Add(1000, 500, 200)

	snap := tracker.Snapshot()
	if snap.EstimatedCostUSD == nil {
		t.Fatal("EstimatedCostUSD = nil, want non-nil")
	}

	// Correct cost: (800/1M)*3.0 + (500/1M)*15.0 + (200/1M)*0.3
	expected := float64(800)/1_000_000*3.0 +
		float64(500)/1_000_000*15.0 +
		float64(200)/1_000_000*0.3

	if math.Abs(*snap.EstimatedCostUSD-expected) > 1e-12 {
		t.Errorf("EstimatedCostUSD = %.12f, want %.12f", *snap.EstimatedCostUSD, expected)
	}

	// Wrong cost (double-charging): (1000/1M)*3.0 + (500/1M)*15.0 + (200/1M)*0.3
	wrongCost := float64(1000)/1_000_000*3.0 +
		float64(500)/1_000_000*15.0 +
		float64(200)/1_000_000*0.3

	if math.Abs(*snap.EstimatedCostUSD-wrongCost) < 1e-12 {
		t.Error("cost appears to double-charge cache tokens (matches wrong formula)")
	}
}

// Test 4c: Nil pricing produces nil EstimatedCostUSD.
// Verifies token counts are still tracked even without pricing.
func TestIntegration_CostTracker_NilPricing(t *testing.T) {
	tracker := costtraces.New("unknown-model", nil)

	tracker.Add(1000, 500, 200)
	tracker.Add(2000, 1000, 100)

	snap := tracker.Snapshot()

	if snap.EstimatedCostUSD != nil {
		t.Errorf("EstimatedCostUSD = %v, want nil", snap.EstimatedCostUSD)
	}

	// Token counts should still be tracked.
	if snap.InputTokens != 3000 {
		t.Errorf("InputTokens = %d, want 3000", snap.InputTokens)
	}

	if snap.OutputTokens != 1500 {
		t.Errorf("OutputTokens = %d, want 1500", snap.OutputTokens)
	}

	if snap.CacheReadTokens != 300 {
		t.Errorf("CacheReadTokens = %d, want 300", snap.CacheReadTokens)
	}

	if snap.CallCount != 2 {
		t.Errorf("CallCount = %d, want 2", snap.CallCount)
	}
}

// Test 4d: Concurrent access to the tracker.
// Spawns many goroutines calling Add() concurrently and verifies
// the final snapshot is consistent.
func TestIntegration_CostTracker_ConcurrentAccess(t *testing.T) {
	pricing := &costtraces.Pricing{
		InputPerMTokens:  3.0,
		OutputPerMTokens: 15.0,
		CachePerMTokens:  0.3,
	}
	tracker := costtraces.New("test", pricing)

	const goroutines = 200

	var wg sync.WaitGroup

	for range goroutines {
		wg.Go(func() {
			tracker.Add(100, 50, 10)
			_ = tracker.Snapshot() // concurrent reads too
		})
	}

	wg.Wait()

	snap := tracker.Snapshot()

	if snap.InputTokens != goroutines*100 {
		t.Errorf("InputTokens = %d, want %d", snap.InputTokens, goroutines*100)
	}

	if snap.OutputTokens != goroutines*50 {
		t.Errorf("OutputTokens = %d, want %d", snap.OutputTokens, goroutines*50)
	}

	if snap.CacheReadTokens != goroutines*10 {
		t.Errorf("CacheReadTokens = %d, want %d", snap.CacheReadTokens, goroutines*10)
	}

	if snap.CallCount != goroutines {
		t.Errorf("CallCount = %d, want %d", snap.CallCount, goroutines)
	}

	if snap.EstimatedCostUSD == nil {
		t.Error("EstimatedCostUSD = nil, want non-nil")
	}
}

// Test 4e: Snapshot returns independent copies.
// Verifies that modifying or adding after a snapshot doesn't affect
// previously captured snapshots.
func TestIntegration_CostTracker_SnapshotIsolation(t *testing.T) {
	pricing := &costtraces.Pricing{InputPerMTokens: 3.0, OutputPerMTokens: 15.0}
	tracker := costtraces.New("test", pricing)

	tracker.Add(1000, 500, 0)
	snap1 := tracker.Snapshot()

	tracker.Add(2000, 1000, 0)
	snap2 := tracker.Snapshot()

	if snap1.InputTokens == snap2.InputTokens {
		t.Error("snap1 and snap2 should have different InputTokens")
	}

	if snap1.EstimatedCostUSD == nil || snap2.EstimatedCostUSD == nil {
		t.Fatal("costs should not be nil")
	}

	if *snap1.EstimatedCostUSD == *snap2.EstimatedCostUSD {
		t.Error("snap1 and snap2 should have different costs")
	}
}

// Test 5a: LookupPricing exact match.
// Verifies "gpt-4o" matches "gpt-4o" exactly.
func TestIntegration_PricingLookup_ExactMatch(t *testing.T) {
	p := costtraces.LookupPricing("gpt-4o", nil)
	if p == nil {
		t.Fatal("LookupPricing(\"gpt-4o\") = nil, want non-nil")
	}

	if p.InputPerMTokens != 2.5 {
		t.Errorf("InputPerMTokens = %f, want 2.5", p.InputPerMTokens)
	}

	if p.OutputPerMTokens != 10.0 {
		t.Errorf("OutputPerMTokens = %f, want 10.0", p.OutputPerMTokens)
	}
}

// Test 5b: Longest prefix match -- "gpt-4o-mini" matches "gpt-4o-mini" not "gpt-4o".
func TestIntegration_PricingLookup_LongestPrefix(t *testing.T) {
	p := costtraces.LookupPricing("gpt-4o-mini", nil)
	if p == nil {
		t.Fatal("LookupPricing(\"gpt-4o-mini\") = nil, want non-nil")
	}

	// gpt-4o-mini pricing is 0.15 input, not 2.5 (gpt-4o).
	if p.InputPerMTokens != 0.15 {
		t.Errorf("InputPerMTokens = %f, want 0.15 (gpt-4o-mini pricing, not gpt-4o)", p.InputPerMTokens)
	}
}

// Test 5c: Prefix match -- "claude-sonnet-4-20250514" matches "claude-sonnet-4".
func TestIntegration_PricingLookup_PrefixMatch(t *testing.T) {
	p := costtraces.LookupPricing("claude-sonnet-4-20250514", nil)
	if p == nil {
		t.Fatal("LookupPricing(\"claude-sonnet-4-20250514\") = nil, want non-nil")
	}

	if p.InputPerMTokens != 3.0 {
		t.Errorf("InputPerMTokens = %f, want 3.0", p.InputPerMTokens)
	}

	if p.CachePerMTokens != 0.3 {
		t.Errorf("CachePerMTokens = %f, want 0.3", p.CachePerMTokens)
	}
}

// Test 5d: Custom overrides take precedence over defaults.
func TestIntegration_PricingLookup_CustomOverrides(t *testing.T) {
	custom := map[string]costtraces.Pricing{
		"gpt-4o": {InputPerMTokens: 99.0, OutputPerMTokens: 99.0},
	}

	p := costtraces.LookupPricing("gpt-4o", custom)
	if p == nil {
		t.Fatal("LookupPricing(\"gpt-4o\", custom) = nil, want non-nil")
	}

	if p.InputPerMTokens != 99.0 {
		t.Errorf("InputPerMTokens = %f, want 99.0 (custom override)", p.InputPerMTokens)
	}
}

// Test 5e: Unknown model returns nil pricing.
func TestIntegration_PricingLookup_UnknownModel(t *testing.T) {
	p := costtraces.LookupPricing("totally-unknown-model-xyz", nil)
	if p != nil {
		t.Errorf("LookupPricing(\"totally-unknown-model-xyz\") = %+v, want nil", p)
	}
}

// Test 5f: Custom prefix match -- custom entries support prefix matching.
func TestIntegration_PricingLookup_CustomPrefixMatch(t *testing.T) {
	custom := map[string]costtraces.Pricing{
		"my-org-model": {InputPerMTokens: 1.0, OutputPerMTokens: 5.0, CachePerMTokens: 0.1},
	}

	p := costtraces.LookupPricing("my-org-model-v2-20260101", custom)
	if p == nil {
		t.Fatal("LookupPricing with custom prefix = nil, want non-nil")
	}

	if p.InputPerMTokens != 1.0 {
		t.Errorf("InputPerMTokens = %f, want 1.0", p.InputPerMTokens)
	}
}

// Test 8a: Configuration loading with model_pricing YAML section.
// Writes a vv.yaml with model_pricing section and verifies parsing.
func TestIntegration_Config_ModelPricingFromYAML(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vv.yaml")

	content := `
llm:
  provider: "openai"
  model: "gpt-4o"
  api_key: "test-key"
model_pricing:
  my-custom-model:
    input_per_m_tokens: 1.5
    output_per_m_tokens: 7.5
    cache_per_m_tokens: 0.15
  another-model:
    input_per_m_tokens: 2.0
    output_per_m_tokens: 10.0
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := configs.Load(path, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}

	if cfg.ModelPricing == nil {
		t.Fatal("ModelPricing = nil, want non-nil")
	}

	if len(cfg.ModelPricing) != 2 {
		t.Fatalf("ModelPricing has %d entries, want 2", len(cfg.ModelPricing))
	}

	entry, ok := cfg.ModelPricing["my-custom-model"]
	if !ok {
		t.Fatal("ModelPricing[\"my-custom-model\"] not found")
	}

	if entry.InputPerMTokens != 1.5 {
		t.Errorf("InputPerMTokens = %f, want 1.5", entry.InputPerMTokens)
	}

	if entry.OutputPerMTokens != 7.5 {
		t.Errorf("OutputPerMTokens = %f, want 7.5", entry.OutputPerMTokens)
	}

	if entry.CachePerMTokens != 0.15 {
		t.Errorf("CachePerMTokens = %f, want 0.15", entry.CachePerMTokens)
	}
}

// Test 8b: VV_MODEL_PRICING env var overrides YAML pricing.
// Sets VV_MODEL_PRICING env var and verifies it merges on top of YAML config.
func TestIntegration_Config_ModelPricingEnvOverride(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vv.yaml")

	content := `
llm:
  provider: "openai"
  model: "gpt-4o"
  api_key: "test-key"
model_pricing:
  existing-model:
    input_per_m_tokens: 1.0
    output_per_m_tokens: 5.0
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	envPricing := map[string]configs.ModelPricingEntry{
		"env-model": {InputPerMTokens: 2.0, OutputPerMTokens: 10.0, CachePerMTokens: 0.2},
		// Override existing model from YAML.
		"existing-model": {InputPerMTokens: 99.0, OutputPerMTokens: 99.0},
	}
	envJSON, _ := json.Marshal(envPricing)
	t.Setenv("VV_MODEL_PRICING", string(envJSON))

	cfg, err := configs.Load(path, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}

	if cfg.ModelPricing == nil {
		t.Fatal("ModelPricing = nil, want non-nil")
	}

	// Check env-only model was added.
	envEntry, ok := cfg.ModelPricing["env-model"]
	if !ok {
		t.Fatal("ModelPricing[\"env-model\"] not found (should be added from env)")
	}

	if envEntry.InputPerMTokens != 2.0 {
		t.Errorf("env-model InputPerMTokens = %f, want 2.0", envEntry.InputPerMTokens)
	}

	// Check that env overrides YAML for existing model.
	existingEntry, ok := cfg.ModelPricing["existing-model"]
	if !ok {
		t.Fatal("ModelPricing[\"existing-model\"] not found")
	}

	if existingEntry.InputPerMTokens != 99.0 {
		t.Errorf("existing-model InputPerMTokens = %f, want 99.0 (env override)", existingEntry.InputPerMTokens)
	}
}

// Test 8c: ConvertPricing converts config entries to costtraces pricing.
func TestIntegration_Config_ConvertPricing(t *testing.T) {
	entries := map[string]configs.ModelPricingEntry{
		"test-model": {InputPerMTokens: 3.0, OutputPerMTokens: 15.0, CachePerMTokens: 0.3},
	}

	result := configs.ConvertPricing(entries)
	if result == nil {
		t.Fatal("ConvertPricing() = nil, want non-nil")
	}

	p, ok := result["test-model"]
	if !ok {
		t.Fatal("result[\"test-model\"] not found")
	}

	if p.InputPerMTokens != 3.0 {
		t.Errorf("InputPerMTokens = %f, want 3.0", p.InputPerMTokens)
	}

	if p.OutputPerMTokens != 15.0 {
		t.Errorf("OutputPerMTokens = %f, want 15.0", p.OutputPerMTokens)
	}

	if p.CachePerMTokens != 0.3 {
		t.Errorf("CachePerMTokens = %f, want 0.3", p.CachePerMTokens)
	}
}

// Test 8d: ConvertPricing returns nil for empty input.
func TestIntegration_Config_ConvertPricingEmpty(t *testing.T) {
	result := configs.ConvertPricing(nil)
	if result != nil {
		t.Errorf("ConvertPricing(nil) = %v, want nil", result)
	}

	result2 := configs.ConvertPricing(map[string]configs.ModelPricingEntry{})
	if result2 != nil {
		t.Errorf("ConvertPricing(empty) = %v, want nil", result2)
	}
}

// Test 8e: End-to-end: config -> costtraces -> pricing lookup.
// Loads config with custom model pricing, converts, and verifies
// LookupPricing works with the converted pricing.
func TestIntegration_Config_EndToEndPricingLookup(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "vv.yaml")

	content := `
llm:
  provider: "openai"
  model: "my-org-model-v2"
  api_key: "test-key"
model_pricing:
  my-org-model:
    input_per_m_tokens: 1.0
    output_per_m_tokens: 5.0
    cache_per_m_tokens: 0.1
`
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, err := configs.Load(path, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}

	customPricing := configs.ConvertPricing(cfg.ModelPricing)

	// "my-org-model-v2" should match "my-org-model" by prefix.
	p := costtraces.LookupPricing("my-org-model-v2", customPricing)
	if p == nil {
		t.Fatal("LookupPricing(\"my-org-model-v2\") = nil, want non-nil (prefix match)")
	}

	if p.InputPerMTokens != 1.0 {
		t.Errorf("InputPerMTokens = %f, want 1.0", p.InputPerMTokens)
	}

	// Create a tracker with looked-up pricing and verify cost calculation.
	tracker := costtraces.New(cfg.LLM.Model, p)
	tracker.Add(1000, 500, 200)

	snap := tracker.Snapshot()
	if snap.EstimatedCostUSD == nil {
		t.Fatal("EstimatedCostUSD = nil, want non-nil")
	}

	// Expected: (800/1M)*1.0 + (500/1M)*5.0 + (200/1M)*0.1
	expected := float64(800)/1_000_000*1.0 +
		float64(500)/1_000_000*5.0 +
		float64(200)/1_000_000*0.1

	if math.Abs(*snap.EstimatedCostUSD-expected) > 1e-12 {
		t.Errorf("EstimatedCostUSD = %.12f, want %.12f", *snap.EstimatedCostUSD, expected)
	}
}
