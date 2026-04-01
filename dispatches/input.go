package dispatches

import (
	"fmt"
	"sort"

	"github.com/vogo/vage/orchestrate"
	"github.com/vogo/vage/schema"
)

// StepInput holds all parameters needed to construct input messages for a DAG step.
type StepInput struct {
	WorkingDir      string
	ContextSummary  string
	OriginalGoal    string
	StepDescription string
	Upstream        map[string]StepResult
}

// StepResult holds the output of a completed upstream step.
type StepResult struct {
	Output string
	Status StepStatus
}

// StepStatus represents the completion status of a step.
type StepStatus int

const (
	StepCompleted StepStatus = iota
	StepFailed
	StepSkipped
)

// BuildMessages constructs the input messages for a DAG step.
// This is a pure function with no side effects, making it fully testable.
func (s *StepInput) BuildMessages() []schema.Message {
	var msgs []schema.Message

	if s.WorkingDir != "" {
		msgs = append(msgs, schema.NewUserMessage(
			fmt.Sprintf("Working directory: %s", s.WorkingDir),
		))
	}

	if s.ContextSummary != "" {
		msgs = append(msgs, schema.NewUserMessage(
			fmt.Sprintf("Project context:\n%s", s.ContextSummary),
		))
	}

	if s.OriginalGoal != "" {
		msgs = append(msgs, schema.NewUserMessage(
			fmt.Sprintf("Original request: %s", s.OriginalGoal),
		))
	}

	// Sort upstream keys for deterministic message ordering.
	upstreamKeys := make([]string, 0, len(s.Upstream))
	for k := range s.Upstream {
		upstreamKeys = append(upstreamKeys, k)
	}

	sort.Strings(upstreamKeys)

	for _, depID := range upstreamKeys {
		result := s.Upstream[depID]
		if result.Status == StepCompleted && result.Output != "" {
			msgs = append(msgs, schema.NewUserMessage(
				fmt.Sprintf("Result from step %q:\n%s", depID, result.Output),
			))
		}
	}

	if s.StepDescription != "" {
		msgs = append(msgs, schema.NewUserMessage(s.StepDescription))
	}

	return msgs
}

// HasFailedDependency returns true if any upstream step has a failed status.
func (s *StepInput) HasFailedDependency() bool {
	for _, r := range s.Upstream {
		if r.Status == StepFailed {
			return true
		}
	}

	return false
}

// BuildInputMapper creates an orchestrate.InputMapFunc from step parameters.
// Replaces the inline closure in buildNodes.
func BuildInputMapper(workDir, contextSummary, goal string, step PlanStep, depIDs []string, sessionID string) orchestrate.InputMapFunc {
	return func(upstream map[string]*schema.RunResponse) (*schema.RunRequest, error) {
		var msgs []schema.Message

		if workDir != "" {
			msgs = append(msgs, schema.NewUserMessage(
				fmt.Sprintf("Working directory: %s", workDir),
			))
		}

		if contextSummary != "" {
			msgs = append(msgs, schema.NewUserMessage(
				fmt.Sprintf("Project context:\n%s", contextSummary),
			))
		}

		msgs = append(msgs, schema.NewUserMessage(
			fmt.Sprintf("Original request: %s", goal),
		))

		for _, depID := range depIDs {
			if resp, ok := upstream[depID]; ok && resp != nil {
				msgs = append(msgs, resp.Messages...)
			}
		}

		msgs = append(msgs, schema.NewUserMessage(step.Description))

		return &schema.RunRequest{
			Messages:  msgs,
			SessionID: sessionID,
		}, nil
	}
}
