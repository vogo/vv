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

// Package tracelog writes a structured JSONL conversation trace to disk by
// subscribing to vage hook events via an AsyncHook. It is the on-disk data
// out-gate that later features (SQLite store, session resume, training-set
// export, replay) consume; it does not implement those features itself.
package tracelog

import (
	"crypto/sha256"
	"encoding/base32"
	"path/filepath"
	"strings"
)

// ProjectHash returns a stable, filesystem-safe token derived from the
// absolute working directory. It is used to bucket trace files by project
// under the base trace directory.
//
// Empty input yields "default". 12 bytes of SHA-256 encoded as lowercase
// base32 without padding gives ~20 characters — ample collision resistance
// for the ~10^3 projects a single user might traverse.
func ProjectHash(workingDir string) string {
	if workingDir == "" {
		return "default"
	}

	abs, err := filepath.Abs(workingDir)
	if err != nil {
		abs = workingDir
	}

	sum := sha256.Sum256([]byte(abs))

	enc := base32.StdEncoding.WithPadding(base32.NoPadding).EncodeToString(sum[:12])

	return strings.ToLower(enc)
}
