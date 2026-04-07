package dispatches

import (
	"context"
	"fmt"
	"log/slog"
	"maps"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/orchestrate"
	"github.com/vogo/vage/schema"
	"github.com/vogo/vv/debugs"
)

// executeTask runs the dispatched task(s), handling direct and plan modes.
func (d *Dispatcher) executeTask(ctx context.Context, req *schema.RunRequest, intent *IntentResult, contextSummary string) (*schema.RunResponse, *aimodel.Usage, error) {
	switch intent.Mode {
	case "direct":
		ctx = debugs.WithAgentName(ctx, intent.Agent)
		cr := &ClassifyResult{Mode: intent.Mode, Agent: intent.Agent}
		resp, err := d.runDirect(ctx, req, cr, nil)
		if err != nil {
			return nil, nil, err
		}

		return resp, resp.Usage, nil
	case "plan":
		if d.replanPolicy.TriggerOnFailure && d.replanPolicy.MaxReplans > 0 {
			return d.executePlanWithReplanning(ctx, req, intent.Plan, contextSummary)
		}

		resp, err := d.runPlan(ctx, req, intent.Plan, nil, contextSummary)
		if err != nil {
			return nil, nil, err
		}

		return resp, resp.Usage, nil
	default:
		resp, err := d.fallbackRun(ctx, req, nil)
		if err != nil {
			return nil, nil, err
		}

		return resp, resp.Usage, nil
	}
}

// executeTaskStream is the streaming variant of executeTask.
func (d *Dispatcher) executeTaskStream(ctx context.Context, req *schema.RunRequest, intent *IntentResult, contextSummary string, send func(schema.Event) error) (*aimodel.Usage, error) {
	sessionID := req.SessionID

	switch intent.Mode {
	case "direct":
		subAgent, ok := d.subAgents[intent.Agent]
		if !ok {
			subAgent = d.fallbackAgent
		}

		ctx = debugs.WithAgentName(ctx, intent.Agent)
		err := d.forwardSubAgentStream(ctx, send, subAgent, req, intent.Agent, "", sessionID)

		return nil, err
	case "plan":
		dagUsage, err := d.streamPlan(ctx, send, req, intent.Plan, contextSummary, sessionID)

		return dagUsage, err
	default:
		err := d.forwardSubAgentStream(ctx, send, d.fallbackAgent, req, "chat", "", sessionID)

		return nil, err
	}
}

// executePlanWithReplanning executes a plan with dynamic replanning support.
// It runs the DAG in topological layers, checking for replan triggers after each layer.
func (d *Dispatcher) executePlanWithReplanning(ctx context.Context, req *schema.RunRequest, plan *Plan, contextSummary string) (*schema.RunResponse, *aimodel.Usage, error) {
	plan.MaxReplans = d.replanPolicy.MaxReplans

	layers := topologicalLayers(plan.Steps)
	if len(layers) == 0 {
		return &schema.RunResponse{}, nil, nil
	}

	completedResults := make(map[string]*schema.RunResponse)
	var totalUsage *aimodel.Usage
	replanCount := 0

	for layerIdx := 0; layerIdx < len(layers); layerIdx++ {
		layer := layers[layerIdx]

		// Build a sub-plan from the current layer.
		layerPlan := &Plan{
			Goal:  plan.Goal,
			Steps: layer,
		}

		// Build nodes for this layer only.
		nodes, err := d.buildNodes(layerPlan, req, contextSummary)
		if err != nil {
			slog.Warn("orchestrator: DAG build failed for layer", "layer", layerIdx, "error", err)

			return d.partialResponse(ctx, completedResults, totalUsage)
		}

		// Clear deps for this layer's nodes (they've been satisfied by prior layers).
		for i := range nodes {
			nodes[i].Deps = nil
			// Pass upstream results via input mapper enrichment.
			origMapper := nodes[i].InputMapper
			layerResults := completedResults

			nodes[i].InputMapper = func(upstream map[string]*schema.RunResponse) (*schema.RunRequest, error) {
				// Merge completed results into upstream.
				merged := make(map[string]*schema.RunResponse)
				maps.Copy(merged, layerResults)

				maps.Copy(merged, upstream)

				return origMapper(merged)
			}
		}

		dagCfg := orchestrate.DAGConfig{
			MaxConcurrency: d.maxConcurrency,
			ErrorStrategy:  orchestrate.Skip,
			Aggregator:     &PlanAggregator{Summarizer: d.planGen},
		}

		result, err := orchestrate.ExecuteDAG(ctx, dagCfg, nodes, req)
		if err != nil {
			// Check for failed nodes that we can replan.
			if d.replanPolicy.TriggerOnFailure && replanCount < d.replanPolicy.MaxReplans {
				replanCount++
				plan.ReplanCount = replanCount

				// Get failed node info from the result.
				failedNodes := make(map[string]error)
				if result != nil {
					for id, status := range result.NodeStatus {
						if status == orchestrate.NodeFailed {
							failedNodes[id] = fmt.Errorf("step %q failed during execution", id)
						}
					}
				}

				if len(failedNodes) > 0 {
					newPlan, replanErr := d.replanIfNeeded(ctx, plan, completedResults, failedNodes)
					if replanErr == nil && newPlan != nil {
						// Replace remaining layers with new plan's layers.
						newLayers := topologicalLayers(newPlan.Steps)
						layers = append(layers[:layerIdx+1], newLayers...)
						plan.Steps = newPlan.Steps

						continue
					}
				}
			}

			return d.partialResponse(ctx, completedResults, totalUsage)
		}

		// Store results from this layer.
		if result != nil {
			maps.Copy(completedResults, result.NodeResults)

			totalUsage = aggregateUsage(totalUsage, result.Usage)

			// Check for failures in this layer.
			failedNodes := make(map[string]error)
			for id, status := range result.NodeStatus {
				if status == orchestrate.NodeFailed {
					failedNodes[id] = fmt.Errorf("step %q failed", id)
				}
			}

			if len(failedNodes) > 0 && d.replanPolicy.TriggerOnFailure && replanCount < d.replanPolicy.MaxReplans {
				replanCount++
				plan.ReplanCount = replanCount

				newPlan, replanErr := d.replanIfNeeded(ctx, plan, completedResults, failedNodes)
				if replanErr == nil && newPlan != nil {
					newLayers := topologicalLayers(newPlan.Steps)
					layers = append(layers[:layerIdx+1], newLayers...)

					continue
				}
			}
		}
	}

	// Aggregate final results.
	aggregator := &PlanAggregator{Summarizer: d.planGen}
	resp, err := aggregator.Aggregate(ctx, completedResults)
	if err != nil {
		return d.partialResponse(ctx, completedResults, totalUsage)
	}

	resp.Usage = totalUsage

	return resp, totalUsage, nil
}

// replanIfNeeded checks completed results and failures and determines if replanning is needed.
// Returns nil if no replanning is needed, or a new Plan for the remaining work.
func (d *Dispatcher) replanIfNeeded(_ context.Context, plan *Plan, _ map[string]*schema.RunResponse, failedNodes map[string]error) (*Plan, error) {
	if len(failedNodes) == 0 {
		return nil, nil
	}

	// For now, replanning creates a simple retry plan for failed steps.
	// Future: use LLM to generate intelligent replacement steps.
	var retrySteps []PlanStep

	for _, step := range plan.Steps {
		if _, failed := failedNodes[step.ID]; failed {
			retryStep := step
			retryStep.ID = fmt.Sprintf("%s_r%d", step.ID, plan.ReplanCount)
			retryStep.ReplanGeneration = plan.ReplanCount
			retryStep.DependsOn = nil // Reset dependencies for retry.
			retrySteps = append(retrySteps, retryStep)
		}
	}

	if len(retrySteps) == 0 {
		return nil, nil
	}

	return &Plan{
		Goal:        plan.Goal,
		Steps:       retrySteps,
		ReplanCount: plan.ReplanCount,
		MaxReplans:  plan.MaxReplans,
	}, nil
}

// topologicalLayers groups DAG nodes into layers by their topological depth.
// Layer 0 = nodes with no deps, layer N = nodes whose deps are all in layers < N.
// Cyclic dependencies are broken by treating back-edges as satisfied (depth 0).
func topologicalLayers(steps []PlanStep) [][]PlanStep {
	if len(steps) == 0 {
		return nil
	}

	// Build a lookup map.
	stepMap := make(map[string]*PlanStep, len(steps))
	for i := range steps {
		stepMap[steps[i].ID] = &steps[i]
	}

	// Calculate depth for each node with cycle detection.
	depths := make(map[string]int, len(steps))
	visiting := make(map[string]bool, len(steps))

	var calcDepth func(id string) int
	calcDepth = func(id string) int {
		if d, ok := depths[id]; ok {
			return d
		}

		// Cycle detection: if we are already visiting this node, break the cycle.
		if visiting[id] {
			return 0
		}

		step, ok := stepMap[id]
		if !ok {
			return 0
		}

		visiting[id] = true

		maxDepDepth := -1
		for _, dep := range step.DependsOn {
			dd := calcDepth(dep)
			if dd > maxDepDepth {
				maxDepDepth = dd
			}
		}

		delete(visiting, id)

		depth := maxDepDepth + 1
		depths[id] = depth

		return depth
	}

	maxLayer := 0

	for _, step := range steps {
		d := calcDepth(step.ID)
		if d > maxLayer {
			maxLayer = d
		}
	}

	layers := make([][]PlanStep, maxLayer+1)

	for _, step := range steps {
		d := depths[step.ID]
		layers[d] = append(layers[d], step)
	}

	return layers
}

// partialResponse builds a response from partially completed results.
func (d *Dispatcher) partialResponse(ctx context.Context, completedResults map[string]*schema.RunResponse, usage *aimodel.Usage) (*schema.RunResponse, *aimodel.Usage, error) {
	if len(completedResults) == 0 {
		return &schema.RunResponse{Usage: usage}, usage, nil
	}

	aggregator := &PlanAggregator{Summarizer: d.planGen}

	resp, err := aggregator.Aggregate(ctx, completedResults)
	if err != nil {
		resp = &schema.RunResponse{}
	}

	resp.Usage = usage

	return resp, usage, nil
}
