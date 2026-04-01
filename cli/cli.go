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
	tea "github.com/charmbracelet/bubbletea"
	"github.com/charmbracelet/huh"
	"github.com/charmbracelet/lipgloss"
	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/memory"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/configs"
)

// App holds the CLI TUI application state.
type App struct {
	orchestrator  agent.StreamAgent // replaces routeFn + routes
	cfg           *configs.Config
	sessionID     string
	history       []schema.Message
	messages      []DisplayMessage
	program       *tea.Program
	persistentMem memory.Memory // for /memory commands
}

// New creates a new CLI App.
func New(
	orchestrator agent.StreamAgent,
	cfg *configs.Config,
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
		filepath.Join(configs.DefaultDir(), "vv.log"),
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

	// Inline mode: no WithAltScreen so output stays in terminal scrollback.
	p := tea.NewProgram(m)
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
	spinner  spinner.Model
	width    int
	height   int

	// State
	status sessionStatus
	output strings.Builder

	// Nesting depth for indentation. 0 = top-level agent,
	// 1 = inside a sub-agent, etc.
	nestingDepth int

	// Sub-agent metrics tracking.
	toolCallCount int // tool calls in current sub-agent

	// Task-level stats accumulation.
	taskStart             time.Time
	totalPromptTokens     int
	totalCompletionTokens int
	totalToolCalls        int

	// Sub-agent level stats accumulation (for DAG path where SubAgentEndData
	// may lack token stats).
	subAgentPromptTokens     int
	subAgentCompletionTokens int

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

	m := &model{
		app:       app,
		ctx:       ctx,
		textarea:  ta,
		spinner:   sp,
		status:    statusIdle,
		confirmCh: make(chan bool, 1),
	}

	return m
}

// toolDepth returns the indent depth for tool call output.
// Tools are always indented at least 1 level, and 1 level deeper than
// any active sub-agent.
func (m *model) toolDepth() int {
	return m.nestingDepth + 1
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
	header := m.headerView()
	welcome := fmt.Sprintf("Working directory: %s\nType a message to begin.",
		m.app.cfg.Tools.BashWorkingDir)

	return tea.Batch(
		textarea.Blink,
		m.spinner.Tick,
		tea.Println(header+"\n"+welcome),
	)
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

	return m, tea.Batch(cmds...)
}

func (m *model) headerView() string {
	dir := m.app.cfg.Tools.BashWorkingDir
	if home, err := os.UserHomeDir(); err == nil && strings.HasPrefix(dir, home) {
		dir = "~" + dir[len(home):]
	}

	headerStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("8")) // dim gray
	providerModel := fmt.Sprintf("vv · %s · %s", m.app.cfg.LLM.Provider, m.app.cfg.LLM.Model)
	line1 := headerStyle.Render(providerModel)
	dirStyle := lipgloss.NewStyle().Foreground(lipgloss.Color("12")).Bold(true)
	line2 := "  " + dirStyle.Render(dir)

	return line1 + "\n" + line2
}

func (m *model) View() string {
	var sb strings.Builder

	// Show current streaming output above input (live, not yet committed to scrollback).
	if m.output.Len() > 0 {
		line := agentStyle.Render("Agent: ") + m.output.String()
		sb.WriteString(indentBlock(line, m.nestingDepth))
		sb.WriteString("\n")
	}

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
		return m, m.printSystem("Request cancelled.")
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
		return m, m.printSystem("Confirmation cancelled.")
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
	if cmd := m.handleCommand(input); cmd != nil {
		m.textarea.Reset()
		return m, cmd
	}

	m.textarea.Reset()
	m.textarea.Blur()
	m.status = statusProcessing

	// Add user message to display and history.
	m.app.messages = append(m.app.messages, DisplayMessage{
		Role:      RoleUser,
		Content:   input,
		Timestamp: time.Now(),
	})
	userMsg := schema.NewUserMessage(input)
	m.app.history = append(m.app.history, userMsg)

	// Reset output builder and task-level stats for this round.
	m.output.Reset()
	m.taskStart = time.Now()
	m.totalPromptTokens = 0
	m.totalCompletionTokens = 0
	m.totalToolCalls = 0

	// Create a cancellable context for this run.
	runCtx, cancel := context.WithCancel(m.ctx)
	m.runCancel = cancel

	// Print user message to scrollback, then start agent.
	return m, tea.Batch(
		tea.Println(renderUserMessage(input)),
		m.invokeAgent(runCtx, input),
	)
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
		}

	case schema.EventToolCallStart:
		if data, ok := event.Data.(schema.ToolCallStartData); ok {
			// Flush any accumulated LLM text before showing the tool call.
			flushLine := m.flushAgentOutputLine()

			m.toolCallCount++
			m.totalToolCalls++
			rendered := renderToolCallStart(data.ToolName, data.Arguments, m.toolDepth())
			m.app.messages = append(m.app.messages, DisplayMessage{
				Role:      RoleTool,
				Content:   rendered,
				Timestamp: time.Now(),
				Rendered:  true,
			})

			if flushLine != "" {
				return m, tea.Println(flushLine + "\n" + rendered)
			}

			return m, tea.Println(rendered)
		}

	case schema.EventToolCallEnd:
		// Suppressed — result event provides richer info.

	case schema.EventToolResult:
		if data, ok := event.Data.(schema.ToolResultData); ok {
			resultText := ""
			for _, part := range data.Result.Content {
				if part.Type == "text" {
					resultText = part.Text
					break
				}
			}
			rendered := renderToolCallResult(data.ToolName, resultText, m.toolDepth())
			if rendered != "" {
				m.app.messages = append(m.app.messages, DisplayMessage{
					Role:      RoleToolResult,
					Content:   rendered,
					Timestamp: time.Now(),
					Rendered:  true,
				})
				return m, tea.Println(rendered)
			}
		}

	case schema.EventTokenBudgetExhausted:
		if data, ok := event.Data.(schema.TokenBudgetExhaustedData); ok {
			return m, m.printSystem(fmt.Sprintf("Token budget exhausted (used %d/%d in %d iterations)", data.Used, data.Budget, data.Iterations))
		}

	case schema.EventAgentEnd:
		// Finalize any remaining text from the agent.
		return m, m.flushAgentOutput()

	case schema.EventError:
		if data, ok := event.Data.(schema.ErrorData); ok {
			return m, m.printError(data.Message)
		}

	case schema.EventPhaseStart:
		if data, ok := event.Data.(schema.PhaseStartData); ok {
			rendered := renderPhaseTransition(data.Phase, true, execStats{}, 0)
			m.app.messages = append(m.app.messages, DisplayMessage{
				Role:      RolePhase,
				Content:   rendered,
				Timestamp: time.Now(),
				Rendered:  true,
			})
			return m, tea.Println(rendered)
		}

	case schema.EventPhaseEnd:
		if data, ok := event.Data.(schema.PhaseEndData); ok {
			var lines []string

			// Render phase summary if available (e.g., plan overview).
			if data.Summary != "" {
				summary := renderPhaseSummary(data.Summary, 1)
				m.app.messages = append(m.app.messages, DisplayMessage{
					Role:      RolePhase,
					Content:   summary,
					Timestamp: time.Now(),
					Rendered:  true,
				})
				lines = append(lines, summary)
			}

			stats := execStats{
				ToolCalls:        data.ToolCalls,
				DurationMs:       data.Duration,
				PromptTokens:     data.PromptTokens,
				CompletionTokens: data.CompletionTokens,
			}
			rendered := renderPhaseTransition(data.Phase, false, stats, 1)
			m.app.messages = append(m.app.messages, DisplayMessage{
				Role:      RolePhase,
				Content:   rendered,
				Timestamp: time.Now(),
				Rendered:  true,
			})
			lines = append(lines, rendered)

			return m, tea.Println(strings.Join(lines, "\n"))
		}

	case schema.EventSubAgentStart:
		if data, ok := event.Data.(schema.SubAgentStartData); ok {
			m.nestingDepth++
			m.toolCallCount = 0 // reset tool call counter
			m.subAgentPromptTokens = 0
			m.subAgentCompletionTokens = 0
			rendered := renderSubAgentStart(data.AgentName, data.StepID, data.Description, data.StepIndex, data.TotalSteps, m.nestingDepth)
			m.app.messages = append(m.app.messages, DisplayMessage{
				Role:      RoleSubAgent,
				Content:   rendered,
				Timestamp: time.Now(),
				Rendered:  true,
			})
			return m, tea.Println(rendered)
		}

	case schema.EventSubAgentEnd:
		if data, ok := event.Data.(schema.SubAgentEndData); ok {
			// Flush any remaining text from the sub-agent before showing its summary.
			flushLine := m.flushAgentOutputLine()

			toolCalls := data.ToolCalls
			if toolCalls == 0 {
				toolCalls = m.toolCallCount
			}

			promptTokens := data.PromptTokens
			if promptTokens == 0 {
				promptTokens = m.subAgentPromptTokens
			}

			completionTokens := data.CompletionTokens
			if completionTokens == 0 {
				completionTokens = m.subAgentCompletionTokens
			}

			stats := execStats{
				ToolCalls:        toolCalls,
				DurationMs:       data.Duration,
				PromptTokens:     promptTokens,
				CompletionTokens: completionTokens,
			}
			rendered := renderSubAgentEnd(data.AgentName, data.StepID, stats, m.nestingDepth)
			m.app.messages = append(m.app.messages, DisplayMessage{
				Role:      RoleSubAgent,
				Content:   rendered,
				Timestamp: time.Now(),
				Rendered:  true,
			})
			m.toolCallCount = 0
			m.subAgentPromptTokens = 0
			m.subAgentCompletionTokens = 0
			m.nestingDepth--
			if m.nestingDepth < 0 {
				m.nestingDepth = 0
			}

			if flushLine != "" {
				return m, tea.Println(flushLine + "\n" + rendered)
			}

			return m, tea.Println(rendered)
		}

	// Suppress LLM call events for cleaner UX, but accumulate token stats.
	case schema.EventLLMCallStart, schema.EventLLMCallError:
		// Intentionally suppressed.
	case schema.EventLLMCallEnd:
		if data, ok := event.Data.(schema.LLMCallEndData); ok {
			m.totalPromptTokens += data.PromptTokens
			m.totalCompletionTokens += data.CompletionTokens
			m.subAgentPromptTokens += data.PromptTokens
			m.subAgentCompletionTokens += data.CompletionTokens
		}
	}

	return m, nil
}

// handleStreamDone handles stream completion.
func (m *model) handleStreamDone(msg streamDoneMsg) (tea.Model, tea.Cmd) {
	var cmds []tea.Cmd

	if msg.err != nil {
		cmds = append(cmds, m.printError(msg.err.Error()))
	}

	// Add agent response to conversation history for multi-turn context.
	if m.output.Len() > 0 {
		agentMsg := schema.NewAssistantMessage(
			agentMessage(m.output.String()),
			"",
		)
		m.app.history = append(m.app.history, agentMsg)
		cmds = append(cmds, m.flushAgentOutput())
	}

	// Render task completion line.
	if !m.taskStart.IsZero() {
		taskDuration := time.Since(m.taskStart).Milliseconds()
		stats := execStats{
			DurationMs:       taskDuration,
			PromptTokens:     m.totalPromptTokens,
			CompletionTokens: m.totalCompletionTokens,
		}
		rendered := renderTaskComplete(stats)
		cmds = append(cmds, tea.Println(rendered))
	}

	m.status = statusIdle
	m.textarea.SetValue("") // Clear any escape sequences accumulated while blurred.
	m.textarea.Focus()
	m.runCancel = nil

	if len(cmds) > 0 {
		return m, tea.Batch(cmds...)
	}
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

// printSystem stores a system message and returns a tea.Cmd to print it to scrollback.
func (m *model) printSystem(text string) tea.Cmd {
	m.app.messages = append(m.app.messages, DisplayMessage{
		Role:      RoleSystem,
		Content:   text,
		Timestamp: time.Now(),
	})
	return tea.Println(renderSystemMessage(text))
}

// printError stores an error message and returns a tea.Cmd to print it to scrollback.
func (m *model) printError(text string) tea.Cmd {
	m.app.messages = append(m.app.messages, DisplayMessage{
		Role:      RoleError,
		Content:   text,
		Timestamp: time.Now(),
	})
	return tea.Println(errorStyle.Render("Error: " + text))
}

// flushAgentOutput finalizes any accumulated LLM text, returns a tea.Cmd to
// print it to the terminal scrollback, and resets the output buffer.
// flushAgentOutputLine flushes accumulated agent text and returns the rendered line.
// Returns empty string if there is nothing to flush.
func (m *model) flushAgentOutputLine() string {
	if m.output.Len() == 0 {
		return ""
	}

	text := m.output.String()
	rendered := renderAgentMessage(text, m.width-4-(m.nestingDepth*indentUnit))

	m.app.messages = append(m.app.messages, DisplayMessage{
		Role:      RoleAgent,
		Content:   rendered,
		Timestamp: time.Now(),
		Rendered:  true,
	})

	m.output.Reset()

	line := agentStyle.Render("Agent: ") + rendered

	return indentBlock(line, m.nestingDepth)
}

// flushAgentOutput flushes accumulated agent text as a tea.Println command.
func (m *model) flushAgentOutput() tea.Cmd {
	line := m.flushAgentOutputLine()
	if line == "" {
		return nil
	}

	return tea.Println(line)
}

// agentMessage creates an aimodel.Message from agent text.
func agentMessage(text string) aimodel.Message {
	return aimodel.Message{
		Role:    aimodel.RoleAssistant,
		Content: aimodel.NewTextContent(text),
	}
}
