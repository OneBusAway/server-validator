package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebusaway/oba-validator/validator"
)

func sampleReport() validator.Report {
	return validator.Report{Results: []validator.Result{
		{Check: "basic-endpoints/current-time", Status: validator.Pass, Message: "OK"},
		{Check: "vehicle-positions-sampling", Source: "dataSource[0]", Status: validator.Fail, Message: "missing"},
	}}
}

func TestWriteJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSON(&buf, sampleReport()); err != nil {
		t.Fatal(err)
	}
	var back struct {
		Results []struct {
			Check  string `json:"check"`
			Status string `json:"status"`
		} `json:"results"`
	}
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatal(err)
	}
	if back.Results[1].Status != "FAIL" {
		t.Errorf("status=%q want FAIL", back.Results[1].Status)
	}
}

func TestWriteText(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteText(&buf, sampleReport()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "✓") || !strings.Contains(out, "✗") {
		t.Errorf("missing glyphs:\n%s", out)
	}
	if !strings.Contains(out, "FAIL") {
		t.Errorf("missing summary:\n%s", out)
	}
}

func TestWriteTextSummaryLine(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteText(&buf, sampleReport()); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(buf.String(), "FAIL (2 checks, 1 failed, 0 warnings)") {
		t.Errorf("summary line wrong:\n%s", buf.String())
	}
}
