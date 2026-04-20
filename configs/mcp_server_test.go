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

import "testing"

func TestValidateMCPServer_DefaultsToStdio(t *testing.T) {
	c := MCPServerConfig{}
	if err := ValidateMCPServer(&c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if c.Transport != "stdio" {
		t.Errorf("want stdio, got %q", c.Transport)
	}
}

func TestValidateMCPServer_HTTPDefaultAddr(t *testing.T) {
	c := MCPServerConfig{Transport: "HTTP"}
	if err := ValidateMCPServer(&c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if c.Transport != "http" {
		t.Errorf("transport should be lower-cased, got %q", c.Transport)
	}

	if c.Addr != "127.0.0.1:7801" {
		t.Errorf("want default loopback addr, got %q", c.Addr)
	}
}

func TestValidateMCPServer_NonLoopbackRequiresToken(t *testing.T) {
	c := MCPServerConfig{Transport: "http", Addr: "0.0.0.0:7801"}
	if err := ValidateMCPServer(&c); err == nil {
		t.Fatal("expected error when non-loopback bind has no auth_token")
	}

	c.AuthToken = "secret"
	if err := ValidateMCPServer(&c); err != nil {
		t.Errorf("unexpected error with auth_token set: %v", err)
	}
}

func TestValidateMCPServer_BarePortRequiresToken(t *testing.T) {
	// ":7801" binds every interface; must not be classified as loopback.
	c := MCPServerConfig{Transport: "http", Addr: ":7801"}
	if err := ValidateMCPServer(&c); err == nil {
		t.Fatal("expected error when bare :port bind has no auth_token")
	}
}

func TestValidateMCPServer_UnknownTransportRejected(t *testing.T) {
	c := MCPServerConfig{Transport: "grpc"}
	if err := ValidateMCPServer(&c); err == nil {
		t.Fatal("expected error for unknown transport")
	}
}

func TestValidateMCPServer_NegativeTimeoutClamped(t *testing.T) {
	c := MCPServerConfig{Transport: "stdio", SessionTimeout: -5}
	if err := ValidateMCPServer(&c); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if c.SessionTimeout != 0 {
		t.Errorf("want SessionTimeout clamped to 0, got %d", c.SessionTimeout)
	}
}
