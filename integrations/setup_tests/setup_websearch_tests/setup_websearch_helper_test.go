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

package setup_websearch_tests

import (
	"context"

	"github.com/vogo/aimodel"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/registries"
)

// mockChatCompleter is a no-op ChatCompleter that lets setup.New build agents
// without making outbound LLM calls. The websearch tests never trigger the
// model — they assert on tool-registry composition, not on completion behavior.
type mockChatCompleter struct {
	response *aimodel.ChatResponse
	err      error
}

func (m *mockChatCompleter) ChatCompletion(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.ChatResponse, error) {
	if m.err != nil {
		return nil, m.err
	}
	return m.response, nil
}

func (m *mockChatCompleter) ChatCompletionStream(_ context.Context, _ *aimodel.ChatRequest) (*aimodel.Stream, error) {
	return nil, m.err
}

// listProfileFull / listProfileReadOnly / listProfileReview build the named
// ToolProfile registry from cfg and return its tool name list. Used by
// TestIntegration_SetupNew_WebSearch_BuildRegistryAcrossProfiles to assert
// that web_search appears in every read-capable profile when configured and
// is absent when not.
func listProfileFull(cfg configs.ToolsConfig) ([]string, error) {
	return listToolNames(registries.ProfileFull, cfg)
}

func listProfileReadOnly(cfg configs.ToolsConfig) ([]string, error) {
	return listToolNames(registries.ProfileReadOnly, cfg)
}

func listProfileReview(cfg configs.ToolsConfig) ([]string, error) {
	return listToolNames(registries.ProfileReview, cfg)
}

func listToolNames(p registries.ToolProfile, cfg configs.ToolsConfig) ([]string, error) {
	reg, err := p.BuildRegistry(cfg)
	if err != nil {
		return nil, err
	}
	defs := reg.List()
	names := make([]string, 0, len(defs))
	for _, td := range defs {
		names = append(names, td.Name)
	}
	return names, nil
}
