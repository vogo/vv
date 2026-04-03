/*
 * Licensed to the Apache Software Foundation (ASF) under one or more
 * contributor license agreements.  See the NOTICE file distributed with
 * this work for additional information regarding copyright ownership.
 * The ASF licenses this file to You under the Apache License, Version 2.0
 * (the "License"); you may not use this file except in compliance with
 * the License.  You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package costtracker

import (
	"math"
	"sync"
	"testing"
)

func TestTracker_Add_Accumulation(t *testing.T) {
	pricing := &Pricing{InputPerMTokens: 3.0, OutputPerMTokens: 15.0, CachePerMTokens: 0.3}
	tracker := New("claude-sonnet-4", pricing)

	tracker.Add(1000, 500, 200)
	tracker.Add(2000, 1000, 300)

	snap := tracker.Snapshot()
	if snap.InputTokens != 3000 {
		t.Errorf("InputTokens = %d, want 3000", snap.InputTokens)
	}
	if snap.OutputTokens != 1500 {
		t.Errorf("OutputTokens = %d, want 1500", snap.OutputTokens)
	}
	if snap.CacheReadTokens != 500 {
		t.Errorf("CacheReadTokens = %d, want 500", snap.CacheReadTokens)
	}
	if snap.TotalTokens != 4500 {
		t.Errorf("TotalTokens = %d, want 4500", snap.TotalTokens)
	}
	if snap.CallCount != 2 {
		t.Errorf("CallCount = %d, want 2", snap.CallCount)
	}
}

func TestTracker_Add_CostCalculation(t *testing.T) {
	// Test that cache-read tokens are not double-charged.
	// 1000 input tokens, 200 cached: charge (800 * inputRate + 200 * cacheRate + 500 * outputRate)
	pricing := &Pricing{InputPerMTokens: 3.0, OutputPerMTokens: 15.0, CachePerMTokens: 0.3}
	tracker := New("test-model", pricing)

	tracker.Add(1000, 500, 200)

	snap := tracker.Snapshot()
	if snap.EstimatedCostUSD == nil {
		t.Fatal("EstimatedCostUSD = nil, want non-nil")
	}

	// Expected: (800/1M)*3.0 + (500/1M)*15.0 + (200/1M)*0.3
	expected := float64(800)/1_000_000*3.0 +
		float64(500)/1_000_000*15.0 +
		float64(200)/1_000_000*0.3

	if math.Abs(*snap.EstimatedCostUSD-expected) > 1e-12 {
		t.Errorf("EstimatedCostUSD = %f, want %f", *snap.EstimatedCostUSD, expected)
	}
}

func TestTracker_NilPricing(t *testing.T) {
	tracker := New("unknown-model", nil)

	tracker.Add(1000, 500, 0)

	snap := tracker.Snapshot()
	if snap.EstimatedCostUSD != nil {
		t.Errorf("EstimatedCostUSD = %v, want nil", snap.EstimatedCostUSD)
	}
	if snap.InputTokens != 1000 {
		t.Errorf("InputTokens = %d, want 1000", snap.InputTokens)
	}
}

func TestTracker_SnapshotCopiesPointer(t *testing.T) {
	pricing := &Pricing{InputPerMTokens: 3.0, OutputPerMTokens: 15.0}
	tracker := New("test", pricing)

	tracker.Add(1000, 500, 0)
	snap1 := tracker.Snapshot()

	tracker.Add(1000, 500, 0)
	snap2 := tracker.Snapshot()

	// snap1 and snap2 should have independent cost pointers.
	if snap1.EstimatedCostUSD == nil || snap2.EstimatedCostUSD == nil {
		t.Fatal("cost pointers should not be nil")
	}
	if *snap1.EstimatedCostUSD == *snap2.EstimatedCostUSD {
		t.Error("snap1 and snap2 should have different costs after additional Add")
	}
}

func TestTracker_Model(t *testing.T) {
	tracker := New("gpt-4o", nil)
	if tracker.Model() != "gpt-4o" {
		t.Errorf("Model() = %q, want %q", tracker.Model(), "gpt-4o")
	}
}

func TestTracker_PricingAvailable(t *testing.T) {
	t.Run("with pricing", func(t *testing.T) {
		pricing := &Pricing{InputPerMTokens: 1.0, OutputPerMTokens: 2.0}
		tracker := New("test", pricing)
		if !tracker.PricingAvailable() {
			t.Error("PricingAvailable() = false, want true")
		}
	})

	t.Run("without pricing", func(t *testing.T) {
		tracker := New("test", nil)
		if tracker.PricingAvailable() {
			t.Error("PricingAvailable() = true, want false")
		}
	})
}

func TestTracker_Concurrent(t *testing.T) {
	pricing := &Pricing{InputPerMTokens: 3.0, OutputPerMTokens: 15.0}
	tracker := New("test", pricing)

	var wg sync.WaitGroup
	for range 100 {
		wg.Go(func() {
			tracker.Add(100, 50, 10)
		})
	}

	wg.Wait()

	snap := tracker.Snapshot()
	if snap.InputTokens != 10000 {
		t.Errorf("InputTokens = %d, want 10000", snap.InputTokens)
	}
	if snap.CallCount != 100 {
		t.Errorf("CallCount = %d, want 100", snap.CallCount)
	}
}

func TestLookupPricing_ExactMatch(t *testing.T) {
	p := LookupPricing("gpt-4o", nil)
	if p == nil {
		t.Fatal("LookupPricing(\"gpt-4o\") = nil, want non-nil")
	}
	if p.InputPerMTokens != 2.5 {
		t.Errorf("InputPerMTokens = %f, want 2.5", p.InputPerMTokens)
	}
}

func TestLookupPricing_LongestPrefixMatch(t *testing.T) {
	// "gpt-4o-mini" should match "gpt-4o-mini" exactly, not "gpt-4o" as prefix.
	p := LookupPricing("gpt-4o-mini", nil)
	if p == nil {
		t.Fatal("LookupPricing(\"gpt-4o-mini\") = nil, want non-nil")
	}
	if p.InputPerMTokens != 0.15 {
		t.Errorf("InputPerMTokens = %f, want 0.15 (gpt-4o-mini pricing)", p.InputPerMTokens)
	}
}

func TestLookupPricing_PrefixMatch(t *testing.T) {
	// "claude-sonnet-4-20250514" should match "claude-sonnet-4".
	p := LookupPricing("claude-sonnet-4-20250514", nil)
	if p == nil {
		t.Fatal("LookupPricing(\"claude-sonnet-4-20250514\") = nil, want non-nil")
	}
	if p.InputPerMTokens != 3.0 {
		t.Errorf("InputPerMTokens = %f, want 3.0", p.InputPerMTokens)
	}
}

func TestLookupPricing_CustomOverrides(t *testing.T) {
	custom := map[string]Pricing{
		"my-model": {InputPerMTokens: 1.0, OutputPerMTokens: 5.0},
	}

	p := LookupPricing("my-model", custom)
	if p == nil {
		t.Fatal("LookupPricing(\"my-model\") = nil, want non-nil")
	}
	if p.InputPerMTokens != 1.0 {
		t.Errorf("InputPerMTokens = %f, want 1.0", p.InputPerMTokens)
	}
}

func TestLookupPricing_CustomOverridesDefault(t *testing.T) {
	custom := map[string]Pricing{
		"gpt-4o": {InputPerMTokens: 99.0, OutputPerMTokens: 99.0},
	}

	p := LookupPricing("gpt-4o", custom)
	if p == nil {
		t.Fatal("LookupPricing(\"gpt-4o\") = nil, want non-nil")
	}
	if p.InputPerMTokens != 99.0 {
		t.Errorf("InputPerMTokens = %f, want 99.0 (custom override)", p.InputPerMTokens)
	}
}

func TestLookupPricing_UnknownModel(t *testing.T) {
	p := LookupPricing("unknown-model-xyz", nil)
	if p != nil {
		t.Errorf("LookupPricing(\"unknown-model-xyz\") = %+v, want nil", p)
	}
}

func TestLookupPricing_NilCustom(t *testing.T) {
	p := LookupPricing("claude-opus-4", nil)
	if p == nil {
		t.Fatal("LookupPricing(\"claude-opus-4\") = nil, want non-nil")
	}
	if p.InputPerMTokens != 15.0 {
		t.Errorf("InputPerMTokens = %f, want 15.0", p.InputPerMTokens)
	}
}
