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

// Package mcps wires the vage MCP server into the vv application. It is the
// MCP counterpart of httpapis: given a resolved configuration and the result
// of setup.Init, Serve starts an MCP server that exposes vv's dispatchable
// agents as MCP tools over stdio or Streamable HTTP.
package mcps

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
	"github.com/vogo/vage/agent"
	mcpserver "github.com/vogo/vage/mcp/server"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/setup"
)

// Serve starts the MCP server bound to the transport specified by cfg.MCP.Server.
// It blocks until ctx is canceled or a fatal error occurs.
func Serve(ctx context.Context, cfg *configs.Config, init *setup.InitResult) error {
	if init == nil || init.SetupResult == nil {
		return errors.New("mcps: init result is nil")
	}

	return serve(ctx, cfg, init.SetupResult, init.SetupResult.Dispatcher, slog.Default())
}

// serve is the testable core of Serve; it accepts an AgentLookup and an
// optional dispatcher so tests can drive it with a fake registry.
func serve(
	ctx context.Context,
	cfg *configs.Config,
	lookup AgentLookup,
	dispatcher agent.Agent,
	logger *slog.Logger,
) error {
	srv, exposed, exposedDispatcher, err := BuildServer(cfg, lookup, dispatcher, logger)
	if err != nil {
		return err
	}

	t, err := ResolveTransport(cfg.MCP.Server)
	if err != nil {
		return err
	}

	logger.Info("vv: mcp server ready",
		"transport", transportName(t.Kind),
		"addr", t.Addr,
		"exposed_tools", toolNames(exposed, exposedDispatcher),
		"auth", cfg.MCP.Server.AuthToken != "",
		"session_timeout_s", cfg.MCP.Server.SessionTimeout,
	)

	switch t.Kind {
	case KindStdio:
		return srv.Serve(ctx, &mcp.StdioTransport{})
	case KindHTTP:
		return serveHTTP(ctx, srv, t, cfg.MCP.Server, logger)
	default:
		return fmt.Errorf("mcps: unknown transport kind %v", t.Kind)
	}
}

// BuildServer assembles an MCP server with the credscrub scanner installed
// and the configured agents (plus optional dispatcher) registered. The
// transport is not started — callers drive it via Serve or directly through
// the returned server.
func BuildServer(
	cfg *configs.Config,
	lookup AgentLookup,
	dispatcher agent.Agent,
	logger *slog.Logger,
) (*mcpserver.Server, []agent.Agent, agent.Agent, error) {
	if logger == nil {
		logger = slog.Default()
	}

	exposed, err := selectAgents(lookup, cfg.MCP.Server.Agents)
	if err != nil {
		return nil, nil, nil, err
	}

	scanner := configs.BuildMCPCredentialScanner(cfg.Security.MCPCredentialFilter)
	srv := mcpserver.NewServer(
		mcpserver.WithCredentialScanner(scanner),
		mcpserver.WithScanCallback(buildScanCallback(logger)),
	)

	for _, a := range exposed {
		if err := srv.RegisterAgent(a); err != nil {
			return nil, nil, nil, fmt.Errorf("register agent %q: %w", a.ID(), err)
		}
	}

	var exposedDispatcher agent.Agent
	if cfg.MCP.Server.ExposeDispatcher && dispatcher != nil {
		if err := srv.RegisterAgent(dispatcher); err != nil {
			return nil, nil, nil, fmt.Errorf("register dispatcher: %w", err)
		}
		exposedDispatcher = dispatcher
	}

	return srv, exposed, exposedDispatcher, nil
}

func serveHTTP(
	ctx context.Context,
	srv *mcpserver.Server,
	t Transport,
	cfg configs.MCPServerConfig,
	logger *slog.Logger,
) error {
	handler := mcp.NewStreamableHTTPHandler(
		func(_ *http.Request) *mcp.Server { return srv.Server() },
		&mcp.StreamableHTTPOptions{
			SessionTimeout: time.Duration(cfg.SessionTimeout) * time.Second,
			Logger:         logger,
		},
	)

	mux := http.NewServeMux()
	if cfg.AuthToken != "" {
		mux.Handle("/", bearerAuth(cfg.AuthToken, handler))
	} else {
		mux.Handle("/", handler)
	}
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	})

	ln, err := net.Listen("tcp", t.Addr)
	if err != nil {
		return fmt.Errorf("listen %s: %w", t.Addr, err)
	}

	httpSrv := &http.Server{
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}

	// done closes once Serve returns, so the shutdown goroutine is not
	// leaked on unexpected Serve errors (e.g. listener closed externally).
	done := make(chan struct{})
	defer close(done)

	go func() {
		select {
		case <-ctx.Done():
			_ = httpSrv.Shutdown(context.Background())
		case <-done:
		}
	}()

	if err := httpSrv.Serve(ln); err != nil && !errors.Is(err, http.ErrServerClosed) {
		return fmt.Errorf("mcp server: %w", err)
	}

	logger.Info("vv: mcp server shutdown complete")

	return nil
}

func transportName(k Kind) string {
	switch k {
	case KindStdio:
		return "stdio"
	case KindHTTP:
		return "http"
	default:
		return "unknown"
	}
}
