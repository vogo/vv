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
	"fmt"
	"path/filepath"

	"github.com/vogo/vv/agents"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/sessionlogs"
)

// sessionRootDir returns the resolved session root directory for the
// configured project. The path is shared by FileSessionStore,
// FileWorkspace, FileTreeStore and FileIterationStore so that
// SessionStore.Delete (which os.RemoveAll's <root>/<id>) wipes every
// per-session subsystem in one call without explicit coordination.
//
// Callers must only invoke this when cfg.Session.IsEnabled() — when the
// session subsystem is off the path has no consumers and the function
// would silently mint a directory no one writes to.
func sessionRootDir(cfg *configs.Config) string {
	return filepath.Join(cfg.Session.EffectiveDir(), SessionProjectName(cfg.Tools.BashWorkingDir))
}

// buildIterationStore constructs a per-iteration checkpoint store rooted
// under the same directory as the session subsystem so a session id maps
// 1:1 to <root>/<id>/. Returns (nil, nil) when the session subsystem is
// disabled — checkpoint persistence is meaningless without a stable
// session identity, and FactoryOptions.IterationStore == nil disables
// the option on every TaskAgent factory.
//
// The backend is sessionlogs, which stores each message body once per
// transcript instead of re-serialising the whole conversation on every
// iteration, and files sub-agent dispatches separately from the
// session's own resume timeline. Sessions written by the previous
// full-snapshot store still load: sessionlogs falls back to the legacy
// checkpoints/ directory when no transcript exists.
func buildIterationStore(cfg *configs.Config) (*sessionlogs.Store, error) {
	if cfg == nil || !cfg.Session.IsEnabled() {
		return nil, nil
	}

	root := sessionRootDir(cfg)

	store, err := sessionlogs.New(
		root,
		sessionlogs.WithPrimaryAgentID(agents.PrimaryAgentID),
		sessionlogs.WithToolResultSpillBytes(cfg.Session.EffectiveToolResultMaxInlineBytes()),
	)
	if err != nil {
		return nil, fmt.Errorf("iteration store: %w", err)
	}

	return store, nil
}
