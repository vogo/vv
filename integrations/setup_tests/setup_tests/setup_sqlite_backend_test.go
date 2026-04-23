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

package setup_tests

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/setup"
)

// writeSQLiteConfig writes a minimal vv.yaml into dir that selects the
// sqlite memory backend and returns the config file path.
func writeSQLiteConfig(t *testing.T, memDir string) string {
	t.Helper()
	cfgPath := filepath.Join(t.TempDir(), "vv.yaml")
	content := `llm:
  provider: openai
  model: test-model
  api_key: test-key
  base_url: http://127.0.0.1:0
memory:
  backend: sqlite
  dir: ` + memDir + `
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return cfgPath
}

// TestIntegration_SetupInit_SQLiteBackend verifies that cfg.Memory.Backend="sqlite"
// wires NewSQLiteStore into setup.Init instead of NewFileStore: the DB file
// is created with WAL journal mode enabled, and Shutdown closes the store
// cleanly.
func TestIntegration_SetupInit_SQLiteBackend(t *testing.T) {
	memDir := filepath.Join(t.TempDir(), "memory")
	cfgPath := writeSQLiteConfig(t, memDir)

	cfg, err := configs.Load(cfgPath, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}
	if cfg.Memory.Backend != configs.MemoryBackendSQLite {
		t.Fatalf("Memory.Backend = %q, want %q", cfg.Memory.Backend, configs.MemoryBackendSQLite)
	}

	init, err := setup.Init(cfg, nil)
	if err != nil {
		t.Fatalf("setup.Init: %v", err)
	}
	defer init.Shutdown(context.Background())

	// The SQLite DB and WAL sidecar files should exist under the configured
	// memory dir once Init has opened the store.
	dbPath := filepath.Join(memDir, "memory.db")
	if _, statErr := os.Stat(dbPath); statErr != nil {
		t.Errorf("expected DB file at %s: %v", dbPath, statErr)
	}
	if _, statErr := os.Stat(dbPath + "-wal"); statErr != nil {
		t.Errorf("expected WAL file at %s-wal: %v", dbPath, statErr)
	}

	// And writing through the persistent memory exposed by Init should
	// roundtrip: this is the end-to-end proof that setup wired the SQLite
	// store into the memory manager.
	ctx := context.Background()
	if err := init.PersistentMem.Set(ctx, "project:arch", "layered", 0); err != nil {
		t.Fatalf("PersistentMem.Set: %v", err)
	}
	val, err := init.PersistentMem.Get(ctx, "project:arch")
	if err != nil {
		t.Fatalf("PersistentMem.Get: %v", err)
	}
	if val != "layered" {
		t.Errorf("Get returned %v, want %q", val, "layered")
	}
}

// TestIntegration_SetupInit_FileBackendDefault verifies the default backend
// remains FileStore (no DB file is created) when cfg.Memory.Backend is empty.
func TestIntegration_SetupInit_FileBackendDefault(t *testing.T) {
	memDir := filepath.Join(t.TempDir(), "memory")
	cfgPath := filepath.Join(t.TempDir(), "vv.yaml")
	content := `llm:
  provider: openai
  model: test-model
  api_key: test-key
  base_url: http://127.0.0.1:0
memory:
  dir: ` + memDir + `
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := configs.Load(cfgPath, true)
	if err != nil {
		t.Fatalf("configs.Load: %v", err)
	}
	if cfg.Memory.Backend != configs.MemoryBackendFile {
		t.Fatalf("Memory.Backend = %q, want %q (default)", cfg.Memory.Backend, configs.MemoryBackendFile)
	}

	init, err := setup.Init(cfg, nil)
	if err != nil {
		t.Fatalf("setup.Init: %v", err)
	}
	defer init.Shutdown(context.Background())

	// FileStore backend: no SQLite DB file should be created.
	dbPath := filepath.Join(memDir, "memory.db")
	if _, statErr := os.Stat(dbPath); statErr == nil {
		t.Errorf("unexpected SQLite DB file at %s with file backend", dbPath)
	}
}

// TestIntegration_SetupInit_SQLiteBackend_RejectsUnknown verifies AC-1.4: an
// unsupported memory.backend value is rejected at config-load time with a
// clear error message. This guards against typos like "sqllite".
func TestIntegration_SetupInit_SQLiteBackend_RejectsUnknown(t *testing.T) {
	memDir := filepath.Join(t.TempDir(), "memory")
	cfgPath := filepath.Join(t.TempDir(), "vv.yaml")
	content := `llm:
  provider: openai
  model: test-model
  api_key: test-key
  base_url: http://127.0.0.1:0
memory:
  backend: bogus
  dir: ` + memDir + `
`
	if err := os.WriteFile(cfgPath, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	_, err := configs.Load(cfgPath, true)
	if err == nil {
		t.Fatal("configs.Load: expected error for bogus backend; got nil")
	}
	if !strings.Contains(err.Error(), `unknown memory backend "bogus"`) {
		t.Errorf("error = %v; want to mention unknown memory backend \"bogus\"", err)
	}
}

// TestIntegration_SetupInit_SQLiteBackend_NoAutoImport verifies AC-4.2: when an
// operator flips `memory.backend` from file to sqlite, there is no automatic
// import of FileStore JSON-per-file data. The SQLite store starts empty; the
// legacy JSON files remain on disk (so the operator can flip back or run a
// dedicated migrator later).
func TestIntegration_SetupInit_SQLiteBackend_NoAutoImport(t *testing.T) {
	memDir := filepath.Join(t.TempDir(), "memory")

	// --- Phase 1: Set up FileStore and write an entry through setup.Init.
	fileCfgPath := filepath.Join(t.TempDir(), "vv-file.yaml")
	fileContent := `llm:
  provider: openai
  model: test-model
  api_key: test-key
  base_url: http://127.0.0.1:0
memory:
  dir: ` + memDir + `
`
	if err := os.WriteFile(fileCfgPath, []byte(fileContent), 0o600); err != nil {
		t.Fatalf("write file config: %v", err)
	}
	fileCfg, err := configs.Load(fileCfgPath, true)
	if err != nil {
		t.Fatalf("configs.Load (file): %v", err)
	}
	if fileCfg.Memory.Backend != configs.MemoryBackendFile {
		t.Fatalf("expected file backend, got %q", fileCfg.Memory.Backend)
	}
	fileInit, err := setup.Init(fileCfg, nil)
	if err != nil {
		t.Fatalf("setup.Init (file): %v", err)
	}
	ctx := context.Background()
	if err := fileInit.PersistentMem.Set(ctx, "project:arch", "file-side", 0); err != nil {
		fileInit.Shutdown(context.Background())
		t.Fatalf("file PersistentMem.Set: %v", err)
	}
	fileInit.Shutdown(context.Background())

	// Sanity check: FileStore wrote a JSON record on disk.
	projDir := filepath.Join(memDir, "project")
	if _, statErr := os.Stat(projDir); statErr != nil {
		t.Fatalf("expected FileStore-written ns dir at %s: %v", projDir, statErr)
	}

	// --- Phase 2: Flip backend to sqlite against the SAME memDir.
	sqliteCfgPath := writeSQLiteConfig(t, memDir)
	sqliteCfg, err := configs.Load(sqliteCfgPath, true)
	if err != nil {
		t.Fatalf("configs.Load (sqlite): %v", err)
	}
	if sqliteCfg.Memory.Backend != configs.MemoryBackendSQLite {
		t.Fatalf("expected sqlite backend, got %q", sqliteCfg.Memory.Backend)
	}
	sqliteInit, err := setup.Init(sqliteCfg, nil)
	if err != nil {
		t.Fatalf("setup.Init (sqlite): %v", err)
	}
	defer sqliteInit.Shutdown(context.Background())

	// The FileStore-written JSON tree must still exist on disk (no migration,
	// no destructive cleanup).
	if _, statErr := os.Stat(projDir); statErr != nil {
		t.Errorf("FileStore data tree unexpectedly removed at %s: %v", projDir, statErr)
	}

	// The SQLite store must start empty — no automatic import.
	val, err := sqliteInit.PersistentMem.Get(ctx, "project:arch")
	if err != nil {
		t.Fatalf("sqlite PersistentMem.Get: %v", err)
	}
	if val != nil {
		t.Errorf("sqlite store leaked FileStore data: got %v, want nil (documented no-auto-import)", val)
	}

	// And the operator can write fresh data into the SQLite store.
	if err := sqliteInit.PersistentMem.Set(ctx, "project:arch", "sqlite-side", 0); err != nil {
		t.Fatalf("sqlite PersistentMem.Set: %v", err)
	}
	val, err = sqliteInit.PersistentMem.Get(ctx, "project:arch")
	if err != nil {
		t.Fatalf("sqlite PersistentMem.Get (after set): %v", err)
	}
	if val != "sqlite-side" {
		t.Errorf("sqlite PersistentMem.Get = %v, want %q", val, "sqlite-side")
	}
}
