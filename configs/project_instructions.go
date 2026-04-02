package configs

import (
	"log/slog"
	"os"
	"path/filepath"
)

// ProjectInstructionsFileName is the expected name of the project instructions file.
const ProjectInstructionsFileName = "VV.md"

// LoadProjectInstructions reads the VV.md file from the given directory.
// Returns the file content, or an empty string if the file does not exist or is empty.
// Logs a warning and returns empty string on read errors (permissions, I/O).
func LoadProjectInstructions(dir string) string {
	path := filepath.Join(dir, ProjectInstructionsFileName)

	data, err := os.ReadFile(path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("failed to read project instructions file", "path", path, "error", err)
		}

		return ""
	}

	return string(data)
}
