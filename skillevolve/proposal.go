package skillevolve

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const (
	proposalsDirName = ".proposals"
	proposalDirPerm  = 0o700
	proposalFilePerm = 0o600
)

// FileQueue persists proposals as JSON files under skill_dir/.proposals/.
type FileQueue struct {
	dir string
}

// NewFileQueue roots the queue at skillDir/.proposals.
func NewFileQueue(skillDir string) *FileQueue {
	return &FileQueue{dir: filepath.Join(skillDir, proposalsDirName)}
}

func (q *FileQueue) ensureDir() error {
	return os.MkdirAll(q.dir, proposalDirPerm)
}

// Put writes a new proposal and fills ID / CreatedAt when empty.
func (q *FileQueue) Put(p Proposal) (Proposal, error) {
	if q == nil {
		return Proposal{}, fmt.Errorf("proposal queue is nil")
	}
	if err := q.ensureDir(); err != nil {
		return Proposal{}, err
	}
	if p.ID == "" {
		p.ID = newProposalID()
	}
	if p.CreatedAt.IsZero() {
		p.CreatedAt = time.Now().UTC()
	}
	if p.Status == "" {
		p.Status = StatusPending
	}
	if err := q.write(p); err != nil {
		return Proposal{}, err
	}
	return p, nil
}

// Get loads one proposal by id.
func (q *FileQueue) Get(id string) (Proposal, error) {
	if !validProposalID(id) {
		return Proposal{}, &ErrInvalidProposalID{ID: id}
	}
	raw, err := os.ReadFile(q.path(id))
	if err != nil {
		if os.IsNotExist(err) {
			return Proposal{}, &ErrProposalNotFound{ID: id}
		}
		return Proposal{}, err
	}
	var p Proposal
	if err := json.Unmarshal(raw, &p); err != nil {
		return Proposal{}, fmt.Errorf("parse proposal %q: %w", id, err)
	}
	return p, nil
}

// List returns every readable proposal, newest first. Corrupt files are skipped.
func (q *FileQueue) List() ([]Proposal, error) {
	entries, err := os.ReadDir(q.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, err
	}
	var out []Proposal
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(q.dir, e.Name()))
		if err != nil {
			slog.Warn("vv: skip unreadable skill proposal", "file", e.Name(), "error", err)
			continue
		}
		var p Proposal
		if err := json.Unmarshal(raw, &p); err != nil {
			slog.Warn("vv: skip corrupt skill proposal", "file", e.Name(), "error", err)
			continue
		}
		out = append(out, p)
	}
	sort.Slice(out, func(i, j int) bool {
		return out[i].CreatedAt.After(out[j].CreatedAt)
	})
	return out, nil
}

// UpdateStatus sets status (and DecidedAt for terminal states) on an existing proposal.
func (q *FileQueue) UpdateStatus(id string, status ProposalStatus) (Proposal, error) {
	p, err := q.Get(id)
	if err != nil {
		return Proposal{}, err
	}
	p.Status = status
	if status == StatusApproved || status == StatusRejected {
		p.DecidedAt = time.Now().UTC()
	}
	if err := q.write(p); err != nil {
		return Proposal{}, err
	}
	return p, nil
}

func (q *FileQueue) write(p Proposal) error {
	if err := q.ensureDir(); err != nil {
		return err
	}
	raw, err := json.MarshalIndent(p, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(q.path(p.ID), raw, proposalFilePerm)
}

func (q *FileQueue) path(id string) string {
	return filepath.Join(q.dir, id+".json")
}

func newProposalID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return time.Now().UTC().Format("20060102T150405Z") + "-fallback"
	}
	return time.Now().UTC().Format("20060102T150405Z") + "-" + hex.EncodeToString(b[:])
}

func validProposalID(id string) bool {
	if id == "" || strings.ContainsAny(id, `/\`) || strings.Contains(id, "..") {
		return false
	}
	return true
}
