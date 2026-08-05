package setup

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	"github.com/vogo/vv/configs"
)

// buildEnvironment renders the runtime environment block appended to every
// agent's system prompt. maxIterations is the ReAct ceiling of the agent the
// block is built for; 0 omits the budget line.
//
// Everything here is a fact the model cannot infer from the conversation. The
// working directory matters most: file tools reject relative paths, so an
// agent without it spends its first iteration on a failed probe. The
// iteration budget matters second: an agent that does not know its ceiling
// cannot tell "I have room to keep reading" from "I must act now", which is
// how a run ends at the ceiling with nothing delivered.
//
// The date is captured at assembly time. For a CLI session that is exact; a
// long-lived HTTP process may drift by a day, which is acceptable for a hint
// of this kind and keeps the prompt prefix stable for prompt caching.
func buildEnvironment(cfg *configs.Config, maxIterations int) string {
	if cfg == nil {
		return ""
	}

	var sb strings.Builder

	workDir := cfg.Tools.BashWorkingDir
	if workDir != "" {
		fmt.Fprintf(&sb, "- Working directory: %s\n", workDir)
		sb.WriteString("- File tools require absolute paths. Resolve any relative path against the working directory above before calling a tool; do not probe with \".\".\n")
	}

	fmt.Fprintf(&sb, "- Platform: %s\n", runtime.GOOS)
	fmt.Fprintf(&sb, "- Today's date: %s\n", time.Now().Format("2006-01-02"))

	if workDir != "" {
		fmt.Fprintf(&sb, "- Git repository: %s\n", yesNo(isGitRepo(workDir)))
	}

	if cfg.ProjectInstructionsFile != "" {
		fmt.Fprintf(&sb, "- Project instructions file: %s (its content is appended below as Project Instructions)\n",
			cfg.ProjectInstructionsFile)
	}

	if maxIterations > 0 {
		fmt.Fprintf(&sb,
			"- Tool-iteration budget: %d iterations for this run. Investigation should consume only a few of them. "+
				"When the budget runs low, stop investigating: complete the task with the tools you have, delegate it, "+
				"or report what you found and what is left — a run that ends at the ceiling delivers nothing to the user.\n",
			maxIterations)
	}

	return sb.String()
}

// isGitRepo reports whether dir contains a .git entry (directory or file, the
// latter covering worktrees and submodules).
func isGitRepo(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, ".git"))
	return err == nil
}

func yesNo(b bool) string {
	if b {
		return "yes"
	}

	return "no"
}
