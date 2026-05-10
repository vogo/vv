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
	"testing"

	"github.com/vogo/vage/session/tree"
	"github.com/vogo/vage/session/tree/vectorhook"
	"github.com/vogo/vage/vector"
	"github.com/vogo/vv/configs"
)

func TestMaybeWrapTreeWithVectorIndex_Disabled(t *testing.T) {
	inner := tree.NewMapTreeStore()
	opts := &Options{
		TreeStore:      inner,
		VectorStore:    vector.NewMapVectorStore(),
		VectorEmbedder: vector.NewHashEmbedder(8),
	}
	cfg := &configs.Config{} // VectorIndex disabled by default

	if err := maybeWrapTreeWithVectorIndex(cfg, opts); err != nil {
		t.Fatalf("err = %v", err)
	}
	if opts.TreeStore != inner {
		t.Errorf("disabled: TreeStore must be unchanged, got %T", opts.TreeStore)
	}
}

func TestMaybeWrapTreeWithVectorIndex_NilCfg(t *testing.T) {
	if err := maybeWrapTreeWithVectorIndex(nil, &Options{}); err != nil {
		t.Errorf("nil cfg: err = %v", err)
	}
}

func TestMaybeWrapTreeWithVectorIndex_NilOpts(t *testing.T) {
	cfg := &configs.Config{
		SessionTree: configs.SessionTreeConfig{
			VectorIndex: configs.SessionTreeVectorIndexConfig{Enabled: trueP()},
		},
	}
	if err := maybeWrapTreeWithVectorIndex(cfg, nil); err != nil {
		t.Errorf("err = %v", err)
	}
}

func TestMaybeWrapTreeWithVectorIndex_PrerequisiteMissing(t *testing.T) {
	// Tree present but vector store nil → log + degrade, no wrap, no error.
	inner := tree.NewMapTreeStore()
	opts := &Options{TreeStore: inner}
	cfg := &configs.Config{
		SessionTree: configs.SessionTreeConfig{
			VectorIndex: configs.SessionTreeVectorIndexConfig{Enabled: trueP()},
		},
	}
	if err := maybeWrapTreeWithVectorIndex(cfg, opts); err != nil {
		t.Fatalf("err = %v", err)
	}
	if opts.TreeStore != inner {
		t.Errorf("missing prereq: TreeStore must be unchanged, got %T", opts.TreeStore)
	}
}

func TestMaybeWrapTreeWithVectorIndex_Wraps(t *testing.T) {
	inner := tree.NewMapTreeStore()
	opts := &Options{
		TreeStore:      inner,
		VectorStore:    vector.NewMapVectorStore(),
		VectorEmbedder: vector.NewHashEmbedder(8),
	}
	cfg := &configs.Config{
		SessionTree: configs.SessionTreeConfig{
			VectorIndex: configs.SessionTreeVectorIndexConfig{Enabled: trueP()},
		},
	}
	if err := maybeWrapTreeWithVectorIndex(cfg, opts); err != nil {
		t.Fatalf("err = %v", err)
	}
	if _, ok := opts.TreeStore.(*vectorhook.Store); !ok {
		t.Errorf("expected *vectorhook.Store, got %T", opts.TreeStore)
	}
}

func TestSessionTreeVectorIndexConfig_Defaults(t *testing.T) {
	var c configs.SessionTreeVectorIndexConfig
	if c.IsEnabled() {
		t.Errorf("default IsEnabled = true, want false")
	}
	c.Enabled = trueP()
	if !c.IsEnabled() {
		t.Errorf("explicit true: IsEnabled = false")
	}
}
