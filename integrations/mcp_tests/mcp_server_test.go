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

package mcp_tests

import (
	"context"
	"log/slog"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/vogo/vage/agent"
	mcpclient "github.com/vogo/vage/mcp/client"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/mcps"
)

// echoAgent runs vv's MCP registration path end-to-end without needing a
// real LLM. It simulates what coder/researcher/reviewer would produce.
type echoAgent struct {
	agent.Base
	response string
}

func newEchoAgent(id, description, response string) *echoAgent {
	return &echoAgent{
		Base:     agent.NewBase(agent.Config{ID: id, Name: id, Description: description}),
		response: response,
	}
}

func (e *echoAgent) Run(_ context.Context, req *schema.RunRequest) (*schema.RunResponse, error) {
	input := ""
	if len(req.Messages) > 0 {
		input = req.Messages[0].Content.Text()
	}

	reply := e.response
	if reply == "" {
		reply = "echo: " + input
	}

	return &schema.RunResponse{
		Messages: []schema.Message{schema.NewUserMessage(reply)},
	}, nil
}

type stubLookup struct {
	byID map[string]agent.Agent
	all  []agent.Agent
}

func (s stubLookup) Agents() []agent.Agent       { return s.all }
func (s stubLookup) Agent(id string) agent.Agent { return s.byID[id] }

func newStubLookup(agents ...agent.Agent) stubLookup {
	byID := make(map[string]agent.Agent, len(agents))
	all := make([]agent.Agent, 0, len(agents))
	for _, a := range agents {
		byID[a.ID()] = a
		all = append(all, a)
	}
	return stubLookup{byID: byID, all: all}
}

// startServer builds the MCP server from a fake registry, starts it on an
// in-memory transport, and returns a connected MCP client. The test context
// is used for both server Serve and client lifetime.
func startServer(
	t *testing.T,
	ctx context.Context,
	cfg *configs.Config,
	lookup mcps.AgentLookup,
	dispatcher agent.Agent,
) *mcpclient.Client {
	t.Helper()

	srv, _, _, err := mcps.BuildServer(cfg, lookup, dispatcher, slog.Default())
	if err != nil {
		t.Fatalf("BuildServer: %v", err)
	}

	clientTransport, serverTransport := mcp.NewInMemoryTransports()

	go func() { _ = srv.Serve(ctx, serverTransport) }()

	cli := mcpclient.NewClient("test://vv")
	if err := cli.Connect(ctx, clientTransport); err != nil {
		t.Fatalf("Connect: %v", err)
	}

	t.Cleanup(func() { _ = cli.Disconnect() })

	return cli
}

// Verifies AC-1.2: ListTools returns the exposed agents as MCP tools.
func TestMCPServer_ListsExposedAgents(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	lookup := newStubLookup(
		newEchoAgent("coder", "Write code", "coder-reply"),
		newEchoAgent("researcher", "Read code", "researcher-reply"),
		newEchoAgent("reviewer", "Review code", "reviewer-reply"),
	)

	cli := startServer(t, ctx, &configs.Config{}, lookup, nil)

	tools, err := cli.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	got := toolNames(tools)
	sort.Strings(got)
	want := []string{"coder", "researcher", "reviewer"}

	if !equalStrings(got, want) {
		t.Errorf("tool names = %v, want %v", got, want)
	}
}

// Verifies AC-1.3: calling an exposed agent routes through agent.Run and
// returns the text response.
func TestMCPServer_CallCoderReturnsText(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	lookup := newStubLookup(newEchoAgent("coder", "Write code", "coder saw: hello"))
	cli := startServer(t, ctx, &configs.Config{}, lookup, nil)

	result, err := cli.CallTool(ctx, "coder", `{"input": "hello"}`)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	if result.IsError {
		t.Fatalf("unexpected IsError: %+v", result)
	}

	if len(result.Content) == 0 || !strings.Contains(result.Content[0].Text, "coder saw: hello") {
		t.Errorf("unexpected content: %+v", result.Content)
	}
}

// Verifies AC-4.1: agents whitelist filters the exposed set.
func TestMCPServer_Whitelist(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	lookup := newStubLookup(
		newEchoAgent("coder", "Write code", ""),
		newEchoAgent("researcher", "Read code", ""),
	)
	cfg := &configs.Config{}
	cfg.MCP.Server.Agents = []string{"coder"}

	cli := startServer(t, ctx, cfg, lookup, nil)

	tools, err := cli.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	if len(tools) != 1 || tools[0].Name != "coder" {
		t.Errorf("unexpected tool list: %v", toolNames(tools))
	}
}

// Verifies AC-4.2: expose_dispatcher=true surfaces the dispatcher tool.
func TestMCPServer_ExposeDispatcher(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	lookup := newStubLookup(newEchoAgent("coder", "Write code", ""))
	dispatcher := newEchoAgent("dispatcher", "Route user request", "plan complete")

	cfg := &configs.Config{}
	cfg.MCP.Server.ExposeDispatcher = true

	cli := startServer(t, ctx, cfg, lookup, dispatcher)

	tools, err := cli.ListTools(ctx)
	if err != nil {
		t.Fatalf("ListTools: %v", err)
	}

	got := toolNames(tools)
	sort.Strings(got)
	want := []string{"coder", "dispatcher"}

	if !equalStrings(got, want) {
		t.Errorf("tool names = %v, want %v", got, want)
	}
}

// Verifies AC-2.3: the outbound scanner redacts credential-like content
// produced by the exposed agent before it reaches the MCP client.
func TestMCPServer_CredentialFilterRedactsOutbound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// The echo response contains an AWS access key-like string; default
	// credscrub config should redact it.
	lookup := newStubLookup(newEchoAgent("coder", "Write code",
		"retrieved key: AKIAIOSFODNN7EXAMPLE"))

	cli := startServer(t, ctx, &configs.Config{}, lookup, nil)

	result, err := cli.CallTool(ctx, "coder", `{"input":"secret"}`)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	if len(result.Content) == 0 {
		t.Fatal("expected content")
	}

	text := result.Content[0].Text
	if strings.Contains(text, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("credential not redacted: %q", text)
	}
}

// Verifies that an unknown whitelist entry fails fast before listening.
func TestMCPServer_UnknownAgentInWhitelist(t *testing.T) {
	lookup := newStubLookup(newEchoAgent("coder", "", ""))
	cfg := &configs.Config{}
	cfg.MCP.Server.Agents = []string{"ghost"}

	_, _, _, err := mcps.BuildServer(cfg, lookup, nil, slog.Default())
	if err == nil {
		t.Fatal("expected error for unknown agent in whitelist")
	}
}

func toolNames(tools []schema.ToolDef) []string {
	out := make([]string, len(tools))
	for i, t := range tools {
		out[i] = t.Name
	}
	return out
}

func equalStrings(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
