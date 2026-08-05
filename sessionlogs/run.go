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

package sessionlogs

import (
	"context"
	"strconv"
	"sync/atomic"
)

// Run identifies one sub-agent dispatch inside a session.
//
// A dispatch — not an agent — is the unit that gets its own transcript
// file: the same specialist is typically invoked several times per
// session with unrelated subgoals, and folding those into one file would
// make the boundaries unrecoverable. The dispatcher mints a Run and
// attaches it to the context it hands the sub-agent; the store reads it
// back in Save.
type Run struct {
	// Key is unique per dispatch within the process. Its only job is to
	// let successive Save calls of the same dispatch resolve to the same
	// file, so any collision-free string works.
	Key string

	// Task is the human-readable subgoal recorded on the pointer line in
	// the parent transcript. Optional.
	Task string
}

// runKeyCounter makes dispatch keys unique within the process; the
// random suffix keeps them unique across processes sharing a session.
var runKeyCounter atomic.Uint64

// NewRunKey mints a dispatch key for WithRun. Callers that already have
// a natural identifier for the dispatch (a DAG step id, say) should use
// that instead — the key only has to be collision-free.
func NewRunKey() string {
	return strconv.FormatUint(runKeyCounter.Add(1), 10) + "-" + newID()
}

type runContextKey struct{}

// WithRun attaches run to ctx so checkpoints saved under it land in a
// per-dispatch sub-agent transcript instead of the session's main log.
//
// Passing an empty Key returns ctx unchanged: a run without identity
// cannot route anything, and silently installing it would only make the
// fallback path (route by agent id) harder to reason about.
func WithRun(ctx context.Context, run Run) context.Context {
	if run.Key == "" {
		return ctx
	}

	return context.WithValue(ctx, runContextKey{}, run)
}

// RunFromContext returns the Run attached by WithRun, if any.
func RunFromContext(ctx context.Context) (Run, bool) {
	if ctx == nil {
		return Run{}, false
	}

	run, ok := ctx.Value(runContextKey{}).(Run)

	return run, ok
}
