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
	"errors"
)

// ErrSessionForbidden is returned when a session-scoped entry is accessed
// from a different session, or when a user-path caller attempts to write a
// session-private namespace.
var ErrSessionForbidden = errors.New("memories: session access forbidden")

type sessionCtxKey struct{}

type userPathCtxKey struct{}

// WithSessionID attaches an owner session_id to the context. Agent-side
// callers (tool handlers, skill writers) wrap the request context with this
// so the store can bind writes to the owning session and verify reads.
func WithSessionID(ctx context.Context, sessionID string) context.Context {
	if sessionID == "" {
		return ctx
	}
	return context.WithValue(ctx, sessionCtxKey{}, sessionID)
}

// SessionIDFrom extracts the owner session_id from the context. Empty
// string means "no agent identity".
func SessionIDFrom(ctx context.Context) string {
	v, _ := ctx.Value(sessionCtxKey{}).(string)
	return v
}

// WithUserPath marks the context as originating from a direct user action
// (CLI /memory command, HTTP memory CRUD). User-path operations are
// restricted to shared namespaces but are not bound to any session_id.
func WithUserPath(ctx context.Context) context.Context {
	return context.WithValue(ctx, userPathCtxKey{}, true)
}

// IsUserPath reports whether the context is a user-path operation.
func IsUserPath(ctx context.Context) bool {
	v, _ := ctx.Value(userPathCtxKey{}).(bool)
	return v
}
