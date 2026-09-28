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

package setup

import (
	"slices"
	"testing"

	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/session"
	"github.com/vogo/vv/configs"
)

// filterOf materialises the effective filter list by applying the options
// to a real SessionHook — asserting on behaviour rather than on the
// option closures.
func filterOf(t *testing.T, mode string) []string {
	t.Helper()

	cfg := &configs.Config{}
	cfg.Session.EventPersist = mode

	h := session.NewSessionHook(nil, sessionHookOptions(cfg)...)

	return h.Filter()
}

func TestSessionHookOptions_ControlExcludesDuplicatedPayloads(t *testing.T) {
	filter := filterOf(t, configs.EventPersistControl)

	if len(filter) == 0 {
		t.Fatal("control mode must install a non-empty whitelist; an empty filter means subscribe-to-all")
	}

	// These three carry payloads whose owner file is elsewhere; keeping
	// them would write the same bytes twice.
	for _, banned := range []string{
		schema.EventTextDelta,
		schema.EventToolResult,
		schema.EventContextBuilt,
	} {
		if slices.Contains(filter, banned) {
			t.Errorf("control whitelist must not contain %q (payload is owned by another file)", banned)
		}
	}

	// A representative sample of what must survive.
	for _, want := range []string{
		schema.EventAgentStart,
		schema.EventAgentEnd,
		schema.EventToolCallStart,
		schema.EventCheckpointWritten,
		schema.EventSubAgentStart,
	} {
		if !slices.Contains(filter, want) {
			t.Errorf("control whitelist is missing %q", want)
		}
	}
}

func TestSessionHookOptions_AllSubscribesToEverything(t *testing.T) {
	if filter := filterOf(t, configs.EventPersistAll); len(filter) != 0 {
		t.Errorf("all mode must leave the filter empty (subscribe to everything), got %v", filter)
	}
}

func TestSessionHookOptions_NoneKeepsAutoCreateAlive(t *testing.T) {
	filter := filterOf(t, configs.EventPersistNone)

	if len(filter) != 1 || filter[0] != schema.EventAgentStart {
		t.Fatalf("none mode must keep exactly agent_start so SessionHook auto-create still materialises meta.json, got %v", filter)
	}
}

func TestSessionHookOptions_UnsetDefaultsToControl(t *testing.T) {
	unset := filterOf(t, "")
	control := filterOf(t, configs.EventPersistControl)

	if !slices.Equal(unset, control) {
		t.Errorf("empty event_persist must behave as control\n got %v\nwant %v", unset, control)
	}
}

func TestControlPlaneEvents_ReturnsCopy(t *testing.T) {
	got := ControlPlaneEvents()
	if len(got) == 0 {
		t.Fatal("ControlPlaneEvents returned an empty list")
	}

	got[0] = "mutated"

	if ControlPlaneEvents()[0] == "mutated" {
		t.Error("ControlPlaneEvents leaked the package-level slice")
	}
}
