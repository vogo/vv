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
	"fmt"
	"net"
	"strings"

	"github.com/vogo/vv/configs"
)

// Kind enumerates the supported transports.
type Kind int

const (
	KindStdio Kind = iota
	KindHTTP
)

// Transport is the resolved transport configuration consumed by Serve.
type Transport struct {
	Kind     Kind
	Addr     string // non-empty only when Kind == KindHTTP
	Loopback bool   // true when Addr binds a loopback host
}

// ResolveTransport normalizes an MCPServerConfig into a Transport.
// configs.ValidateMCPServer is expected to have run via configs.Load, but
// ResolveTransport re-enforces the non-loopback + auth guard so callers
// that construct *configs.Config directly (e.g. tests, embedders) cannot
// silently bypass the defence-in-depth check.
func ResolveTransport(cfg configs.MCPServerConfig) (Transport, error) {
	switch strings.ToLower(strings.TrimSpace(cfg.Transport)) {
	case "", "stdio":
		return Transport{Kind: KindStdio}, nil
	case "http":
		if strings.TrimSpace(cfg.Addr) == "" {
			return Transport{}, fmt.Errorf("mcp.server.addr is required when transport=http")
		}

		loopback := IsLoopbackAddr(cfg.Addr)
		if !loopback && strings.TrimSpace(cfg.AuthToken) == "" {
			return Transport{}, fmt.Errorf(
				"mcp.server.auth_token is required when mcp.server.addr binds a non-loopback host (%q)",
				cfg.Addr,
			)
		}

		return Transport{
			Kind:     KindHTTP,
			Addr:     cfg.Addr,
			Loopback: loopback,
		}, nil
	default:
		return Transport{}, fmt.Errorf("unsupported mcp.server.transport %q", cfg.Transport)
	}
}

// IsLoopbackAddr reports whether addr's host is a loopback address.
// Mirrors configs.isLoopbackAddr: "localhost" and explicit loopback IPs
// (127.0.0.0/8, ::1) count; an empty host (bare ":port") does NOT, since
// net.Listen on that address binds every interface.
func IsLoopbackAddr(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}

	host = strings.TrimSpace(host)
	if host == "" {
		return false
	}

	if host == "localhost" {
		return true
	}

	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}

	return false
}
