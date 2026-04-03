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

package costtraces

import "sync"

// Pricing defines cost rates for a model (USD per million tokens).
type Pricing struct {
	InputPerMTokens  float64 `json:"input_per_m_tokens" yaml:"input_per_m_tokens"`
	OutputPerMTokens float64 `json:"output_per_m_tokens" yaml:"output_per_m_tokens"`
	CachePerMTokens  float64 `json:"cache_per_m_tokens,omitempty" yaml:"cache_per_m_tokens,omitempty"`
}

// Usage holds accumulated token usage and cost.
type Usage struct {
	InputTokens      int      `json:"input_tokens"`
	OutputTokens     int      `json:"output_tokens"`
	CacheReadTokens  int      `json:"cache_read_tokens"`
	TotalTokens      int      `json:"total_tokens"`
	EstimatedCostUSD *float64 `json:"estimated_cost_usd"`
	CallCount        int      `json:"call_count"`
}

// Tracker accumulates token usage and estimates cost.
type Tracker struct {
	mu      sync.Mutex
	usage   Usage
	model   string
	pricing *Pricing // nil if no pricing available
}

// New creates a Tracker for the given model with optional pricing.
func New(model string, pricing *Pricing) *Tracker {
	return &Tracker{model: model, pricing: pricing}
}

// Add records tokens from a single LLM call.
//
// promptTokens (from aimodel.Usage.PromptTokens) includes cache-read tokens
// for Anthropic (via totalInputTokens()). The cost calculation separates them:
// non-cached input tokens are charged at the input rate, cache-read tokens at
// the (lower) cache rate.
func (t *Tracker) Add(promptTokens, completionTokens, cacheReadTokens int) {
	t.mu.Lock()
	defer t.mu.Unlock()

	t.usage.InputTokens += promptTokens
	t.usage.OutputTokens += completionTokens
	t.usage.CacheReadTokens += cacheReadTokens
	t.usage.TotalTokens += promptTokens + completionTokens
	t.usage.CallCount++

	if t.pricing != nil {
		// Subtract cache-read tokens from input count to avoid double-charging.
		// PromptTokens includes cache-read tokens (Anthropic's totalInputTokens()),
		// so we charge: (input - cached) * inputRate + cached * cacheRate + output * outputRate.
		nonCachedInput := t.usage.InputTokens - t.usage.CacheReadTokens

		cost := float64(nonCachedInput)/1_000_000*t.pricing.InputPerMTokens +
			float64(t.usage.OutputTokens)/1_000_000*t.pricing.OutputPerMTokens +
			float64(t.usage.CacheReadTokens)/1_000_000*t.pricing.CachePerMTokens
		t.usage.EstimatedCostUSD = &cost
	}
}

// Snapshot returns a copy of the current usage.
func (t *Tracker) Snapshot() Usage {
	t.mu.Lock()
	defer t.mu.Unlock()

	u := t.usage
	if t.usage.EstimatedCostUSD != nil {
		v := *t.usage.EstimatedCostUSD
		u.EstimatedCostUSD = &v
	}

	return u
}

// Model returns the model name.
func (t *Tracker) Model() string { return t.model }

// PricingAvailable returns whether pricing is configured.
func (t *Tracker) PricingAvailable() bool { return t.pricing != nil }
