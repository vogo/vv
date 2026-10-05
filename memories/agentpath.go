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

package memories

import (
	"context"

	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/memory"
	memtool "github.com/vogo/vage/tool/memory"
)

// BindAgentPath wraps mem so every call stamps memories.WithSessionID from
// schema.SessionIDFromContext when the agent-path identity is not already
// present. Persistent-memory tools live in vage and only see the schema
// session key; the store ACL (MEM-R3) reads this package's key.
//
// nil mem is returned unchanged so callers can pass BindAgentPath through
// a fail-open registration path.
func BindAgentPath(mem memory.Memory) memory.Memory {
	if mem == nil {
		return nil
	}
	return agentPathMemory{inner: mem}
}

type agentPathMemory struct {
	inner memory.Memory
}

func bindAgentPath(ctx context.Context) context.Context {
	if SessionIDFrom(ctx) != "" {
		return ctx
	}
	if sid := schema.SessionIDFromContext(ctx); sid != "" {
		return WithSessionID(ctx, sid)
	}
	return ctx
}

func (m agentPathMemory) Get(ctx context.Context, key string) (any, error) {
	return m.inner.Get(bindAgentPath(ctx), key)
}

func (m agentPathMemory) Set(ctx context.Context, key string, value any, ttl int64) error {
	return m.inner.Set(bindAgentPath(ctx), key, value, ttl)
}

func (m agentPathMemory) Delete(ctx context.Context, key string) error {
	return m.inner.Delete(bindAgentPath(ctx), key)
}

func (m agentPathMemory) List(ctx context.Context, prefix string) ([]memory.Entry, error) {
	return m.inner.List(bindAgentPath(ctx), prefix)
}

func (m agentPathMemory) Clear(ctx context.Context) error {
	return m.inner.Clear(bindAgentPath(ctx))
}

func (m agentPathMemory) BatchGet(ctx context.Context, keys []string) (map[string]any, error) {
	return m.inner.BatchGet(bindAgentPath(ctx), keys)
}

func (m agentPathMemory) BatchSet(ctx context.Context, entries map[string]any, ttl int64) error {
	return m.inner.BatchSet(bindAgentPath(ctx), entries, ttl)
}

// AgentToolStore adapts a vage memory.Memory into the L2 tool Store seam
// (vage/tool/memory must not import vage/memory). The adapter also binds
// schema session identity onto the agent-path context.
func AgentToolStore(mem memory.Memory) memtool.Store {
	if mem == nil {
		return nil
	}
	return agentToolStore{inner: BindAgentPath(mem)}
}

type agentToolStore struct {
	inner memory.Memory
}

var _ memtool.Store = agentToolStore{}

func (s agentToolStore) Get(ctx context.Context, key string) (any, error) {
	return s.inner.Get(ctx, key)
}

func (s agentToolStore) Set(ctx context.Context, key string, value any, ttl int64) error {
	return s.inner.Set(ctx, key, value, ttl)
}

func (s agentToolStore) List(ctx context.Context, prefix string) ([]memtool.Entry, error) {
	raw, err := s.inner.List(ctx, prefix)
	if err != nil {
		return nil, err
	}
	out := make([]memtool.Entry, len(raw))
	for i, e := range raw {
		out[i] = memtool.Entry{Key: e.Key, Value: e.Value}
	}
	return out, nil
}
