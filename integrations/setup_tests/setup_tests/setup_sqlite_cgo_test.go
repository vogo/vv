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
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// findVVModuleRoot walks upward from the current test source file until it
// finds the vv/ module directory (identified by go.mod containing
// "module github.com/vogo/vv").
func findVVModuleRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller failed")
	}
	dir := filepath.Dir(file)
	for i := 0; i < 20; i++ {
		goMod := filepath.Join(dir, "go.mod")
		if data, err := os.ReadFile(goMod); err == nil {
			if strings.Contains(string(data), "module github.com/vogo/vv") {
				return dir
			}
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
	t.Fatal("could not locate vv module root")
	return ""
}

// TestIntegration_SQLiteStore_CGODisabledBuild verifies AC-4.3: adding
// modernc.org/sqlite (a pure-Go SQLite driver) as the backend for SQLiteStore
// must not pull in any CGO-requiring transitive dependency. We build the
// entire vv module under CGO_ENABLED=0 and assert zero errors.
//
// This test is slow (a full go build of vv) — O(seconds) on SSD — so it lives
// in the integrations suite rather than a unit test. Skipped in -short mode.
func TestIntegration_SQLiteStore_CGODisabledBuild(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping slow CGO_ENABLED=0 build in -short mode")
	}
	if _, err := exec.LookPath("go"); err != nil {
		t.Skipf("go toolchain not on PATH: %v", err)
	}

	moduleDir := findVVModuleRoot(t)

	// Build to a throwaway location so we don't pollute the module tree. -o
	// with a dir-style path tells `go build` to drop the resulting binaries
	// (one per main package) under that dir.
	outDir := t.TempDir()

	cmd := exec.Command("go", "build", "-o", filepath.Join(outDir, "bin")+string(os.PathSeparator), "./...")
	cmd.Dir = moduleDir
	// Force CGO off; everything else comes from the parent env so go modules
	// / proxy caches continue to work.
	cmd.Env = append(os.Environ(), "CGO_ENABLED=0")

	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("CGO_ENABLED=0 go build ./... failed: %v\n%s", err, string(out))
	}
}
