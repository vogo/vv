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
	"crypto/subtle"
	"log/slog"
	"net"
	"net/http"
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

// bearerAuthTestMiddleware replicates mcps.bearerAuth (unexported) so the
// HTTP integration test can assemble the same handler stack serveHTTP builds.
// Keep in sync with mcps/auth.go.
func bearerAuthTestMiddleware(token string, next http.Handler) http.Handler {
	expected := []byte("Bearer " + token)

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got := []byte(r.Header.Get("Authorization"))
		if subtle.ConstantTimeCompare(got, expected) != 1 {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// startHTTPServer builds the same HTTP handler stack as mcps.serveHTTP,
// binds it to a loopback address with a free port, and returns the base
// URL plus a shutdown hook. It does not call mcps.Serve directly because
// mcps.Serve demands a *setup.InitResult whose inner *setup.Result has
// unexported fields; instead, it exercises the public BuildServer entry
// point plus the exported transport/auth primitives the production Serve
// wires together.
func startHTTPServer(
	t *testing.T,
	cfg *configs.Config,
	token string,
) (string, func()) {
	t.Helper()

	srv, _, _, err := mcps.BuildServer(cfg,
		newStubLookup(newEchoAgent("coder", "Write code", "ok")), nil, slog.Default())
	if err != nil {
		t.Fatalf("BuildServer: %v", err)
	}

	// Probe a free loopback port and close immediately so the HTTP server
	// can re-bind it.
	probe, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("probe listen: %v", err)
	}
	addr := probe.Addr().String()
	_ = probe.Close()

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		t.Fatalf("listen %s: %v", addr, err)
	}

	handler := mcp.NewStreamableHTTPHandler(
		func(_ *http.Request) *mcp.Server { return srv.Server() },
		&mcp.StreamableHTTPOptions{},
	)

	mux := http.NewServeMux()
	if token != "" {
		mux.Handle("/", bearerAuthTestMiddleware(token, handler))
	} else {
		mux.Handle("/", handler)
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	httpSrv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() { _ = httpSrv.Serve(ln) }()

	base := "http://" + addr
	shutdown := func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		_ = httpSrv.Shutdown(shutdownCtx)
	}
	return base, shutdown
}
