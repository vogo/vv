package dispatches

import (
	"context"
	"fmt"
	"strings"

	"github.com/vogo/vage/schema"
)

// shouldSummarize determines if summarization should run based on policy and context.
func (d *Dispatcher) shouldSummarize(req *schema.RunRequest) bool {
	switch d.summaryPolicy {
	case SummaryAlways:
		return true
	case SummaryNever:
		return false
	case SummaryAuto, "":
		if req.Metadata != nil {
			// Check if parent explicitly requested summary.
			if v, ok := req.Metadata["request_summary"]; ok {
				if v == true || v == "true" {
					return true
				}
			}

			// In auto mode, summarize for HTTP, skip for CLI.
			if mode, ok := req.Metadata["mode"]; ok {
				return mode == "http"
			}
		}

		return false
	default:
		return false
	}
}

// summarize generates a summary of the execution results.
func (d *Dispatcher) summarize(ctx context.Context, req *schema.RunRequest, results []*schema.RunResponse) (*schema.RunResponse, error) {
	summarizer := d.summarizer
	if summarizer == nil {
		summarizer = d.planGen
	}

	if summarizer == nil {
		// No summarizer available, return first result.
		if len(results) > 0 {
			return results[0], nil
		}

		return &schema.RunResponse{}, nil
	}

	var sb strings.Builder

	sb.WriteString("Summarize the following execution results for the user:\n\n")
	sb.WriteString("Original request: ")

	if len(req.Messages) > 0 {
		sb.WriteString(req.Messages[len(req.Messages)-1].Content.Text())
	}

	sb.WriteString("\n\n")

	for i, resp := range results {
		if resp == nil {
			continue
		}

		fmt.Fprintf(&sb, "## Result %d\n", i+1)

		for _, m := range resp.Messages {
			sb.WriteString(m.Content.Text())
			sb.WriteString("\n")
		}

		sb.WriteString("\n")
	}

	summaryReq := &schema.RunRequest{
		Messages:  []schema.Message{schema.NewUserMessage(sb.String())},
		SessionID: req.SessionID,
	}

	return summarizer.Run(ctx, summaryReq)
}
