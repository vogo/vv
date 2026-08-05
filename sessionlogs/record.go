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
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"time"

	"github.com/vogo/vage/schema"
)

// Record kinds. Every line of a transcript log is exactly one of these.
const (
	// KindMessage carries one message body. Written at most once per
	// distinct content per file — this is where the deduplication lives.
	KindMessage = "msg"

	// KindCheckpoint carries one ReAct iteration snapshot as an ordered
	// list of message ids. It never embeds bodies, which is what turns
	// the old O(n²) snapshot chain into O(n).
	KindCheckpoint = "ckpt"

	// KindSubAgent is a pointer written into the parent transcript when a
	// sub-agent run file is opened. It is the answer to "where did the
	// delegated work go".
	KindSubAgent = "subagent"
)

// record is the wire form of one transcript line. Field names are kept
// short because they repeat on every line; the JSON tags are the
// on-disk contract (see doc/domains/core/session/storage-redesign.md).
type record struct {
	Kind string `json:"k"`

	// --- KindMessage ---

	// ID is the content address: the first 8 bytes of the SHA-256 over
	// the message JSON with Timestamp zeroed, hex encoded.
	ID string `json:"id,omitempty"`
	// Role is denormalised for cheap scanning / debugging by eye.
	Role string `json:"role,omitempty"`
	// Message holds the marshalled schema.Message. Empty when the body
	// was spilled — see Spill.
	Message json.RawMessage `json:"message,omitempty"`
	// Spill is the session-relative path of an externalised body. Set
	// instead of Message for oversized tool results.
	Spill string `json:"spill,omitempty"`
	// SpillBytes records the externalised size so a listing can report
	// it without opening the spill file.
	SpillBytes int `json:"spill_bytes,omitempty"`

	// --- KindCheckpoint ---

	Seq             int               `json:"seq,omitempty"`
	CheckpointID    string            `json:"ckpt_id,omitempty"`
	Agent           string            `json:"agent,omitempty"`
	Iteration       int               `json:"iter,omitempty"`
	Final           bool              `json:"final,omitempty"`
	StopReason      schema.StopReason `json:"stop,omitempty"`
	Usage           *schema.Usage     `json:"usage,omitempty"`
	Estimated       bool              `json:"estimated,omitempty"`
	SessionMsgCount int               `json:"session_msg_count,omitempty"`
	// Msgs is the ordered content-address list restoring this iteration.
	Msgs []string `json:"msgs,omitempty"`

	// --- KindSubAgent ---

	Run  int    `json:"run,omitempty"`
	File string `json:"file,omitempty"`
	Task string `json:"task,omitempty"`

	CreatedAt time.Time `json:"ts,omitzero"`
}

// messageID returns the content address of m plus the bytes to store.
//
// The hash deliberately ignores Message.Timestamp while the stored bytes
// keep it: a system prompt rebuilt on every turn is byte-identical apart
// from its creation stamp, and hashing that stamp would defeat the whole
// point of content addressing (it was 2.6 KiB re-written per iteration in
// the measurements that motivated this format). The cost is that a
// repeated identical message restores with the timestamp of its first
// occurrence — a stamp that carries no meaning for identical content.
func messageID(m schema.Message) (id string, stored []byte, err error) {
	stored, err = json.Marshal(m)
	if err != nil {
		return "", nil, err
	}

	hashable := m
	hashable.Timestamp = time.Time{}

	digestSrc, err := json.Marshal(hashable)
	if err != nil {
		return "", nil, err
	}

	sum := sha256.Sum256(digestSrc)

	return hex.EncodeToString(sum[:8]), stored, nil
}

// newID mints a random 8-byte hex token for checkpoint identity. It
// mirrors the format vage's checkpoint package uses so ids stay
// interchangeable across the legacy fallback boundary.
func newID() string {
	var buf [8]byte
	if _, err := randRead(buf[:]); err != nil {
		// Uniqueness within a file is already guaranteed by the
		// monotonic sequence; a time-derived token only has to avoid
		// panicking here.
		return "fb" + hex.EncodeToString([]byte(time.Now().Format("150405.000000")))
	}

	return hex.EncodeToString(buf[:])
}
