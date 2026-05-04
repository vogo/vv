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

package session_tests

import (
	"context"
	"errors"
	"net"
	"net/http"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/session"
	"github.com/vogo/vv/httpapis"
	"github.com/vogo/vv/setup"
)

// TestLifecycle_HookToFileToHTTP wires the full end-to-end loop that the vv
// runtime actually exercises:
//
//  1. setup.Init constructs the SessionStore + HookManager.
//  2. An agent-style event is dispatched through the HookManager, which
//     causes SessionHook to autoCreate the session and append the event.
//  3. httpapis.Serve is booted with the SAME SessionStore.
//  4. GET /v1/sessions/{id}/events returns the event written in step 2.
//
// This is the strongest integration assertion we can make for Story B+C
// without booting the vv binary as a subprocess.
func TestLifecycle_HookToFileToHTTP(t *testing.T) {
	sessDir := filepath.Join(t.TempDir(), "sessions")
	cfg := newCLITestConfig(t, sessDir)

	res, err := setup.Init(cfg, nil)
	if err != nil {
		t.Fatalf("setup.Init: %v", err)
	}
	defer res.Shutdown(context.Background())

	if res.SessionStore == nil {
		t.Fatal("expected non-nil SessionStore")
	}
	mgr := res.SetupResult.HookManager
	if mgr == nil {
		t.Fatal("expected non-nil HookManager")
	}

	const sid = "lifecycle-1"
	mgr.Dispatch(context.Background(), schema.Event{
		Type: schema.EventAgentStart, AgentID: "coder", SessionID: sid,
		Timestamp: time.Now(), Data: schema.AgentStartData{},
	})

	// Wait for SessionHook to flush the event into events.jsonl.
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		evs, lerr := res.SessionStore.ListEvents(context.Background(), sid)
		if lerr == nil && len(evs) >= 1 {
			break
		}
		if lerr != nil && !errors.Is(lerr, session.ErrSessionNotFound) {
			t.Fatalf("ListEvents: %v", lerr)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Boot httpapis.Serve against the same SessionStore.
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	cfg.Server.Addr = addr

	srvCtx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var wg sync.WaitGroup
	wg.Go(func() {
		_ = httpapis.Serve(
			srvCtx, cfg, res.LLMClient, res.SetupResult.Dispatcher, res.SetupResult.Agents(),
			res.PersistentMem, nil, res.Compactor,
			res.SessionBudget, res.DailyBudget, res.SessionStore, res.Workspace, res.TreeStore,
			res.VectorStore, res.VectorEmb, res,
		)
	})

	baseURL := "http://" + addr
	waitForServer(t, baseURL)

	// HTTP GET must surface the event written by the hook.
	body := getJSON(t, baseURL+"/v1/sessions/"+sid+"/events")
	events := body["events"].([]any)
	if len(events) < 1 {
		t.Errorf("expected >=1 event over HTTP, got %d", len(events))
	}
	first := events[0].(map[string]any)
	if first["type"] != schema.EventAgentStart {
		t.Errorf("event[0].type = %v, want %s", first["type"], schema.EventAgentStart)
	}
	if first["session_id"] != sid {
		t.Errorf("event[0].session_id = %v, want %s", first["session_id"], sid)
	}

	cancel()
	wg.Wait()
}

// _ keeps the http import alive in lint configs that flag unused imports
// when only error-path branches reference http.StatusNotFound.
var _ = http.StatusOK
