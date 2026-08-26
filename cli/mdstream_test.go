package cli

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/vogo/vv/configs"
)

func TestSplitAtSafeBoundary(t *testing.T) {
	tests := []struct {
		name       string
		text       string
		wantCommit string
		wantRest   string
	}{
		{
			name:       "no blank line yields nothing committable",
			text:       "one line only",
			wantCommit: "",
			wantRest:   "one line only",
		},
		{
			name:       "splits at the last blank line",
			text:       "para one\n\npara two\n\npara three",
			wantCommit: "para one\n\npara two",
			wantRest:   "para three",
		},
		{
			name:       "never splits inside a fenced block",
			text:       "intro\n\n```go\nfunc a() {\n\n}\n```\ntail",
			wantCommit: "intro",
			wantRest:   "```go\nfunc a() {\n\n}\n```\ntail",
		},
		{
			name:       "an unclosed fence keeps earlier boundaries valid",
			text:       "intro\n\n```go\nfunc a() {\n\nstill streaming",
			wantCommit: "intro",
			wantRest:   "```go\nfunc a() {\n\nstill streaming",
		},
		{
			name:       "tilde fence is honoured too",
			text:       "intro\n\n~~~\nblank\n\nline\n~~~\ntail",
			wantCommit: "intro",
			wantRest:   "~~~\nblank\n\nline\n~~~\ntail",
		},
		{
			name:       "a longer fence does not close a shorter one",
			text:       "intro\n\n```\ncode\n\nmore\n````\ntail\n\nafter",
			wantCommit: "intro\n\n```\ncode\n\nmore\n````\ntail",
			wantRest:   "after",
		},
		{
			name:       "does not split between items of one list",
			text:       "intro\n\n- first\n\n- second",
			wantCommit: "intro",
			wantRest:   "- first\n\n- second",
		},
		{
			name:       "splits after a list ends",
			text:       "- first\n- second\n\nclosing para\n\ntail",
			wantCommit: "- first\n- second\n\nclosing para",
			wantRest:   "tail",
		},
		{
			name:       "does not split an ordered list, which would renumber it",
			text:       "intro\n\n1. first\n\n2. second",
			wantCommit: "intro",
			wantRest:   "1. first\n\n2. second",
		},
		{
			name:       "leading blank lines produce no empty commit",
			text:       "\n\nfirst real content",
			wantCommit: "",
			wantRest:   "\n\nfirst real content",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			commit, rest := splitAtSafeBoundary(tt.text)
			if commit != tt.wantCommit {
				t.Errorf("commit = %q, want %q", commit, tt.wantCommit)
			}

			if rest != tt.wantRest {
				t.Errorf("rest = %q, want %q", rest, tt.wantRest)
			}
		})
	}
}

// TestSplitAtSafeBoundary_LosesNothing is the invariant that matters most:
// a split must never drop or duplicate content, or the conversation history
// assembled from committed+pending would diverge from what the model said.
func TestSplitAtSafeBoundary_LosesNothing(t *testing.T) {
	texts := []string{
		"para one\n\npara two\n\npara three",
		"intro\n\n```go\ncode\n\nmore\n```\n\ntail",
		"- a\n- b\n\ntext\n\n| h |\n|---|\n| c |\n\nend",
	}

	for _, text := range texts {
		commit, rest := splitAtSafeBoundary(text)
		if commit == "" {
			continue
		}

		joined := commit + "\n\n" + rest
		if stripBlank(joined) != stripBlank(text) {
			t.Errorf("split lost content:\n got %q\nwant %q", joined, text)
		}
	}
}

// stripBlank normalizes away blank-line differences at the split seam.
func stripBlank(s string) string {
	var kept []string

	for line := range strings.SplitSeq(s, "\n") {
		if strings.TrimSpace(line) != "" {
			kept = append(kept, line)
		}
	}

	return strings.Join(kept, "\n")
}

func TestStreamRenderer_Throttle(t *testing.T) {
	calls := 0
	render := func(text string, _ int) string {
		calls++

		return "R:" + text
	}

	base := time.Unix(1000, 0)

	var s streamRenderer

	// First render always happens: the zero timestamp is far in the past.
	s.set("a", 80)
	s.refresh(base, render)

	if calls != 1 || s.view() != "R:a" {
		t.Fatalf("first refresh: calls = %d, view = %q", calls, s.view())
	}

	// Within the throttle window the cached render is reused.
	s.set("ab", 80)
	s.refresh(base.Add(streamThrottle/2), render)

	if calls != 1 {
		t.Errorf("throttled refresh rendered again: calls = %d", calls)
	}

	if s.view() != "R:a" {
		t.Errorf("throttled view = %q, want the cached %q", s.view(), "R:a")
	}

	// Past the window, the pending text lands.
	s.refresh(base.Add(streamThrottle*2), render)

	if calls != 2 || s.view() != "R:ab" {
		t.Errorf("after window: calls = %d, view = %q", calls, s.view())
	}

	// Unchanged text does not re-render even long after.
	s.set("ab", 80)
	s.refresh(base.Add(time.Hour), render)

	if calls != 2 {
		t.Errorf("unchanged text re-rendered: calls = %d", calls)
	}

	// A width change is a real change and must re-render.
	s.set("ab", 40)
	s.refresh(base.Add(2*time.Hour), render)

	if calls != 3 {
		t.Errorf("width change did not re-render: calls = %d", calls)
	}
}

func TestStreamRenderer_ViewFallsBackToRaw(t *testing.T) {
	var s streamRenderer

	s.set("raw text", 80)

	if got := s.view(); got != "raw text" {
		t.Errorf("view before first render = %q, want the raw text", got)
	}
}

func TestStreamRenderer_Reset(t *testing.T) {
	var s streamRenderer

	s.set("a", 80)
	s.refresh(time.Unix(1000, 0), func(text string, _ int) string { return text })
	s.reset()

	if s.view() != "" {
		t.Errorf("view after reset = %q, want empty", s.view())
	}
}

func TestTailLines(t *testing.T) {
	tests := []struct {
		name string
		text string
		max  int
		want string
	}{
		{name: "shorter than limit is untouched", text: "a\nb", max: 5, want: "a\nb"},
		{name: "keeps the last lines", text: "a\nb\nc\nd", max: 2, want: "c\nd"},
		{name: "exactly at the limit is untouched", text: "a\nb", max: 2, want: "a\nb"},
		{name: "non-positive limit disables trimming", text: "a\nb\nc", max: 0, want: "a\nb\nc"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tailLines(tt.text, tt.max); got != tt.want {
				t.Errorf("tailLines(%q, %d) = %q, want %q", tt.text, tt.max, got, tt.want)
			}
		})
	}
}

// TestStreamOverflow_PreservesFullText covers the invariant that the overflow
// path must not break: text moved to scrollback leaves m.output, so anything
// reading m.output alone would silently truncate the turn. Conversation
// history and the DisplayMessage entry both have to see the whole message.
func TestStreamOverflow_PreservesFullText(t *testing.T) {
	app := New(&stubStreamAgent{id: "orchestrator"}, &configs.Config{}, nil, nil, nil)
	m := newModel(app, context.Background())
	m.width = 80
	m.height = 20 // maxLiveLines() == 12

	// Twenty short paragraphs: far past the live budget, with a safe
	// boundary (a blank line outside any fence) after every one of them.
	var sent strings.Builder

	for i := range 20 {
		delta := "paragraph " + string(rune('a'+i)) + "\n\n"
		sent.WriteString(delta)
		m.output.WriteString(delta)
		m.syncStream()
		// Force a render each round; the throttle would otherwise leave
		// stream.rendered empty and overflow would never trigger.
		m.stream.refresh(time.Now().Add(time.Duration(i+1)*time.Second), renderAgentMessage)
	}

	if m.outputCommitted.Len() == 0 {
		t.Fatal("no overflow was committed; the test never exercised the path")
	}

	got := m.outputCommitted.String() + m.output.String()
	if stripBlank(got) != stripBlank(sent.String()) {
		t.Errorf("committed+pending lost content:\n got %q\nwant %q", got, sent.String())
	}
}

// TestStreamOverflow_PrefixPrintedOnce guards against every committed chunk
// carrying its own "Agent: " prefix, which would read as separate replies.
func TestStreamOverflow_PrefixPrintedOnce(t *testing.T) {
	app := New(&stubStreamAgent{id: "orchestrator"}, &configs.Config{}, nil, nil, nil)
	m := newModel(app, context.Background())
	m.width = 80
	m.height = 20

	first := m.decorateAgentBlock("one")
	second := m.decorateAgentBlock("two")

	if !strings.Contains(first, "Agent:") {
		t.Error("first block is missing the Agent: prefix")
	}

	if strings.Contains(second, "Agent:") {
		t.Error("second block repeated the Agent: prefix")
	}

	m.resetStream()

	if third := m.decorateAgentBlock("three"); !strings.Contains(third, "Agent:") {
		t.Error("prefix did not come back after resetStream")
	}
}

// TestView_LiveRegionIsRendered is the user-visible point of the whole
// change: mid-stream the live region must show rendered markdown, not the
// raw source the model is still emitting.
func TestView_LiveRegionIsRendered(t *testing.T) {
	app := New(&stubStreamAgent{id: "orchestrator"}, &configs.Config{}, nil, nil, nil)
	m := newModel(app, context.Background())
	m.width = 80
	m.height = 40
	m.status = statusProcessing

	m.output.WriteString("# Heading\n\nsome **bold** prose")
	m.syncStream()

	view := m.View()

	if strings.Contains(stripANSI(view), "# Heading") {
		t.Error("live region still shows the raw markdown source")
	}

	if !strings.Contains(stripANSI(view), "Heading") {
		t.Errorf("live region lost the heading text:\n%s", view)
	}

	if !strings.Contains(view, "\x1b[") {
		t.Error("live region carries no styling, so nothing was rendered")
	}
}

// TestView_UnclosedFenceRenders guards the case an incremental parser would
// get wrong: a code fence that is still streaming. Rendering the full text
// every frame means goldmark closes it implicitly at end of input.
func TestView_UnclosedFenceRenders(t *testing.T) {
	app := New(&stubStreamAgent{id: "orchestrator"}, &configs.Config{}, nil, nil, nil)
	m := newModel(app, context.Background())
	m.width = 80
	m.height = 40
	m.status = statusProcessing

	m.output.WriteString("intro\n\n```go\nfunc main() {\n\t// # not a heading\n")
	m.syncStream()

	view := m.View()

	if strings.Contains(stripANSI(view), "```") {
		t.Error("the fence marker leaked into the rendered output")
	}

	if !strings.Contains(stripANSI(view), "func main() {") {
		t.Errorf("code body missing from the live region:\n%s", view)
	}
}
