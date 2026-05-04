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
	"errors"
	"fmt"
	"log/slog"

	"github.com/vogo/vage/vector"
	"github.com/vogo/vage/vector/archivehook"
	"github.com/vogo/vage/vector/openai"
	"github.com/vogo/vage/vector/qdrant"
	"github.com/vogo/vv/configs"
)

// VectorSubsystem groups the runtime handles produced by buildVectorSubsystem.
// Init wires these onto Options so downstream factories pick them up the
// same way they pick up Workspace / TreeStore.
type VectorSubsystem struct {
	Store    vector.VectorStore
	Embedder vector.Embedder
	// Hook is non-nil when auto-write is enabled. Init registers it on
	// the process hook.Manager; the lifecycle is owned by the manager,
	// not by VectorSubsystem.
	Hook *archivehook.Hook
}

// buildVectorSubsystem constructs the VectorStore + Embedder + (optional)
// archive hook from cfg.Vector. Returns (nil, nil) when the subsystem
// is disabled — Init treats that as a no-op path.
//
// API key absence is a soft-fail (log warn + return disabled) so a user
// running `vv help` with `vector.enabled: true` but no key in scope does
// not hard-error at boot.
func buildVectorSubsystem(cfg configs.VectorConfig) (*VectorSubsystem, error) {
	if !cfg.IsEnabled() {
		return nil, nil
	}

	emb, err := buildVectorEmbedder(cfg)
	if err != nil {
		return nil, err
	}
	if emb == nil {
		// Soft-fail: the embedder builder logged a warn and chose to
		// disable rather than fail the whole vv startup. Treat the
		// subsystem as off so wiring downstream is a no-op.
		return nil, nil
	}

	store, err := buildVectorStore(cfg, emb)
	if err != nil {
		return nil, err
	}

	subsys := &VectorSubsystem{Store: store, Embedder: emb}

	if cfg.IsAutoWriteEnabled() {
		hook, err := archivehook.New(store, emb)
		if err != nil {
			return nil, fmt.Errorf("vector auto-write hook: %w", err)
		}
		subsys.Hook = hook
	}

	slog.Info("vv: vector subsystem enabled",
		"backend", cfg.EffectiveBackend(),
		"embedder", cfg.EffectiveEmbedder(),
		"collection", cfg.EffectiveCollection(),
		"auto_write", cfg.IsAutoWriteEnabled(),
	)
	return subsys, nil
}

// buildVectorEmbedder constructs the chosen Embedder. Returns
// (nil, nil) for the soft-fail "missing API key" case so the caller
// can treat the whole subsystem as disabled.
func buildVectorEmbedder(cfg configs.VectorConfig) (vector.Embedder, error) {
	switch cfg.EffectiveEmbedder() {
	case configs.VectorEmbedderHash:
		return vector.NewHashEmbedder(0), nil
	case configs.VectorEmbedderOpenAI:
		if cfg.OpenAI.APIKey == "" && cfg.OpenAI.BaseURL == "" {
			slog.Warn("vv: vector subsystem disabled — OpenAI API key missing",
				"hint", "set VV_VECTOR_OPENAI_API_KEY or OPENAI_API_KEY, or pick embedder=hash for offline use")
			return nil, nil
		}
		opts := []openai.Option{
			openai.WithModel(cfg.EffectiveOpenAIModel()),
		}
		if cfg.OpenAI.APIKey != "" {
			opts = append(opts, openai.WithAPIKey(cfg.OpenAI.APIKey))
		}
		if cfg.OpenAI.BaseURL != "" {
			opts = append(opts, openai.WithBaseURL(cfg.OpenAI.BaseURL))
		}
		if cfg.OpenAI.Dimensions > 0 {
			opts = append(opts, openai.WithDimensions(cfg.OpenAI.Dimensions))
		}
		emb, err := openai.New(opts...)
		if err != nil {
			return nil, fmt.Errorf("openai embedder: %w", err)
		}
		return emb, nil
	default:
		return nil, fmt.Errorf("unknown embedder %q", cfg.Embedder)
	}
}

// buildVectorStore constructs the chosen VectorStore.
func buildVectorStore(cfg configs.VectorConfig, emb vector.Embedder) (vector.VectorStore, error) {
	switch cfg.EffectiveBackend() {
	case configs.VectorBackendMemory:
		return buildMemoryStore(cfg, emb), nil
	case configs.VectorBackendQdrant:
		return buildQdrantStore(cfg, emb)
	default:
		return nil, fmt.Errorf("unknown backend %q", cfg.Backend)
	}
}

// buildMemoryStore returns an in-process MapVectorStore. The locked
// dimension comes from the embedder when it implements
// LimitedEmbedder + has been configured with explicit dimensions; this
// keeps the dimension self-consistent so a Search before any Add does
// not panic.
func buildMemoryStore(cfg configs.VectorConfig, _ vector.Embedder) *vector.MapVectorStore {
	opts := []vector.MapStoreOption{}
	if cfg.OpenAI.Dimensions > 0 {
		opts = append(opts, vector.WithLockedDimension(cfg.OpenAI.Dimensions))
	}
	if cfg.TopK > 0 {
		opts = append(opts, vector.WithDefaultTopK(cfg.TopK))
	}
	return vector.NewMapVectorStore(opts...)
}

// buildQdrantStore returns a qdrant-backed Store. Returns an error
// only on configuration problems — connectivity failures surface
// lazily on first Add / Search and are handled by the consumers
// (VectorRecallSource fail-open, archivehook fail-open, HTTP 502).
func buildQdrantStore(cfg configs.VectorConfig, _ vector.Embedder) (vector.VectorStore, error) {
	url := cfg.EffectiveQdrantURL()
	if url == "" {
		return nil, errors.New("vector.qdrant.url is empty")
	}
	opts := []qdrant.Option{}
	if cfg.Qdrant.APIKey != "" {
		opts = append(opts, qdrant.WithAPIKey(cfg.Qdrant.APIKey))
	}
	if cfg.TopK > 0 {
		opts = append(opts, qdrant.WithDefaultTopK(cfg.TopK))
	}
	if cfg.OpenAI.Dimensions > 0 {
		// Eagerly create the collection at the embedder's dimension so
		// the first Add does not race the create-collection RPC.
		opts = append(opts, qdrant.WithLockedDimension(cfg.OpenAI.Dimensions))
	}
	store, err := qdrant.New(url, cfg.EffectiveCollection(), opts...)
	if err != nil {
		return nil, fmt.Errorf("qdrant store: %w", err)
	}
	return store, nil
}
