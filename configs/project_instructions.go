package configs

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
)

// ProjectInstructionsFileName is the canonical name of the project
// instructions file. Kept for backward compatibility with callers and tests
// that reference the single-file contract; the effective lookup order is
// DefaultProjectInstructionsFiles.
const ProjectInstructionsFileName = "VV.md"

// DefaultProjectInstructionsFiles is the lookup order used when
// `project_instructions_files` is not configured. The first readable,
// non-empty file wins — vv's own convention first, then the two filenames the
// wider agent ecosystem has standardised on. Without the AGENTS.md / CLAUDE.md
// fallbacks a repository that carries only those files silently ran with an
// empty project-instruction section, which is exactly how a project's "basic
// rules" end up being ignored by every agent in the process.
var DefaultProjectInstructionsFiles = []string{"VV.md", "AGENTS.md", "CLAUDE.md"}

// LoadProjectInstructions reads the first available project instructions file
// from the given directory, following DefaultProjectInstructionsFiles.
// Returns the file content, or an empty string when no candidate exists.
func LoadProjectInstructions(dir string) string {
	content, _ := LoadProjectInstructionsFrom(dir, nil)
	return content
}

// LoadProjectInstructionsFrom reads the first available project instructions
// file from dir, trying each name in candidates in order. Empty candidates
// fall back to DefaultProjectInstructionsFiles. It returns the file content
// and the base name of the file it came from; both are empty when nothing
// matched.
//
// A candidate that exists but is empty (or whitespace-only) is skipped rather
// than accepted, so an empty placeholder file cannot mask a real instruction
// file later in the list. Read errors other than "not exist" are logged and
// skipped — a permissions problem on one candidate must not hide the next.
func LoadProjectInstructionsFrom(dir string, candidates []string) (content, fileName string) {
	if len(candidates) == 0 {
		candidates = DefaultProjectInstructionsFiles
	}

	seen := make(map[string]struct{}, len(candidates))

	for _, name := range candidates {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}

		if _, dup := seen[name]; dup {
			continue
		}

		seen[name] = struct{}{}

		path := filepath.Join(dir, name)

		data, err := os.ReadFile(path)
		if err != nil {
			if !os.IsNotExist(err) {
				slog.Warn("failed to read project instructions file", "path", path, "error", err)
			}

			continue
		}

		if strings.TrimSpace(string(data)) == "" {
			continue
		}

		return string(data), name
	}

	return "", ""
}
