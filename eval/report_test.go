package eval

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	vageeval "github.com/vogo/vage/eval"
)

func TestPrintSummary_Basic(t *testing.T) {
	report := &vageeval.EvalReport{
		TotalCases:    3,
		PassedCases:   2,
		FailedCases:   1,
		ErrorCases:    0,
		AvgScore:      0.75,
		TotalDuration: 1234,
		Results: []*vageeval.EvalResult{
			{CaseID: "c1", Score: 1, Passed: true},
			{CaseID: "c2", Score: 0.5, Passed: false},
			{CaseID: "c3", Score: 1, Passed: true},
		},
	}

	var buf bytes.Buffer

	PrintSummary(&buf, report)

	out := buf.String()

	for _, want := range []string{
		"Total      : 3",
		"Passed     : 2",
		"Failed     : 1",
		"Avg Score  : 0.750",
		"Duration   : 1234 ms",
		"[FAIL] c2",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("output missing %q, got:\n%s", want, out)
		}
	}
}

func TestPrintSummary_NilReport(t *testing.T) {
	var buf bytes.Buffer

	PrintSummary(&buf, nil)

	if !strings.Contains(buf.String(), "nil") {
		t.Errorf("nil report should print <nil>, got %q", buf.String())
	}
}

func TestWriteReportJSON(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "report.json")

	report := &vageeval.EvalReport{
		TotalCases:  2,
		PassedCases: 1,
	}

	if err := WriteReportJSON(path, report); err != nil {
		t.Fatalf("write: %v", err)
	}

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	var decoded vageeval.EvalReport
	if err := json.Unmarshal(data, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}

	if decoded.TotalCases != 2 || decoded.PassedCases != 1 {
		t.Errorf("roundtrip mismatch: %+v", decoded)
	}
}
