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

package setup

import (
	"testing"

	"github.com/vogo/vv/configs"
)

// trueP is a tiny convenience used by config tests; pointer-to-true.
func trueP() *bool { b := true; return &b }

// TestBuildTreePromoter_Kinds covers the three legitimate promoter selections
// plus the unknown-kind error path.
func TestBuildTreePromoter_Kinds(t *testing.T) {
	mock := &mockChatCompleter{}
	cfgFor := func(kind string) *configs.Config {
		return &configs.Config{
			LLM: configs.LLMConfig{Model: "test"},
			SessionTree: configs.SessionTreeConfig{
				Enabled: trueP(),
				Promotion: configs.SessionTreePromotionConfig{
					Enabled:  trueP(),
					Promoter: kind,
				},
			},
		}
	}

	for _, kind := range []string{"noop", "compressor", "llm"} {
		_, err := buildTreePromoter(cfgFor(kind), mock)
		if err != nil {
			t.Errorf("buildTreePromoter(%q) returned error: %v", kind, err)
		}
	}

	if _, err := buildTreePromoter(cfgFor("zzz"), mock); err == nil {
		t.Errorf("buildTreePromoter(unknown) returned nil error")
	}
}

// TestBuildTreePromoter_LLMRequiresClient verifies the explicit error when
// promoter=llm is selected but no chat completer is wired.
func TestBuildTreePromoter_LLMRequiresClient(t *testing.T) {
	cfg := &configs.Config{
		LLM: configs.LLMConfig{Model: "test"},
		SessionTree: configs.SessionTreeConfig{
			Enabled: trueP(),
			Promotion: configs.SessionTreePromotionConfig{
				Enabled:  trueP(),
				Promoter: "llm",
			},
		},
	}
	if _, err := buildTreePromoter(cfg, nil); err == nil {
		t.Errorf("buildTreePromoter(llm, nil client) returned nil error")
	}
}

// TestBuildTreeDecider_Composition spot-checks that both threshold deciders
// are always present and AllChildrenDone toggles via config.
func TestBuildTreeDecider_Composition(t *testing.T) {
	cfg := &configs.Config{
		SessionTree: configs.SessionTreeConfig{
			Promotion: configs.SessionTreePromotionConfig{
				ChildrenThreshold:     2,
				SubtreeBytesThreshold: 100,
			},
		},
	}
	if d := buildTreeDecider(cfg); d == nil {
		t.Fatal("decider must not be nil")
	}

	// disable AllChildrenDone
	f := false
	cfg.SessionTree.Promotion.AllChildrenDone = &f
	if d := buildTreeDecider(cfg); d == nil {
		t.Fatal("decider must not be nil with AllChildrenDone=false")
	}
}
