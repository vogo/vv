package dispatches

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/tool"
	"github.com/vogo/vv/registries"
)

// PrimaryToolSpawnWorker is the general worker derivation tool exposed to the
// Primary. It replaces "pick one of three fixed personas" with "declare the
// capability combination you need".
const PrimaryToolSpawnWorker = "spawn_worker"

// WorkerOptions enumerates the values a worker spec may reference. It is
// rendered into the tool schema so the model picks from registered values
// instead of inventing them — the same allow-lists validation enforces.
type WorkerOptions struct {
	BaseTypes      []string
	ToolProfiles   []string
	Skills         []registries.Skill
	ContextSources []registries.ContextSource
}

// WorkerSpawner is the dispatcher capability the spawn_worker tool needs:
// derive a worker from a spec and report which values a spec may name.
// *Dispatcher implements it.
type WorkerSpawner interface {
	SpawnWorker(ctx context.Context, spec *WorkerSpec, task, background string) (string, error)
	WorkerOptions() WorkerOptions
}

// WorkerOptions reports the registered values a worker spec may reference.
// Base types advertise only dispatchable descriptors — validation still
// accepts any registered descriptor, but the schema steers the model toward
// the runtimes meant to execute delegated work.
func (d *Dispatcher) WorkerOptions() WorkerOptions {
	descs := d.registry.Dispatchable()

	baseTypes := make([]string, 0, len(descs))
	for _, desc := range descs {
		baseTypes = append(baseTypes, desc.ID)
	}

	return WorkerOptions{
		BaseTypes:      baseTypes,
		ToolProfiles:   registries.ProfileNames(),
		Skills:         d.skillRegistry().All(),
		ContextSources: d.contextSourceRegistry().All(),
	}
}

// spawnWorkerArgs is the parsed argument schema for spawn_worker. The spec
// dimensions are flat rather than nested: one level of JSON is markedly more
// reliable for models to emit, and the handler maps them onto WorkerSpec.
type spawnWorkerArgs struct {
	Task           string   `json:"task"`
	Context        string   `json:"context,omitempty"`
	BaseType       string   `json:"base_type"`
	ToolAccess     string   `json:"tool_access,omitempty"`
	Skills         []string `json:"skills,omitempty"`
	ContextSources []string `json:"context_sources,omitempty"`
	SystemPrompt   string   `json:"system_prompt,omitempty"`
	Model          string   `json:"model,omitempty"`
	Isolation      string   `json:"isolation,omitempty"`
}

// spec converts parsed arguments into a WorkerSpec.
func (a *spawnWorkerArgs) spec() *WorkerSpec {
	return &WorkerSpec{
		BaseType:       strings.TrimSpace(a.BaseType),
		SystemPrompt:   a.SystemPrompt,
		ToolAccess:     strings.TrimSpace(a.ToolAccess),
		Model:          strings.TrimSpace(a.Model),
		Skills:         a.Skills,
		ContextSources: a.ContextSources,
		Isolation:      strings.TrimSpace(a.Isolation),
	}
}

// spawnWorkerDescription renders the tool description, including the
// capability model and the code-review combination as a worked example.
func spawnWorkerDescription(opts WorkerOptions) string {
	var sb strings.Builder

	sb.WriteString("Derive a single-use worker by declaring the capability combination it needs, ")
	sb.WriteString("instead of picking a fixed persona. Dimensions are orthogonal: ")
	sb.WriteString("`base_type` (runtime), `tool_access` (tool subset), `skills` (task discipline), ")
	sb.WriteString("`context_sources` (read-only context injected for it), `isolation`, `model`.\n\n")
	sb.WriteString("The worker runs once, returns its answer as this tool's result, and is discarded. ")
	sb.WriteString("Skills and prompts never grant tools: the effective tool set is `tool_access` ")
	sb.WriteString("intersected with what permission / path guard / sandbox allow.\n\n")
	sb.WriteString("Example — a code review needs no new agent type: ")
	sb.WriteString("`{base_type: \"coder\", tool_access: \"review\", skills: [\"review\"], context_sources: [\"diff\"]}` ")
	sb.WriteString("gives the coding runtime the review discipline, read/search/execute but no write tools, ")
	sb.WriteString("and the working-tree diff as read-only context.")

	if len(opts.Skills) > 0 {
		sb.WriteString("\n\nSkills: ")

		parts := make([]string, 0, len(opts.Skills))
		for _, s := range opts.Skills {
			parts = append(parts, fmt.Sprintf("%q — %s", s.ID, s.Description))
		}

		sb.WriteString(strings.Join(parts, "; "))
	}

	if len(opts.ContextSources) > 0 {
		sb.WriteString("\n\nContext sources: ")

		parts := make([]string, 0, len(opts.ContextSources))
		for _, s := range opts.ContextSources {
			parts = append(parts, fmt.Sprintf("%q — %s", s.ID, s.Description))
		}

		sb.WriteString(strings.Join(parts, "; "))
	}

	return sb.String()
}

// spawnWorkerParameters returns the JSON Schema advertised for spawn_worker.
// Enums are generated from the live registries so the advertised values and
// the validated values can never drift apart.
func spawnWorkerParameters(opts WorkerOptions) map[string]any {
	skillIDs := make([]string, 0, len(opts.Skills))
	for _, s := range opts.Skills {
		skillIDs = append(skillIDs, s.ID)
	}

	sourceIDs := make([]string, 0, len(opts.ContextSources))
	for _, s := range opts.ContextSources {
		sourceIDs = append(sourceIDs, s.ID)
	}

	baseType := map[string]any{
		"type":        "string",
		"description": "Agent runtime the worker is built from. Must be a registered agent type.",
	}
	if len(opts.BaseTypes) > 0 {
		baseType["enum"] = opts.BaseTypes
	}

	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"task": map[string]any{
				"type":        "string",
				"description": "The imperative instruction for the worker. Be concrete and self-contained.",
			},
			"context": map[string]any{
				"type":        "string",
				"description": "Optional background you already gathered (file paths, prior findings) the worker should know.",
			},
			"base_type": baseType,
			"tool_access": map[string]any{
				"type":        "string",
				"enum":        opts.ToolProfiles,
				"description": "Tool subset: full (read+write+execute+search), review (read+search+execute), read-only (read+search), none. Omit to inherit the base type's own profile.",
			},
			"skills": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string", "enum": skillIDs},
				"description": "Registered skills adding task discipline to the worker. Skills never grant tools.",
			},
			"context_sources": map[string]any{
				"type":        "array",
				"items":       map[string]any{"type": "string", "enum": sourceIDs},
				"description": "Registered read-only context injected into the worker's input (e.g. the working-tree diff).",
			},
			"system_prompt": map[string]any{
				"type":        "string",
				"description": "Optional system prompt overriding the base type's default. Cannot widen tool access.",
			},
			"model": map[string]any{
				"type":        "string",
				"description": "Optional model override. Omit to inherit the configured model.",
			},
			"isolation": map[string]any{
				"type":        "string",
				"enum":        []string{IsolationIsolated, IsolationShared},
				"description": "isolated (default) runs on a fresh sub-context; shared lets the worker continue from the session's task background.",
			},
		},
		"required": []string{"task", "base_type"},
	}
}

// RegisterSpawnWorkerTool installs the `spawn_worker` tool onto reg.
//
// Failures are returned as IsError tool results, never as handler errors:
// an invalid spec folds back to the Primary as a diagnosable message (and no
// worker is started at all), and a failing worker run folds back per ORCH-R6
// instead of aborting the request.
func RegisterSpawnWorkerTool(reg tool.ToolRegistry, spawner WorkerSpawner) error {
	if spawner == nil {
		return fmt.Errorf("spawn_worker: spawner is required")
	}

	opts := spawner.WorkerOptions()

	def := schema.ToolDef{
		Name:        PrimaryToolSpawnWorker,
		Description: spawnWorkerDescription(opts),
		Parameters:  spawnWorkerParameters(opts),
		Source:      schema.ToolSourceLocal,
	}

	if err := registerIfAbsent(reg, def, newSpawnWorkerHandler(spawner)); err != nil {
		return fmt.Errorf("register spawn_worker tool: %w", err)
	}

	return nil
}

// newSpawnWorkerHandler returns the ToolHandler closure that derives and runs
// a worker.
func newSpawnWorkerHandler(spawner WorkerSpawner) tool.ToolHandler {
	return func(ctx context.Context, _ string, args string) (schema.ToolResult, error) {
		var parsed spawnWorkerArgs
		if err := json.Unmarshal([]byte(args), &parsed); err != nil {
			return schema.ErrorResult("", "spawn_worker: invalid arguments: "+err.Error()), nil
		}

		task := strings.TrimSpace(parsed.Task)
		if task == "" {
			return schema.ErrorResult("", "spawn_worker: 'task' must be a non-empty string"), nil
		}

		if strings.TrimSpace(parsed.BaseType) == "" {
			return schema.ErrorResult("", "spawn_worker: 'base_type' must be a non-empty string"), nil
		}

		text, err := spawner.SpawnWorker(ctx, parsed.spec(), task, parsed.Context)
		if err != nil {
			return schema.ErrorResult("", "spawn_worker: "+err.Error()), nil
		}

		return schema.TextResult("", text), nil
	}
}
