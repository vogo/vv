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

// Package sessionlogs stores a session's conversation as one append-only,
// content-addressed transcript per agent run.
//
// It implements checkpoint.IterationStore, so TaskAgent keeps handing it
// the full message slice at the end of every ReAct iteration — the
// deduplication happens here, in the storage layer, rather than asking
// the agent loop to track deltas.
//
// On-disk layout, rooted at the shared session directory:
//
//	<root>/<session_id>/messages.jsonl              — main (primary) transcript
//	<root>/<session_id>/subagents/<agent>-<n>.jsonl — one file per dispatch
//	<root>/<session_id>/tool-results/<id>.json      — spilled oversized bodies
//
// Every line is a record (see record.go): a "msg" line holds one message
// body and is written at most once per distinct content, while a "ckpt"
// line holds an iteration snapshot as an ordered list of content
// addresses. Restoring a checkpoint means resolving those ids — the
// bodies are never duplicated across iterations.
//
// Two invariants make this safe to swap in for the previous full-snapshot
// store:
//
//   - Load(id="") returns the latest checkpoint of the MAIN transcript
//     only. Sub-agent checkpoints live in their own files and can never
//     be mistaken for the session's conversation, which the previous
//     shared-sequence layout allowed.
//   - Sessions written before this format still resolve: when
//     messages.jsonl is absent, reads fall through to the legacy
//     <session_id>/checkpoints/ store.
package sessionlogs

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/vogo/vage/checkpoint"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/session"
)

const (
	mainLogName    = "messages.jsonl"
	subAgentsDir   = "subagents"
	toolResultsDir = "tool-results"

	dirPerm  os.FileMode = 0o700
	filePerm os.FileMode = 0o600

	// maxLineBytes caps a single transcript line during scans. Bodies
	// above the spill threshold never reach a line, so this only has to
	// accommodate a large-but-inline message.
	maxLineBytes = 16 << 20
)

// DefaultPrimaryAgentID is the agent id whose checkpoints own the main
// transcript. Overridable so the package does not have to import the
// agent registry.
const DefaultPrimaryAgentID = "primary"

// randRead is crypto/rand.Read, indirected for tests.
var randRead = rand.Read

// Store implements checkpoint.IterationStore on top of per-session
// append-only transcripts. It is safe for concurrent use.
type Store struct {
	root           string
	primaryAgentID string
	spillBytes     int
	legacy         *checkpoint.FileIterationStore

	mu      sync.Mutex
	logs    map[string]*logFile // absolute path -> handle
	runs    map[string]string   // sessionID\x00runKey -> session-relative path
	runSeqs map[string]int      // sessionID\x00agent -> last dispatch number
}

// Compile-time check.
var _ checkpoint.IterationStore = (*Store)(nil)

// Option configures a Store.
type Option func(*Store)

// WithPrimaryAgentID overrides which agent id owns the main transcript.
func WithPrimaryAgentID(id string) Option {
	return func(s *Store) {
		if id != "" {
			s.primaryAgentID = id
		}
	}
}

// WithToolResultSpillBytes sets the size above which a tool message body
// is written to tool-results/ and replaced by a pointer. A value <= 0
// disables spilling.
func WithToolResultSpillBytes(n int) Option {
	return func(s *Store) { s.spillBytes = n }
}

// New constructs a Store rooted at dir, which must be the same directory
// the session store, plan workspace and session tree share so that one
// recursive delete still wipes every per-session subsystem.
func New(root string, opts ...Option) (*Store, error) {
	if root == "" {
		return nil, fmt.Errorf("%w: root directory is empty", checkpoint.ErrInvalidArgument)
	}

	if err := os.MkdirAll(root, dirPerm); err != nil {
		return nil, fmt.Errorf("sessionlogs: create root %q: %w", root, err)
	}

	legacy, err := checkpoint.NewFileIterationStore(root)
	if err != nil {
		return nil, fmt.Errorf("sessionlogs: legacy store: %w", err)
	}

	s := &Store{
		root:           root,
		primaryAgentID: DefaultPrimaryAgentID,
		spillBytes:     0,
		legacy:         legacy,
		logs:           make(map[string]*logFile),
		runs:           make(map[string]string),
		runSeqs:        make(map[string]int),
	}

	for _, opt := range opts {
		opt(s)
	}

	return s, nil
}

// Root returns the configured root directory.
func (s *Store) Root() string { return s.root }

func (s *Store) sessionDir(sessionID string) string {
	return filepath.Join(s.root, sessionID)
}

func validateSessionID(id string) error {
	if id == "" || id == "." || id == ".." || !session.IDPattern.MatchString(id) {
		return fmt.Errorf("%w: invalid session id %q", checkpoint.ErrInvalidArgument, id)
	}

	return nil
}

// --- checkpoint.IterationStore ---

// Save appends the new message bodies of cp plus one checkpoint line to
// the transcript that owns cp, then populates cp.Sequence / cp.ID /
// cp.CreatedAt as the interface requires.
//
// Sequence is monotonic within the transcript file. For the main log
// that is "per session", matching the previous behaviour; a sub-agent
// dispatch numbers its own iterations from 1, which is what makes a run
// readable in isolation.
func (s *Store) Save(ctx context.Context, cp *checkpoint.Checkpoint) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if cp == nil {
		return fmt.Errorf("%w: checkpoint is nil", checkpoint.ErrInvalidArgument)
	}

	if err := validateSessionID(cp.SessionID); err != nil {
		return err
	}

	lf, err := s.logFor(ctx, cp)
	if err != nil {
		return err
	}

	if cp.CreatedAt.IsZero() {
		cp.CreatedAt = time.Now()
	}

	cp.ID = newID()

	return lf.append(cp, s.spillBytes)
}

// Load returns the checkpoint identified by sessionID and id; id == ""
// means the latest one in the main transcript.
func (s *Store) Load(ctx context.Context, sessionID, id string) (*checkpoint.Checkpoint, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := validateSessionID(sessionID); err != nil {
		return nil, err
	}

	lf, err := s.mainLog(sessionID)
	if err != nil {
		return nil, err
	}

	snap, err := lf.read()
	if err != nil {
		return nil, err
	}

	if snap == nil || len(snap.ckpts) == 0 {
		// No transcript in the current format — the session may predate
		// it. Fall through to the previous full-snapshot layout.
		return s.legacy.Load(ctx, sessionID, id)
	}

	rec, ok := snap.checkpoint(id)
	if !ok {
		return nil, checkpoint.ErrCheckpointNotFound
	}

	return snap.materialise(sessionID, rec)
}

// List returns metadata for every checkpoint of the session's main
// transcript in ascending sequence order.
//
// Sub-agent dispatches are intentionally absent: they are separate
// conversations, not points on this session's resume timeline. Reach
// them through ListRuns / LoadRun.
func (s *Store) List(ctx context.Context, sessionID string) ([]*checkpoint.CheckpointMeta, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	if err := validateSessionID(sessionID); err != nil {
		return nil, err
	}

	lf, err := s.mainLog(sessionID)
	if err != nil {
		return nil, err
	}

	snap, err := lf.read()
	if err != nil {
		return nil, err
	}

	if snap == nil || len(snap.ckpts) == 0 {
		return s.legacy.List(ctx, sessionID)
	}

	out := make([]*checkpoint.CheckpointMeta, 0, len(snap.ckpts))
	for _, rec := range snap.ckpts {
		out = append(out, metaFrom(sessionID, rec))
	}

	return out, nil
}

// Delete removes every transcript of sessionID: the main log, all
// sub-agent runs, spilled bodies and any legacy checkpoint directory.
// Idempotent.
func (s *Store) Delete(ctx context.Context, sessionID string) error {
	if err := ctx.Err(); err != nil {
		return err
	}

	if err := validateSessionID(sessionID); err != nil {
		return err
	}

	dir := s.sessionDir(sessionID)

	s.mu.Lock()
	for path := range s.logs {
		if strings.HasPrefix(path, dir+string(os.PathSeparator)) {
			delete(s.logs, path)
		}
	}

	prefix := sessionID + "\x00"
	for key := range s.runs {
		if strings.HasPrefix(key, prefix) {
			delete(s.runs, key)
		}
	}

	for key := range s.runSeqs {
		if strings.HasPrefix(key, prefix) {
			delete(s.runSeqs, key)
		}
	}
	s.mu.Unlock()

	for _, target := range []string{
		filepath.Join(dir, mainLogName),
		filepath.Join(dir, subAgentsDir),
		filepath.Join(dir, toolResultsDir),
	} {
		if err := os.RemoveAll(target); err != nil {
			return fmt.Errorf("sessionlogs: delete %q: %w", target, err)
		}
	}

	return s.legacy.Delete(ctx, sessionID)
}

// --- routing ---

// mainLog returns the handle for a session's primary transcript.
func (s *Store) mainLog(sessionID string) (*logFile, error) {
	return s.handle(filepath.Join(s.sessionDir(sessionID), mainLogName))
}

func (s *Store) handle(path string) (*logFile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if lf, ok := s.logs[path]; ok {
		return lf, nil
	}

	lf := &logFile{path: path}
	s.logs[path] = lf

	return lf, nil
}

// logFor resolves which transcript owns cp, opening a sub-agent run file
// (and recording a pointer to it in the parent transcript) the first time
// a dispatch is seen.
//
// Routing is deliberately belt-and-braces: a Run in the context is the
// precise signal, but any checkpoint whose agent id is not the primary is
// also kept out of the main log. A dispatcher path that forgets to attach
// a Run therefore degrades to "one file per agent" instead of corrupting
// the session's resume timeline.
func (s *Store) logFor(ctx context.Context, cp *checkpoint.Checkpoint) (*logFile, error) {
	run, hasRun := RunFromContext(ctx)
	isPrimary := cp.AgentID == "" || cp.AgentID == s.primaryAgentID

	if isPrimary && !hasRun {
		return s.mainLog(cp.SessionID)
	}

	key := run.Key
	if key == "" {
		key = cp.AgentID
	}

	rel, err := s.runFile(cp.SessionID, cp.AgentID, key, run.Task)
	if err != nil {
		return nil, err
	}

	return s.handle(filepath.Join(s.sessionDir(cp.SessionID), rel))
}

// runFile resolves (creating on first use) the session-relative path of a
// dispatch transcript, and writes the parent pointer line when the file
// is new.
func (s *Store) runFile(sessionID, agentID, runKey, task string) (string, error) {
	mapKey := sessionID + "\x00" + runKey

	s.mu.Lock()
	if rel, ok := s.runs[mapKey]; ok {
		s.mu.Unlock()

		return rel, nil
	}

	dir := filepath.Join(s.sessionDir(sessionID), subAgentsDir)
	if err := os.MkdirAll(dir, dirPerm); err != nil {
		s.mu.Unlock()

		return "", fmt.Errorf("sessionlogs: create %q: %w", dir, err)
	}

	name := sanitizeAgentID(agentID)

	// Dispatch numbers come from an in-memory counter seeded from disk,
	// not from a per-call directory scan: concurrent dispatches of the
	// same specialist all scan before any of them has written a file,
	// so a scan-per-call hands out the same number several times.
	counterKey := sessionID + "\x00" + name

	seq, ok := s.runSeqs[counterKey]
	if !ok {
		seq = highestRunSeq(dir, name)
	}

	seq++
	s.runSeqs[counterKey] = seq

	rel := filepath.Join(subAgentsDir, fmt.Sprintf("%s-%d.jsonl", name, seq))
	s.runs[mapKey] = rel
	s.mu.Unlock()

	// Materialise the file immediately so a dispatch is listable before
	// its first checkpoint lands.
	if err := touchFile(filepath.Join(s.sessionDir(sessionID), rel)); err != nil {
		return "", err
	}

	// The pointer goes into the parent transcript outside the store lock
	// and before the run file is handed back, so the parent is never
	// holding two transcript locks at once.
	parent, err := s.mainLog(sessionID)
	if err != nil {
		return "", err
	}

	if err := parent.appendPointer(record{
		Kind:      KindSubAgent,
		Agent:     agentID,
		Run:       seq,
		File:      filepath.ToSlash(rel),
		Task:      task,
		CreatedAt: time.Now(),
	}); err != nil {
		return "", err
	}

	return rel, nil
}

// highestRunSeq returns the highest dispatch number already on disk for
// agent, so run numbering survives a process restart.
func highestRunSeq(dir, agent string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}

	prefix := agent + "-"
	max := 0

	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, ".jsonl") {
			continue
		}

		n, convErr := strconv.Atoi(strings.TrimSuffix(strings.TrimPrefix(name, prefix), ".jsonl"))
		if convErr == nil && n > max {
			max = n
		}
	}

	return max
}

// touchFile creates path (and its parent) if absent, leaving an existing
// file untouched.
func touchFile(path string) error {
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return fmt.Errorf("sessionlogs: create %q: %w", filepath.Dir(path), err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, filePerm)
	if err != nil {
		return fmt.Errorf("sessionlogs: create %q: %w", path, err)
	}

	return f.Close()
}

// sanitizeAgentID keeps agent ids usable as file names without turning
// two distinct ids into the same file: every rune outside [A-Za-z0-9_-]
// becomes '-'.
func sanitizeAgentID(id string) string {
	if id == "" {
		return "agent"
	}

	var sb strings.Builder
	sb.Grow(len(id))

	for _, r := range id {
		switch {
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_':
			sb.WriteRune(r)
		default:
			sb.WriteByte('-')
		}
	}

	return sb.String()
}

// --- projections ---

func metaFrom(sessionID string, rec record) *checkpoint.CheckpointMeta {
	meta := &checkpoint.CheckpointMeta{
		ID:            rec.CheckpointID,
		Sequence:      rec.Seq,
		SessionID:     sessionID,
		AgentID:       rec.Agent,
		Iteration:     rec.Iteration,
		Final:         rec.Final,
		StopReason:    rec.StopReason,
		MessagesCount: len(rec.Msgs),
		CreatedAt:     rec.CreatedAt,
	}

	if rec.Usage != nil {
		meta.Usage = *rec.Usage
	}

	return meta
}

// --- snapshot ---

// snapshot is a parsed transcript: message bodies keyed by content
// address plus the checkpoint lines in file order.
type snapshot struct {
	bodies map[string]schema.Message
	ckpts  []record
	runs   []record
}

// checkpoint returns the requested checkpoint line; id == "" means the
// last one in the file.
func (s *snapshot) checkpoint(id string) (record, bool) {
	if len(s.ckpts) == 0 {
		return record{}, false
	}

	if id == "" {
		return s.ckpts[len(s.ckpts)-1], true
	}

	for _, rec := range s.ckpts {
		if rec.CheckpointID == id {
			return rec, true
		}
	}

	return record{}, false
}

// materialise resolves a checkpoint line's content addresses back into a
// full checkpoint.
func (s *snapshot) materialise(sessionID string, rec record) (*checkpoint.Checkpoint, error) {
	msgs := make([]schema.Message, 0, len(rec.Msgs))

	for _, id := range rec.Msgs {
		body, ok := s.bodies[id]
		if !ok {
			// A checkpoint line referencing a body that is not in the
			// file means the transcript was truncated or hand-edited.
			// Surfacing it beats silently resuming a conversation with
			// a hole in the middle.
			return nil, fmt.Errorf("sessionlogs: checkpoint %q references unknown message %q", rec.CheckpointID, id)
		}

		msgs = append(msgs, body)
	}

	cp := &checkpoint.Checkpoint{
		ID:              rec.CheckpointID,
		Sequence:        rec.Seq,
		SessionID:       sessionID,
		AgentID:         rec.Agent,
		Iteration:       rec.Iteration,
		Final:           rec.Final,
		StopReason:      rec.StopReason,
		Messages:        msgs,
		SessionMsgCount: rec.SessionMsgCount,
		Estimated:       rec.Estimated,
		CreatedAt:       rec.CreatedAt,
	}

	if rec.Usage != nil {
		cp.Usage = *rec.Usage
	}

	return cp, nil
}

// --- logFile ---

// logFile serialises access to one transcript file and caches the set of
// content addresses already written, so Save only has to hash.
type logFile struct {
	mu     sync.Mutex
	path   string
	loaded bool
	seen   map[string]struct{}
	maxSeq int
}

// append writes the not-yet-stored bodies of cp followed by its
// checkpoint line, in one write.
func (l *logFile) append(cp *checkpoint.Checkpoint, spillBytes int) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.ensureLoaded(); err != nil {
		return err
	}

	var (
		buf     strings.Builder
		ids     = make([]string, 0, len(cp.Messages))
		fresh   = make([]string, 0, len(cp.Messages))
		dir     = filepath.Dir(l.path)
		spilled []string
	)

	for _, msg := range cp.Messages {
		id, stored, err := messageID(msg)
		if err != nil {
			return fmt.Errorf("sessionlogs: marshal message: %w", err)
		}

		ids = append(ids, id)

		if _, ok := l.seen[id]; ok {
			continue
		}

		rec := record{
			Kind:      KindMessage,
			ID:        id,
			Role:      string(msg.Role()),
			Message:   stored,
			CreatedAt: msg.Timestamp,
		}

		if spillBytes > 0 && len(stored) > spillBytes && msg.Role() == schema.RoleTool {
			rel, err := writeSpill(dir, id, stored)
			if err != nil {
				return err
			}

			rec.Message = nil
			rec.Spill = rel
			rec.SpillBytes = len(stored)
			spilled = append(spilled, filepath.Join(dir, rel))
		}

		line, err := json.Marshal(rec)
		if err != nil {
			return fmt.Errorf("sessionlogs: marshal message record: %w", err)
		}

		buf.Write(line)
		buf.WriteByte('\n')
		fresh = append(fresh, id)
	}

	seq := l.maxSeq + 1

	ckpt := record{
		Kind:            KindCheckpoint,
		Seq:             seq,
		CheckpointID:    cp.ID,
		Agent:           cp.AgentID,
		Iteration:       cp.Iteration,
		Final:           cp.Final,
		StopReason:      cp.StopReason,
		Estimated:       cp.Estimated,
		SessionMsgCount: cp.SessionMsgCount,
		Msgs:            ids,
		CreatedAt:       cp.CreatedAt,
	}

	if cp.Usage != (schema.Usage{}) {
		usage := cp.Usage
		ckpt.Usage = &usage
	}

	line, err := json.Marshal(ckpt)
	if err != nil {
		return fmt.Errorf("sessionlogs: marshal checkpoint record: %w", err)
	}

	buf.Write(line)
	buf.WriteByte('\n')

	if err := appendFile(l.path, buf.String()); err != nil {
		// Roll back the spill files: with no line referencing them they
		// would be unreachable garbage that Delete still has to carry.
		for _, p := range spilled {
			_ = os.Remove(p)
		}

		return err
	}

	// Commit in-memory state only after the bytes are on disk, so a
	// failed write cannot make a later Save skip a body it never wrote.
	for _, id := range fresh {
		l.seen[id] = struct{}{}
	}

	l.maxSeq = seq
	cp.Sequence = seq

	return nil
}

// appendPointer writes a single non-message line (currently only the
// sub-agent pointer) without disturbing the sequence counter.
func (l *logFile) appendPointer(rec record) error {
	l.mu.Lock()
	defer l.mu.Unlock()

	if err := l.ensureLoaded(); err != nil {
		return err
	}

	line, err := json.Marshal(rec)
	if err != nil {
		return fmt.Errorf("sessionlogs: marshal pointer record: %w", err)
	}

	return appendFile(l.path, string(line)+"\n")
}

// read parses the whole transcript. Returns (nil, nil) when the file
// does not exist, which is the caller's cue to try the legacy layout.
func (l *logFile) read() (*snapshot, error) {
	l.mu.Lock()
	defer l.mu.Unlock()

	f, err := os.Open(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}

		return nil, fmt.Errorf("sessionlogs: open %q: %w", l.path, err)
	}
	defer func() { _ = f.Close() }()

	snap := &snapshot{bodies: make(map[string]schema.Message)}
	dir := filepath.Dir(l.path)
	sc := newScanner(f)

	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}

		var rec record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			// One corrupt line must not cost the whole transcript.
			continue
		}

		switch rec.Kind {
		case KindMessage:
			msg, err := decodeMessage(dir, rec)
			if err != nil {
				return nil, err
			}

			snap.bodies[rec.ID] = msg
		case KindCheckpoint:
			snap.ckpts = append(snap.ckpts, rec)
		case KindSubAgent:
			snap.runs = append(snap.runs, rec)
		}
	}

	if err := sc.Err(); err != nil {
		return nil, fmt.Errorf("sessionlogs: scan %q: %w", l.path, err)
	}

	return snap, nil
}

// ensureLoaded seeds seen / maxSeq from disk on first use so a restarted
// process neither re-writes existing bodies nor reuses a sequence.
// Callers must hold l.mu.
func (l *logFile) ensureLoaded() error {
	if l.loaded {
		return nil
	}

	l.seen = make(map[string]struct{})

	f, err := os.Open(l.path)
	if err != nil {
		if os.IsNotExist(err) {
			l.loaded = true

			return nil
		}

		return fmt.Errorf("sessionlogs: open %q: %w", l.path, err)
	}
	defer func() { _ = f.Close() }()

	sc := newScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" {
			continue
		}

		var rec record
		if err := json.Unmarshal([]byte(line), &rec); err != nil {
			continue
		}

		switch rec.Kind {
		case KindMessage:
			l.seen[rec.ID] = struct{}{}
		case KindCheckpoint:
			if rec.Seq > l.maxSeq {
				l.maxSeq = rec.Seq
			}
		}
	}

	if err := sc.Err(); err != nil {
		return fmt.Errorf("sessionlogs: scan %q: %w", l.path, err)
	}

	l.loaded = true

	return nil
}

// --- io helpers ---

func newScanner(f *os.File) *bufio.Scanner {
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64<<10), maxLineBytes)

	return sc
}

func appendFile(path, payload string) error {
	if err := os.MkdirAll(filepath.Dir(path), dirPerm); err != nil {
		return fmt.Errorf("sessionlogs: create %q: %w", filepath.Dir(path), err)
	}

	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, filePerm)
	if err != nil {
		return fmt.Errorf("sessionlogs: open %q: %w", path, err)
	}

	if _, err := f.WriteString(payload); err != nil {
		_ = f.Close()

		return fmt.Errorf("sessionlogs: write %q: %w", path, err)
	}

	return f.Close()
}

// writeSpill externalises an oversized body and returns its path
// relative to the transcript's directory.
func writeSpill(dir, id string, body []byte) (string, error) {
	target := filepath.Join(dir, toolResultsDir)
	if err := os.MkdirAll(target, dirPerm); err != nil {
		return "", fmt.Errorf("sessionlogs: create %q: %w", target, err)
	}

	path := filepath.Join(target, id+".json")
	if err := os.WriteFile(path, body, filePerm); err != nil {
		return "", fmt.Errorf("sessionlogs: write spill %q: %w", path, err)
	}

	return filepath.ToSlash(filepath.Join(toolResultsDir, id+".json")), nil
}

// decodeMessage rehydrates a message record, reading the spill file when
// the body was externalised. Spilling is invisible above this line.
func decodeMessage(dir string, rec record) (schema.Message, error) {
	body := []byte(rec.Message)

	if rec.Spill != "" {
		raw, err := os.ReadFile(filepath.Join(dir, filepath.FromSlash(rec.Spill)))
		if err != nil {
			return schema.Message{}, fmt.Errorf("sessionlogs: read spill %q: %w", rec.Spill, err)
		}

		body = raw
	}

	var msg schema.Message
	if err := json.Unmarshal(body, &msg); err != nil {
		return schema.Message{}, fmt.Errorf("sessionlogs: decode message %q: %w", rec.ID, err)
	}

	return msg, nil
}

// sortRunsByFile keeps run listings stable regardless of map iteration
// or filesystem ordering.
func sortRunsByFile(runs []RunMeta) {
	sort.Slice(runs, func(i, j int) bool {
		if runs[i].AgentID != runs[j].AgentID {
			return runs[i].AgentID < runs[j].AgentID
		}

		return runs[i].Run < runs[j].Run
	})
}
