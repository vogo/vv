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
	"log/slog"

	"github.com/vogo/vage/session/tree/vectorhook"
	"github.com/vogo/vv/configs"
)

// maybeWrapTreeWithVectorIndex installs the vectorhook decorator on
// opts.TreeStore when (1) session_tree.vector_index.enabled is set,
// and (2) the prerequisites — TreeStore + VectorStore + VectorEmbedder
// — are all already populated on opts.
//
// When the flag is set but a prerequisite is missing, the wiring
// degrades to a logged Warn rather than a hard error: the user has
// asked for an enhancement that is not currently possible (e.g.
// vector subsystem is off), so we surface the misconfiguration but
// do not abort startup.
//
// Returns a non-nil error only when the wrap itself fails (the
// decorator constructor rejects nil inner — caught earlier by the
// prerequisite check, so reaching that branch implies a bug).
func maybeWrapTreeWithVectorIndex(cfg *configs.Config, opts *Options) error {
	if cfg == nil || !cfg.SessionTree.VectorIndex.IsEnabled() {
		return nil
	}
	if opts == nil || opts.TreeStore == nil || opts.VectorStore == nil || opts.VectorEmbedder == nil {
		slog.Warn("vv: session_tree.vector_index.enabled but prerequisites not met",
			"tree_store", opts != nil && opts.TreeStore != nil,
			"vector_store", opts != nil && opts.VectorStore != nil,
			"vector_embedder", opts != nil && opts.VectorEmbedder != nil)
		return nil
	}

	wrapped, err := vectorhook.WrapStore(opts.TreeStore, opts.VectorStore, opts.VectorEmbedder)
	if err != nil {
		return fmt.Errorf("session tree vector index: %w", err)
	}
	opts.TreeStore = wrapped
	slog.Info("vv: session tree vector index enabled")
	return nil
}
