package memories

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/vogo/vage/memory"
)

// FileStore persists key-value pairs as individual JSON files in a directory.
// Keys are sanitized to safe filenames. The store is not safe for concurrent
// use; wrap with PersistentMemory (which uses syncMemory) for thread safety.
type FileStore struct {
	dir string
}

// fileRecord is the JSON-serialized format of a stored entry.
type fileRecord struct {
	Key       string    `json:"key"`
	Value     string    `json:"value"`
	Namespace string    `json:"namespace"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
	TTL       int64     `json:"ttl"`
}

// Compile-time check: FileStore implements memory.Store.
var _ memory.Store = (*FileStore)(nil)

// NewFileStore creates a new FileStore rooted at dir.
// The directory is created if it does not exist.
func NewFileStore(dir string) (*FileStore, error) {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, fmt.Errorf("filestore: create directory %s: %w", dir, err)
	}
	return &FileStore{dir: dir}, nil
}

// parseKey splits a key into namespace and name.
// Format: "namespace:key" -> ("namespace", "key").
// If no colon, namespace is "default".
func parseKey(key string) (namespace, name string) {
	if before, after, ok := strings.Cut(key, ":"); ok {
		return before, after
	}
	return "default", key
}

// sanitize replaces characters that are unsafe in filenames.
func sanitize(s string) string {
	return strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9',
			r == '-', r == '_', r == '.':
			return r
		default:
			return '_'
		}
	}, s)
}

// filePath returns the filesystem path for a given key.
func (s *FileStore) filePath(key string) string {
	ns, name := parseKey(key)
	return filepath.Join(s.dir, sanitize(ns), sanitize(name)+".json")
}

func (s *FileStore) Get(_ context.Context, key string) (any, bool, error) {
	data, err := os.ReadFile(s.filePath(key))
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, fmt.Errorf("filestore: read %s: %w", key, err)
	}

	var rec fileRecord
	if err := json.Unmarshal(data, &rec); err != nil {
		return nil, false, fmt.Errorf("filestore: unmarshal %s: %w", key, err)
	}

	// Check TTL expiry.
	if rec.TTL > 0 && time.Since(rec.UpdatedAt) > time.Duration(rec.TTL)*time.Second {
		_ = os.Remove(s.filePath(key))
		return nil, false, nil
	}

	return rec.Value, true, nil
}

func (s *FileStore) Set(_ context.Context, key string, value any, ttl int64) error {
	ns, _ := parseKey(key)
	fp := s.filePath(key)

	// Create namespace directory.
	if err := os.MkdirAll(filepath.Dir(fp), 0o700); err != nil {
		return fmt.Errorf("filestore: create namespace dir: %w", err)
	}

	// Check if existing record has a created_at we should preserve.
	now := time.Now()
	createdAt := now

	existing, err := os.ReadFile(fp)
	if err == nil {
		var old fileRecord
		if json.Unmarshal(existing, &old) == nil {
			createdAt = old.CreatedAt
		}
	}

	// Convert value to string.
	var strValue string
	switch v := value.(type) {
	case string:
		strValue = v
	default:
		b, err := json.Marshal(v)
		if err != nil {
			return fmt.Errorf("filestore: marshal value: %w", err)
		}
		strValue = string(b)
	}

	rec := fileRecord{
		Key:       key,
		Value:     strValue,
		Namespace: ns,
		CreatedAt: createdAt,
		UpdatedAt: now,
		TTL:       ttl,
	}

	data, err := json.MarshalIndent(rec, "", "  ")
	if err != nil {
		return fmt.Errorf("filestore: marshal record: %w", err)
	}

	if err := os.WriteFile(fp, data, 0o600); err != nil {
		return fmt.Errorf("filestore: write %s: %w", key, err)
	}

	return nil
}

func (s *FileStore) Delete(_ context.Context, key string) error {
	fp := s.filePath(key)
	if err := os.Remove(fp); err != nil && !os.IsNotExist(err) {
		return fmt.Errorf("filestore: delete %s: %w", key, err)
	}
	return nil
}

func (s *FileStore) List(_ context.Context, prefix string) ([]memory.StoreEntry, error) {
	var entries []memory.StoreEntry

	// Walk all namespace directories.
	nsDirs, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return entries, nil
		}
		return nil, fmt.Errorf("filestore: read dir: %w", err)
	}

	for _, nsDir := range nsDirs {
		if !nsDir.IsDir() {
			continue
		}

		nsPath := filepath.Join(s.dir, nsDir.Name())
		files, err := os.ReadDir(nsPath)
		if err != nil {
			continue
		}

		for _, f := range files {
			if f.IsDir() || !strings.HasSuffix(f.Name(), ".json") {
				continue
			}

			data, err := os.ReadFile(filepath.Join(nsPath, f.Name()))
			if err != nil {
				continue
			}

			var rec fileRecord
			if err := json.Unmarshal(data, &rec); err != nil {
				continue
			}

			// Check TTL expiry.
			if rec.TTL > 0 && time.Since(rec.UpdatedAt) > time.Duration(rec.TTL)*time.Second {
				_ = os.Remove(filepath.Join(nsPath, f.Name()))
				continue
			}

			if prefix != "" && !strings.HasPrefix(rec.Key, prefix) {
				continue
			}

			entries = append(entries, memory.StoreEntry{
				Key:       rec.Key,
				Value:     rec.Value,
				CreatedAt: rec.CreatedAt,
				TTL:       rec.TTL,
			})
		}
	}

	return entries, nil
}

func (s *FileStore) Clear(_ context.Context) error {
	// Remove all contents of the directory.
	entries, err := os.ReadDir(s.dir)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("filestore: read dir for clear: %w", err)
	}

	for _, entry := range entries {
		p := filepath.Join(s.dir, entry.Name())
		if err := os.RemoveAll(p); err != nil {
			return fmt.Errorf("filestore: clear %s: %w", p, err)
		}
	}

	return nil
}
