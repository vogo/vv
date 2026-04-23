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

package memories

// defaultSharedNamespaces mirrors the four values defined in
// doc/prd/dictionaries/core/dictionary-memory-namespace.md, plus the
// implicit "default" namespace used when keys are passed without a prefix.
// Namespaces not in this set are treated as session-private.
var defaultSharedNamespaces = map[string]struct{}{
	"project":     {},
	"user":        {},
	"conventions": {},
	"notes":       {},
	"default":     {},
}

// isShared reports whether ns is a cross-session shared namespace.
func isShared(ns string, extra map[string]struct{}) bool {
	if _, ok := defaultSharedNamespaces[ns]; ok {
		return true
	}
	if extra == nil {
		return false
	}
	_, ok := extra[ns]
	return ok
}

// IsSharedNamespace reports whether ns is globally shared across sessions.
// Exposed so CLI/HTTP entry points can pre-validate user-path writes before
// reaching the store layer.
func IsSharedNamespace(ns string) bool {
	return isShared(ns, nil)
}
