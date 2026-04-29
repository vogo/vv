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

package setup

import (
	"path/filepath"
	"strings"
)

// SessionProjectName turns an absolute project working directory into a
// filesystem-safe bucket name used by the persistent session store.
//
// Rules:
//   - Path separators ('/' and '\\' for cross-platform safety) become '_'.
//   - ASCII letters and digits are kept verbatim.
//   - Every other rune is replaced by '-'.
//
// Empty input yields "default", matching tracelog's ProjectHash convention so
// projects without a configured working directory still land in a stable
// bucket. The output is human-readable on purpose — debug operators can match
// on-disk session directories back to their project paths without a hashing
// step. Collisions across projects are possible (e.g. paths differing only in
// non-alphanumeric punctuation) but acceptable for the local-only file store
// MVP; tracelog uses a hash for exactly the cases where collision-resistance
// matters more than legibility.
func SessionProjectName(workingDir string) string {
	if workingDir == "" {
		return "default"
	}

	abs, err := filepath.Abs(workingDir)
	if err != nil {
		abs = workingDir
	}

	var sb strings.Builder
	sb.Grow(len(abs))

	for _, r := range abs {
		switch {
		case r == '/' || r == '\\':
			sb.WriteByte('_')
		case (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9'):
			sb.WriteRune(r)
		default:
			sb.WriteByte('-')
		}
	}

	out := sb.String()
	if out == "" {
		return "default"
	}
	return out
}
