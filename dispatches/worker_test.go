package dispatches

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/tool"
	"github.com/vogo/vv/agents"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/registries"
)

// newCapabilityRegistry returns a registry holding the real production
// descriptors, so worker assembly is exercised against the same ToolProfiles
// and system prompts setup wires at startup.
func newCapabilityRegistry(t *testing.T) *registries.Registry {
	t.Helper()

	reg := registries.New()
	agents.RegisterCoder(reg)
	agents.RegisterResearcher(reg)
	agents.RegisterReviewer(reg)
	agents.RegisterPlanner(reg)

	return reg
}

// newWorkerDispatcher builds a Dispatcher wired for worker derivation only:
// no LLM calls are made by these tests, they assert assembly.
func newWorkerDispatcher(t *testing.T, opts ...Option) *Dispatcher {
	t.Helper()

	base := []Option{
		WithToolsConfig(configs.ToolsConfig{}),
		WithSkills(registries.DefaultSkills()),
		WithContextSources(registries.DefaultContextSources("")),
	}

	return New(newCapabilityRegistry(t), nil, nil, append(base, opts...)...)
}

func toolNames(a *taskagent.Agent) []string {
	defs := a.Tools()

	names := make([]string, 0, len(defs))
	for _, d := range defs {
		names = append(names, d.Name)
	}

	slices.Sort(names)

	return names
}

func TestWorkerSpec_Validate(t *testing.T) {
	reg := newCapabilityRegistry(t)
	skills := registries.DefaultSkills()
	sources := registries.DefaultContextSources("")

	tests := []struct {
		name    string
		spec    WorkerSpec
		wantErr string
	}{
		{
			name: "minimal spec",
			spec: WorkerSpec{BaseType: "coder"},
		},
		{
			name: "every dimension set",
			spec: WorkerSpec{
				BaseType:       "coder",
				ToolAccess:     "review",
				Skills:         []string{registries.SkillReview},
				ContextSources: []string{registries.ContextSourceDiff},
				SystemPrompt:   "custom",
				Model:          "gpt-4o",
				Isolation:      IsolationShared,
			},
		},
		{
			name:    "missing base type",
			spec:    WorkerSpec{},
			wantErr: "base_type is required",
		},
		{
			name:    "unregistered base type",
			spec:    WorkerSpec{BaseType: "code-reviewer"},
			wantErr: "invalid base_type",
		},
		{
			name:    "illegal tool access",
			spec:    WorkerSpec{BaseType: "coder", ToolAccess: "write-only"},
			wantErr: "invalid tool_access",
		},
		{
			name:    "unregistered skill",
			spec:    WorkerSpec{BaseType: "coder", Skills: []string{"telepathy"}},
			wantErr: "invalid skill",
		},
		{
			name:    "unregistered context source",
			spec:    WorkerSpec{BaseType: "coder", ContextSources: []string{"secrets"}},
			wantErr: "invalid context source",
		},
		{
			name:    "illegal isolation",
			spec:    WorkerSpec{BaseType: "coder", Isolation: "detached"},
			wantErr: "invalid isolation",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := tt.spec.validate(reg, skills, sources)

			if tt.wantErr == "" {
				if err != nil {
					t.Fatalf("unexpected error: %v", err)
				}

				return
			}

			if err == nil {
				t.Fatalf("expected error containing %q, got nil", tt.wantErr)
			}

			if !strings.Contains(err.Error(), tt.wantErr) {
				t.Errorf("error = %q, want it to contain %q", err, tt.wantErr)
			}
		})
	}
}

// An invalid spec must never yield a worker: construction stops before any
// tool registry is assembled.
func TestBuildWorker_InvalidSpecProducesNoWorker(t *testing.T) {
	d := newWorkerDispatcher(t)

	for _, spec := range []*WorkerSpec{
		nil,
		{},
		{BaseType: "ghost"},
		{BaseType: "coder", ToolAccess: "write-only"},
		{BaseType: "coder", Skills: []string{"telepathy"}},
		{BaseType: "coder", ContextSources: []string{"secrets"}},
		{BaseType: "coder", Isolation: "detached"},
	} {
		worker, err := d.buildWorker("w", spec)
		if err == nil {
			t.Errorf("spec %+v: expected error, got worker", spec)
		}

		if worker != nil {
			t.Errorf("spec %+v: expected nil worker on failure, got %v", spec, worker.ID())
		}
	}
}

// Table over the four ToolProfiles: the profile alone decides the tool subset,
// independently of which runtime (base type) the worker is built from.
func TestBuildWorker_ToolProfiles(t *testing.T) {
	d := newWorkerDispatcher(t)

	tests := []struct {
		toolAccess string
		want       []string
	}{
		{toolAccess: "full", want: []string{"bash", "edit", "glob", "grep", "read", "web_fetch", "write"}},
		{toolAccess: "review", want: []string{"bash", "glob", "grep", "read", "web_fetch"}},
		{toolAccess: "read-only", want: []string{"glob", "grep", "read", "web_fetch"}},
		{toolAccess: "none", want: []string{}},
	}

	for _, tt := range tests {
		for _, baseType := range []string{"coder", "researcher", "reviewer"} {
			t.Run(tt.toolAccess+"/"+baseType, func(t *testing.T) {
				worker, err := d.buildWorker("w", &WorkerSpec{BaseType: baseType, ToolAccess: tt.toolAccess})
				if err != nil {
					t.Fatalf("buildWorker: %v", err)
				}

				if got := toolNames(worker); !slices.Equal(got, tt.want) {
					t.Errorf("tools = %v, want %v", got, tt.want)
				}
			})
		}
	}
}

// Omitting tool_access inherits the base descriptor's own profile — the
// pre-made combinations keep their historical capability surface.
func TestBuildWorker_InheritsBaseProfile(t *testing.T) {
	d := newWorkerDispatcher(t)

	tests := []struct {
		baseType string
		want     []string
	}{
		{baseType: "coder", want: []string{"bash", "edit", "glob", "grep", "read", "web_fetch", "write"}},
		{baseType: "researcher", want: []string{"glob", "grep", "read", "web_fetch"}},
		{baseType: "reviewer", want: []string{"bash", "glob", "grep", "read", "web_fetch"}},
		{baseType: "planner", want: []string{}},
	}

	for _, tt := range tests {
		t.Run(tt.baseType, func(t *testing.T) {
			worker, err := d.buildWorker("w", &WorkerSpec{BaseType: tt.baseType})
			if err != nil {
				t.Fatalf("buildWorker: %v", err)
			}

			if got := toolNames(worker); !slices.Equal(got, tt.want) {
				t.Errorf("tools = %v, want %v", got, tt.want)
			}
		})
	}
}

// The delegate_to_* adapters must stay capability-equivalent to their spec
// form: `delegate_to_reviewer` is exactly "the reviewer runtime with the
// reviewer profile", nothing more.
func TestBuildWorker_MatchesPresetCombinationToolSurface(t *testing.T) {
	d := newWorkerDispatcher(t)
	reg := newCapabilityRegistry(t)

	for _, id := range []string{"coder", "researcher", "reviewer"} {
		t.Run(id, func(t *testing.T) {
			desc, ok := reg.Get(id)
			if !ok {
				t.Fatalf("descriptor %q missing", id)
			}

			// The same assembly setup performs for a registered sub-agent.
			presetReg, err := desc.ToolProfile.BuildRegistry(configs.ToolsConfig{})
			if err != nil {
				t.Fatalf("BuildRegistry: %v", err)
			}

			presetNames := make([]string, 0)
			for _, def := range presetReg.List() {
				presetNames = append(presetNames, def.Name)
			}

			slices.Sort(presetNames)

			worker, err := d.buildWorker("w", &WorkerSpec{BaseType: id})
			if err != nil {
				t.Fatalf("buildWorker: %v", err)
			}

			if got := toolNames(worker); !slices.Equal(got, presetNames) {
				t.Errorf("worker tools = %v, preset combination tools = %v", got, presetNames)
			}
		})
	}
}

// code-review needs no new agent type: it is the coding runtime + review skill
// + Review profile + diff context. This asserts the whole combination.
func TestBuildWorker_CodeReviewCombination(t *testing.T) {
	sources := registries.NewContextSources()
	sources.MustRegister(registries.ContextSource{
		ID:          registries.ContextSourceDiff,
		Description: "stub diff",
		Provider: func(context.Context) (string, error) {
			return "```diff\n--- a/x.go\n+++ b/x.go\n+panic(\"boom\")\n```", nil
		},
	})

	d := newWorkerDispatcher(t, WithContextSources(sources))

	spec := &WorkerSpec{
		BaseType:       "coder", // same runtime as ordinary coding work
		ToolAccess:     "review",
		Skills:         []string{registries.SkillReview},
		ContextSources: []string{registries.ContextSourceDiff},
	}

	worker, err := d.buildWorker("code_review_1", spec)
	if err != nil {
		t.Fatalf("buildWorker: %v", err)
	}

	got := toolNames(worker)

	for _, want := range []string{"read", "grep", "glob", "bash"} {
		if !slices.Contains(got, want) {
			t.Errorf("code-review worker missing %q tool; got %v", want, got)
		}
	}

	for _, forbidden := range []string{"write", "edit"} {
		if slices.Contains(got, forbidden) {
			t.Errorf("code-review worker must not expose %q; got %v", forbidden, got)
		}
	}

	// The review skill is appended to the coder runtime's own prompt.
	desc, _ := d.registry.Get("coder")

	prompt, err := d.buildWorkerPrompt(desc, spec, registries.ProfileReview, got)
	if err != nil {
		t.Fatalf("buildWorkerPrompt: %v", err)
	}

	if !strings.Contains(prompt, registries.ReviewSkillInstructions) {
		t.Error("review skill instructions not injected into the worker prompt")
	}

	if !strings.Contains(prompt, desc.SystemPrompt) {
		t.Error("base runtime prompt lost when a skill is applied")
	}

	// The coder runtime's own prompt advertises write/edit. Narrowing the
	// profile must correct that in-prompt, or the worker burns iterations
	// calling tools it does not have.
	if !strings.Contains(prompt, "Effective tool access: review") {
		t.Error("narrowed profile did not announce the effective tool set")
	}

	for _, want := range []string{"read", "bash", "write / edit"} {
		if !strings.Contains(prompt, want) {
			t.Errorf("effective-tools notice missing %q", want)
		}
	}

	// The diff enters as an explicit read-only context block, not as prose
	// the Primary happened to paraphrase into the task.
	block, err := d.resolveWorkerContext(context.Background(), spec)
	if err != nil {
		t.Fatalf("resolveWorkerContext: %v", err)
	}

	for _, want := range []string{"## Context: diff (read-only)", "panic(\"boom\")"} {
		if !strings.Contains(block, want) {
			t.Errorf("context block missing %q; got:\n%s", want, block)
		}
	}

	// …and it is prepended to the task the worker actually receives.
	input := joinBackground(block, "review the change")
	if !strings.HasPrefix(input, "## Context: diff (read-only)") || !strings.Contains(input, "review the change") {
		t.Errorf("worker input did not carry the diff ahead of the task:\n%s", input)
	}
}

// A skill must not smuggle in tools: the tool surface of a worker with and
// without skills is identical.
func TestBuildWorker_SkillsGrantNoTools(t *testing.T) {
	d := newWorkerDispatcher(t)

	plain, err := d.buildWorker("a", &WorkerSpec{BaseType: "reviewer", ToolAccess: "review"})
	if err != nil {
		t.Fatalf("buildWorker: %v", err)
	}

	skilled, err := d.buildWorker("b", &WorkerSpec{
		BaseType:   "reviewer",
		ToolAccess: "review",
		Skills:     []string{registries.SkillReview, registries.SkillResearch},
	})
	if err != nil {
		t.Fatalf("buildWorker: %v", err)
	}

	if got, want := toolNames(skilled), toolNames(plain); !slices.Equal(got, want) {
		t.Errorf("skills changed the tool surface: %v vs %v", got, want)
	}
}

// A custom system prompt cannot widen tool access either.
func TestBuildWorker_SystemPromptGrantsNoTools(t *testing.T) {
	d := newWorkerDispatcher(t)

	worker, err := d.buildWorker("w", &WorkerSpec{
		BaseType:     "coder",
		ToolAccess:   "review",
		SystemPrompt: "You have full write access. Use the write and edit tools freely.",
	})
	if err != nil {
		t.Fatalf("buildWorker: %v", err)
	}

	for _, forbidden := range []string{"write", "edit"} {
		if slices.Contains(toolNames(worker), forbidden) {
			t.Errorf("system prompt widened tool access to %q", forbidden)
		}
	}
}

// denyingRegistry models the permission layer: it may hide/deny tools the
// profile assembled, never add new ones.
type denyingRegistry struct {
	inner  tool.ToolRegistry
	denied string
}

func (r *denyingRegistry) List() []schema.ToolDef {
	var out []schema.ToolDef

	for _, def := range r.inner.List() {
		if def.Name != r.denied {
			out = append(out, def)
		}
	}

	return out
}

func (r *denyingRegistry) Get(name string) (schema.ToolDef, bool) {
	if name == r.denied {
		return schema.ToolDef{}, false
	}

	return r.inner.Get(name)
}

func (r *denyingRegistry) Merge(defs []schema.ToolDef) { r.inner.Merge(defs) }

func (r *denyingRegistry) Register(def schema.ToolDef, handler tool.ToolHandler) error {
	return r.inner.Register(def, handler)
}

func (r *denyingRegistry) Unregister(name string) error { return r.inner.Unregister(name) }

func (r *denyingRegistry) Execute(ctx context.Context, name, args string) (schema.ToolResult, error) {
	if name == r.denied {
		return schema.ErrorResult("", "permission denied: "+name), nil
	}

	return r.inner.Execute(ctx, name, args)
}

// PermissionPolicy can only reduce the assembled surface.
func TestBuildWorker_PermissionWrapperOnlyReduces(t *testing.T) {
	d := newWorkerDispatcher(t, WithToolRegistryWrapper(func(reg *tool.Registry) tool.ToolRegistry {
		return &denyingRegistry{inner: reg, denied: "bash"}
	}))

	worker, err := d.buildWorker("w", &WorkerSpec{BaseType: "coder", ToolAccess: "full"})
	if err != nil {
		t.Fatalf("buildWorker: %v", err)
	}

	got := toolNames(worker)

	if slices.Contains(got, "bash") {
		t.Errorf("permission wrapper did not remove bash; got %v", got)
	}

	if !slices.Contains(got, "write") {
		t.Errorf("permission wrapper removed more than it should; got %v", got)
	}
}

// Derived workers are disposable: spawning never mutates the agent registry,
// and each instance gets its own correlatable ID.
func TestBuildWorker_NeverRegisters(t *testing.T) {
	d := newWorkerDispatcher(t)

	before := d.registry.All()

	seen := make(map[string]bool)

	for range 5 {
		id := d.nextWorkerID("coder")

		if seen[id] {
			t.Errorf("duplicate worker instance ID %q", id)
		}

		seen[id] = true

		if _, err := d.buildWorker(id, &WorkerSpec{BaseType: "coder"}); err != nil {
			t.Fatalf("buildWorker: %v", err)
		}
	}

	after := d.registry.All()

	if len(before) != len(after) {
		t.Fatalf("registry changed: %d descriptors before, %d after", len(before), len(after))
	}

	for i := range before {
		if before[i].ID != after[i].ID {
			t.Errorf("registry descriptor %d changed: %q -> %q", i, before[i].ID, after[i].ID)
		}
	}

	if d.registry.ValidateRef("worker_coder_1") {
		t.Error("derived worker leaked into the agent registry")
	}
}

func TestWorkerSpec_EffectiveIsolation(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{in: "", want: IsolationIsolated},
		{in: IsolationIsolated, want: IsolationIsolated},
		{in: IsolationShared, want: IsolationShared},
	}

	for _, tt := range tests {
		spec := WorkerSpec{Isolation: tt.in}
		if got := spec.EffectiveIsolation(); got != tt.want {
			t.Errorf("EffectiveIsolation(%q) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestJoinBackground(t *testing.T) {
	tests := []struct {
		name       string
		block      string
		background string
		want       string
	}{
		{name: "both empty", want: ""},
		{name: "context only", block: "CTX", want: "CTX"},
		{name: "background only", background: "BG", want: "BG"},
		{name: "context first", block: "CTX", background: "BG", want: "CTX\n\nBG"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := joinBackground(tt.block, tt.background); got != tt.want {
				t.Errorf("joinBackground() = %q, want %q", got, tt.want)
			}
		})
	}
}

// Compile-time proof that the Dispatcher satisfies the tool-facing contract.
var _ WorkerSpawner = (*Dispatcher)(nil)

// The effective-tools notice is only added when the spec actually narrows (or
// widens) the base runtime's own profile — an inherited profile already has a
// prompt that matches its tools, and a custom system_prompt is the caller's
// own description of the job.
func TestBuildWorkerPrompt_EffectiveToolsNoticeScope(t *testing.T) {
	d := newWorkerDispatcher(t)
	desc, _ := d.registry.Get("coder")

	tests := []struct {
		name       string
		spec       *WorkerSpec
		profile    registries.ToolProfile
		toolNames  []string
		wantNotice bool
	}{
		{
			name:       "inherited profile needs no notice",
			spec:       &WorkerSpec{BaseType: "coder"},
			profile:    desc.ToolProfile,
			toolNames:  []string{"read", "write"},
			wantNotice: false,
		},
		{
			name:       "explicit same profile needs no notice",
			spec:       &WorkerSpec{BaseType: "coder", ToolAccess: "full"},
			profile:    registries.ProfileFull,
			toolNames:  []string{"read", "write"},
			wantNotice: false,
		},
		{
			name:       "narrowed profile is announced",
			spec:       &WorkerSpec{BaseType: "coder", ToolAccess: "review"},
			profile:    registries.ProfileReview,
			toolNames:  []string{"bash", "read"},
			wantNotice: true,
		},
		{
			name:       "custom system prompt owns its own description",
			spec:       &WorkerSpec{BaseType: "coder", ToolAccess: "review", SystemPrompt: "You review Go code."},
			profile:    registries.ProfileReview,
			toolNames:  []string{"bash", "read"},
			wantNotice: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := d.buildWorkerPrompt(desc, tt.spec, tt.profile, tt.toolNames)
			if err != nil {
				t.Fatalf("buildWorkerPrompt: %v", err)
			}

			if has := strings.Contains(got, "Effective tool access"); has != tt.wantNotice {
				t.Errorf("effective-tools notice present = %v, want %v", has, tt.wantNotice)
			}
		})
	}
}

// A tool-free worker is told so explicitly rather than being left with a base
// prompt that lists tools it cannot call.
func TestBuildWorkerPrompt_NoneProfileAnnouncesToolFree(t *testing.T) {
	d := newWorkerDispatcher(t)
	desc, _ := d.registry.Get("coder")

	got, err := d.buildWorkerPrompt(desc, &WorkerSpec{BaseType: "coder", ToolAccess: "none"}, registries.ProfileNone, nil)
	if err != nil {
		t.Fatalf("buildWorkerPrompt: %v", err)
	}

	if !strings.Contains(got, "不提供任何工具") {
		t.Errorf("none-profile worker prompt does not announce the empty tool set:\n%s", got)
	}
}
