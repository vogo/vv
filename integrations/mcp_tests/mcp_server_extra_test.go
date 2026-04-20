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
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/mcps"
)

// Verifies AC-2.3 block path: when credscrub action is "block", an outbound
// credential match should surface as an error CallToolResult rather than a
// redacted string.
func TestMCPServer_CredentialFilterBlocksOutbound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Echo agent replies with an AWS access key-like string.
	lookup := newStubLookup(newEchoAgent("coder", "Write code",
		"leaked key: AKIAIOSFODNN7EXAMPLE"))

	cfg := &configs.Config{}
	cfg.Security.MCPCredentialFilter = configs.MCPCredentialFilterConfig{Action: "block"}

	cli := startServer(t, ctx, cfg, lookup, nil)

	result, err := cli.CallTool(ctx, "coder", `{"input":"give me the key"}`)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	if !result.IsError {
		t.Fatalf("expected IsError=true when credential is blocked, got result=%+v", result)
	}

	if len(result.Content) == 0 ||
		!strings.Contains(result.Content[0].Text, "blocked by mcp credential filter") {
		t.Errorf("expected block message in content, got %+v", result.Content)
	}
}

// Verifies AC-2.3 inbound scan: a credential contained in the client-supplied
// arguments is redacted by the server before the agent sees it. The echo
// agent mirrors the redacted input back, proving the scanner rewrote the
// map in place.
func TestMCPServer_CredentialFilterRedactsInbound(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Empty response string forces echoAgent to use "echo: <input>".
	lookup := newStubLookup(newEchoAgent("coder", "Write code", ""))
	cli := startServer(t, ctx, &configs.Config{}, lookup, nil)

	payload := `{"input":"my AWS key is AKIAIOSFODNN7EXAMPLE please use it"}`

	result, err := cli.CallTool(ctx, "coder", payload)
	if err != nil {
		t.Fatalf("CallTool: %v", err)
	}

	if result.IsError {
		t.Fatalf("unexpected IsError: %+v", result)
	}

	if len(result.Content) == 0 {
		t.Fatal("expected content")
	}

	text := result.Content[0].Text
	if strings.Contains(text, "AKIAIOSFODNN7EXAMPLE") {
		t.Errorf("inbound credential not redacted; echo returned %q", text)
	}
}

// Verifies AC-1.4: the same exposed agent can be called multiple times in
// sequence on the same MCP session without state leaking between calls.
// Each call's response must reflect its own distinct input.
func TestMCPServer_MultipleSequentialCalls(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Empty response means the echo agent returns "echo: <input>" for each call.
	lookup := newStubLookup(newEchoAgent("coder", "Write code", ""))
	cli := startServer(t, ctx, &configs.Config{}, lookup, nil)

	inputs := []string{"first", "second", "third"}
	for i, in := range inputs {
		result, err := cli.CallTool(ctx, "coder", fmt.Sprintf(`{"input":%q}`, in))
		if err != nil {
			t.Fatalf("call #%d (%q): %v", i+1, in, err)
		}

		if result.IsError {
			t.Fatalf("call #%d (%q) unexpected IsError: %+v", i+1, in, result)
		}

		if len(result.Content) == 0 {
			t.Fatalf("call #%d (%q) empty content", i+1, in)
		}

		want := "echo: " + in
		if got := result.Content[0].Text; got != want {
			t.Errorf("call #%d: got %q, want %q", i+1, got, want)
		}
	}
}

// Verifies AC-3.4 stdio: ctx cancellation causes Serve to return well under
// the 2s acceptance threshold.
func TestMCPServer_StdioGracefulShutdown(t *testing.T) {
	srv, _, _, err := mcps.BuildServer(&configs.Config{},
		newStubLookup(newEchoAgent("coder", "Write code", "")), nil, slog.Default())
	if err != nil {
		t.Fatalf("BuildServer: %v", err)
	}

	_, serverTransport := mcp.NewInMemoryTransports()

	ctx, cancel := context.WithCancel(context.Background())

	serveDone := make(chan error, 1)
	go func() { serveDone <- srv.Serve(ctx, serverTransport) }()

	// Give the server a moment to actually enter Serve.
	time.Sleep(50 * time.Millisecond)

	start := time.Now()
	cancel()

	select {
	case <-serveDone:
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Errorf("stdio Serve took %v to return after cancel; want <2s", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("stdio Serve did not return within 2s of ctx cancel")
	}
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

// Verifies AC-3.3 auth failure: when auth_token is configured, a request to
// the HTTP MCP endpoint without the correct Authorization header is
// rejected with 401, even on the protected MCP root path. /healthz stays
// in front of the auth layer so it is not a useful failure probe here;
// we probe "/" instead, which is what MCP clients hit.
func TestMCPServer_HTTPBearerAuthRejectsMissingHeader(t *testing.T) {
	cfg := &configs.Config{}
	base, shutdown := startHTTPServer(t, cfg, "s3cret")
	defer shutdown()

	req, err := http.NewRequest(http.MethodPost, base+"/", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	if resp.StatusCode != http.StatusUnauthorized {
		t.Errorf("want 401 without auth header, got %d", resp.StatusCode)
	}

	// Wrong token -> still 401.
	req2, _ := http.NewRequest(http.MethodPost, base+"/", strings.NewReader(`{}`))
	req2.Header.Set("Content-Type", "application/json")
	req2.Header.Set("Authorization", "Bearer wrong")

	resp2, err := http.DefaultClient.Do(req2)
	if err != nil {
		t.Fatalf("Do wrong: %v", err)
	}
	defer func() { _ = resp2.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp2.Body)

	if resp2.StatusCode != http.StatusUnauthorized {
		t.Errorf("want 401 with wrong bearer, got %d", resp2.StatusCode)
	}
}

// Verifies AC-3.3 auth success: with the correct Authorization header, the
// auth layer lets the request through to the MCP handler. We send an empty
// JSON body so the MCP handler responds with a non-401 status (it will
// reject the malformed payload itself, but auth has already succeeded).
func TestMCPServer_HTTPBearerAuthAcceptsCorrectHeader(t *testing.T) {
	cfg := &configs.Config{}
	token := "s3cret"
	base, shutdown := startHTTPServer(t, cfg, token)
	defer shutdown()

	req, err := http.NewRequest(http.MethodPost, base+"/", strings.NewReader(`{}`))
	if err != nil {
		t.Fatalf("NewRequest: %v", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer "+token)

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer func() { _ = resp.Body.Close() }()
	_, _ = io.Copy(io.Discard, resp.Body)

	// The MCP handler may reject the empty/invalid payload with 4xx, but
	// it must NOT be 401 — that would mean auth failed.
	if resp.StatusCode == http.StatusUnauthorized {
		t.Errorf("expected auth to succeed with correct token, got 401")
	}
}

// Verifies AC-3.4 HTTP: Shutdown stops the listener in well under 2s, so
// Ctrl+C / SIGTERM results in a prompt exit even under HTTP transport.
func TestMCPServer_HTTPGracefulShutdown(t *testing.T) {
	cfg := &configs.Config{}
	base, shutdown := startHTTPServer(t, cfg, "")

	// Prove the server is actually accepting connections before we shut down.
	resp, err := http.Get(base + "/healthz")
	if err != nil {
		t.Fatalf("pre-shutdown healthz GET: %v", err)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	_ = resp.Body.Close()
	if resp.StatusCode != http.StatusNoContent {
		t.Fatalf("pre-shutdown healthz want 204, got %d", resp.StatusCode)
	}

	start := time.Now()
	done := make(chan struct{})
	go func() { shutdown(); close(done) }()

	select {
	case <-done:
		if elapsed := time.Since(start); elapsed > 2*time.Second {
			t.Errorf("HTTP shutdown took %v; want <2s", elapsed)
		}
	case <-time.After(2 * time.Second):
		t.Fatalf("HTTP shutdown did not complete within 2s")
	}

	// After shutdown, further requests must fail (connection refused, etc.).
	client := &http.Client{Timeout: 500 * time.Millisecond}
	if _, err := client.Get(base + "/healthz"); err == nil {
		t.Errorf("expected request to fail after shutdown, but it succeeded")
	}
}
