package dispatches

import (
	"context"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/sessionlogs"
)

// maxFinalizeActivityEntries caps how many tool calls are replayed into the
// finalizer prompt. The finalizer only needs enough of the trail to describe
// what was learned, not a full transcript.
const maxFinalizeActivityEntries = 20

// maxFinalizeArgLen truncates each recorded tool argument blob.
const maxFinalizeArgLen = 160

// isIncompleteStop reports whether a stop reason means the agent ran out of
// budget rather than finishing its work. Both cases end the ReAct loop with
// the model mid-thought: vage returns immediately after the last tool batch,
// so unless we do something the user gets tool noise and no answer.
func isIncompleteStop(r schema.StopReason) bool {
	return r == schema.StopReasonMaxIterations || r == schema.StopReasonBudgetExhausted
}

// primaryRunObserver accumulates the facts needed to salvage a run that ended
// on a budget ceiling: the terminal stop reason, the tool calls made, and
// whether the Primary managed to emit any prose at all.
type primaryRunObserver struct {
	stop     schema.StopReason
	tools    []string
	textSeen bool
}

// observe records a relayed event. It never mutates or filters the event —
// the stream the caller sees is unchanged.
func (o *primaryRunObserver) observe(ev schema.Event) {
	switch ev.Type {
	case schema.EventTextDelta:
		if data, ok := ev.Data.(schema.TextDeltaData); ok && strings.TrimSpace(data.Delta) != "" {
			o.textSeen = true
		}
	case schema.EventToolCallStart:
		if data, ok := ev.Data.(schema.ToolCallStartData); ok {
			o.recordTool(data.ToolName, data.Arguments)
		}
	case schema.EventAgentEnd:
		if data, ok := ev.Data.(schema.AgentEndData); ok {
			o.stop = data.StopReason
		}
	}
}

func (o *primaryRunObserver) recordTool(name, args string) {
	if len(o.tools) >= maxFinalizeActivityEntries {
		return
	}

	args = strings.TrimSpace(args)
	if len(args) > maxFinalizeArgLen {
		args = args[:maxFinalizeArgLen] + "…"
	}

	if args == "" {
		o.tools = append(o.tools, name)
		return
	}

	o.tools = append(o.tools, name+" "+args)
}

// activity renders the recorded tool trail for the finalizer prompt.
func (o *primaryRunObserver) activity() string {
	if len(o.tools) == 0 {
		return ""
	}

	var sb strings.Builder
	for _, t := range o.tools {
		sb.WriteString("- ")
		sb.WriteString(t)
		sb.WriteString("\n")
	}

	return sb.String()
}

// finalizeIncompleteStream salvages a Primary run that hit a budget ceiling.
// It asks the tool-free fallback Primary for a closing answer and relays it as
// ordinary text, followed by a fresh AgentEnd that preserves the original stop
// reason so the CLI / SSE layer can still label the turn as incomplete.
//
// Failures here are logged and swallowed: the user already received the run's
// events, and turning a salvage attempt into a stream error would replace a
// partial result with none at all.
func (d *Dispatcher) finalizeIncompleteStream(
	ctx context.Context,
	send func(schema.Event) error,
	req *schema.RunRequest,
	obs *primaryRunObserver,
) error {
	if !isIncompleteStop(obs.stop) {
		return nil
	}

	text, err := d.runFinalizer(ctx, req, obs.stop, obs.activity())
	if err != nil {
		slog.Warn("vv: finalize incomplete primary run", "stop_reason", obs.stop, "error", err)
		return nil
	}

	if text == "" {
		return nil
	}

	agentID := PrimaryAgentName
	if d.fallbackAgent != nil {
		agentID = d.fallbackAgent.ID()
	}

	if err := send(schema.NewEvent(schema.EventTextDelta, agentID, req.SessionID, schema.TextDeltaData{
		Delta: text,
	})); err != nil {
		return err
	}

	return send(schema.NewEvent(schema.EventAgentEnd, agentID, req.SessionID, schema.AgentEndData{
		Message:    text,
		StopReason: obs.stop,
	}))
}

// finalizeIncompleteResponse is the non-streaming counterpart: it appends the
// salvaged closing message to the response the Primary returned. The stop
// reason is left untouched so callers keep seeing why the run ended.
func (d *Dispatcher) finalizeIncompleteResponse(
	ctx context.Context,
	req *schema.RunRequest,
	resp *schema.RunResponse,
) {
	if resp == nil || !isIncompleteStop(resp.StopReason) {
		return
	}

	text, err := d.runFinalizer(ctx, req, resp.StopReason, "")
	if err != nil {
		slog.Warn("vv: finalize incomplete primary run", "stop_reason", resp.StopReason, "error", err)
		return
	}

	if text == "" {
		return
	}

	resp.Messages = append(resp.Messages, schema.NewTextMessage(d.Protocol(), schema.RoleAssistant, text))
}

// runFinalizer runs the tool-free fallback Primary over the original request
// plus a closing instruction. Tool-free is the point: the run already proved
// it cannot finish within its budget, so handing it more tools would only
// restart the loop that exhausted it.
func (d *Dispatcher) runFinalizer(
	ctx context.Context,
	req *schema.RunRequest,
	stop schema.StopReason,
	activity string,
) (string, error) {
	if d.fallbackAgent == nil {
		return "", nil
	}

	proto := d.fallbackAgent.Protocol()

	msgs := make([]schema.Message, 0, len(req.Messages)+1)
	msgs = append(msgs, req.Messages...)
	msgs = append(msgs, schema.NewUserMessage(proto, finalizeInstruction(stop, activity)))

	// Tag the salvage run so its transcript lands in subagents/ rather than
	// the session's resume timeline — the user-facing text still reaches the
	// caller through the returned string.
	ctx = sessionlogs.WithRun(ctx, sessionlogs.Run{
		Key:  sessionlogs.NewRunKey(),
		Task: "finalize incomplete run",
	})

	start := time.Now()

	resp, err := d.fallbackAgent.Run(ctx, &schema.RunRequest{
		Messages:  msgs,
		SessionID: req.SessionID,
	})
	if err != nil {
		return "", fmt.Errorf("finalizer run: %w", err)
	}

	slog.Debug("vv: finalized incomplete run", "stop_reason", stop, "duration_ms", time.Since(start).Milliseconds())

	var sb strings.Builder

	for _, m := range resp.Messages {
		if text := strings.TrimSpace(m.Text()); text != "" {
			if sb.Len() > 0 {
				sb.WriteString("\n\n")
			}

			sb.WriteString(text)
		}
	}

	if sb.Len() == 0 {
		return "", nil
	}

	return "\n\n" + sb.String() + "\n", nil
}

// finalizeInstruction renders the closing prompt handed to the finalizer.
func finalizeInstruction(stop schema.StopReason, activity string) string {
	var reason string

	switch stop {
	case schema.StopReasonBudgetExhausted:
		reason = "the token budget for this run was exhausted"
	default:
		reason = "the tool-iteration budget for this run was exhausted"
	}

	var sb strings.Builder

	fmt.Fprintf(&sb, "SYSTEM: This turn was cut short because %s, so the assistant never produced a reply. "+
		"You have no tools now — do not propose calling any.\n\n", reason)

	if activity != "" {
		sb.WriteString("Tool calls made during the turn:\n")
		sb.WriteString(activity)
		sb.WriteString("\n")
	}

	sb.WriteString("Write the reply the user is still waiting for, in their language, covering:\n" +
		"1. what was established about the request (be concrete — name files and findings, never invent any);\n" +
		"2. what remains undone;\n" +
		"3. the single most useful next step, phrased so the user can approve it in one message.\n" +
		"Do not apologise at length and do not restate this instruction.")

	return sb.String()
}
