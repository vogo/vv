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
	"os"
	"path/filepath"
	"testing"
)

// TestAgentsConfig_PromptCachingDefaultOn confirms the nil-default-on
// resolver: an unset `prompt_caching` returns true.
func TestAgentsConfig_PromptCachingDefaultOn(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "vv.yaml")
	content := `llm:
  provider: openai
  model: test-model
  api_key: test-key
  base_url: http://127.0.0.1:0
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(cfgPath, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Agents.PromptCaching != nil {
		t.Errorf("PromptCaching pointer = %v, want nil (unset)", *cfg.Agents.PromptCaching)
	}
	if !cfg.Agents.EffectivePromptCaching() {
		t.Errorf("EffectivePromptCaching() = false, want true (nil-default-on)")
	}
}

// TestAgentsConfig_PromptCachingExplicitFalse verifies an explicit
// `prompt_caching: false` in YAML disables caching.
func TestAgentsConfig_PromptCachingExplicitFalse(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "vv.yaml")
	content := `llm:
  provider: openai
  model: test-model
  api_key: test-key
  base_url: http://127.0.0.1:0
agents:
  prompt_caching: false
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(cfgPath, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Agents.PromptCaching == nil || *cfg.Agents.PromptCaching {
		t.Errorf("PromptCaching = %v, want pointer to false", cfg.Agents.PromptCaching)
	}
	if cfg.Agents.EffectivePromptCaching() {
		t.Errorf("EffectivePromptCaching() = true, want false")
	}
}

// TestAgentsConfig_PromptCachingEnvOverride verifies that
// VV_AGENTS_PROMPT_CACHING=false wins over a YAML default.
func TestAgentsConfig_PromptCachingEnvOverride(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "vv.yaml")
	content := `llm:
  provider: openai
  model: test-model
  api_key: test-key
  base_url: http://127.0.0.1:0
agents:
  prompt_caching: true
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("VV_AGENTS_PROMPT_CACHING", "false")
	cfg, err := Load(cfgPath, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Agents.PromptCaching == nil || *cfg.Agents.PromptCaching {
		t.Errorf("PromptCaching = %v, want pointer to false (env override)", cfg.Agents.PromptCaching)
	}
}

// TestAgentsConfig_EffectivePromptCaching_NilReceiver guards the nil-safe
// path in the resolver — protects against panics if a caller forgets to
// run applyDefaults.
func TestAgentsConfig_EffectivePromptCaching_NilReceiver(t *testing.T) {
	var c *AgentsConfig
	if !c.EffectivePromptCaching() {
		t.Errorf("nil receiver EffectivePromptCaching() = false, want true")
	}
}
