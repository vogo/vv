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

package cli

import (
	"context"
	"strings"
	"testing"

	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/checkpoint"
	"github.com/vogo/vage/session"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/sessionlogs"
)

func newRestoreApp(t *testing.T, seed []schema.Message) *App {
	t.Helper()

	store, err := sessionlogs.New(t.TempDir())
	if err != nil {
		t.Fatalf("sessionlogs.New: %v", err)
	}

	if seed != nil {
		if err := store.Save(context.Background(), &checkpoint.Checkpoint{
			SessionID: "sess-restore",
			AgentID:   sessionlogs.DefaultPrimaryAgentID,
			Messages:  seed,
		}); err != nil {
			t.Fatalf("seed: %v", err)
		}
	}

	return &App{
		cfg:               &configs.Config{},
		sessionID:         "sess-restore",
		sessionStore:      session.NewMapSessionStore(),
		transcripts:       store,
		sessionResumeMode: SessionResumeExisting,
		restoredMessages:  -1,
	}
}

func TestRestoreHistory_ReplaysTheConversation(t *testing.T) {
	app := newRestoreApp(t, []schema.Message{
		schema.NewUserMessage(schema.ProtocolOpenAIChat, "what changed?"),
		schema.NewTextMessage(schema.ProtocolOpenAIChat, schema.RoleAssistant, "three files"),
	})

	app.restoreHistory(context.Background())

	if len(app.history) != 2 {
		t.Fatalf("history = %d messages, want 2", len(app.history))
	}

	if app.history[0].Text() != "what changed?" || app.history[1].Text() != "three files" {
		t.Errorf("conversation not restored in order: %+v", app.history)
	}

	if app.restoredMessages != 2 {
		t.Errorf("restoredMessages = %d, want 2", app.restoredMessages)
	}

	if app.estimatedTokens <= 0 {
		t.Error("restoring history must seed the token estimate, else the first compaction check is wrong")
	}

	if banner := app.sessionBanner(); !strings.Contains(banner, "restored 2 messages") {
		t.Errorf("banner = %q, want it to report the restore", banner)
	}
}

func TestRestoreHistory_HonoursTheCap(t *testing.T) {
	app := newRestoreApp(t, []schema.Message{
		schema.NewUserMessage(schema.ProtocolOpenAIChat, "one"),
		schema.NewUserMessage(schema.ProtocolOpenAIChat, "two"),
		schema.NewUserMessage(schema.ProtocolOpenAIChat, "three"),
	})
	app.cfg.Session.ResumeMaxMessages = 2

	app.restoreHistory(context.Background())

	if len(app.history) != 2 {
		t.Fatalf("history = %d, want 2 (capped)", len(app.history))
	}

	if app.history[0].Text() != "two" {
		t.Errorf("cap must keep the most recent messages, got first = %q", app.history[0].Text())
	}
}

func TestRestoreHistory_NoTranscriptKeepsIDOnlyBanner(t *testing.T) {
	app := newRestoreApp(t, nil)
	app.transcripts = nil

	app.restoreHistory(context.Background())

	if app.history != nil {
		t.Errorf("history = %+v, want nil", app.history)
	}

	if banner := app.sessionBanner(); !strings.Contains(banner, "history not restored") {
		t.Errorf("banner = %q, want the id-only wording", banner)
	}
}

func TestRestoreHistory_EmptySessionIsNotAnError(t *testing.T) {
	app := newRestoreApp(t, nil)

	app.restoreHistory(context.Background())

	if len(app.history) != 0 {
		t.Errorf("history = %+v, want empty", app.history)
	}

	if banner := app.sessionBanner(); !strings.Contains(banner, "restored 0 messages") {
		t.Errorf("banner = %q, want an explicit zero restore", banner)
	}
}
