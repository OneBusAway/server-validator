package report

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/onebusaway/oba-validator/config"
	"github.com/onebusaway/oba-validator/validator"
)

func TestSplitCheck(t *testing.T) {
	cases := []struct {
		in       string
		category string
		step     string
	}{
		{"basic-endpoints/current-time", "basic-endpoints", "current-time"},
		{"vehicle-positions-sampling/trip-for-vehicle", "vehicle-positions-sampling", "trip-for-vehicle"},
		{"agency-union", "agency-union", ""},
		{"a/b/c", "a", "b/c"},
	}
	for _, c := range cases {
		cat, step := splitCheck(c.in)
		if cat != c.category || step != c.step {
			t.Errorf("splitCheck(%q) = (%q,%q) want (%q,%q)", c.in, cat, step, c.category, c.step)
		}
	}
}

func TestRedactString(t *testing.T) {
	if got := redactString("https://x/?key=SEKRET", "SEKRET"); got != "https://x/?key=***" {
		t.Errorf("redactString did not redact: %q", got)
	}
	if got := redactString("no-key-here", "SEKRET"); got != "no-key-here" {
		t.Errorf("redactString altered non-matching string: %q", got)
	}
	if got := redactString("anything", ""); got != "anything" {
		t.Errorf("empty apiKey should be a no-op: %q", got)
	}
}

func fixedTime() time.Time { return time.Date(2026, 5, 25, 17, 4, 0, 0, time.UTC) }

func sampleReport() validator.Report {
	return validator.Report{Results: []validator.Result{
		{Check: "basic-endpoints/current-time", Status: validator.Pass, Message: "OK"},
		{Check: "agency-union", Status: validator.Pass, Message: "all agencies present"},
		{Check: "vehicle-positions-sampling/trip-for-vehicle", Source: "dataSource[0]", Status: validator.Fail, Message: "missing", Details: map[string]any{"vehicleId": "1_1234"}},
		{Check: "freshness", Source: "dataSource[0]", Status: validator.Warn, Message: "empty feed"},
	}}
}

func sampleConfig() config.Config {
	return config.Config{
		OBAServerURL: "https://oba.example.org",
		APIKey:       "secret-key",
		DataSources: []config.DataSource{{
			StaticGtfsFeedURL:   "https://feeds.example.org/gtfs.zip",
			VehiclePositionsURL: "https://feeds.example.org/vp.pb",
			AgencyMapping:       map[string]string{"KCM": "1"},
		}},
	}
}

func TestBuildDocument_GroupingAndOrder(t *testing.T) {
	doc := BuildDocument(sampleReport(), sampleConfig(), fixedTime())
	if len(doc.Groups) != 2 {
		t.Fatalf("groups=%d want 2", len(doc.Groups))
	}
	if doc.Groups[0].ID != "server" || doc.Groups[0].Label != "Server" {
		t.Errorf("group[0]=%+v want server/Server", doc.Groups[0])
	}
	if doc.Groups[1].ID != "dataSource[0]" || doc.Groups[1].Label != "Data source 0" {
		t.Errorf("group[1]=%+v want dataSource[0]/Data source 0", doc.Groups[1])
	}
	if len(doc.Groups[0].Results) != 2 || len(doc.Groups[1].Results) != 2 {
		t.Errorf("group sizes = %d,%d want 2,2", len(doc.Groups[0].Results), len(doc.Groups[1].Results))
	}
}

func TestBuildDocument_CategoryStep(t *testing.T) {
	doc := BuildDocument(sampleReport(), sampleConfig(), fixedTime())
	got := doc.Groups[0].Results
	if got[0].Category != "basic-endpoints" || got[0].Step != "current-time" {
		t.Errorf("result[0] cat/step = %q/%q", got[0].Category, got[0].Step)
	}
	if got[1].Category != "agency-union" || got[1].Step != "" {
		t.Errorf("result[1] cat/step = %q/%q want agency-union/empty", got[1].Category, got[1].Step)
	}
}

func TestBuildDocument_CountsVerdictExit(t *testing.T) {
	doc := BuildDocument(sampleReport(), sampleConfig(), fixedTime())
	if doc.Groups[0].Counts != (Counts{Pass: 2}) {
		t.Errorf("server counts=%+v", doc.Groups[0].Counts)
	}
	if doc.Groups[1].Counts != (Counts{Fail: 1, Warn: 1}) {
		t.Errorf("ds counts=%+v", doc.Groups[1].Counts)
	}
	if doc.Summary.Counts != (Counts{Pass: 2, Warn: 1, Fail: 1}) {
		t.Errorf("summary counts=%+v", doc.Summary.Counts)
	}
	if doc.Summary.Total != 4 {
		t.Errorf("total=%d want 4", doc.Summary.Total)
	}
	if doc.Summary.Verdict != "FAIL" || doc.Summary.ExitCode != 1 {
		t.Errorf("verdict/exit = %q/%d want FAIL/1", doc.Summary.Verdict, doc.Summary.ExitCode)
	}
	if doc.SchemaVersion != SchemaVersion {
		t.Errorf("schemaVersion=%q", doc.SchemaVersion)
	}
}

func TestBuildDocument_MetaEcho(t *testing.T) {
	doc := BuildDocument(sampleReport(), sampleConfig(), fixedTime())
	if doc.Meta.GeneratedAt != "2026-05-25T17:04:00Z" {
		t.Errorf("generatedAt=%q", doc.Meta.GeneratedAt)
	}
	if doc.Meta.OBAServerURL != "https://oba.example.org" {
		t.Errorf("obaServerURL=%q", doc.Meta.OBAServerURL)
	}
	if len(doc.Meta.DataSources) != 1 {
		t.Fatalf("meta dataSources=%d want 1", len(doc.Meta.DataSources))
	}
	ms := doc.Meta.DataSources[0]
	if ms.ID != "dataSource[0]" || ms.Index != 0 || ms.AgencyMapping["KCM"] != "1" {
		t.Errorf("metaSource=%+v", ms)
	}
	// tripUpdatesURL/serviceAlertsURL were not configured -> omitted from JSON.
	b, _ := json.Marshal(doc)
	if strings.Contains(string(b), "tripUpdatesURL") || strings.Contains(string(b), "serviceAlertsURL") {
		t.Errorf("unconfigured URLs should be omitted:\n%s", b)
	}
}

func TestBuildDocument_RedactsAPIKey(t *testing.T) {
	cfg := sampleConfig()
	cfg.APIKey = "SEKRET"
	cfg.OBAServerURL = "https://oba.example.org/?key=SEKRET"
	b, _ := json.Marshal(BuildDocument(sampleReport(), cfg, fixedTime()))
	if strings.Contains(string(b), "SEKRET") {
		t.Errorf("apiKey leaked into output:\n%s", b)
	}
	if !strings.Contains(string(b), "***") {
		t.Errorf("expected redaction marker in output:\n%s", b)
	}
}
