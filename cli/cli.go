package cli

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/charmbracelet/bubbles/spinner"
	"github.com/charmbracelet/bubbles/textarea"
	"github.com/charmbracelet/bubbles/viewport"
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/memory"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vagents/vaga/config"
)

// App holds the CLI TUI application state.
type App struct {
	orchestrator  agent.StreamAgent // replaces routeFn + routes
	cfg           *config.Config
	sessionID     string
	history       []schema.Message
	messages      []DisplayMessage
	program       *tea.Program
	persistentMem memory.Memory // for /memory commands
}

// New creates a new CLI App.
func New(
	orchestrator agent.StreamAgent,
	cfg *config.Config,
	persistentMem memory.Memory,
) *App {
	return &App{
		orchestrator:  orchestrator,
		cfg:           cfg,
		persistentMem: persistentMem,
	}
}

// Run starts the bubbletea program and blocks until exit.
func (a *App) Run(ctx context.Context) error {
	// Generate session ID.
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	a.sessionID = hex.EncodeToString(b)

	// Redirect slog to a file to avoid corrupting TUI.
	logFile, err := os.OpenFile(
		filepath.Join(config.DefaultDir(), "vaga.log"),
		os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600,
	)
	if err != nil {
		slog.SetDefault(slog.New(slog.NewTextHandler(io.Discard, nil)))
	} else {
		defer func() { _ = logFile.Close() }()
		slog.SetDefault(slog.New(slog.NewTextHandler(logFile, nil)))
	}

	// Wire up the confirming executor if configured.
	a.wireConfirmFn()

	m := newModel(a, ctx)

	p := tea.NewProgram(m, tea.WithAltScreen())
	a.program = p

	_, err = p.Run()

	return err
}

// wireConfirmFn is a placeholder for wiring the confirming executor's confirmFn
// to the TUI. The default confirmFn (set in WrapRegistry) allows all tools.
// A future iteration will wire this to the TUI confirmation dialog via
// the program reference once it is available.
func (a *App) wireConfirmFn() {}

// model is the bubbletea model for the CLI TUI.
type model struct {
	app *App
	ctx context.Context
	// UI components
	textarea textarea.Model
	viewport viewport.Model
	spinner  spinner.Model
	width    int
	height   int

	// State
	status          sessionStatus
	output          strings.Builder
	currentAgentIdx int // index of the current round's agent message in app.messages, -1 if none

	// Confirmation
	confirmCh   chan bool
	confirmForm *huh.Form
	pendingTC   *schema.ToolCallStartData

	// Track whether we have a running cancel function for current processing.
	runCancel context.CancelFunc
}

// newModel creates a new bubbletea model.
func newModel(app *App, ctx context.Context) *model {
	ta := textarea.New()
	ta.Placeholder = "Type your message... (Enter to send, /exit to quit)"
	ta.Focus()
	ta.SetHeight(3)
	ta.ShowLineNumbers = false
	ta.KeyMap.InsertNewline.SetEnabled(false)

	sp := spinner.New()
	sp.Spinner = spinner.Dot
	sp.Style = lipgloss.NewStyle().Foreground(lipgloss.Color("12"))

	vp := viewport.New(80, 20)
	welcome := fmt.Sprintf("Welcome to vaga CLI. (provider: %s, model: %s)\nWorking directory: %s\nType a message to begin.\n",
		app.cfg.LLM.Provider, app.cfg.LLM.Model, app.cfg.Tools.BashWorkingDir)
	vp.SetContent(welcome)

	m := &model{
		app:             app,
		ctx:             ctx,
		textarea:        ta,
		viewport:        vp,
		spinner:         sp,
		status:          statusIdle,
		currentAgentIdx: -1,
		confirmCh:       make(chan bool, 1),
	}

	return m
}

// escapeSeqRe matches ANSI escape sequences and OSC responses that terminals
// may inject as input (e.g. ]11;rgb:1818/1818/1818\, CSI mouse sequences).
var escapeSeqRe = regexp.MustCompile(`\x1b[^a-zA-Z]*[a-zA-Z]|\x1b\][^\x07\x1b\\]*(?:\x07|\x1b\\)|\][0-9]+;[^\x07\\\n]*\\?|<[0-9;]+[mMhHlL]`)

// sanitizeInput strips terminal escape sequences from text.
func sanitizeInput(s string) string {
	return escapeSeqRe.ReplaceAllString(s, "")
}

// isExitCommand checks if the input is an exit command.
func isExitCommand(input string) bool {
	trimmed := strings.TrimSpace(input)
	return trimmed == "/exit" || trimmed == "/quit"
}

func (m *model) Init() tea.Cmd {
	return tea.Batch(textarea.Blink, m.spinner.Tick)
}

func (m *model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	switch msg := msg.(type) {
	case tea.KeyMsg:
		switch msg.Type {
		case tea.KeyCtrlC:
			return m.handleCtrlC()
		case tea.KeyEnter:
			if m.status == statusIdle {
				return m.handleSubmit()
			}
		}

	case tea.WindowSizeMsg:
		m.width = msg.Width
		m.height = msg.Height
		m.viewport.Width = msg.Width
		m.viewport.Height = msg.Height - 6 - headerHeight // leave space for header, textarea, status bar
		m.textarea.SetWidth(msg.Width)
		return m, nil

	case streamEventMsg:
		return m.handleStreamEvent(msg)

	case streamDoneMsg:
		return m.handleStreamDone(msg)

	case confirmRequestMsg:
		return m.handleConfirmRequest(msg)

	case spinner.TickMsg:
		if m.status == statusProcessing {
			var cmd tea.Cmd
			m.spinner, cmd = m.spinner.Update(msg)
			cmds = append(cmds, cmd)
		}
	}

	// Handle confirm form updates.
	if m.status == statusConfirming && m.confirmForm != nil {
		form, cmd := m.confirmForm.Update(msg)
		if f, ok := form.(*huh.Form); ok {
			m.confirmForm = f
			if f.State == huh.StateCompleted {
				// Form completed, get the result.
				approved := f.GetBool("confirm")
				m.confirmCh <- approved
				m.status = statusProcessing
				m.confirmForm = nil
				m.pendingTC = nil
			}
		}
		cmds = append(cmds, cmd)
		return m, tea.Batch(cmds...)
	}

	// Always let the textarea process messages so it can absorb escape
	// sequences rather than leaving them in the input buffer.
	{
		var cmd tea.Cmd
		m.textarea, cmd = m.textarea.Update(msg)
		cmds = append(cmds, cmd)
	}
	// Strip terminal escape sequences that leak into the textarea
	// (e.g. OSC 11 background color responses from lipgloss/glamour).
	if v := m.textarea.Value(); v != "" {
		if cleaned := sanitizeInput(v); cleaned != v {
			m.textarea.SetValue(cleaned)
		}
	}

	// Update viewport for scroll.
	var cmd tea.Cmd
	m.viewport, cmd = m.viewport.Update(msg)
	cmds = append(cmds, cmd)

	return m, tea.Batch(cmds...)
}

// headerHeight is the number of lines the persistent header occupies.
const headerHeight = 2

func (m *model) headerView() string {
	dir := m.app.cfg.Tools.BashWorkingDir
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(dir, home) {
		dir = "~" + dir[len(home):]
	}

	headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8")) // dim gray
	providerModel := fmt.Sprintf("vaga · %s · %s", m.app.cfg.LLM.Provider, m.app.cfg.LLM.Model)
	line1 := headerStyle.Render(providerModel)
	dirStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	line2 := "  " + dirStyle.Render(dir)

	return line1 + "\n" + line2
}

func (m *model) View() string {
	var sb strings.Builder

	// Persistent header showing project directory.
	sb.WriteString(m.headerView())
	sb.WriteString("\n")

	// Viewport (output area).
	sb.WriteString(m.viewport.View())
	sb.WriteString("\n")

	// Status line.
	switch m.status {
	case statusProcessing:
		sb.WriteString(m.spinner.View())
		sb.WriteString(" Processing...")
		sb.WriteString("\n")
	case statusConfirming:
		if m.confirmForm != nil {
			sb.WriteString(m.confirmForm.View())
			sb.WriteString("\n")
		}
	default:
		sb.WriteString("\n")
	}

	// Input area.
	sb.WriteString(m.textarea.View())

	return sb.String()
}

// handleCtrlC handles Ctrl+C based on current status.
func (m *model) handleCtrlC() (tea.Model, tea.Cmd) {
	switch m.status {
	case statusProcessing:
		if m.runCancel != nil {
			m.runCancel()
		}
		m.status = statusIdle
		m.textarea.Focus()
		m.appendSystemMessage("Request cancelled.")
		return m, nil
	case statusConfirming:
		// Reject the confirmation and cancel the run.
		select {
		case m.confirmCh <- false:
		default:
		}
		if m.runCancel != nil {
			m.runCancel()
		}
		m.status = statusIdle
		m.confirmForm = nil
		m.pendingTC = nil
		m.textarea.Focus()
		m.appendSystemMessage("Confirmation cancelled.")
		return m, nil
	default:
		m.status = statusQuitting
		return m, tea.Quit
	}
}

// handleSubmit handles message submission.
func (m *model) handleSubmit() (tea.Model, tea.Cmd) {
	input := strings.TrimSpace(m.textarea.Value())
	if input == "" {
		return m, nil
	}

	if isExitCommand(input) {
		return m, tea.Quit
	}

	// Handle /memory commands before agent routing.
	if m.handleCommand(input) {
		m.textarea.Reset()
		return m, nil
	}

	m.textarea.Reset()
	m.textarea.Blur()
	m.status = statusProcessing

	// Add user message to display.
	m.appendMessage(DisplayMessage{
		Role:      "user",
		Content:   input,
		Timestamp: time.Now(),
	})

	// Add to conversation history.
	userMsg := schema.NewUserMessage(input)
	m.app.history = append(m.app.history, userMsg)

	// Reset output builder and agent message index for this round.
	m.output.Reset()
	m.currentAgentIdx = -1

	// Create a cancellable context for this run.
	runCtx, cancel := context.WithCancel(m.ctx)
	m.runCancel = cancel

	// Start agent invocation.
	return m, m.invokeAgent(runCtx, input)
}

// invokeAgent creates a tea.Cmd that runs the agent asynchronously.
func (m *model) invokeAgent(ctx context.Context, _ string) tea.Cmd {
	return func() tea.Msg {
		// Build request with full history.
		req := &schema.RunRequest{
			Messages:  m.app.history,
			SessionID: m.app.sessionID,
		}

		// Run stream via orchestrator.
		stream, err := m.app.orchestrator.RunStream(ctx, req)
		if err != nil {
			return streamDoneMsg{err: err}
		}

		// Consume stream in a goroutine, sending events to the TUI.
		go func() {
			defer func() { _ = stream.Close() }()

			for {
				event, recvErr := stream.Recv()
				if recvErr != nil {
					if errors.Is(recvErr, io.EOF) {
						m.app.program.Send(streamDoneMsg{})
					} else {
						m.app.program.Send(streamDoneMsg{err: recvErr})
					}

					return
				}

				m.app.program.Send(streamEventMsg{event: event})
			}
		}()

		return nil // The goroutine will send messages.
	}
}

// handleStreamEvent processes a streaming event from the agent.
func (m *model) handleStreamEvent(msg streamEventMsg) (tea.Model, tea.Cmd) {
	event := msg.event

	switch event.Type {
	case schema.EventAgentStart, schema.EventIterationStart:
		// Suppressed for cleaner UX.

	case schema.EventTextDelta:
		if data, ok := event.Data.(schema.TextDeltaData); ok {
			m.output.WriteString(data.Delta)
			m.updateOutputInViewport()
		}

	case schema.EventToolCallStart:
		if data, ok := event.Data.(schema.ToolCallStartData); ok {
			m.appendToolMessage(fmt.Sprintf("Tool call: %s(%s)", data.ToolName, truncate(data.Arguments, 200)))
		}

	case schema.EventToolCallEnd:
		if data, ok := event.Data.(schema.ToolCallEndData); ok {
			m.appendToolMessage(fmt.Sprintf("Tool %s completed (%dms)", data.ToolName, data.Duration))
		}

	case schema.EventToolResult:
		if data, ok := event.Data.(schema.ToolResultData); ok {
			resultText := ""
			for _, part := range data.Result.Content {
				if part.Type == "text" {
					resultText = part.Text

					break
				}
			}
			m.appendToolMessage(fmt.Sprintf("Result: %s", truncate(resultText, 500)))
		}

	case schema.EventTokenBudgetExhausted:
		if data, ok := event.Data.(schema.TokenBudgetExhaustedData); ok {
			m.appendSystemMessage(fmt.Sprintf("Token budget exhausted (used %d/%d in %d iterations)", data.Used, data.Budget, data.Iterations))
		}

	case schema.EventAgentEnd:
		// Finalize the response: render markdown on the complete text.
		if m.output.Len() > 0 {
			rendered := renderAgentMessage(m.output.String(), m.width-4)
			m.replaceLastAgentOutput(rendered)
		}

	case schema.EventError:
		if data, ok := event.Data.(schema.ErrorData); ok {
			m.appendErrorMessage(data.Message)
		}

	// Suppress LLM call events for cleaner UX.
	case schema.EventLLMCallStart, schema.EventLLMCallEnd, schema.EventLLMCallError:
		// Intentionally suppressed.
	}

	return m, nil
}

// handleStreamDone handles stream completion.
func (m *model) handleStreamDone(msg streamDoneMsg) (tea.Model, tea.Cmd) {
	if msg.err != nil {
		m.appendErrorMessage(msg.err.Error())
	}

	// Add agent response to conversation history for multi-turn context.
	if m.output.Len() > 0 {
		agentMsg := schema.NewAssistantMessage(
			agentMessage(m.output.String()),
			"",
		)
		m.app.history = append(m.app.history, agentMsg)
	}

	m.status = statusIdle
	m.textarea.SetValue("") // Clear any escape sequences accumulated while blurred.
	m.textarea.Focus()
	m.runCancel = nil

	return m, nil
}

// handleConfirmRequest shows a confirmation dialog.
func (m *model) handleConfirmRequest(msg confirmRequestMsg) (tea.Model, tea.Cmd) {
	m.status = statusConfirming
	m.pendingTC = &schema.ToolCallStartData{
		ToolName:  msg.toolName,
		Arguments: msg.arguments,
	}

	var approved bool
	m.confirmForm = huh.NewForm(
		huh.NewGroup(
			huh.NewConfirm().
				Key("confirm").
				Title(fmt.Sprintf("Allow tool call: %s?", msg.toolName)).
				Description(truncate(msg.arguments, 200)).
				Affirmative("Yes").
				Negative("No").
				Value(&approved),
		),
	).WithShowHelp(false)

	return m, m.confirmForm.Init()
}

// appendMessage adds a display message and updates the viewport.
func (m *model) appendMessage(msg DisplayMessage) {
	m.app.messages = append(m.app.messages, msg)
	m.refreshViewport()
}

// appendSystemMessage adds a styled system message.
func (m *model) appendSystemMessage(text string) {
	m.appendMessage(DisplayMessage{
		Role:      "system",
		Content:   text,
		Timestamp: time.Now(),
	})
}

// appendToolMessage adds a styled tool message.
func (m *model) appendToolMessage(text string) {
	m.appendMessage(DisplayMessage{
		Role:      "tool",
		Content:   text,
		Timestamp: time.Now(),
	})
}

// appendErrorMessage adds a styled error message.
func (m *model) appendErrorMessage(text string) {
	m.appendMessage(DisplayMessage{
		Role:      "error",
		Content:   text,
		Timestamp: time.Now(),
	})
}

// updateOutputInViewport tracks the current streaming output in the message
// list and refreshes the viewport.
func (m *model) updateOutputInViewport() {
	if m.currentAgentIdx >= 0 && m.currentAgentIdx < len(m.app.messages) {
		m.app.messages[m.currentAgentIdx].Content = m.output.String()
	} else {
		m.currentAgentIdx = len(m.app.messages)
		m.app.messages = append(m.app.messages, DisplayMessage{
			Role:      "agent",
			Content:   m.output.String(),
			Timestamp: time.Now(),
		})
	}

	m.refreshViewport()
}

// replaceLastAgentOutput replaces the raw streaming output with rendered markdown.
func (m *model) replaceLastAgentOutput(rendered string) {
	if m.currentAgentIdx >= 0 && m.currentAgentIdx < len(m.app.messages) {
		m.app.messages[m.currentAgentIdx].Content = rendered
		m.refreshViewport()

		return
	}

	// No agent message found, add one.
	m.currentAgentIdx = len(m.app.messages)
	m.appendMessage(DisplayMessage{
		Role:      "agent",
		Content:   rendered,
		Timestamp: time.Now(),
	})
}

// refreshViewport rebuilds the viewport content from all messages.
// This method only reads m.app.messages; it never mutates the slice.
func (m *model) refreshViewport() {
	var sb strings.Builder

	for _, msg := range m.app.messages {
		switch msg.Role {
		case "user":
			sb.WriteString(renderUserMessage(msg.Content))
		case "agent":
			sb.WriteString(agentStyle.Render("Agent: "))
			sb.WriteString(msg.Content)
		case "system":
			sb.WriteString(renderSystemMessage(msg.Content))
		case "tool":
			sb.WriteString(renderToolMessage(msg.Content))
		case "error":
			sb.WriteString(errorStyle.Render("Error: " + msg.Content))
		}

		sb.WriteString("\n\n")
	}

	m.viewport.SetContent(sb.String())
	m.viewport.GotoBottom()
}

// agentMessage creates an aimodel.Message from agent text.
func agentMessage(text string) aimodel.Message {
	return aimodel.Message{
		Role:    aimodel.RoleAssistant,
		Content: aimodel.NewTextContent(text),
	}
}
