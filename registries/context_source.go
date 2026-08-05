package registries

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

// ContextProvider resolves one context source into a read-only text block.
// Providers are pure readers: they never mutate project state and never grant
// tools. A provider error aborts worker construction with a diagnosable
// message instead of silently producing a worker with missing context.
type ContextProvider func(ctx context.Context) (string, error)

// ContextSource is one orthogonal capability dimension of a worker: a named,
// allow-listed source of task context (a diff, a plan view, …) injected as a
// clearly-marked read-only block.
type ContextSource struct {
	ID          string // unique source identifier (e.g. "diff")
	Description string // one-line summary, surfaced in tool schemas
	Provider    ContextProvider
}

// ContextSourceRegistry is a thread-safe context source store, constructed
// once at startup and read-only afterwards.
type ContextSourceRegistry struct {
	mu      sync.RWMutex
	sources map[string]ContextSource
}

// NewContextSources creates an empty ContextSourceRegistry.
func NewContextSources() *ContextSourceRegistry {
	return &ContextSourceRegistry{sources: make(map[string]ContextSource)}
}

// Register adds a context source. Returns an error on duplicate/empty ID or
// a nil provider.
func (r *ContextSourceRegistry) Register(s ContextSource) error {
	if strings.TrimSpace(s.ID) == "" {
		return fmt.Errorf("context sources: source ID is required")
	}

	if s.Provider == nil {
		return fmt.Errorf("context sources: source %q has no provider", s.ID)
	}

	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.sources[s.ID]; exists {
		return fmt.Errorf("context sources: duplicate source ID %q", s.ID)
	}

	r.sources[s.ID] = s

	return nil
}

// MustRegister adds a context source, panicking on collision — a startup-time
// programming error, mirroring Registry.MustRegister.
func (r *ContextSourceRegistry) MustRegister(s ContextSource) {
	if err := r.Register(s); err != nil {
		panic(err)
	}
}

// Get returns a source by ID, or false if not registered.
func (r *ContextSourceRegistry) Get(id string) (ContextSource, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	s, ok := r.sources[id]

	return s, ok
}

// ValidateRef reports whether the given source ID is registered.
func (r *ContextSourceRegistry) ValidateRef(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, ok := r.sources[id]

	return ok
}

// All returns every registered source, sorted by ID.
func (r *ContextSourceRegistry) All() []ContextSource {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]ContextSource, 0, len(r.sources))
	for _, s := range r.sources {
		result = append(result, s)
	}

	sort.Slice(result, func(i, j int) bool { return result[i].ID < result[j].ID })

	return result
}

// IDs returns every registered source ID, sorted. Used to render the
// spawn_worker tool schema enum.
func (r *ContextSourceRegistry) IDs() []string {
	all := r.All()

	ids := make([]string, 0, len(all))
	for _, s := range all {
		ids = append(ids, s.ID)
	}

	return ids
}

// Resolve renders the named sources into a single read-only context block.
// Each source is wrapped in a labelled section so the worker can tell injected
// context apart from the task instruction itself. Unknown IDs and provider
// failures are returned as errors.
func (r *ContextSourceRegistry) Resolve(ctx context.Context, ids []string) (string, error) {
	if len(ids) == 0 {
		return "", nil
	}

	var sb strings.Builder

	for _, id := range ids {
		src, ok := r.Get(id)
		if !ok {
			return "", fmt.Errorf("unknown context source %q", id)
		}

		body, err := src.Provider(ctx)
		if err != nil {
			return "", fmt.Errorf("context source %q: %w", id, err)
		}

		if sb.Len() > 0 {
			sb.WriteString("\n\n")
		}

		fmt.Fprintf(&sb, "## Context: %s (read-only)\n\n%s", id, strings.TrimRight(body, "\n"))
	}

	return sb.String(), nil
}

// Built-in context source IDs.
const (
	ContextSourceDiff = "diff"
)

// contextSourceMaxBytes caps a single rendered source. A repository-wide diff
// can dwarf the model's context window, so the block is truncated with an
// explicit marker rather than silently blowing the budget.
const contextSourceMaxBytes = 60_000

// gitDiffTimeout bounds the `git diff` subprocess used by the diff source.
const gitDiffTimeout = 30 * time.Second

// truncateUTF8 caps s at maxBytes without splitting a multi-byte rune — a
// naive s[:maxBytes] can cut mid-rune and put invalid UTF-8 into the prompt.
// Truncation is announced in-band so the worker knows the context is partial
// rather than silently reasoning about half a diff.
func truncateUTF8(s string, maxBytes int) string {
	if len(s) <= maxBytes {
		return s
	}

	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}

	return s[:cut] + fmt.Sprintf(
		"\n\n[... 已截断:内容超过 %d 字节,请用 read/grep 工具查看剩余部分 ...]", maxBytes,
	)
}

// GitDiffProvider returns a ContextProvider that renders the working tree diff
// against HEAD in dir. It is read-only: `git diff` neither mutates the index
// nor the working tree.
func GitDiffProvider(dir string) ContextProvider {
	return func(ctx context.Context) (string, error) {
		ctx, cancel := context.WithTimeout(ctx, gitDiffTimeout)
		defer cancel()

		cmd := exec.CommandContext(ctx, "git", "diff", "HEAD")
		if dir != "" {
			cmd.Dir = dir
		}

		out, err := cmd.Output()
		if err != nil {
			// git's own stderr says far more than "exit status 128" —
			// carry it so the Primary can act on the actual reason.
			var exitErr *exec.ExitError
			if errors.As(err, &exitErr) && len(exitErr.Stderr) > 0 {
				return "", fmt.Errorf("git diff HEAD failed: %w: %s", err, strings.TrimSpace(string(exitErr.Stderr)))
			}

			return "", fmt.Errorf("git diff HEAD failed (not a git repository, or git unavailable): %w", err)
		}

		diff := strings.TrimSpace(string(out))
		if diff == "" {
			return "(git diff HEAD 为空:工作区与 HEAD 无差异)", nil
		}

		return "```diff\n" + truncateUTF8(diff, contextSourceMaxBytes) + "\n```", nil
	}
}

// DefaultContextSources returns the built-in context source registry bound to
// workingDir. The set is a startup-time constant: there is no runtime
// registration entry point, so a worker spec can only name an allow-listed
// source.
func DefaultContextSources(workingDir string) *ContextSourceRegistry {
	reg := NewContextSources()

	reg.MustRegister(ContextSource{
		ID:          ContextSourceDiff,
		Description: "Working tree diff against HEAD, injected as a read-only block",
		Provider:    GitDiffProvider(workingDir),
	})

	return reg
}
