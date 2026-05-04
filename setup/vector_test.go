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
	"testing"

	"github.com/vogo/vage/vector"
	"github.com/vogo/vv/configs"
)

func TestBuildVectorSubsystem_DisabledIsNoOp(t *testing.T) {
	subsys, err := buildVectorSubsystem(configs.VectorConfig{})
	if err != nil {
		t.Fatalf("disabled config produced error: %v", err)
	}
	if subsys != nil {
		t.Errorf("expected nil subsystem when disabled, got %+v", subsys)
	}
}

func TestBuildVectorSubsystem_MemoryHash(t *testing.T) {
	cfg := configs.VectorConfig{
		Enabled:  new(true),
		Backend:  configs.VectorBackendMemory,
		Embedder: configs.VectorEmbedderHash,
	}
	subsys, err := buildVectorSubsystem(cfg)
	if err != nil {
		t.Fatalf("buildVectorSubsystem: %v", err)
	}
	if subsys == nil {
		t.Fatal("expected subsystem, got nil")
	}
	if subsys.Store == nil || subsys.Embedder == nil {
		t.Errorf("subsystem fields nil: %+v", subsys)
	}
	if subsys.Hook != nil {
		t.Error("auto-write off, hook should be nil")
	}
	// Smoke: embedder + store wire end-to-end.
	v, err := subsys.Embedder.Embed(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Embed: %v", err)
	}
	if err := subsys.Store.Add(context.Background(), vector.Document{ID: "x", Text: "hello", Embedding: v}); err != nil {
		t.Fatalf("Add: %v", err)
	}
}

func TestBuildVectorSubsystem_MemoryHash_AutoWriteOn(t *testing.T) {
	cfg := configs.VectorConfig{
		Enabled:   new(true),
		Backend:   configs.VectorBackendMemory,
		Embedder:  configs.VectorEmbedderHash,
		AutoWrite: new(true),
	}
	subsys, err := buildVectorSubsystem(cfg)
	if err != nil {
		t.Fatalf("buildVectorSubsystem: %v", err)
	}
	if subsys == nil || subsys.Hook == nil {
		t.Fatalf("expected hook to be constructed: subsys=%+v", subsys)
	}
}

func TestBuildVectorSubsystem_OpenAIWithoutKeySoftFails(t *testing.T) {
	// Empty API key + empty BaseURL means we cannot reach an OpenAI
	// server. Build must NOT error — it logs and disables the subsystem.
	cfg := configs.VectorConfig{
		Enabled:  new(true),
		Backend:  configs.VectorBackendQdrant,
		Embedder: configs.VectorEmbedderOpenAI,
	}
	subsys, err := buildVectorSubsystem(cfg)
	if err != nil {
		t.Fatalf("expected soft-fail (nil, nil), got err=%v", err)
	}
	if subsys != nil {
		t.Errorf("expected disabled subsystem, got %+v", subsys)
	}
}

func TestBuildVectorSubsystem_OpenAIWithBaseURLOnly(t *testing.T) {
	// httptest-style usage: base URL set, key empty. The OpenAI builder
	// allows this for tests / OpenAI-compatible providers behind their
	// own auth.
	cfg := configs.VectorConfig{
		Enabled:  new(true),
		Backend:  configs.VectorBackendMemory,
		Embedder: configs.VectorEmbedderOpenAI,
		OpenAI: configs.OpenAIEmbedderConfig{
			BaseURL: "http://localhost:65535",
		},
	}
	subsys, err := buildVectorSubsystem(cfg)
	if err != nil {
		t.Fatalf("buildVectorSubsystem: %v", err)
	}
	if subsys == nil || subsys.Embedder == nil {
		t.Fatalf("expected subsystem with embedder, got %+v", subsys)
	}
}

func TestBuildVectorSubsystem_QdrantConstructs(t *testing.T) {
	// We do not connect — Store construction is local; connectivity
	// surfaces lazily on first Add / Search.
	cfg := configs.VectorConfig{
		Enabled:  new(true),
		Backend:  configs.VectorBackendQdrant,
		Embedder: configs.VectorEmbedderHash,
		Qdrant:   configs.QdrantConfig{URL: "http://localhost:65535"},
	}
	subsys, err := buildVectorSubsystem(cfg)
	if err != nil {
		t.Fatalf("buildVectorSubsystem: %v", err)
	}
	if subsys == nil || subsys.Store == nil {
		t.Fatalf("expected qdrant store, got %+v", subsys)
	}
}

func TestBuildExtraContextSources_AppendsVectorRecall(t *testing.T) {
	store := vector.NewMapVectorStore()
	emb := vector.NewHashEmbedder(8)
	opts := &Options{VectorStore: store, VectorEmbedder: emb, VectorTopK: 7}

	srcs := buildExtraContextSources(opts)
	if len(srcs) != 1 {
		t.Fatalf("len(srcs) = %d, want 1", len(srcs))
	}
	// We do not import vctx in this test (cyclic concern); structurally
	// we know the only source is VectorRecallSource.
	if srcs[0].Name() == "" {
		t.Errorf("source name is empty")
	}
}

func TestBuildExtraContextSources_NilWhenEitherMissing(t *testing.T) {
	store := vector.NewMapVectorStore()
	emb := vector.NewHashEmbedder(8)

	for _, tc := range []struct {
		name string
		opts *Options
	}{
		{"only store", &Options{VectorStore: store}},
		{"only embedder", &Options{VectorEmbedder: emb}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			srcs := buildExtraContextSources(tc.opts)
			if len(srcs) != 0 {
				t.Errorf("expected no sources, got %d", len(srcs))
			}
		})
	}
}
