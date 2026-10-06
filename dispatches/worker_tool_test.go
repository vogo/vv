package dispatches

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/vogo/largemodel/schema"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/tool"
	"github.com/vogo/vv/registries"
)

// stubSpawner records what the tool handler asked for and executes the given
// agent through the shared derived-run path, so tool-level tests cover the
// real execution semantics without an LLM.
type stubSpawner struct {
	opts       WorkerOptions
	runner     agent.Agent
	err        error
	spec       *WorkerSpec
	task       string
	background string
	calls      int
}

func (s *stubSpawner) WorkerOptions() WorkerOptions { return s.opts }

func (s *stubSpawner) SpawnWorker(ctx context.Context, spec *WorkerSpec, task, background string) (string, error) {
	s.calls++
	s.spec = spec
	s.task = task
	s.background = background

	if s.err != nil {
		return "", s.err
	}

	if s.runner == nil {
		return "ok", nil
	}

	return runSubAgentTask(ctx, s.runner, task, background)
}

func newStubSpawner(runner agent.Agent) *stubSpawner {
	return &stubSpawner{
		runner: runner,
		opts: WorkerOptions{
			BaseTypes:      []string{"coder", "researcher", "reviewer"},
			ToolProfiles:   registries.ProfileNames(),
			Skills:         registries.DefaultSkills().All(),
			ContextSources: registries.DefaultContextSources("").All(),
		},
	}
}

func TestRegisterSpawnWorkerTool_RequiresSpawner(t *testing.T) {
	if _, err := RegisterSpawnWorkerTool(tool.NewRegistry(), nil); err == nil {
		t.Error("expected error when spawner is nil")
	}
}

func TestRegisterSpawnWorkerTool_Schema(t *testing.T) {
	reg := tool.NewRegistry()

	if _, err := RegisterSpawnWorkerTool(reg, newStubSpawner(nil)); err != nil {
		t.Fatalf("RegisterSpawnWorkerTool: %v", err)
	}

	def, ok := reg.Get(PrimaryToolSpawnWorker)
	if !ok {
		t.Fatalf("tool %q not registered", PrimaryToolSpawnWorker)
	}

	// The description must teach the capability model, with code-review as
	// the worked example that needs no new agent type.
	for _, want := range []string{"tool_access", "skills", "context_sources", "code review"} {
		if !strings.Contains(def.Description, want) {
			t.Errorf("description missing %q", want)
		}
	}

	params, ok := def.Parameters.(map[string]any)
	if !ok {
		t.Fatalf("parameters = %T, want map[string]any", def.Parameters)
	}

	required, _ := params["required"].([]string)
	if !slices.Contains(required, "task") || !slices.Contains(required, "base_type") {
		t.Errorf("required = %v, want task and base_type", required)
	}

	props, ok := params["properties"].(map[string]any)
	if !ok {
		t.Fatalf("parameters have no properties map")
	}

	for _, name := range []string{"task", "context", "base_type", "tool_access", "skills", "context_sources", "system_prompt", "model", "isolation"} {
		if _, ok := props[name]; !ok {
			t.Errorf("schema missing property %q", name)
		}
	}

	// Enums are generated from the live registries: advertised values and
	// validated values cannot drift.
	toolAccess, _ := props["tool_access"].(map[string]any)
	if got, _ := toolAccess["enum"].([]string); !slices.Equal(got, registries.ProfileNames()) {
		t.Errorf("tool_access enum = %v, want %v", got, registries.ProfileNames())
	}

	skills, _ := props["skills"].(map[string]any)
	skillItems, _ := skills["items"].(map[string]any)

	if got, _ := skillItems["enum"].([]string); !slices.Contains(got, registries.SkillReview) {
		t.Errorf("skills enum = %v, want it to contain %q", got, registries.SkillReview)
	}

	sources, _ := props["context_sources"].(map[string]any)
	sourceItems, _ := sources["items"].(map[string]any)

	if got, _ := sourceItems["enum"].([]string); !slices.Contains(got, registries.ContextSourceDiff) {
		t.Errorf("context_sources enum = %v, want it to contain %q", got, registries.ContextSourceDiff)
	}
}

func TestSpawnWorkerTool_RejectsBadArgs(t *testing.T) {
	cases := []struct {
		name string
		args string
	}{
		{"invalid JSON", `{not json`},
		{"empty task", `{"task":"","base_type":"coder"}`},
		{"missing task", `{"base_type":"coder"}`},
		{"missing base type", `{"task":"do x"}`},
		{"blank base type", `{"task":"do x","base_type":"   "}`},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			reg := tool.NewRegistry()
			spawner := newStubSpawner(&stubAgent{id: "w"})

			if _, err := RegisterSpawnWorkerTool(reg, spawner); err != nil {
				t.Fatalf("RegisterSpawnWorkerTool: %v", err)
			}

			res, err := reg.Execute(context.Background(), PrimaryToolSpawnWorker, tc.args)
			if err != nil {
				t.Fatalf("Execute returned go error: %v", err)
			}

			if !res.IsError {
				t.Errorf("want IsError=true, got %q", toolResultText(res))
			}

			if spawner.calls != 0 {
				t.Errorf("spawner called %d times, want 0 (bad args must short-circuit)", spawner.calls)
			}
		})
	}
}

func TestSpawnWorkerTool_MapsArgsOntoSpec(t *testing.T) {
	reg := tool.NewRegistry()
	runner := &stubAgent{id: "worker"}
	spawner := newStubSpawner(runner)

	if _, err := RegisterSpawnWorkerTool(reg, spawner); err != nil {
		t.Fatalf("RegisterSpawnWorkerTool: %v", err)
	}

	args := `{"task":"review the change","context":"touched dispatches/","base_type":"coder",` +
		`"tool_access":"review","skills":["review"],"context_sources":["diff"],` +
		`"system_prompt":"be terse","model":"gpt-4o","isolation":"shared"}`

	res, err := reg.Execute(context.Background(), PrimaryToolSpawnWorker, args)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if res.IsError {
		t.Fatalf("handler returned IsError=true: %s", toolResultText(res))
	}

	want := &WorkerSpec{
		BaseType:       "coder",
		ToolAccess:     "review",
		Skills:         []string{registries.SkillReview},
		ContextSources: []string{registries.ContextSourceDiff},
		SystemPrompt:   "be terse",
		Model:          "gpt-4o",
		Isolation:      IsolationShared,
	}

	got := spawner.spec
	if got == nil {
		t.Fatal("spawner received no spec")
	}

	if got.BaseType != want.BaseType || got.ToolAccess != want.ToolAccess ||
		got.SystemPrompt != want.SystemPrompt || got.Model != want.Model || got.Isolation != want.Isolation ||
		!slices.Equal(got.Skills, want.Skills) || !slices.Equal(got.ContextSources, want.ContextSources) {
		t.Errorf("spec = %+v, want %+v", got, want)
	}

	if spawner.task != "review the change" || spawner.background != "touched dispatches/" {
		t.Errorf("task/background = %q / %q", spawner.task, spawner.background)
	}

	if runner.ranCount() != 1 {
		t.Errorf("worker ran %d times, want 1", runner.ranCount())
	}
}

// Construction and execution failures both fold back as IsError tool results
// so the Primary can react (ORCH-R6) instead of the request aborting.
func TestSpawnWorkerTool_FoldsFailuresAsToolErrors(t *testing.T) {
	reg := tool.NewRegistry()
	spawner := newStubSpawner(nil)
	spawner.err = errors.New("worker spec: invalid base_type \"code-reviewer\"")

	if _, err := RegisterSpawnWorkerTool(reg, spawner); err != nil {
		t.Fatalf("RegisterSpawnWorkerTool: %v", err)
	}

	res, err := reg.Execute(context.Background(), PrimaryToolSpawnWorker, `{"task":"x","base_type":"code-reviewer"}`)
	if err != nil {
		t.Fatalf("Execute returned go error: %v", err)
	}

	if !res.IsError {
		t.Fatal("want IsError=true for a failed spawn")
	}

	text := toolResultText(res)
	if !strings.Contains(text, "spawn_worker:") || !strings.Contains(text, "invalid base_type") {
		t.Errorf("error text = %q, want a diagnosable spawn_worker message", text)
	}
}

// A derived worker's execution is expandable in the UI: its native events are
// relayed between SubAgentStart and SubAgentEnd, exactly like a delegation.
func TestSpawnWorkerTool_StreamsWorkerEvents(t *testing.T) {
	reg := tool.NewRegistry()
	runner := &delegateStreamingAgent{stubAgent: stubAgent{id: "worker"}}

	if _, err := RegisterSpawnWorkerTool(reg, newStubSpawner(runner)); err != nil {
		t.Fatalf("RegisterSpawnWorkerTool: %v", err)
	}

	var events []schema.Event

	ctx := schema.WithEmitter(context.Background(), func(event schema.Event) error {
		events = append(events, event)

		return nil
	})

	res, err := reg.Execute(ctx, PrimaryToolSpawnWorker, `{"task":"review","base_type":"coder","tool_access":"review"}`)
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if res.IsError {
		t.Fatalf("handler returned IsError=true: %s", toolResultText(res))
	}

	wantTypes := []string{
		schema.EventSubAgentStart,
		schema.EventTextDelta,
		schema.EventToolCallStart,
		schema.EventLLMCallEnd,
		schema.EventAgentEnd,
		schema.EventSubAgentEnd,
	}

	if len(events) != len(wantTypes) {
		t.Fatalf("event count = %d, want %d: %+v", len(events), len(wantTypes), events)
	}

	for i, want := range wantTypes {
		if events[i].Type != want {
			t.Errorf("events[%d].Type = %q, want %q", i, events[i].Type, want)
		}
	}
}

// A derived run shares the Primary's recursion budget: depth is incremented
// before the worker executes, exactly as delegation does.
func TestSpawnWorkerTool_IncrementsDepth(t *testing.T) {
	reg := tool.NewRegistry()

	var seenDepth int

	spy := &depthSpyAgent{id: "worker", onRun: func(ctx context.Context) {
		seenDepth = DepthFrom(ctx)
	}}

	if _, err := RegisterSpawnWorkerTool(reg, newStubSpawner(spy)); err != nil {
		t.Fatalf("RegisterSpawnWorkerTool: %v", err)
	}

	args := `{"task":"do x","base_type":"coder"}`

	if _, err := reg.Execute(context.Background(), PrimaryToolSpawnWorker, args); err != nil {
		t.Fatalf("Execute: %v", err)
	}

	if seenDepth != 1 {
		t.Errorf("worker saw depth=%d, want 1", seenDepth)
	}

	seenDepth = -1

	if _, err := reg.Execute(WithDepth(context.Background(), 1), PrimaryToolSpawnWorker, args); err != nil {
		t.Fatalf("Execute at depth 1: %v", err)
	}

	if seenDepth != 2 {
		t.Errorf("worker saw depth=%d from parent depth=1, want 2", seenDepth)
	}
}

// blockingAgent runs until its context is cancelled, proving cancellation
// reaches the derived worker rather than leaving it running past the request.
type blockingAgent struct {
	stubAgent
	started chan struct{}
}

func (b *blockingAgent) Run(ctx context.Context, _ *schema.RunRequest) (*schema.RunResponse, error) {
	close(b.started)

	<-ctx.Done()

	return nil, ctx.Err()
}

func TestSpawnWorkerTool_ParentCancellationStopsWorker(t *testing.T) {
	reg := tool.NewRegistry()
	runner := &blockingAgent{stubAgent: stubAgent{id: "worker"}, started: make(chan struct{})}

	if _, err := RegisterSpawnWorkerTool(reg, newStubSpawner(runner)); err != nil {
		t.Fatalf("RegisterSpawnWorkerTool: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	type outcome struct {
		res schema.ToolResult
		err error
	}

	done := make(chan outcome, 1)

	go func() {
		res, err := reg.Execute(ctx, PrimaryToolSpawnWorker, `{"task":"long job","base_type":"coder"}`)
		done <- outcome{res, err}
	}()

	<-runner.started
	cancel()

	select {
	case got := <-done:
		if got.err != nil {
			t.Fatalf("Execute returned go error: %v", got.err)
		}

		if !got.res.IsError {
			t.Error("cancelled worker should fold back as an IsError result")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("worker kept running after parent cancellation")
	}
}

func TestDispatcher_WorkerOptions(t *testing.T) {
	d := newWorkerDispatcher(t)

	opts := d.WorkerOptions()

	// Only dispatchable runtimes are advertised; planner stays internal.
	want := []string{"coder", "researcher", "reviewer"}
	if !slices.Equal(opts.BaseTypes, want) {
		t.Errorf("BaseTypes = %v, want %v", opts.BaseTypes, want)
	}

	if !slices.Equal(opts.ToolProfiles, registries.ProfileNames()) {
		t.Errorf("ToolProfiles = %v, want %v", opts.ToolProfiles, registries.ProfileNames())
	}

	if len(opts.Skills) == 0 || len(opts.ContextSources) == 0 {
		t.Errorf("Skills/ContextSources empty: %+v", opts)
	}
}

// End-to-end on the real dispatcher: an invalid spec never starts a worker and
// never touches the registry.
func TestDispatcher_SpawnWorker_InvalidSpec(t *testing.T) {
	d := newWorkerDispatcher(t)

	before := len(d.registry.All())

	for _, spec := range []*WorkerSpec{
		nil,
		{BaseType: "code-reviewer"},
		{BaseType: "coder", ToolAccess: "write-only"},
		{BaseType: "coder", Skills: []string{"telepathy"}},
	} {
		if _, err := d.SpawnWorker(context.Background(), spec, "task", ""); err == nil {
			t.Errorf("spec %+v: expected error", spec)
		}
	}

	if after := len(d.registry.All()); after != before {
		t.Errorf("registry size changed from %d to %d", before, after)
	}
}

// A context source that cannot be resolved aborts the spawn: the worker must
// not run without the context it was told to read.
func TestDispatcher_SpawnWorker_ContextSourceFailureAborts(t *testing.T) {
	sources := registries.NewContextSources()
	sources.MustRegister(registries.ContextSource{
		ID:          registries.ContextSourceDiff,
		Description: "always fails",
		Provider:    func(context.Context) (string, error) { return "", errors.New("not a git repository") },
	})

	d := newWorkerDispatcher(t, WithContextSources(sources))

	_, err := d.SpawnWorker(context.Background(), &WorkerSpec{
		BaseType:       "coder",
		ToolAccess:     "review",
		ContextSources: []string{registries.ContextSourceDiff},
	}, "review the diff", "")

	if err == nil {
		t.Fatal("expected the spawn to fail when its context source cannot be resolved")
	}

	if !strings.Contains(err.Error(), "not a git repository") {
		t.Errorf("error = %v, want the underlying source failure", err)
	}
}

func TestSpawnWorkerTool_RefreshIncludesNewSkill(t *testing.T) {
	skills := registries.DefaultSkills()
	spawner := &stubSpawner{
		opts: WorkerOptions{
			BaseTypes:    []string{"coder"},
			ToolProfiles: registries.ProfileNames(),
			Skills:       skills.All(),
		},
	}
	reg := tool.NewRegistry()
	spawn, err := RegisterSpawnWorkerTool(reg, spawner)
	if err != nil {
		t.Fatal(err)
	}
	if err := skills.Register(registries.Skill{ID: "hot-worker-skill", Description: "d", Instructions: "do hot"}); err != nil {
		t.Fatal(err)
	}
	spawner.opts.Skills = skills.All()
	wrapped := tool.NewTruncatingToolRegistry(reg, 128)
	if err := spawn.Refresh(wrapped); err != nil {
		t.Fatal(err)
	}
	def, ok := wrapped.Get(PrimaryToolSpawnWorker)
	if !ok {
		t.Fatal("spawn_worker missing")
	}
	params, _ := def.Parameters.(map[string]any)
	props, _ := params["properties"].(map[string]any)
	skillsProp, _ := props["skills"].(map[string]any)
	items, _ := skillsProp["items"].(map[string]any)
	enum, _ := items["enum"].([]string)
	found := slices.Contains(enum, "hot-worker-skill")
	if !found {
		t.Errorf("skills enum missing hot-worker-skill: %v", enum)
	}
}
