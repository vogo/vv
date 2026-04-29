package registries

import (
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	vctx "github.com/vogo/vage/context"
	"github.com/vogo/vage/guard"
	"github.com/vogo/vage/hook"
	"github.com/vogo/vage/memory"
	"github.com/vogo/vage/tool"
)

// AgentDescriptor holds metadata and a factory function for a single agent type.
type AgentDescriptor struct {
	ID           string       // unique agent identifier (e.g., "coder")
	DisplayName  string       // human-readable name (e.g., "Coder")
	Description  string       // description for planner prompt auto-generation
	ToolProfile  ToolProfile  // capability-based tool access
	SystemPrompt string       // default system prompt for dynamic agent creation
	Factory      AgentFactory // creates an agent instance
	Dispatchable bool         // true if this agent can be a dispatch target (sub-agent)
}

// AgentFactory creates an agent.Agent from the given options.
type AgentFactory func(opts FactoryOptions) (agent.Agent, error)

// FactoryOptions holds the dependencies needed to create an agent.
type FactoryOptions struct {
	LLM            aimodel.ChatCompleter
	Model          string
	ToolRegistry   tool.ToolRegistry // filtered by ToolProfile
	MaxIterations  int
	RunTokenBudget int
	// MaxParallelToolCalls caps concurrent tool dispatch within an assistant
	// message. 0 uses the taskagent default; values <= 1 force serial.
	MaxParallelToolCalls int
	// PromptCaching controls emission of prompt-cache boundary hints on
	// the system message and last tool definition. True by default at the
	// vv layer; false opts out.
	PromptCaching       bool
	Memory              *memory.Manager
	PersistentMemory    memory.Memory // for coder's persistent memory prompt; nil if not available
	ProjectInstructions string        // content from VV.md; empty if no file
	ToolResultGuards    []guard.Guard // optional: scanners for tool-result injection; nil means not enabled
	HookManager         *hook.Manager // optional: event bus for trace/observability hooks; nil disables dispatch
	// ExtraContextSources are vage/context Sources appended to the
	// TaskAgent's ContextBuilder pipeline (between SessionMemory and
	// RequestMessages). Used to plug in cross-cutting context like the
	// Plan Workspace without rewriting agent factories. nil / empty leaves
	// the default builder configuration unchanged.
	ExtraContextSources []vctx.Source
}

// Registry is a thread-safe agent descriptor store.
type Registry struct {
	mu     sync.RWMutex
	agents map[string]AgentDescriptor
}

// New creates an empty Registry.
func New() *Registry {
	return &Registry{
		agents: make(map[string]AgentDescriptor),
	}
}

// MustRegister adds an agent descriptor. Panics on duplicate ID (programming error at startup).
func (r *Registry) MustRegister(d AgentDescriptor) {
	if err := r.Register(d); err != nil {
		panic(err)
	}
}

// Register adds an agent descriptor. Returns an error on duplicate ID.
func (r *Registry) Register(d AgentDescriptor) error {
	r.mu.Lock()
	defer r.mu.Unlock()

	if _, exists := r.agents[d.ID]; exists {
		return fmt.Errorf("registry: duplicate agent ID %q", d.ID)
	}

	r.agents[d.ID] = d

	return nil
}

// Get returns a descriptor by ID, or false if not found.
func (r *Registry) Get(id string) (AgentDescriptor, bool) {
	r.mu.RLock()
	defer r.mu.RUnlock()

	d, ok := r.agents[id]

	return d, ok
}

// All returns all registered descriptors, sorted by ID for deterministic output.
func (r *Registry) All() []AgentDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()

	result := make([]AgentDescriptor, 0, len(r.agents))
	for _, d := range r.agents {
		result = append(result, d)
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})

	return result
}

// Dispatchable returns only descriptors with Dispatchable=true, sorted by ID.
func (r *Registry) Dispatchable() []AgentDescriptor {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []AgentDescriptor

	for _, d := range r.agents {
		if d.Dispatchable {
			result = append(result, d)
		}
	}

	sort.Slice(result, func(i, j int) bool {
		return result[i].ID < result[j].ID
	})

	return result
}

// ValidateRef returns true if the given ID is registered.
func (r *Registry) ValidateRef(id string) bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	_, ok := r.agents[id]

	return ok
}

// PlannerAgentList generates the agent list section for the planner system prompt.
// Output format matches the existing hardcoded list:
//
//   - "coder": Full tool access, code modification and creation
//   - "researcher": Read-only access, explores codebases and gathers information
//
// Only includes Dispatchable agents.
func (r *Registry) PlannerAgentList() string {
	agents := r.Dispatchable()

	var sb strings.Builder

	for _, d := range agents {
		fmt.Fprintf(&sb, "- %q: %s\n", d.ID, d.Description)
	}

	return sb.String()
}
