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

package cli

import (
	"context"
	"errors"
	"fmt"
	"io"

	vctx "github.com/vogo/vage/context"
	"github.com/vogo/vage/session/tree"
)

// PrintTree renders the SessionTree for sessionID using the same renderer
// the LLM sees in its prompt. includePromoted=true surfaces folded subtrees
// (otherwise the default rendering hides them and reports a fold count).
//
// Returns an explicit message when no tree exists for the session — this
// is more useful than a stack of wrapped sentinel errors when a user typo's
// the id from the command line.
func PrintTree(ctx context.Context, store tree.SessionTreeStore, sessionID string, includePromoted bool, w io.Writer) error {
	if store == nil {
		return errors.New("vv: session tree subsystem is disabled (set session_tree.enabled: true)")
	}

	src := &vctx.SessionTreeSource{
		Store:           store,
		IncludePromoted: includePromoted,
	}
	res, err := src.Fetch(ctx, vctx.FetchInput{SessionID: sessionID})
	if err != nil {
		return fmt.Errorf("fetch tree: %w", err)
	}

	if len(res.Messages) == 0 {
		// Source signalled "skipped" — most likely tree missing. Print a
		// crisp message so the user knows they're not looking at a bug.
		_, _ = fmt.Fprintf(w, "(no tree for session %q)\n", sessionID)
		return nil
	}

	for _, m := range res.Messages {
		text := m.Content.Text()
		if text == "" {
			continue
		}
		if _, werr := fmt.Fprintln(w, text); werr != nil {
			return werr
		}
	}
	return nil
}
