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

package configs

import (
	"testing"
)

// TestSessionTreeConfig_Defaults locks the design doc invariant: SessionTree
// is opt-in, so a zero value reads as disabled.
func TestSessionTreeConfig_Defaults(t *testing.T) {
	var c SessionTreeConfig
	if c.IsEnabled() {
		t.Errorf("default SessionTreeConfig.IsEnabled = true, want false")
	}
	if c.Promotion.IsEnabled() {
		t.Errorf("default Promotion.IsEnabled = true, want false")
	}
	if got := c.Promotion.PromoterKind(); got != "compressor" {
		t.Errorf("default PromoterKind = %q, want compressor", got)
	}
	if !c.Promotion.AllChildrenDoneEnabled() {
		t.Errorf("default AllChildrenDoneEnabled = false, want true")
	}
}

// TestSessionTreeConfig_PromoterKindNormalises confirms whitespace and case
// don't break the "compressor / llm / noop" switch downstream.
func TestSessionTreeConfig_PromoterKindNormalises(t *testing.T) {
	cases := map[string]string{
		"":           "compressor",
		" llm ":      "llm",
		"LLM":        "llm",
		"NoOp":       "noop",
		"compressor": "compressor",
	}
	for input, want := range cases {
		c := SessionTreePromotionConfig{Promoter: input}
		if got := c.PromoterKind(); got != want {
			t.Errorf("PromoterKind(%q) = %q, want %q", input, got, want)
		}
	}
}

// TestOrchestrateConfig_WriteTreeDefault keeps the dispatcher auto-write
// surface opt-in. A nil pointer (= unset YAML) means "do not mirror".
func TestOrchestrateConfig_WriteTreeDefault(t *testing.T) {
	var o OrchestrateConfig
	if o.IsWriteTreeEnabled() {
		t.Errorf("default WriteTree = true, want false")
	}
	tr := true
	o.WriteTree = &tr
	if !o.IsWriteTreeEnabled() {
		t.Errorf("WriteTree=true not honoured")
	}
}
