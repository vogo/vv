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

package mcps

import (
	"context"
	"fmt"
	"log/slog"

	"github.com/vogo/vage/agent"
	mcpserver "github.com/vogo/vage/mcp/server"
	"github.com/vogo/vage/security/credscrub"
)

// AgentLookup resolves a dispatchable agent by ID. The result from
// setup.Init wraps a registry that fits this interface.
type AgentLookup interface {
	Agents() []agent.Agent
	Agent(id string) agent.Agent
}

// selectAgents returns the agents to expose. An empty whitelist means
// all dispatchable agents.
func selectAgents(lookup AgentLookup, whitelist []string) ([]agent.Agent, error) {
	if len(whitelist) == 0 {
		return lookup.Agents(), nil
	}

	out := make([]agent.Agent, 0, len(whitelist))
	for i, id := range whitelist {
		a := lookup.Agent(id)
		if a == nil {
			return nil, fmt.Errorf("mcp.server.agents[%d]=%q not registered", i, id)
		}
		out = append(out, a)
	}

	return out, nil
}

// toolNames returns the list of MCP tool names that will be exposed,
// which equals each agent's ID plus the dispatcher ID when enabled.
func toolNames(agents []agent.Agent, dispatcher agent.Agent) []string {
	names := make([]string, 0, len(agents)+1)
	for _, a := range agents {
		names = append(names, a.ID())
	}

	if dispatcher != nil {
		names = append(names, dispatcher.ID())
	}

	return names
}

// buildScanCallback wires credential-scan events from the MCP server into
// slog so they appear alongside the existing mcp_credential_detected log
// trail. Plaintext values are never logged — callers pass only masked
// previews via credscrub.Hit.Masked.
func buildScanCallback(logger *slog.Logger) mcpserver.ScanCallback {
	if logger == nil {
		logger = slog.Default()
	}

	return func(_ context.Context, ev mcpserver.ScanEvent) {
		logger.Warn(
			"vv: mcp credential scanner hit",
			"direction", ev.Direction,
			"tool", ev.ToolName,
			"action", string(ev.Action),
			"hit_types", credscrub.SummarizeTypes(ev.Hits),
			"hit_count", len(ev.Hits),
			"truncated", ev.Truncated,
		)
	}
}
