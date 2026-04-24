package dispatches

import (
	"context"
	"log/slog"
	"time"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/agent"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/configs"
	"github.com/vogo/vv/hooks"
	"github.com/vogo/vv/registries"
)

// phaseTracker intercepts events to accumulate per-phase execution stats.
type phaseTracker struct {
	toolCalls        int
	promptTokens     int
	completionTokens int
}

// wrap returns a send function that intercepts stats events before forwarding.
func (pt *phaseTracker) wrap(send func(schema.Event) error) func(schema.Event) error {
	return func(ev schema.Event) error {
		switch ev.Type {
		case schema.EventToolCallStart:
			pt.toolCalls++
		case schema.EventLLMCallEnd:
			if data, ok := ev.Data.(schema.LLMCallEndData); ok {
				pt.promptTokens += data.PromptTokens
				pt.completionTokens += data.CompletionTokens
			}
		}

		return send(ev)
	}
}

// Dispatcher is the main orchestration agent. It receives user requests,
// performs intent recognition, and dispatches to sub-agents.
// Behavioral equivalent of the former OrchestratorAgent.
type Dispatcher struct {
	agent.Base
	llm                aimodel.ChatCompleter
	model              string
	registry           *registries.Registry
	subAgents          map[string]agent.Agent // built from registry, keyed by descriptor ID
	planGen            agent.Agent            // agent.Agent interface, not *taskagent.Agent
	maxConcurrency     int
	fallbackAgent      agent.Agent
	workingDir         string
	explorerAgent      agent.Agent
	plannerAgent       agent.Agent
	toolsCfg           configs.ToolsConfig // for dynamic agent tool registry construction
	hooks              []hooks.Hook
	maxIterations      int
	runTokenBudget     int
	intentSystemPrompt string // used for direct LLM intent recognition

	projectInstructions string // content from VV.md for intent recognition and dynamic agents

	// New fields for adaptive decision loop.
	summaryPolicy     SummaryPolicy
	replanPolicy      ReplanPolicy
	maxRecursionDepth int
	summarizer        agent.Agent // agent for generating summaries (reuses planGen if nil)

	// fastPath shorts-circuits trivial requests without invoking the intent LLM.
	fastPath FastPathConfig
}

// Option configures a Dispatcher.
type Option func(*Dispatcher)

// New creates a Dispatcher with required parameters and optional configuration.
func New(
	reg *registries.Registry,
	subAgents map[string]agent.Agent,
	explorerAgent agent.Agent,
	plannerAgent agent.Agent,
	planGen agent.Agent,
	opts ...Option,
) *Dispatcher {
	d := &Dispatcher{
		Base: agent.NewBase(agent.Config{
			ID:          "orchestrator",
			Name:        "Orchestrator Agent",
			Description: "Orchestrates user requests: recognizes intent, dispatches to agents",
		}),
		registry:          reg,
		subAgents:         subAgents,
		explorerAgent:     explorerAgent,
		plannerAgent:      plannerAgent,
		planGen:           planGen,
		summaryPolicy:     SummaryAuto,
		replanPolicy:      DefaultReplanPolicy(),
		maxRecursionDepth: 2,
		fastPath:          DefaultFastPathConfig(),
	}

	for _, opt := range opts {
		opt(d)
	}

	return d
}

// WithLLM sets the LLM client for direct classification calls and dynamic agent creation.
func WithLLM(llm aimodel.ChatCompleter, model string) Option {
	return func(d *Dispatcher) {
		d.llm = llm
		d.model = model
	}
}

// WithMaxConcurrency sets DAG concurrency limit.
func WithMaxConcurrency(n int) Option {
	return func(d *Dispatcher) {
		d.maxConcurrency = n
	}
}

// WithFallbackAgent sets the fallback agent for failed classifications.
func WithFallbackAgent(a agent.Agent) Option {
	return func(d *Dispatcher) {
		d.fallbackAgent = a
	}
}

// WithWorkingDir sets the working directory for enriching requests.
func WithWorkingDir(dir string) Option {
	return func(d *Dispatcher) {
		d.workingDir = dir
	}
}

// WithToolsConfig sets tool configuration for dynamic agent registry construction.
func WithToolsConfig(cfg configs.ToolsConfig) Option {
	return func(d *Dispatcher) {
		d.toolsCfg = cfg
	}
}

// WithHooks sets lifecycle hooks for sub-agent execution.
func WithHooks(hooks []hooks.Hook) Option {
	return func(d *Dispatcher) {
		d.hooks = hooks
	}
}

// WithMaxIterations sets the max iterations for dynamic agents.
func WithMaxIterations(n int) Option {
	return func(d *Dispatcher) {
		d.maxIterations = n
	}
}

// WithRunTokenBudget sets the token budget for dynamic agents.
func WithRunTokenBudget(n int) Option {
	return func(d *Dispatcher) {
		d.runTokenBudget = n
	}
}

// WithIntentSystemPrompt sets the system prompt for intent recognition.
func WithIntentSystemPrompt(p string) Option {
	return func(d *Dispatcher) {
		d.intentSystemPrompt = p
	}
}

// WithPlannerSystemPrompt sets the system prompt for intent recognition.
// Deprecated: Use WithIntentSystemPrompt. Kept for backward compatibility.
func WithPlannerSystemPrompt(p string) Option {
	return WithIntentSystemPrompt(p)
}

// WithSummaryPolicy sets the summary policy.
func WithSummaryPolicy(p SummaryPolicy) Option {
	return func(d *Dispatcher) {
		d.summaryPolicy = p
	}
}

// WithReplanPolicy sets the replan policy.
func WithReplanPolicy(p ReplanPolicy) Option {
	return func(d *Dispatcher) {
		d.replanPolicy = p
	}
}

// WithMaxRecursionDepth sets the max recursion depth.
func WithMaxRecursionDepth(n int) Option {
	return func(d *Dispatcher) {
		d.maxRecursionDepth = n
	}
}

// WithSummarizer sets the agent used for generating summaries.
func WithSummarizer(a agent.Agent) Option {
	return func(d *Dispatcher) {
		d.summarizer = a
	}
}

// WithProjectInstructions sets the project instructions for intent recognition
// and dynamic agent creation.
func WithProjectInstructions(instructions string) Option {
	return func(d *Dispatcher) {
		d.projectInstructions = instructions
	}
}

// WithFastPath configures the heuristic short-circuit filter applied before
// intent recognition. Pass DisabledFastPathConfig() to turn it off.
func WithFastPath(cfg FastPathConfig) Option {
	return func(d *Dispatcher) {
		d.fastPath = cfg
	}
}

// Run implements agent.Agent. Orchestrates: intent recognition -> execution -> optional summarization.
func (d *Dispatcher) Run(ctx context.Context, req *schema.RunRequest) (*schema.RunResponse, error) {
	depth := DepthFrom(ctx)

	// Fast path: at max depth, execute directly with fallback agent.
	if depth >= d.maxRecursionDepth {
		return d.fallbackRun(ctx, req, nil)
	}

	// Heuristic short-circuit: greetings, small-talk, and obvious shell-like
	// inputs bypass the intent LLM entirely.
	if hit := d.fastPathClassify(req); hit.Hit {
		return d.runFastPath(ctx, req, hit)
	}

	// Phase 1: Intent Recognition (may invoke explorer on-demand).
	intent, contextSummary, intentUsage, err := d.recognizeIntent(ctx, req)
	if err != nil {
		slog.Warn("orchestrator: intent recognition failed, falling back to chat", "error", err)

		return d.fallbackRun(ctx, req, nil)
	}

	// Phase 2: Execute.
	enrichedReq := d.enrichRequest(req, contextSummary)
	childCtx := IncrementDepth(ctx)

	resp, execUsage, err := d.executeTask(childCtx, enrichedReq, intent, contextSummary)
	if err != nil {
		return nil, err
	}

	totalUsage := aggregateUsage(intentUsage, execUsage)
	resp.Usage = totalUsage

	// Phase 3: Summarize (optional).
	if d.shouldSummarize(req) && len(resp.Messages) > 0 {
		summaryResp, summaryErr := d.summarize(ctx, req, []*schema.RunResponse{resp})
		if summaryErr == nil {
			summaryResp.Usage = aggregateUsage(totalUsage, summaryResp.Usage)
			resp = summaryResp
		}
	}

	return resp, nil
}

// RunStream implements agent.StreamAgent. Same flow as Run but with streaming events.
func (d *Dispatcher) RunStream(ctx context.Context, req *schema.RunRequest) (*schema.RunStream, error) {
	return schema.NewRunStream(ctx, agent.DefaultStreamBufferSize, func(ctx context.Context, send func(schema.Event) error) error {
		agentID := d.ID()
		sessionID := req.SessionID
		depth := DepthFrom(ctx)

		// Fast path: at max depth, execute directly.
		if depth >= d.maxRecursionDepth {
			return d.forwardSubAgentStream(ctx, send, d.fallbackAgent, req, "chat", "", sessionID)
		}

		// Heuristic short-circuit: bypass intent LLM for trivial inputs.
		if hit := d.fastPathClassify(req); hit.Hit {
			return d.runFastPathStream(ctx, send, req, hit)
		}

		// Phase 1: Intent Recognition.
		if err := send(schema.NewEvent(schema.EventPhaseStart, agentID, sessionID, schema.PhaseStartData{
			Phase: "intent", PhaseIndex: 1, TotalPhase: 0,
		})); err != nil {
			return err
		}

		intentStart := time.Now()

		var intentTracker phaseTracker
		intent, contextSummary, _, intentErr := d.recognizeIntentStream(ctx, req, intentTracker.wrap(send))

		intentSummary := buildIntentSummary(intent)
		intentDuration := time.Since(intentStart).Milliseconds()

		if err := send(schema.NewEvent(schema.EventPhaseEnd, agentID, sessionID, schema.PhaseEndData{
			Phase:            "intent",
			Duration:         intentDuration,
			Summary:          intentSummary,
			ToolCalls:        intentTracker.toolCalls,
			PromptTokens:     intentTracker.promptTokens,
			CompletionTokens: intentTracker.completionTokens,
		})); err != nil {
			return err
		}

		if intentErr != nil {
			slog.Warn("orchestrator: intent recognition failed, falling back to chat stream", "error", intentErr)

			return d.forwardSubAgentStream(ctx, send, d.fallbackAgent, req, "chat", "", sessionID)
		}

		enrichedReq := d.enrichRequest(req, contextSummary)
		childCtx := IncrementDepth(ctx)

		// Phase 2: Execute.
		if err := send(schema.NewEvent(schema.EventPhaseStart, agentID, sessionID, schema.PhaseStartData{
			Phase: "execute", PhaseIndex: 2, TotalPhase: 0,
		})); err != nil {
			return err
		}

		executeStart := time.Now()

		var executeTracker phaseTracker
		dagUsage, executeErr := d.executeTaskStream(childCtx, enrichedReq, intent, contextSummary, executeTracker.wrap(send))

		// Augment execute phase stats from DAG result usage.
		if dagUsage != nil {
			executeTracker.promptTokens += dagUsage.PromptTokens
			executeTracker.completionTokens += dagUsage.CompletionTokens
		}

		executeDuration := time.Since(executeStart).Milliseconds()

		if sendErr := send(schema.NewEvent(schema.EventPhaseEnd, agentID, sessionID, schema.PhaseEndData{
			Phase:            "execute",
			Duration:         executeDuration,
			ToolCalls:        executeTracker.toolCalls,
			PromptTokens:     executeTracker.promptTokens,
			CompletionTokens: executeTracker.completionTokens,
		})); sendErr != nil {
			return sendErr
		}

		if executeErr != nil {
			return executeErr
		}

		// Phase 3: Summarize (optional).
		if d.shouldSummarize(req) {
			if err := send(schema.NewEvent(schema.EventPhaseStart, agentID, sessionID, schema.PhaseStartData{
				Phase: "summarize", PhaseIndex: 3, TotalPhase: 0,
			})); err != nil {
				return err
			}

			summarizeStart := time.Now()

			summarizer := d.summarizer
			if summarizer == nil {
				summarizer = d.planGen
			}

			if summarizer != nil {
				err := d.forwardSubAgentStream(ctx, send, summarizer, req, "summarizer", "", sessionID)
				if err != nil {
					slog.Warn("orchestrator: summarization stream failed", "error", err)
				}
			}

			summarizeDuration := time.Since(summarizeStart).Milliseconds()

			if err := send(schema.NewEvent(schema.EventPhaseEnd, agentID, sessionID, schema.PhaseEndData{
				Phase:    "summarize",
				Duration: summarizeDuration,
			})); err != nil {
				return err
			}
		}

		return nil
	}), nil
}

// Compile-time interface checks.
var (
	_ agent.Agent       = (*Dispatcher)(nil)
	_ agent.StreamAgent = (*Dispatcher)(nil)
)
