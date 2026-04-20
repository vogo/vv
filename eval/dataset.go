// Package eval provides the CLI and HTTP adapters that bridge vv to
// vage's evaluation framework. The real evaluation logic lives in
// github.com/vogo/vage/eval; this package only loads datasets, wires
// configuration, and renders reports.
package eval

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/vogo/aimodel"
	"github.com/vogo/vage/eval"
	"github.com/vogo/vage/schema"
)

// LoadError records a JSONL line that could not be decoded. The overall
// batch continues past parse failures so partial datasets still produce a
// useful report.
type LoadError struct {
	Line int
	Err  error
}

func (e LoadError) Error() string {
	return fmt.Sprintf("line %d: %s", e.Line, e.Err)
}

// caseLine is the JSONL wire schema. Input is deliberately `json.RawMessage`
// so callers can write either `"input": "hello"` or
// `"input": {"messages": [...]}`. Expected follows the same convention.
type caseLine struct {
	ID       string          `json:"id"`
	Input    json.RawMessage `json:"input"`
	Expected json.RawMessage `json:"expected,omitempty"`
	Criteria []string        `json:"criteria,omitempty"`
	Tags     []string        `json:"tags,omitempty"`
}

// LoadJSONL reads a JSONL file and returns successfully decoded cases plus
// a list of per-line decode errors. An error is returned only for I/O or
// file-open failures.
func LoadJSONL(path string) ([]*eval.EvalCase, []LoadError, error) {
	f, err := os.Open(path)
	if err != nil {
		return nil, nil, fmt.Errorf("open dataset %s: %w", path, err)
	}
	defer func() { _ = f.Close() }()

	return DecodeJSONL(f)
}

// DecodeJSONL reads JSONL records from r. Split out from LoadJSONL so
// tests and HTTP handlers can reuse the same decoder without a temp file.
func DecodeJSONL(r io.Reader) ([]*eval.EvalCase, []LoadError, error) {
	var (
		cases    []*eval.EvalCase
		loadErrs []LoadError
	)

	scanner := bufio.NewScanner(r)
	// Raise the line cap so long inputs (e.g. full chat transcripts) decode.
	buf := make([]byte, 0, 64*1024)
	scanner.Buffer(buf, 1024*1024)

	lineNo := 0
	for scanner.Scan() {
		lineNo++

		raw := bytes.TrimSpace(scanner.Bytes())
		if len(raw) == 0 {
			continue
		}

		c, err := DecodeCaseLine(raw)
		if err != nil {
			loadErrs = append(loadErrs, LoadError{Line: lineNo, Err: err})

			continue
		}

		cases = append(cases, c)
	}

	if err := scanner.Err(); err != nil {
		return cases, loadErrs, fmt.Errorf("scan dataset: %w", err)
	}

	return cases, loadErrs, nil
}

// DecodeCaseLine parses a single JSONL record into an EvalCase.
func DecodeCaseLine(raw []byte) (*eval.EvalCase, error) {
	var line caseLine
	if err := json.Unmarshal(raw, &line); err != nil {
		return nil, fmt.Errorf("decode json: %w", err)
	}

	if line.ID == "" {
		return nil, errors.New("missing id")
	}

	input, err := decodeRunRequest(line.Input)
	if err != nil {
		return nil, fmt.Errorf("input: %w", err)
	}

	if input == nil {
		return nil, errors.New("missing input")
	}

	var expected *schema.RunResponse

	if len(line.Expected) > 0 {
		expected, err = decodeRunResponse(line.Expected)
		if err != nil {
			return nil, fmt.Errorf("expected: %w", err)
		}
	}

	return &eval.EvalCase{
		ID:       line.ID,
		Input:    input,
		Expected: expected,
		Criteria: line.Criteria,
		Tags:     line.Tags,
	}, nil
}

// decodeRunRequest accepts either a plain string (shorthand for a single
// user message) or a full RunRequest object.
func decodeRunRequest(raw json.RawMessage) (*schema.RunRequest, error) {
	if len(raw) == 0 {
		return nil, nil
	}

	trimmed := bytes.TrimSpace(raw)

	if len(trimmed) > 0 && trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return nil, err
		}

		return &schema.RunRequest{Messages: []schema.Message{schema.NewUserMessage(text)}}, nil
	}

	var req schema.RunRequest
	if err := json.Unmarshal(trimmed, &req); err != nil {
		return nil, err
	}

	if len(req.Messages) == 0 {
		return nil, errors.New("request has no messages")
	}

	return &req, nil
}

// decodeRunResponse accepts either a plain string (shorthand for a single
// assistant reply) or a full RunResponse object. Used for optional
// Expected fields.
func decodeRunResponse(raw json.RawMessage) (*schema.RunResponse, error) {
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) == 0 {
		return nil, nil
	}

	if trimmed[0] == '"' {
		var text string
		if err := json.Unmarshal(trimmed, &text); err != nil {
			return nil, err
		}

		return &schema.RunResponse{
			Messages: []schema.Message{{
				Message: aimodel.Message{Role: aimodel.RoleAssistant, Content: aimodel.NewTextContent(text)},
			}},
		}, nil
	}

	var resp schema.RunResponse
	if err := json.Unmarshal(trimmed, &resp); err != nil {
		return nil, err
	}

	return &resp, nil
}
