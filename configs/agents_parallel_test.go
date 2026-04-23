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

// TestAgentsConfig_MaxParallelToolCallsDefault verifies that loading a
// config that omits `agents.max_parallel_tool_calls` applies the framework
// default (4) via applyDefaults.
func TestAgentsConfig_MaxParallelToolCallsDefault(t *testing.T) {
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
	if cfg.Agents.MaxParallelToolCalls != 4 {
		t.Errorf("MaxParallelToolCalls = %d, want 4 (default)", cfg.Agents.MaxParallelToolCalls)
	}
}

// TestAgentsConfig_MaxParallelToolCallsYAML verifies that an explicit YAML
// value overrides the default.
func TestAgentsConfig_MaxParallelToolCallsYAML(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "vv.yaml")
	content := `llm:
  provider: openai
  model: test-model
  api_key: test-key
  base_url: http://127.0.0.1:0
agents:
  max_parallel_tool_calls: 2
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(cfgPath, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Agents.MaxParallelToolCalls != 2 {
		t.Errorf("MaxParallelToolCalls = %d, want 2 (yaml override)", cfg.Agents.MaxParallelToolCalls)
	}
}

// TestAgentsConfig_MaxParallelToolCallsEnv verifies that the env override
// beats the YAML value.
func TestAgentsConfig_MaxParallelToolCallsEnv(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "vv.yaml")
	content := `llm:
  provider: openai
  model: test-model
  api_key: test-key
  base_url: http://127.0.0.1:0
agents:
  max_parallel_tool_calls: 2
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	t.Setenv("VV_AGENTS_MAX_PARALLEL_TOOL_CALLS", "8")
	cfg, err := Load(cfgPath, true)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Agents.MaxParallelToolCalls != 8 {
		t.Errorf("MaxParallelToolCalls = %d, want 8 (env override)", cfg.Agents.MaxParallelToolCalls)
	}
}
