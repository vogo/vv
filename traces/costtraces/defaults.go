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

import "sort"

// DefaultPricing maps model name patterns to pricing.
var DefaultPricing = map[string]Pricing{
	"claude-opus-4":   {InputPerMTokens: 15.0, OutputPerMTokens: 75.0, CachePerMTokens: 1.5},
	"claude-sonnet-4": {InputPerMTokens: 3.0, OutputPerMTokens: 15.0, CachePerMTokens: 0.3},
	"gpt-4o":          {InputPerMTokens: 2.5, OutputPerMTokens: 10.0},
	"gpt-4o-mini":     {InputPerMTokens: 0.15, OutputPerMTokens: 0.6},
	"gpt-4.1":         {InputPerMTokens: 2.0, OutputPerMTokens: 8.0},
	"gpt-4.1-mini":    {InputPerMTokens: 0.4, OutputPerMTokens: 1.6},
}

// LookupPricing finds pricing for a model name.
// It tries exact match first, then longest-prefix match (e.g., "claude-sonnet-4-20250514"
// matches "claude-sonnet-4", and "gpt-4o-mini" matches "gpt-4o-mini" not "gpt-4o").
func LookupPricing(model string, custom map[string]Pricing) *Pricing {
	// Check custom overrides first (exact then longest prefix).
	if p, ok := exactOrLongestPrefixMatch(model, custom); ok {
		return p
	}

	// Fall back to defaults.
	if p, ok := exactOrLongestPrefixMatch(model, DefaultPricing); ok {
		return p
	}

	return nil
}

// exactOrLongestPrefixMatch tries exact match, then longest prefix match.
func exactOrLongestPrefixMatch(model string, pricing map[string]Pricing) (*Pricing, bool) {
	if len(pricing) == 0 {
		return nil, false
	}

	// Exact match.
	if p, ok := pricing[model]; ok {
		return &p, true
	}

	// Collect and sort keys by length (longest first) for correct prefix matching.
	keys := make([]string, 0, len(pricing))
	for k := range pricing {
		keys = append(keys, k)
	}

	sort.Slice(keys, func(i, j int) bool {
		return len(keys[i]) > len(keys[j])
	})

	for _, k := range keys {
		if len(model) > len(k) && model[:len(k)] == k {
			p := pricing[k]
			return &p, true
		}
	}

	return nil, false
}
