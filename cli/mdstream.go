package cli

import (
	"regexp"
	"strings"
	"time"
)

// streamThrottle bounds how often accumulated stream text is re-rendered.
// The live region is redrawn on every spinner tick (~100ms), so a render
// skipped by the throttle is picked up on the next frame at the latest.
const streamThrottle = 60 * time.Millisecond

// streamRenderer re-renders the whole accumulated markdown on a throttle.
//
// This mirrors how the Ink-based agents (Claude Code, Gemini CLI) and the
// rich-based ones (Aider) drive their live region: the dynamic area is
// erased and redrawn every frame, so each frame can render the *complete*
// text received so far. Correctness then comes for free — no incremental
// parser can misread a fence, a table or a list continuation, because
// nothing is ever parsed in isolation. The only cost is CPU, and that is
// what the throttle bounds.
type streamRenderer struct {
	pending  string    // raw text awaiting a render
	width    int       // width the cached render was produced at
	rendered string    // most recent render result
	at       time.Time // when that result was produced
	dirty    bool      // pending/width changed since the last render
}

// set records the currently accumulated text. Cheap enough to call on every
// delta; the actual render is deferred to refresh.
func (s *streamRenderer) set(text string, width int) {
	if text == s.pending && width == s.width {
		return
	}

	s.pending = text
	s.width = width
	s.dirty = true
}

// refresh re-renders if a render is due. Callers drive it from both delta
// arrival and the spinner tick so throttled-away renders still land.
func (s *streamRenderer) refresh(now time.Time, render func(string, int) string) {
	if s.pending == "" {
		s.rendered = ""
		s.dirty = false

		return
	}

	if !s.dirty || now.Sub(s.at) < streamThrottle {
		return
	}

	s.rendered = render(s.pending, s.width)
	s.at = now
	s.dirty = false
}

// view returns the last render. It falls back to the raw text so the very
// first frame of a message shows something rather than a blank gap.
func (s *streamRenderer) view() string {
	if s.rendered == "" {
		return s.pending
	}

	return s.rendered
}

func (s *streamRenderer) reset() {
	*s = streamRenderer{}
}

// fenceRe matches the opening run of a fenced code block.
var fenceRe = regexp.MustCompile("^(`{3,}|~{3,})")

// listItemRe matches a bullet or ordered list marker.
var listItemRe = regexp.MustCompile(`^([-*+]\s|\d+[.)]\s)`)

// splitAtSafeBoundary splits markdown at the last blank line that is
// provably a block boundary: outside any fenced code block, and not sitting
// between two items of the same list.
//
// Only the overflow path calls this — the steady state never splits. A
// boundary missed here costs at most some ragged wrapping in scrollback; it
// can never cause the live region to misparse anything, because the live
// region always renders the full remaining text.
func splitAtSafeBoundary(text string) (commit, rest string) {
	lines := strings.Split(text, "\n")

	fence := ""
	candidates := []int{}

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)

		if fence != "" {
			if isFenceClose(trimmed, fence) {
				fence = ""
			}

			continue
		}

		if f := fenceRe.FindString(trimmed); f != "" {
			fence = f

			continue
		}

		if trimmed == "" {
			candidates = append(candidates, i)
		}
	}

	// An unclosed fence at the end does not invalidate boundaries found
	// before it opened — those blocks are already complete.
	for i := len(candidates) - 1; i >= 0; i-- {
		at := candidates[i]
		if splitsList(lines, at) {
			continue
		}

		commit = strings.TrimRight(strings.Join(lines[:at], "\n"), "\n")
		if commit == "" {
			continue
		}

		return commit, strings.Join(lines[at+1:], "\n")
	}

	return "", text
}

// isFenceClose reports whether trimmed closes a fence opened by open: same
// fence character, at least as long, and nothing but the run on the line.
func isFenceClose(trimmed, open string) bool {
	match := fenceRe.FindString(trimmed)
	if match == "" {
		return false
	}

	return match[0] == open[0] &&
		len(match) >= len(open) &&
		strings.TrimSpace(trimmed[len(match):]) == ""
}

// splitsList reports whether the blank line at index at falls between two
// items of one list, where cutting would renumber or respace the tail.
func splitsList(lines []string, at int) bool {
	return prevIsListItem(lines, at) && nextIsListItem(lines, at)
}

func prevIsListItem(lines []string, at int) bool {
	for i := at - 1; i >= 0; i-- {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}

		return listItemRe.MatchString(trimmed) || strings.HasPrefix(lines[i], "  ")
	}

	return false
}

func nextIsListItem(lines []string, at int) bool {
	for i := at + 1; i < len(lines); i++ {
		trimmed := strings.TrimSpace(lines[i])
		if trimmed == "" {
			continue
		}

		return listItemRe.MatchString(trimmed) || strings.HasPrefix(lines[i], "  ")
	}

	return false
}

// tailLines keeps the last max lines of text, so a block too large to
// commit (a single huge code fence, say) still cannot outgrow the screen.
func tailLines(text string, max int) string {
	if max <= 0 {
		return text
	}

	lines := strings.Split(text, "\n")
	if len(lines) <= max {
		return text
	}

	return strings.Join(lines[len(lines)-max:], "\n")
}
