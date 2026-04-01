package cli

import (
	"time"

	"github.com/vogo/vage/schema"
)

// streamEventMsg wraps a schema.Event received from RunStream.
type streamEventMsg struct {
	event schema.Event
}

// streamDoneMsg signals the stream has ended (EOF or error).
type streamDoneMsg struct {
	err error
}

// confirmRequestMsg signals that a tool call needs user confirmation.
type confirmRequestMsg struct {
	toolName  string
	arguments string
}

// sessionStatus represents the current state of the CLI session.
type sessionStatus int

const (
	statusIdle sessionStatus = iota
	statusProcessing
	statusConfirming
	statusQuitting
)

// Display message role constants.
const (
	RoleUser       = "user"
	RoleAgent      = "agent"
	RoleSystem     = "system"
	RoleTool       = "tool"
	RoleToolResult = "tool_result"
	RoleError      = "error"
	RolePhase      = "phase"
	RoleSubAgent   = "subagent"
)

// DisplayMessage represents a rendered message in the conversation history.
type DisplayMessage struct {
	Role      string
	Content   string // rendered text content
	Timestamp time.Time
	Rendered  bool // true if Content is already styled (skip default styling in refreshViewport)
}
