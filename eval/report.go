package eval

import (
	"encoding/json"
	"fmt"
	"io"
	"os"

	vageeval "github.com/vogo/vage/eval"
)

// PrintSummary writes a compact human-readable summary to w. Full per-case
// details go through WriteReportJSON; this is the terminal-friendly view.
func PrintSummary(w io.Writer, report *vageeval.EvalReport) {
	if report == nil {
		_, _ = fmt.Fprintln(w, "Eval Report: <nil>")

		return
	}

	_, _ = fmt.Fprintln(w, "Eval Report")
	_, _ = fmt.Fprintf(w, "  Total      : %d\n", report.TotalCases)
	_, _ = fmt.Fprintf(w, "  Passed     : %d\n", report.PassedCases)
	_, _ = fmt.Fprintf(w, "  Failed     : %d\n", report.FailedCases)
	_, _ = fmt.Fprintf(w, "  Errors     : %d\n", report.ErrorCases)
	_, _ = fmt.Fprintf(w, "  Avg Score  : %.3f\n", report.AvgScore)
	_, _ = fmt.Fprintf(w, "  Duration   : %d ms\n", report.TotalDuration)

	if len(report.Results) == 0 {
		return
	}

	_, _ = fmt.Fprintln(w, "\nFailures:")

	hadFailure := false

	for _, r := range report.Results {
		if r == nil || (r.Passed && r.Error == "") {
			continue
		}

		hadFailure = true

		status := "FAIL"
		if r.Error != "" {
			status = "ERROR"
		}

		_, _ = fmt.Fprintf(w, "  [%s] %s  score=%.3f", status, r.CaseID, r.Score)

		if r.Error != "" {
			_, _ = fmt.Fprintf(w, "  err=%s", r.Error)
		}

		_, _ = fmt.Fprintln(w)
	}

	if !hadFailure {
		_, _ = fmt.Fprintln(w, "  (none)")
	}
}

// WriteReportJSON serializes the report as indented JSON and writes it to
// path, creating the file if needed. 0o644 matches other vv-written files.
func WriteReportJSON(path string, report *vageeval.EvalReport) error {
	data, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal report: %w", err)
	}

	if err := os.WriteFile(path, data, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", path, err)
	}

	return nil
}
