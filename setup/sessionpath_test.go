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
	"strings"
	"testing"
)

func TestSessionProjectName(t *testing.T) {
	cases := []struct {
		name string
		in   string
		want string
	}{
		// Empty input falls back to "default" so the store always has a bucket.
		{"empty falls back to default", "", "default"},
		// A canonical Unix project path: slashes → underscores, alnum kept.
		{"unix path slashes", "/Users/hk/workspaces/vv", "_Users_hk_workspaces_vv"},
		// Spaces and other punctuation are non-alnum and become hyphens.
		{"space and dot become hyphens", "/Users/hk/foo bar/v.0", "_Users_hk_foo-bar_v-0"},
		// Mixed punctuation: every non-alnum, non-separator rune is a hyphen.
		{"mixed punctuation", "/a/@b#c/d", "_a_-b-c_d"},
		// Backslashes embedded in an absolute Unix path are also treated as
		// separators (defensive — real Windows paths take this branch via
		// filepath.Abs on Windows hosts).
		{"backslash treated as separator", `/proj\sub`, "_proj_sub"},
		// Only alnum input is kept verbatim and considered already absolute on
		// some shells; filepath.Abs may prepend cwd, so we just check the suffix.
		{"alnum-only relative input", "abc123", ""}, // checked specially below
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := SessionProjectName(c.in)
			if c.in == "abc123" {
				// filepath.Abs will prepend the cwd; we only require the trailing
				// alnum segment to survive intact.
				if !strings.HasSuffix(got, "abc123") {
					t.Errorf("got %q, want suffix abc123", got)
				}
				return
			}
			if got != c.want {
				t.Errorf("SessionProjectName(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}
