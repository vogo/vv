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
	"context"
	"sync"
	"sync/atomic"

	"github.com/vogo/vage/hook"
	"github.com/vogo/vage/schema"
)

// sessionEventCounter tracks AgentEnd events per sessionID so the
// SessionTreeSource predicate can lazily activate once a session has
// crossed a configured threshold. The counter is in-process only —
// process restarts re-zero every session's count, which matches the
// design intent: "auto-enable" is a UX hint about conversation length,
// not an audit fact, so authoritative state belongs to the SessionStore.
//
// Concurrency: the keys map is sync.Map for the typical "many readers,
// occasional writer" shape; per-key counters are *atomic.Int64.
//
// The counter implements hook.Hook (sync) rather than hook.AsyncHook
// because the work per event is a single Add — cheaper than the
// channel-based async wiring for this case.
type sessionEventCounter struct {
	counts sync.Map // map[string]*atomic.Int64
}

// newSessionEventCounter builds an empty counter. Call .Hook() to register
// it with a hook.Manager.
func newSessionEventCounter() *sessionEventCounter {
	return &sessionEventCounter{}
}

// Count returns the AgentEnd event count for sessionID. Unknown sessions
// return 0.
func (c *sessionEventCounter) Count(sessionID string) int64 {
	v, ok := c.counts.Load(sessionID)
	if !ok {
		return 0
	}
	return v.(*atomic.Int64).Load()
}

// inc atomically increments the per-session counter, allocating it on
// first sight. Used by the OnEvent hook handler.
func (c *sessionEventCounter) inc(sessionID string) {
	v, ok := c.counts.Load(sessionID)
	if !ok {
		fresh := new(atomic.Int64)
		v, _ = c.counts.LoadOrStore(sessionID, fresh)
	}
	v.(*atomic.Int64).Add(1)
}

// reset is a test-only helper that drops every per-session counter.
//
//nolint:unused // exposed for tests; kept on the type for symmetry.
func (c *sessionEventCounter) reset() {
	c.counts = sync.Map{}
}

// counterHook is the hook.Hook adapter that increments the counter. It
// only fires on EventAgentEnd so internal events (tool calls, LLM calls)
// do not inflate the count.
type counterHook struct {
	c *sessionEventCounter
}

// OnEvent implements hook.Hook.
func (h counterHook) OnEvent(_ context.Context, e schema.Event) error {
	if e.SessionID != "" {
		h.c.inc(e.SessionID)
	}
	return nil
}

// Filter scopes the hook to AgentEnd events; other event types short-
// circuit at the manager level and never reach the OnEvent path.
func (counterHook) Filter() []string {
	return []string{schema.EventAgentEnd}
}

// Hook returns the hook.Hook adapter suitable for hook.Manager.Register.
func (c *sessionEventCounter) Hook() hook.Hook {
	return counterHook{c: c}
}

// Predicate returns a func compatible with vctx.SessionTreeSource.Predicate
// that returns true once the session's event count has met or exceeded
// threshold. threshold <= 0 returns a constant-true predicate so callers
// can keep the gating off transparently.
func (c *sessionEventCounter) Predicate(threshold int) func(context.Context, string) bool {
	if threshold <= 0 {
		return func(context.Context, string) bool { return true }
	}
	target := int64(threshold)
	return func(_ context.Context, sessionID string) bool {
		return c.Count(sessionID) >= target
	}
}
