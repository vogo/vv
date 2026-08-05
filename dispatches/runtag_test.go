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

package dispatches

import (
	"context"
	"testing"

	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/sessionlogs"
)

// capturingAgent records what its caller handed it.
type capturingAgent struct {
	stubAgent

	gotSessionID string
	gotRun       sessionlogs.Run
	gotRunOK     bool
}

func (c *capturingAgent) Run(ctx context.Context, req *schema.RunRequest) (*schema.RunResponse, error) {
	c.gotSessionID = req.SessionID
	c.gotRun, c.gotRunOK = sessionlogs.RunFromContext(ctx)

	return c.stubAgent.Run(ctx, req)
}

// TestDelegateHandler_PropagatesSessionAndRun locks in the fix for
// delegated work leaving nothing on disk: without a session id every
// checkpoint save fails and every event is dropped.
func TestDelegateHandler_PropagatesSessionAndRun(t *testing.T) {
	ag := &capturingAgent{stubAgent: stubAgent{id: "coder"}}
	handler := newDelegateHandler(ag)

	ctx := schema.WithSessionID(context.Background(), "sess-42")

	res, err := handler(ctx, "delegate_to_coder", `{"task":"rename the file"}`)
	if err != nil {
		t.Fatalf("handler: %v", err)
	}

	if res.IsError {
		t.Fatalf("handler returned an error result: %+v", res)
	}

	if ag.gotSessionID != "sess-42" {
		t.Errorf("sub-agent ran with session id %q, want %q", ag.gotSessionID, "sess-42")
	}

	if !ag.gotRunOK {
		t.Fatal("sub-agent ran without a dispatch tag; its checkpoints would land in the session's own transcript")
	}

	if ag.gotRun.Key == "" {
		t.Error("dispatch tag must carry a key")
	}

	if ag.gotRun.Task != "rename the file" {
		t.Errorf("dispatch task = %q, want the delegated task", ag.gotRun.Task)
	}
}

func TestDelegateHandler_WithoutSessionStillRuns(t *testing.T) {
	ag := &capturingAgent{stubAgent: stubAgent{id: "coder"}}
	handler := newDelegateHandler(ag)

	res, err := handler(context.Background(), "delegate_to_coder", `{"task":"do it"}`)
	if err != nil || res.IsError {
		t.Fatalf("delegation must not depend on a session being configured: err=%v res=%+v", err, res)
	}

	if ag.gotSessionID != "" {
		t.Errorf("session id = %q, want empty", ag.gotSessionID)
	}
}

func TestTagRun_MintsAFreshDispatchPerInvocation(t *testing.T) {
	inner := &capturingAgent{stubAgent: stubAgent{id: "coder"}}
	tagged := tagRun(inner, "step description")

	ctx := context.Background()

	if _, err := tagged.Run(ctx, &schema.RunRequest{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	first := inner.gotRun.Key

	if _, err := tagged.Run(ctx, &schema.RunRequest{}); err != nil {
		t.Fatalf("Run: %v", err)
	}

	if inner.gotRun.Key == first {
		t.Error("a retried step must be a new dispatch, not an append to the previous transcript")
	}

	if inner.gotRun.Task != "step description" {
		t.Errorf("task = %q, want the step description", inner.gotRun.Task)
	}
}

func TestTagRun_PreservesIdentity(t *testing.T) {
	inner := &stubAgent{id: "coder"}

	tagged := tagRun(inner, "x")
	if tagged.ID() != "coder" || tagged.Name() != "coder" || tagged.Protocol() != schema.ProtocolOpenAIChat {
		t.Errorf("wrapper must be transparent: id=%q name=%q proto=%q", tagged.ID(), tagged.Name(), tagged.Protocol())
	}

	if tagRun(nil, "x") != nil {
		t.Error("tagRun(nil) must stay nil so callers can wrap unconditionally")
	}
}
