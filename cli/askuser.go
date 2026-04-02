package cli

import (
	"context"

	tea "github.com/charmbracelet/bubbletea"
)

// askUserRequestMsg signals that the agent wants to ask the user a question.
type askUserRequestMsg struct {
	question string
}

// CLIInteractor implements askuser.UserInteractor for the CLI TUI.
type CLIInteractor struct {
	program *tea.Program // set after tea.NewProgram() via SetProgram()
	respCh  chan string
}

// NewCLIInteractor creates a CLIInteractor with a buffered response channel.
func NewCLIInteractor() *CLIInteractor {
	return &CLIInteractor{
		respCh: make(chan string, 1),
	}
}

// SetProgram wires the tea.Program reference. Must be called from App.Run()
// after creating the program and before any agent execution.
func (c *CLIInteractor) SetProgram(p *tea.Program) {
	c.program = p
}

// AskUser presents the question to the user via the TUI and blocks until
// the user responds or the context is canceled.
func (c *CLIInteractor) AskUser(ctx context.Context, question string) (string, error) {
	if c.program == nil {
		return "User interaction not available.", nil
	}

	c.program.Send(askUserRequestMsg{question: question})

	select {
	case resp := <-c.respCh:
		return resp, nil
	case <-ctx.Done():
		return "User did not respond within the timeout. " +
			"Proceed with your best judgment.", nil
	}
}
