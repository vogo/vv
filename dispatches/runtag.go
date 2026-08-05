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

	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/sessionlogs"
)

// tagRun wraps runner so every Run/RunStream it performs is attributed to
// one sub-agent dispatch.
//
// Without the tag a DAG step's checkpoints are only kept out of the main
// transcript by the "agent id is not primary" fallback, which collapses
// every dispatch of the same specialist into a single file. Tagging is
// what gives each plan step its own readable transcript.
func tagRun(runner agent.Agent, task string) agent.Agent {
	if runner == nil {
		return nil
	}

	return &runTaggedAgent{inner: runner, task: task}
}

type runTaggedAgent struct {
	inner agent.Agent
	task  string
}

// runCtx mints a fresh dispatch key per invocation: the same wrapped
// node can be re-run (retry, replan) and each attempt is its own
// conversation.
func (r *runTaggedAgent) runCtx(ctx context.Context) context.Context {
	return sessionlogs.WithRun(ctx, sessionlogs.Run{
		Key:  sessionlogs.NewRunKey(),
		Task: r.task,
	})
}

func (r *runTaggedAgent) Run(ctx context.Context, req *schema.RunRequest) (*schema.RunResponse, error) {
	return r.inner.Run(r.runCtx(ctx), req)
}

func (r *runTaggedAgent) RunStream(ctx context.Context, req *schema.RunRequest) (*schema.RunStream, error) {
	tagged := r.runCtx(ctx)

	sa, ok := r.inner.(agent.StreamAgent)
	if !ok {
		return agent.RunToStream(tagged, r.inner, req), nil
	}

	return sa.RunStream(tagged, req)
}

func (r *runTaggedAgent) ID() string                { return r.inner.ID() }
func (r *runTaggedAgent) Name() string              { return r.inner.Name() }
func (r *runTaggedAgent) Description() string       { return r.inner.Description() }
func (r *runTaggedAgent) Protocol() schema.Protocol { return r.inner.Protocol() }

// Compile-time checks.
var (
	_ agent.Agent       = (*runTaggedAgent)(nil)
	_ agent.StreamAgent = (*runTaggedAgent)(nil)
)
