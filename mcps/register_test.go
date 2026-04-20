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
	"testing"

	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
)

type fakeAgent struct {
	agent.Base
}

func (*fakeAgent) Run(_ context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	return &schema.RunResponse{}, nil
}

func newFakeAgent(id string) agent.Agent {
	return &fakeAgent{Base: agent.NewBase(agent.Config{ID: id, Name: id, Description: "fake " + id})}
}

type fakeLookup struct {
	byID map[string]agent.Agent
	all  []agent.Agent
}

func (f fakeLookup) Agents() []agent.Agent       { return f.all }
func (f fakeLookup) Agent(id string) agent.Agent { return f.byID[id] }

func newFakeLookup(ids ...string) fakeLookup {
	byID := make(map[string]agent.Agent, len(ids))
	all := make([]agent.Agent, 0, len(ids))
	for _, id := range ids {
		a := newFakeAgent(id)
		byID[id] = a
		all = append(all, a)
	}
	return fakeLookup{byID: byID, all: all}
}

func TestSelectAgents_AllWhenWhitelistEmpty(t *testing.T) {
	lookup := newFakeLookup("coder", "researcher", "reviewer")

	got, err := selectAgents(lookup, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got) != 3 {
		t.Fatalf("want 3 agents, got %d", len(got))
	}
}

func TestSelectAgents_Whitelist(t *testing.T) {
	lookup := newFakeLookup("coder", "researcher", "reviewer")

	got, err := selectAgents(lookup, []string{"coder", "reviewer"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(got) != 2 || got[0].ID() != "coder" || got[1].ID() != "reviewer" {
		t.Errorf("unexpected selection: %+v", agentIDs(got))
	}
}

func TestSelectAgents_UnknownIDReturnsError(t *testing.T) {
	lookup := newFakeLookup("coder")

	_, err := selectAgents(lookup, []string{"coder", "nope"})
	if err == nil {
		t.Fatal("expected error for unknown agent id")
	}
}

func TestToolNames_WithAndWithoutDispatcher(t *testing.T) {
	agents := []agent.Agent{newFakeAgent("coder"), newFakeAgent("researcher")}

	got := toolNames(agents, nil)
	want := []string{"coder", "researcher"}

	if !equalStrings(got, want) {
		t.Errorf("without dispatcher: got %v want %v", got, want)
	}

	got = toolNames(agents, newFakeAgent("dispatcher"))
	want = []string{"coder", "researcher", "dispatcher"}

	if !equalStrings(got, want) {
		t.Errorf("with dispatcher: got %v want %v", got, want)
	}
}

func agentIDs(as []agent.Agent) []string {
	out := make([]string, len(as))
	for i, a := range as {
		out[i] = a.ID()
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
