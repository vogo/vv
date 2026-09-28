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
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/checkpoint"
)

// RunMeta describes one sub-agent dispatch of a session.
type RunMeta struct {
	AgentID    string            `json:"agent_id"`
	Run        int               `json:"run"`
	File       string            `json:"file"`
	Task       string            `json:"task,omitempty"`
	StartedAt  time.Time         `json:"started_at,omitzero"`
	Iterations int               `json:"iterations"`
	Messages   int               `json:"messages"`
	Final      bool              `json:"final"`
	StopReason schema.StopReason `json:"stop_reason,omitempty"`
}

// ErrRunNotFound is returned when a dispatch transcript does not exist.
var ErrRunNotFound = fmt.Errorf("sessionlogs: sub-agent run not found")

// ListRuns returns every sub-agent dispatch recorded for sessionID,
// ordered by agent id then dispatch number.
//
// The pointer lines in the main transcript are the index; the run files
// are also scanned so a dispatch whose pointer never made it to disk is
// still reported rather than silently invisible.
func (s *Store) ListRuns(ctx context.Context, sessionID string) ([]RunMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := validateSessionID(sessionID); err != nil {
		return nil, err
	}

	dir := s.sessionDir(sessionID)
	byFile := make(map[string]RunMeta)

	main, err := s.mainLog(sessionID)
	if err != nil {
		return nil, err
	}

	snap, err := main.read()
	if err != nil {
		return nil, err
	}

	if snap != nil {
		for _, rec := range snap.runs {
			byFile[rec.File] = RunMeta{
				AgentID:   rec.Agent,
				Run:       rec.Run,
				File:      rec.File,
				Task:      rec.Task,
				StartedAt: rec.CreatedAt,
			}
		}
	}

	entries, err := os.ReadDir(filepath.Join(dir, subAgentsDir))
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("sessionlogs: read %q: %w", filepath.Join(dir, subAgentsDir), err)
	}

	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".jsonl") {
			continue
		}

		rel := subAgentsDir + "/" + e.Name()
		meta, ok := byFile[rel]
		if !ok {
			agent, run := parseRunFileName(e.Name())
			meta = RunMeta{AgentID: agent, Run: run, File: rel}
		}

		if err := s.fillRunStats(dir, rel, &meta); err != nil {
			return nil, err
		}

		byFile[rel] = meta
	}

	out := make([]RunMeta, 0, len(byFile))
	for _, meta := range byFile {
		out = append(out, meta)
	}

	sortRunsByFile(out)

	return out, nil
}

// fillRunStats reads a dispatch transcript to complete its counters.
func (s *Store) fillRunStats(sessionDir, rel string, meta *RunMeta) error {
	lf, err := s.handle(filepath.Join(sessionDir, filepath.FromSlash(rel)))
	if err != nil {
		return err
	}

	snap, err := lf.read()
	if err != nil || snap == nil {
		return err
	}

	meta.Iterations = len(snap.ckpts)

	if last := len(snap.ckpts) - 1; last >= 0 {
		meta.Messages = len(snap.ckpts[last].Msgs)
		meta.Final = snap.ckpts[last].Final
		meta.StopReason = snap.ckpts[last].StopReason

		if meta.AgentID == "" {
			meta.AgentID = snap.ckpts[last].Agent
		}

		if meta.StartedAt.IsZero() {
			meta.StartedAt = snap.ckpts[0].CreatedAt
		}
	}

	return nil
}

// LoadRun returns the final state of one sub-agent dispatch: its last
// checkpoint, materialised the same way Load materialises the session's
// own timeline.
func (s *Store) LoadRun(ctx context.Context, sessionID, agentID string, run int) (*checkpoint.Checkpoint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := validateSessionID(sessionID); err != nil {
		return nil, err
	}

	if agentID == "" {
		return nil, fmt.Errorf("%w: session id and agent id are required", checkpoint.ErrInvalidArgument)
	}

	rel := fmt.Sprintf("%s/%s-%d.jsonl", subAgentsDir, sanitizeAgentID(agentID), run)

	lf, err := s.handle(filepath.Join(s.sessionDir(sessionID), filepath.FromSlash(rel)))
	if err != nil {
		return nil, err
	}

	snap, err := lf.read()
	if err != nil {
		return nil, err
	}

	if snap == nil {
		return nil, ErrRunNotFound
	}

	rec, ok := snap.checkpoint("")
	if !ok {
		return nil, ErrRunNotFound
	}

	return snap.materialise(sessionID, rec)
}

// Messages returns the conversation to restore for sessionID: the
// message list of the latest main-transcript checkpoint, capped to the
// most recent limit entries (limit <= 0 means no cap).
//
// This is what turns resume from "reuse the id" into "reuse the
// conversation" — the event stream could never do it because
// schema.Event.Data is an interface and decodes back as nil.
func (s *Store) Messages(ctx context.Context, sessionID string, limit int) ([]schema.Message, error) {
	cp, err := s.Load(ctx, sessionID, "")
	if err != nil {
		return nil, err
	}

	msgs := restoreWindow(cp.Messages, limit)

	out := make([]schema.Message, len(msgs))
	copy(out, msgs)

	return out, nil
}

// restoreWindow keeps a recent message window without beginning with an
// orphaned tool result. When the nominal boundary cuts a tool exchange, the
// whole assistant-call/result group is retained, so the result still has the
// call it answers. A leading system prefix is retained as request-level
// instruction state. These integrity rules may make the result slightly
// larger than limit.
func restoreWindow(msgs []schema.Message, limit int) []schema.Message {
	if limit <= 0 || len(msgs) <= limit {
		return msgs
	}

	start := len(msgs) - limit
	for start > 0 && msgs[start].Role() == schema.RoleTool {
		start--
	}

	prefixEnd := 0
	for prefixEnd < start && msgs[prefixEnd].Role() == schema.RoleSystem {
		prefixEnd++
	}

	if prefixEnd == 0 {
		return msgs[start:]
	}

	out := make([]schema.Message, 0, prefixEnd+len(msgs)-start)
	out = append(out, msgs[:prefixEnd]...)
	out = append(out, msgs[start:]...)

	return out
}

// parseRunFileName recovers (agent, run) from "<agent>-<n>.jsonl".
func parseRunFileName(name string) (string, int) {
	base := strings.TrimSuffix(name, ".jsonl")

	idx := strings.LastIndex(base, "-")
	if idx <= 0 {
		return base, 0
	}

	n, err := strconv.Atoi(base[idx+1:])
	if err != nil {
		return base, 0
	}

	return base[:idx], n
}
