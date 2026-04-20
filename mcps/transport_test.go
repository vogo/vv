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
	"testing"

	"github.com/vogo/vv/configs"
)

func TestResolveTransport_DefaultsToStdio(t *testing.T) {
	got, err := ResolveTransport(configs.MCPServerConfig{})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.Kind != KindStdio {
		t.Errorf("want KindStdio, got %v", got.Kind)
	}

	if got.Addr != "" {
		t.Errorf("stdio should not carry an addr, got %q", got.Addr)
	}
}

func TestResolveTransport_HTTPLoopback(t *testing.T) {
	got, err := ResolveTransport(configs.MCPServerConfig{
		Transport: "http",
		Addr:      "127.0.0.1:7801",
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if got.Kind != KindHTTP {
		t.Errorf("want KindHTTP, got %v", got.Kind)
	}

	if !got.Loopback {
		t.Error("want Loopback=true for 127.0.0.1")
	}
}

func TestResolveTransport_HTTPMissingAddr(t *testing.T) {
	_, err := ResolveTransport(configs.MCPServerConfig{Transport: "http"})
	if err == nil {
		t.Fatal("expected error for empty addr")
	}
}

func TestResolveTransport_HTTPNonLoopbackRequiresToken(t *testing.T) {
	// Defence-in-depth: ResolveTransport must reject non-loopback addrs
	// without an auth token even if ValidateMCPServer was not called.
	_, err := ResolveTransport(configs.MCPServerConfig{
		Transport: "http",
		Addr:      "0.0.0.0:7801",
	})
	if err == nil {
		t.Fatal("expected error for non-loopback addr without auth_token")
	}

	// Bare :port binds every interface too; must also require a token.
	_, err = ResolveTransport(configs.MCPServerConfig{
		Transport: "http",
		Addr:      ":7801",
	})
	if err == nil {
		t.Fatal("expected error for bare :port without auth_token")
	}

	// With a token, the same addr should resolve cleanly.
	got, err := ResolveTransport(configs.MCPServerConfig{
		Transport: "http",
		Addr:      "0.0.0.0:7801",
		AuthToken: "secret",
	})
	if err != nil {
		t.Fatalf("unexpected error with auth_token: %v", err)
	}

	if got.Loopback {
		t.Error("0.0.0.0 should not be classified as loopback")
	}
}

func TestResolveTransport_UnknownTransport(t *testing.T) {
	_, err := ResolveTransport(configs.MCPServerConfig{Transport: "grpc"})
	if err == nil {
		t.Fatal("expected error for unknown transport")
	}
}

func TestIsLoopbackAddr(t *testing.T) {
	cases := map[string]bool{
		"127.0.0.1:8080": true,
		"[::1]:8080":     true,
		"localhost:9":    true,
		"0.0.0.0:8080":   false,
		"10.0.0.1:80":    false,
		"example.com:80": false,
		// Empty / bare-port addresses bind every interface on net.Listen,
		// so they must NOT be classified as loopback.
		"":        false,
		":8080":   false,
		"0.0.0.0": false,
	}

	for addr, want := range cases {
		if got := IsLoopbackAddr(addr); got != want {
			t.Errorf("IsLoopbackAddr(%q) = %v, want %v", addr, got, want)
		}
	}
}
