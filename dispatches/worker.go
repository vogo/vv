package dispatches

import (
	"context"
	"fmt"
	"sort"
	"strings"

	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/agent/taskagent"
	"github.com/vogo/vage/prompt"
	"github.com/vogo/vage/tool"
	"github.com/vogo/vv/registries"
)

// Isolation modes for a derived worker.
//
//   - IsolationIsolated (default): the worker runs on a fresh sub-context. It
//     sees only the task, the Primary-supplied background and the context
//     sources named in its spec.
//   - IsolationShared: the worker additionally shares the caller's session
//     memory, so it continues from the same task background the Primary has.
//
// Both modes are single-shot and disposable — isolation controls *context
// sharing*, never lifetime and never permission.
const (
	IsolationIsolated = "isolated"
	IsolationShared   = "shared"
)

// WorkerSpec is the capability contract for a derived worker. It replaces the
// implicit "role name decides everything" model with orthogonal dimensions:
//
//	Agent runtime   → BaseType (a registered descriptor: Factory + base behaviour)
//	ToolProfile     → ToolAccess (Full / Review / ReadOnly / None)
//	Skills          → Skills (prompt-level specialisation; grants no tools)
//	ContextSources  → ContextSources (allow-listed read-only context, e.g. diff)
//	ModelPolicy     → Model (system-configured model, or inherited default)
//	IsolationMode   → Isolation (shared task background vs isolated sub-context)
//
// PermissionPolicy is not a spec field on purpose: permission, path guard and
// sandbox are applied by the assembly layer to every worker and can only
// *reduce* what ToolAccess assembled — a spec can never widen them.
//
// The same struct is consumed by DAG nodes (`dynamic_spec` in a plan step) and
// by the general `spawn_worker` tool, so both entry points produce an
// identical permission surface.
type WorkerSpec struct {
	BaseType       string   `json:"base_type"`               // required: a registered agent descriptor ID
	SystemPrompt   string   `json:"system_prompt,omitempty"` // optional: overrides the base descriptor prompt
	ToolAccess     string   `json:"tool_access,omitempty"`   // optional: profile name; inherits base descriptor when empty
	Model          string   `json:"model,omitempty"`         // optional: overrides the configured model
	Skills         []string `json:"skills,omitempty"`        // optional: registered skill IDs
	ContextSources []string `json:"context,omitempty"`       // optional: registered context source IDs
	Isolation      string   `json:"isolation,omitempty"`     // optional: "isolated" (default) | "shared"
}

// DynamicAgentSpec is the historical name of WorkerSpec, kept as an alias so
// existing DAG plans, JSON payloads and call sites keep working unchanged. The
// wire format is identical — WorkerSpec only *adds* optional fields.
type DynamicAgentSpec = WorkerSpec

// EffectiveIsolation returns the isolation mode, defaulting to IsolationIsolated.
func (s *WorkerSpec) EffectiveIsolation() string {
	if s.Isolation == "" {
		return IsolationIsolated
	}

	return s.Isolation
}

// validate checks that a WorkerSpec is well-formed against the registries that
// bound every dimension. nil skills / sources fall back to the built-in
// defaults so callers holding only the agent registry (e.g. plan validation)
// still get full checking.
//
// Validation is total: an invalid spec never yields a partially assembled
// worker — construction stops before any tool registry is built.
func (s *WorkerSpec) validate(
	reg *registries.Registry,
	skills *registries.SkillRegistry,
	sources *registries.ContextSourceRegistry,
) error {
	if s.BaseType == "" {
		return fmt.Errorf("worker spec: base_type is required")
	}

	if reg == nil || !reg.ValidateRef(s.BaseType) {
		return fmt.Errorf("worker spec: invalid base_type %q", s.BaseType)
	}

	if s.ToolAccess != "" {
		if _, ok := registries.ProfileByName(s.ToolAccess); !ok {
			return fmt.Errorf("worker spec: invalid tool_access %q", s.ToolAccess)
		}
	}

	if skills == nil {
		skills = registries.DefaultSkills()
	}

	for _, id := range s.Skills {
		if !skills.ValidateRef(id) {
			return fmt.Errorf("worker spec: invalid skill %q (registered: %s)", id, strings.Join(skills.IDs(), ", "))
		}
	}

	if sources == nil {
		sources = registries.DefaultContextSources("")
	}

	for _, id := range s.ContextSources {
		if !sources.ValidateRef(id) {
			return fmt.Errorf("worker spec: invalid context source %q (registered: %s)", id, strings.Join(sources.IDs(), ", "))
		}
	}

	switch s.Isolation {
	case "", IsolationIsolated, IsolationShared:
	default:
		return fmt.Errorf("worker spec: invalid isolation %q (want %q or %q)", s.Isolation, IsolationIsolated, IsolationShared)
	}

	return nil
}

// skillRegistry returns the dispatcher's skill registry, falling back to the
// built-in defaults when the assembly layer did not install one.
func (d *Dispatcher) skillRegistry() *registries.SkillRegistry {
	if d.skills != nil {
		return d.skills
	}

	return registries.DefaultSkills()
}

// contextSourceRegistry returns the dispatcher's context source registry,
// falling back to defaults bound to the dispatcher's working directory.
func (d *Dispatcher) contextSourceRegistry() *registries.ContextSourceRegistry {
	if d.contextSources != nil {
		return d.contextSources
	}

	return registries.DefaultContextSources(d.workingDir)
}

// buildWorker assembles an ephemeral taskagent from a WorkerSpec. It is the
// single construction path shared by DAG dynamic nodes and `spawn_worker`, so
// neither entry point can produce a different permission surface.
//
// instanceID becomes the worker's agent ID; it is correlatable within a run
// but not stable across runs. The worker is never registered: the agent
// registry stays exactly as it was built at startup.
func (d *Dispatcher) buildWorker(instanceID string, spec *WorkerSpec) (*taskagent.Agent, error) {
	if spec == nil {
		return nil, fmt.Errorf("worker spec: spec is required")
	}

	if err := spec.validate(d.registry, d.skillRegistry(), d.contextSourceRegistry()); err != nil {
		return nil, err
	}

	desc, ok := d.registry.Get(spec.BaseType)
	if !ok {
		return nil, fmt.Errorf("worker spec: unknown base type %q", spec.BaseType)
	}

	// ToolProfile: explicit tool_access wins, otherwise inherit the base
	// descriptor's profile.
	profile := desc.ToolProfile

	if spec.ToolAccess != "" {
		p, ok := registries.ProfileByName(spec.ToolAccess)
		if !ok {
			return nil, fmt.Errorf("worker spec: unknown tool access profile %q", spec.ToolAccess)
		}

		profile = p
	}

	toolReg, err := d.buildWorkerTools(profile)
	if err != nil {
		return nil, err
	}

	systemPrompt, err := d.buildWorkerPrompt(desc, spec, profile, toolRegistryNames(toolReg))
	if err != nil {
		return nil, err
	}

	model := spec.Model
	if model == "" {
		model = d.model
	}

	maxIter := d.maxIterations
	if maxIter == 0 {
		maxIter = 10 // sensible default
	}

	opts := []taskagent.Option{
		taskagent.WithCaller(d.llm),
		taskagent.WithModel(model),
		taskagent.WithSystemPrompt(prompt.StringPrompt(systemPrompt)),
		taskagent.WithMaxIterations(maxIter),
	}

	if toolReg != nil {
		opts = append(opts, taskagent.WithToolRegistry(toolReg))
	}

	if d.runTokenBudget > 0 {
		opts = append(opts, taskagent.WithRunTokenBudget(d.runTokenBudget))
	}

	// Runtime knobs configured for registered sub-agents apply to workers
	// too, so behaviour does not depend on who executes the task.
	if d.maxParallelToolCalls > 0 {
		opts = append(opts, taskagent.WithMaxParallelToolCalls(d.maxParallelToolCalls))
	}

	if d.promptCaching != nil {
		opts = append(opts, taskagent.WithPromptCaching(*d.promptCaching))
	}

	if d.hookManager != nil {
		opts = append(opts, taskagent.WithHookManager(d.hookManager))
	}

	// IsolationMode: only the shared mode attaches the caller's session
	// memory. The isolated default keeps the worker on a fresh sub-context.
	if spec.EffectiveIsolation() == IsolationShared && d.memory != nil {
		opts = append(opts, taskagent.WithMemory(d.memory))
	}

	return taskagent.New(
		agent.Config{
			ID:          instanceID,
			Name:        fmt.Sprintf("Worker (%s)", instanceID),
			Description: fmt.Sprintf("Ephemeral %s worker: %s", spec.BaseType, describeWorkerCapabilities(profile, spec)),
		},
		opts...,
	), nil
}

// buildWorkerTools assembles the worker's tool registry from its profile and
// then applies the assembly layer's wrapping chain (permission → truncation →
// debug). The wrapper can only *reject* calls to already-assembled tools; it
// never adds a tool, which is what keeps ToolProfile the sole widening axis.
//
// Returns nil (no registry) for ProfileNone so the worker stays LLM-only.
func (d *Dispatcher) buildWorkerTools(profile registries.ToolProfile) (tool.ToolRegistry, error) {
	if len(profile.Capabilities) == 0 {
		return nil, nil
	}

	reg, err := profile.BuildRegistry(d.toolsCfg, d.regOpts...)
	if err != nil {
		return nil, fmt.Errorf("worker spec: build %s tool registry: %w", profile.Name, err)
	}

	if d.wrapToolRegistry != nil {
		return d.wrapToolRegistry(reg), nil
	}

	return reg, nil
}

// buildWorkerPrompt composes the worker system prompt in four layers: the base
// descriptor prompt (or the spec's override), the effective-tool notice when
// the profile was overridden, the instructions of every named skill, and the
// project instructions. Skills only append text — they never touch the tool set.
func (d *Dispatcher) buildWorkerPrompt(
	desc registries.AgentDescriptor,
	spec *WorkerSpec,
	profile registries.ToolProfile,
	toolNames []string,
) (string, error) {
	systemPrompt := spec.SystemPrompt
	if systemPrompt == "" {
		systemPrompt = desc.SystemPrompt
	}

	sections := []string{systemPrompt}

	// A base runtime prompt enumerates the tools *its own* profile grants.
	// Once tool_access narrows that (coder runtime under Review access, say),
	// the inherited prompt would advertise write/edit the worker does not
	// have, and the model would burn iterations calling missing tools. State
	// the real surface instead of hoping the model infers it.
	if spec.SystemPrompt == "" && spec.ToolAccess != "" && profile.Name != desc.ToolProfile.Name {
		sections = append(sections, effectiveToolsNotice(profile, toolNames))
	}

	skillText, err := d.skillRegistry().Instructions(spec.Skills)
	if err != nil {
		return "", fmt.Errorf("worker spec: %w", err)
	}

	if skillText != "" {
		sections = append(sections, skillText)
	}

	var joined []string

	for _, s := range sections {
		if strings.TrimSpace(s) != "" {
			joined = append(joined, s)
		}
	}

	return appendProjectInstructions(strings.Join(joined, "\n\n"), d.projectInstructions), nil
}

// effectiveToolsNotice renders the authoritative tool list for a worker whose
// profile differs from its base runtime's.
func effectiveToolsNotice(profile registries.ToolProfile, toolNames []string) string {
	if len(toolNames) == 0 {
		return fmt.Sprintf("## Effective tool access: %s\n\n"+
			"本次执行**不提供任何工具**。上面基础提示中提到的工具一律不可用,请直接基于已有信息作答,不要尝试调用工具。", profile.Name)
	}

	return fmt.Sprintf("## Effective tool access: %s\n\n"+
		"本次执行实际可用的工具只有:%s。\n"+
		"上面基础提示中提到的其它工具(例如 write / edit)在本次执行中**不可用**,调用它们只会失败——"+
		"需要修改文件时,请把建议写进你的回答,由调用方决定是否执行。",
		profile.Name, strings.Join(toolNames, "、"))
}

// toolRegistryNames lists the tool names a registry exposes, sorted for a
// stable prompt (an unstable ordering would defeat prompt caching).
func toolRegistryNames(reg tool.ToolRegistry) []string {
	if reg == nil {
		return nil
	}

	defs := reg.List()

	names := make([]string, 0, len(defs))
	for _, def := range defs {
		names = append(names, def.Name)
	}

	sort.Strings(names)

	return names
}

// describeWorkerCapabilities renders a one-line capability summary used as the
// worker's Description, so traces show what the instance could actually do.
func describeWorkerCapabilities(profile registries.ToolProfile, spec *WorkerSpec) string {
	parts := []string{"tools=" + profile.Name}

	if len(spec.Skills) > 0 {
		parts = append(parts, "skills="+strings.Join(spec.Skills, "+"))
	}

	if len(spec.ContextSources) > 0 {
		parts = append(parts, "context="+strings.Join(spec.ContextSources, "+"))
	}

	parts = append(parts, "isolation="+spec.EffectiveIsolation())

	return strings.Join(parts, " ")
}

// resolveWorkerContext renders the spec's context sources into a single
// read-only block prepended to the worker's task input. A failing source
// aborts the spawn with a diagnosable error rather than silently running a
// worker without the context it was supposed to read.
func (d *Dispatcher) resolveWorkerContext(ctx context.Context, spec *WorkerSpec) (string, error) {
	if spec == nil || len(spec.ContextSources) == 0 {
		return "", nil
	}

	return d.contextSourceRegistry().Resolve(ctx, spec.ContextSources)
}

// nextWorkerID returns a per-dispatcher unique worker instance ID. Correlatable
// within a process run; deliberately not stable across runs (workers are
// disposable and never registered).
func (d *Dispatcher) nextWorkerID(baseType string) string {
	n := d.workerSeq.Add(1)

	return fmt.Sprintf("worker_%s_%d", baseType, n)
}

// SpawnWorker validates a spec, builds the ephemeral worker, resolves its
// context sources and runs it through the shared sub-agent execution path
// (recursion accounting, session tagging, streamed sub-agent envelope).
//
// It returns a tool result rather than an error for runtime failures so a
// failed worker folds back into the Primary as an IsError result (ORCH-R6)
// instead of aborting the whole request.
func (d *Dispatcher) SpawnWorker(ctx context.Context, spec *WorkerSpec, task, background string) (string, error) {
	if spec == nil {
		return "", fmt.Errorf("worker spec is required")
	}

	// Construction failures (invalid spec, unresolvable context source) stop
	// here: no partially assembled worker is ever started.
	worker, err := d.buildWorker(d.nextWorkerID(spec.BaseType), spec)
	if err != nil {
		return "", err
	}

	contextBlock, err := d.resolveWorkerContext(ctx, spec)
	if err != nil {
		return "", err
	}

	text, err := runSubAgentTask(ctx, worker, task, joinBackground(contextBlock, background))
	if err != nil {
		return "", fmt.Errorf("execution failed: %w", err)
	}

	return text, nil
}

// joinBackground merges the resolved context-source block with the caller's
// free-form background text, keeping the read-only block first so the worker
// reads authoritative context before narrative framing.
func joinBackground(contextBlock, background string) string {
	contextBlock = strings.TrimSpace(contextBlock)
	background = strings.TrimSpace(background)

	switch {
	case contextBlock == "":
		return background
	case background == "":
		return contextBlock
	default:
		return contextBlock + "\n\n" + background
	}
}
