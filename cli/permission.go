package cli

import (
	"context"
	"fmt"
	"sync"

	tea "github.com/charmbracelet/bubbletea"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vage/tool"
	"github.com/vogo/vv/configs"
)

// PermissionAction represents the user's response to a confirmation dialog.
type PermissionAction int

const (
	PermissionAllow       PermissionAction = iota // approve this invocation only
	PermissionAllowAlways                         // approve this and future invocations in session
	PermissionDeny                                // reject this invocation
)

// PermissionState holds the mutable permission state shared across all
// permissionExecutor instances. This is necessary because setup.Init()
// creates a separate wrapped registry per agent, but permission policy
// must be uniform across all agents.
type PermissionState struct {
	mu             sync.Mutex
	mode           configs.PermissionMode
	sessionAllowed map[string]bool
	executors      []*permissionExecutor
}

// NewPermissionState creates a PermissionState with the given initial mode.
func NewPermissionState(mode configs.PermissionMode) *PermissionState {
	return &PermissionState{
		mode:           mode,
		sessionAllowed: make(map[string]bool),
	}
}

// Mode returns the current permission mode.
func (s *PermissionState) Mode() configs.PermissionMode {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.mode
}

// SetMode updates the permission mode and clears session-allowed tools.
func (s *PermissionState) SetMode(mode configs.PermissionMode) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.mode = mode
	s.sessionAllowed = make(map[string]bool)
}

// IsSessionAllowed returns true if the tool has been approved via "Allow Always".
func (s *PermissionState) IsSessionAllowed(name string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.sessionAllowed[name]
}

// AddSessionAllowed marks a tool as approved for the remainder of the session.
func (s *PermissionState) AddSessionAllowed(name string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessionAllowed[name] = true
}

// RegisterExecutor registers a permissionExecutor with the state.
func (s *PermissionState) RegisterExecutor(pe *permissionExecutor) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.executors = append(s.executors, pe)
}

// SetConfirmFn sets the confirm function on all registered executors.
func (s *PermissionState) SetConfirmFn(fn func(ctx context.Context, toolName, args string) (PermissionAction, error)) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, pe := range s.executors {
		pe.confirmFn = fn
	}
}

// permissionExecutor wraps a tool.ToolRegistry, intercepting Execute() calls
// based on the active permission mode and session-allowed tools.
type permissionExecutor struct {
	tool.ToolRegistry
	state     *PermissionState
	confirmFn func(ctx context.Context, toolName, args string) (PermissionAction, error)
}

// Execute intercepts tool calls and applies permission logic.
func (p *permissionExecutor) Execute(ctx context.Context, name, args string) (schema.ToolResult, error) {
	mode := p.state.Mode()

	// 1. Auto mode: approve everything.
	if mode == configs.PermissionModeAuto {
		return p.ToolRegistry.Execute(ctx, name, args)
	}

	// 2. Look up tool definition to check ReadOnly.
	def, found := p.Get(name)
	readOnly := found && def.ReadOnly

	// 3. Plan mode: reject non-read-only tools.
	if mode == configs.PermissionModePlan && !readOnly {
		return schema.ErrorResult("",
			fmt.Sprintf("Tool %q is not permitted in plan mode (read-only).", name)), nil
	}

	// 4. Read-only tools are always approved in default/accept-edits/plan modes.
	if readOnly {
		return p.ToolRegistry.Execute(ctx, name, args)
	}

	// 5. Accept-edits mode: also approve write and edit.
	if mode == configs.PermissionModeAcceptEdits && (name == "write" || name == "edit") {
		return p.ToolRegistry.Execute(ctx, name, args)
	}

	// 6. Check session-allowed set.
	if p.state.IsSessionAllowed(name) {
		return p.ToolRegistry.Execute(ctx, name, args)
	}

	// 7. Show confirmation dialog.
	action, err := p.confirmFn(ctx, name, args)
	if err != nil {
		return schema.ErrorResult("", err.Error()), nil
	}

	switch action {
	case PermissionAllowAlways:
		p.state.AddSessionAllowed(name)

		return p.ToolRegistry.Execute(ctx, name, args)
	case PermissionAllow:
		return p.ToolRegistry.Execute(ctx, name, args)
	default:
		return schema.ErrorResult("", "Tool call rejected by user"), nil
	}
}

// WrapRegistryWithPermission wraps a tool.ToolRegistry with permission logic.
// The shared PermissionState ensures all wrapped registries (one per agent)
// share the same mode and session-allowed set.
func WrapRegistryWithPermission(
	inner tool.ToolRegistry,
	state *PermissionState,
) tool.ToolRegistry {
	pe := &permissionExecutor{
		ToolRegistry: inner,
		state:        state,
		confirmFn: func(_ context.Context, _, _ string) (PermissionAction, error) {
			// Default: allow all. App.wireConfirmFn() replaces this with the
			// real TUI confirmation function once the program is started.
			return PermissionAllow, nil
		},
	}

	state.RegisterExecutor(pe)

	return pe
}

// handlePermissionCommand handles the /permission CLI command.
func (m *model) handlePermissionCommand(args []string) tea.Cmd {
	ps := m.app.permissionState
	if ps == nil {
		return m.printSystem("Permission mode is not available.")
	}

	if len(args) == 0 {
		return m.printSystem(fmt.Sprintf("Current permission mode: %s", ps.Mode()))
	}

	mode := configs.PermissionMode(args[0])
	if !configs.IsValidPermissionMode(mode) {
		return m.printSystem(fmt.Sprintf(
			"Invalid permission mode: %q. Valid modes: default, accept-edits, auto, plan", args[0]))
	}

	ps.SetMode(mode)

	return m.printSystem(fmt.Sprintf("Permission mode changed to %s.", mode))
}
