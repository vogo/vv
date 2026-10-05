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
	"testing"

	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/memory"
	memtool "github.com/vogo/vage/tool/memory"
)

func TestBindAgentPath_Nil(t *testing.T) {
	if BindAgentPath(nil) != nil {
		t.Fatal("BindAgentPath(nil) must be nil")
	}
}

func TestBindAgentPath_SchemaSessionBecomesAgentPath(t *testing.T) {
	dir := t.TempDir()
	raw, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	store := BindAgentPath(memory.NewLongTermMemory(raw))

	ctxA := schema.WithSessionID(context.Background(), "session-A")
	if err := store.Set(ctxA, "scratch:note", "from-A", 0); err != nil {
		t.Fatalf("Set A: %v", err)
	}

	got, err := store.Get(ctxA, "scratch:note")
	if err != nil || got != "from-A" {
		t.Fatalf("Get A: val=%v err=%v", got, err)
	}

	ctxB := schema.WithSessionID(context.Background(), "session-B")
	got, err = store.Get(ctxB, "scratch:note")
	if err != nil {
		t.Fatalf("Get B: %v", err)
	}
	if got != nil {
		t.Errorf("session B must not see A's private entry, got %v", got)
	}

	listed, err := store.List(ctxB, "")
	if err != nil {
		t.Fatalf("List B: %v", err)
	}
	for _, e := range listed {
		if e.Key == "scratch:note" {
			t.Fatal("session B List leaked A's private key")
		}
	}
}

func TestBindAgentPath_UserPathStillForbiddenOnPrivate(t *testing.T) {
	dir := t.TempDir()
	raw, err := NewFileStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	inner := memory.NewLongTermMemory(raw)

	ctxA := WithSessionID(context.Background(), "session-A")
	if err := inner.Set(ctxA, "scratch:x", "v", 0); err != nil {
		t.Fatalf("seed: %v", err)
	}

	userCtx := WithUserPath(context.Background())
	err = inner.Set(userCtx, "scratch:x", "hijack", 0)
	if !errors.Is(err, ErrSessionForbidden) {
		t.Errorf("user-path Set: %v, want ErrSessionForbidden", err)
	}
}

func TestSharedNamespaceSetMatchesToolPackage(t *testing.T) {
	for _, ns := range memtool.SharedNamespaceNames {
		if !IsSharedNamespace(ns) {
			t.Errorf("vv IsSharedNamespace(%q)=false, tool package lists it as shared", ns)
		}
	}
	if IsSharedNamespace("scratch") != memtool.IsSharedNamespace("scratch") {
		t.Fatal("scratch sharedness drifted between vv and vage/tool/memory")
	}
}
