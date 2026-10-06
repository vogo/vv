package skillevolve

import (
	"os"
	"path/filepath"
	"testing"
)

func TestFileQueue_RoundTrip(t *testing.T) {
	q := NewFileQueue(t.TempDir())
	saved, err := q.Put(Proposal{Status: StatusPending, SessionID: "s1", Extracted: ExtractedSkill{Name: "n"}})
	if err != nil {
		t.Fatal(err)
	}
	if saved.ID == "" {
		t.Fatal("empty id")
	}
	got, err := q.Get(saved.ID)
	if err != nil {
		t.Fatal(err)
	}
	if got.Extracted.Name != "n" {
		t.Errorf("name = %q", got.Extracted.Name)
	}
	info, err := os.Stat(filepath.Join(q.dir, saved.ID+".json"))
	if err != nil {
		t.Fatal(err)
	}
	if info.Mode().Perm() != proposalFilePerm {
		t.Logf("proposal file mode = %o (umask may differ)", info.Mode().Perm())
	}

	updated, err := q.UpdateStatus(saved.ID, StatusApproved)
	if err != nil {
		t.Fatal(err)
	}
	if updated.Status != StatusApproved || updated.DecidedAt.IsZero() {
		t.Errorf("update = %+v", updated)
	}

	list, err := q.List()
	if err != nil || len(list) != 1 {
		t.Fatalf("list = %v err=%v", list, err)
	}
}

func TestFileQueue_SkipsBadJSON(t *testing.T) {
	dir := t.TempDir()
	q := NewFileQueue(dir)
	if _, err := q.Put(Proposal{Status: StatusPending, Extracted: ExtractedSkill{Name: "ok"}}); err != nil {
		t.Fatal(err)
	}
	bad := filepath.Join(dir, proposalsDirName, "bad.json")
	if err := os.WriteFile(bad, []byte("{not json"), 0o600); err != nil {
		t.Fatal(err)
	}
	list, err := q.List()
	if err != nil {
		t.Fatal(err)
	}
	if len(list) != 1 {
		t.Errorf("list len = %d, want 1 (bad skipped)", len(list))
	}
}

func TestFileQueue_GetMissing(t *testing.T) {
	q := NewFileQueue(t.TempDir())
	_, err := q.Get("20260101T000000Z-deadbeef")
	if _, ok := err.(*ErrProposalNotFound); !ok {
		t.Fatalf("err = %v, want ErrProposalNotFound", err)
	}
}

func TestFileQueue_InvalidID(t *testing.T) {
	q := NewFileQueue(t.TempDir())
	if _, err := q.Get("../etc/passwd"); err == nil {
		t.Fatal("expected invalid id")
	}
}
