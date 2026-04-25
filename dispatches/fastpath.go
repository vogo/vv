package dispatches

import (
	"context"
	"fmt"
	"log/slog"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/debugs"
)

const (
	// DefaultFastPathMaxChars is the default rune-length cap beyond which the
	// heuristic short-circuit declines.
	DefaultFastPathMaxChars = 60

	// FastPathCategoryGreeting matches greetings / small-talk and routes to chat.
	FastPathCategoryGreeting = "greeting"

	// FastPathCategoryToolTrigger matches shell-like prefixes and routes to coder.
	FastPathCategoryToolTrigger = "tool_trigger"

	// fastPathPhase is the Phase value emitted in stream events when the
	// fast-path short-circuit fires instead of the intent phase.
	fastPathPhase = "fast_path"
)

// defaultGreetingPatterns are the built-in greeting triggers. Case-insensitive
// for ASCII; Chinese patterns match literal prefixes.
var defaultGreetingPatterns = []string{
	`(?i)^(hi|hello|hey|thanks|thank you|bye|goodbye)(\b|$)`,
	`^(你好|您好|在吗|哈喽|再见)`,
}

// defaultToolTriggerPatterns are the built-in shell-like triggers that route
// to coder so its bash tool can handle them directly.
var defaultToolTriggerPatterns = []string{
	`(?i)^(calc|echo|date|pwd|ls|whoami|uptime)(\s|$)`,
}

// FastPathRule matches a user prompt and routes it to a sub-agent.
//
// Agent is a registered sub-agent ID. As of M6 an empty Agent value means
// "use the Dispatcher's fallback agent at execution time" — late-bound so
// the rule does not have to hard-code the historical "chat" name; the
// fallback is resolved by Dispatcher.fallbackAgentName when the rule fires.
type FastPathRule struct {
	Category string
	Pattern  *regexp.Regexp
	Agent    string
}

// FastPathConfig configures the Dispatcher's heuristic short-circuit.
// When Enabled is true and a rule matches, the request bypasses intent
// recognition entirely and dispatches straight to Agent.
type FastPathConfig struct {
	Enabled  bool
	MaxChars int
	Rules    []FastPathRule
}

// DefaultFastPathConfig returns the built-in defaults: enabled, MaxChars=60,
// greetings → fallback (empty Agent, late-bound per M6 G6), tool triggers
// → coder.
func DefaultFastPathConfig() FastPathConfig {
	rules := make([]FastPathRule, 0, len(defaultGreetingPatterns)+len(defaultToolTriggerPatterns))

	for _, p := range defaultGreetingPatterns {
		rules = append(rules, FastPathRule{
			Category: FastPathCategoryGreeting,
			Pattern:  regexp.MustCompile(p),
			Agent:    "", // late-bound to Dispatcher fallback agent
		})
	}

	for _, p := range defaultToolTriggerPatterns {
		rules = append(rules, FastPathRule{
			Category: FastPathCategoryToolTrigger,
			Pattern:  regexp.MustCompile(p),
			Agent:    "coder",
		})
	}

	return FastPathConfig{Enabled: true, MaxChars: DefaultFastPathMaxChars, Rules: rules}
}

// DisabledFastPathConfig returns a FastPathConfig with Enabled=false.
func DisabledFastPathConfig() FastPathConfig {
	return FastPathConfig{Enabled: false}
}

// fastPathResult is the outcome of fast-path classification.
type fastPathResult struct {
	Hit      bool
	Agent    string
	Category string
	Pattern  string
}

// fastPathClassify decides whether req qualifies for the short-circuit.
// Returns a zero-value result when it does not.
func (d *Dispatcher) fastPathClassify(req *schema.RunRequest) fastPathResult {
	if !d.fastPath.Enabled || len(d.fastPath.Rules) == 0 {
		return fastPathResult{}
	}

	// Multi-turn context guard: only single-shot requests are eligible.
	if len(req.Messages) > 2 {
		return fastPathResult{}
	}

	// Tool-history guard: any prior tool call or tool-role message disqualifies.
	for _, m := range req.Messages {
		if m.Role == aimodel.RoleTool || len(m.ToolCalls) > 0 {
			return fastPathResult{}
		}
	}

	last, ok := lastUserMessage(req.Messages)
	if !ok {
		return fastPathResult{}
	}

	text := strings.TrimSpace(last.Content.Text())
	if text == "" {
		return fastPathResult{}
	}

	maxChars := d.fastPath.MaxChars
	if maxChars <= 0 {
		maxChars = DefaultFastPathMaxChars
	}

	if utf8.RuneCountInString(text) > maxChars {
		return fastPathResult{}
	}

	for _, rule := range d.fastPath.Rules {
		if rule.Pattern == nil {
			continue
		}

		if !rule.Pattern.MatchString(text) {
			continue
		}

		// Resolve a late-bound empty Agent to the Dispatcher's fallback
		// at hit time (design M6 G6 — keeps DefaultFastPathConfig from
		// hard-coding "chat"). The fallback agent does not live in
		// d.subAgents, so the membership check is skipped on this branch.
		agentID := rule.Agent
		if agentID == "" {
			if d.fallbackAgent == nil {
				continue
			}

			agentID = d.fallbackAgentName()
		} else if _, known := d.subAgents[agentID]; !known {
			continue
		}

		return fastPathResult{
			Hit:      true,
			Agent:    agentID,
			Category: rule.Category,
			Pattern:  rule.Pattern.String(),
		}
	}

	return fastPathResult{}
}

// lastUserMessage returns the last user-role message in msgs.
func lastUserMessage(msgs []schema.Message) (schema.Message, bool) {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == aimodel.RoleUser {
			return msgs[i], true
		}
	}

	return schema.Message{}, false
}

// runFastPath executes a fast-path hit in non-streaming mode.
func (d *Dispatcher) runFastPath(ctx context.Context, req *schema.RunRequest, hit fastPathResult) (*schema.RunResponse, error) {
	slog.Info("dispatcher: fast-path hit",
		"category", hit.Category,
		"agent", hit.Agent,
		"matched_pattern", hit.Pattern,
	)

	sub, ok := d.subAgents[hit.Agent]
	if !ok {
		// Late-bound rule (empty Agent in config) resolves to the fallback
		// agent at classify time; that agent is not in d.subAgents, so look
		// it up directly. If the names disagree, fall through to
		// fallbackRun's classification-failure path.
		if d.fallbackAgent != nil && hit.Agent == d.fallbackAgentName() {
			sub = d.fallbackAgent
		} else {
			return d.fallbackRun(ctx, req, nil)
		}
	}

	ctx = debugs.WithAgentName(ctx, hit.Agent)

	resp, err := d.runWithHooks(ctx, hit.Agent, req, func() (*schema.RunResponse, error) {
		return sub.Run(ctx, req)
	})
	if err != nil {
		return nil, fmt.Errorf("fast-path sub-agent %q failed: %w", hit.Agent, err)
	}

	if d.shouldSummarize(req) && len(resp.Messages) > 0 {
		summaryResp, summaryErr := d.summarize(ctx, req, []*schema.RunResponse{resp})
		if summaryErr == nil {
			summaryResp.Usage = aggregateUsage(resp.Usage, summaryResp.Usage)
			resp = summaryResp
		}
	}

	return resp, nil
}

// runFastPathStream executes a fast-path hit in streaming mode. It emits a
// phase_start/phase_end pair with Phase="fast_path" and zero token / tool-call
// counts so dashboards can surface the savings.
func (d *Dispatcher) runFastPathStream(
	ctx context.Context,
	send func(schema.Event) error,
	req *schema.RunRequest,
	hit fastPathResult,
) error {
	slog.Info("dispatcher: fast-path hit (stream)",
		"category", hit.Category,
		"agent", hit.Agent,
		"matched_pattern", hit.Pattern,
	)

	agentID := d.ID()
	sessionID := req.SessionID

	if err := send(schema.NewEvent(schema.EventPhaseStart, agentID, sessionID, schema.PhaseStartData{
		Phase:      fastPathPhase,
		PhaseIndex: 1,
		TotalPhase: 0,
	})); err != nil {
		return err
	}

	start := time.Now()

	sub, ok := d.subAgents[hit.Agent]
	if !ok {
		// Late-bound rule resolves to the fallback agent at classify time;
		// it lives outside d.subAgents. If names match, run it directly;
		// otherwise emit a zero-cost phase_end and forward to fallback.
		if d.fallbackAgent != nil && hit.Agent == d.fallbackAgentName() {
			sub = d.fallbackAgent
		} else {
			_ = send(schema.NewEvent(schema.EventPhaseEnd, agentID, sessionID, schema.PhaseEndData{
				Phase:    fastPathPhase,
				Duration: time.Since(start).Milliseconds(),
				Summary:  "Fast path declined (agent missing)",
			}))

			return d.forwardSubAgentStream(ctx, send, d.fallbackAgent, req, d.fallbackAgentName(), "", sessionID)
		}
	}

	if err := send(schema.NewEvent(schema.EventPhaseEnd, agentID, sessionID, schema.PhaseEndData{
		Phase:            fastPathPhase,
		Duration:         time.Since(start).Milliseconds(),
		Summary:          "Fast path -> " + hit.Agent,
		ToolCalls:        0,
		PromptTokens:     0,
		CompletionTokens: 0,
	})); err != nil {
		return err
	}

	ctx = debugs.WithAgentName(ctx, hit.Agent)

	if err := d.forwardSubAgentStream(ctx, send, sub, req, hit.Agent, "", sessionID); err != nil {
		return err
	}

	if d.shouldSummarize(req) {
		if err := send(schema.NewEvent(schema.EventPhaseStart, agentID, sessionID, schema.PhaseStartData{
			Phase:      "summarize",
			PhaseIndex: 2,
			TotalPhase: 0,
		})); err != nil {
			return err
		}

		summarizeStart := time.Now()

		summarizer := d.summarizer
		if summarizer == nil {
			summarizer = d.planGen
		}

		if summarizer != nil {
			if err := d.forwardSubAgentStream(ctx, send, summarizer, req, "summarizer", "", sessionID); err != nil {
				slog.Warn("dispatcher: fast-path summarization stream failed", "error", err)
			}
		}

		if err := send(schema.NewEvent(schema.EventPhaseEnd, agentID, sessionID, schema.PhaseEndData{
			Phase:    "summarize",
			Duration: time.Since(summarizeStart).Milliseconds(),
		})); err != nil {
			return err
		}
	}

	return nil
}
