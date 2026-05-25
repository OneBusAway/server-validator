package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestWriteJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSON(&buf, sampleReport(), sampleConfig()); err != nil {
		t.Fatal(err)
	}
	var doc Document
	if err := json.Unmarshal(buf.Bytes(), &doc); err != nil {
		t.Fatalf("output not a Document: %v\n%s", err, buf.String())
	}
	if doc.SchemaVersion != SchemaVersion {
		t.Errorf("schemaVersion=%q", doc.SchemaVersion)
	}
	if len(doc.Groups) != 2 || doc.Summary.Verdict != "FAIL" {
		t.Errorf("unexpected document: %+v", doc.Summary)
	}
	if !strings.Contains(buf.String(), "\n  ") {
		t.Error("expected indented JSON")
	}
}

func TestWriteErrorJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteErrorJSON(&buf, "boom with SEKRET", "SEKRET"); err != nil {
		t.Fatal(err)
	}
	var ed ErrorDocument
	if err := json.Unmarshal(buf.Bytes(), &ed); err != nil {
		t.Fatalf("output not an ErrorDocument: %v\n%s", err, buf.String())
	}
	if ed.SchemaVersion != SchemaVersion {
		t.Errorf("schemaVersion=%q", ed.SchemaVersion)
	}
	if strings.Contains(ed.Error, "SEKRET") || !strings.Contains(ed.Error, "***") {
		t.Errorf("error not redacted: %q", ed.Error)
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
	if !strings.Contains(buf.String(), "FAIL (4 checks, 1 failed, 1 warnings)") {
		t.Errorf("summary line wrong:\n%s", buf.String())
	}
}
