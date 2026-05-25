# JSON-for-UI Output Mode Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Replace the flat `--json` output with a structured, UI-oriented JSON document (meta + summary + grouped results), and ship a JSON Schema file describing it.

**Architecture:** A pure `BuildDocument(report, config, now)` transform in the `report` package converts the engine's `validator.Report` into a view model; `WriteJSON` encodes it. Errors during a `--json` run are emitted as a JSON `ErrorDocument` to stdout (the Render one-off-job convention). A committed JSON Schema (draft 2020-12) describes both variants, guarded by a conformance test.

**Tech Stack:** Go 1.25, `encoding/json`, `net/http/httptest` for tests, `github.com/santhosh-tekuri/jsonschema/v6` (test-only) for schema conformance.

---

## File Structure

- **Create** `report/document.go` — view-model types, `SchemaVersion`, `BuildDocument` + helpers (`splitCheck`, `redactString`, `Counts.add`, `buildMeta`/`buildGroup`/`buildSummary`).
- **Create** `report/document_test.go` — tests for the helpers and `BuildDocument`; shared test fixtures (`sampleReport`, `sampleConfig`, `fixedTime`).
- **Create** `report/schema_test.go` — schema-conformance tests.
- **Create** `schema/oba-validator-report.schema.json` — the published JSON Schema.
- **Modify** `report/report.go` — change `WriteJSON` signature, add `WriteErrorJSON`; `WriteText` unchanged.
- **Modify** `report/report_test.go` — rewrite `TestWriteJSON` to the new shape; drop its local `sampleReport` (moved to `document_test.go`).
- **Modify** `cmd/oba-validator/main.go` — pass `cfg` to `WriteJSON`; emit error JSON on failure under `--json`.
- **Modify** `cmd/oba-validator/main_test.go` — add `--json` shape test and config-error test.
- **Modify** `go.mod` / `go.sum` — add the test-only dependency.
- **Modify** `README.md`, `CLAUDE.md`, and the spec status line — document the new output.

Naming contract used across tasks (define once, reuse exactly):
`Document{SchemaVersion, Meta, Summary, Groups}`, `Meta{GeneratedAt, OBAServerURL, DataSources}`, `MetaSource{ID, Index, StaticGtfsFeedURL, VehiclePositionsURL, TripUpdatesURL, ServiceAlertsURL, AgencyMapping}`, `Summary{Verdict, ExitCode, Total, Counts}`, `Counts{Pass, Warn, Fail, Skip}`, `Group{ID, Label, Counts, Results}`, `Item{Check, Category, Step, Status, Message, Details}`, `ErrorDocument{SchemaVersion, Error}`. Functions: `BuildDocument(rep validator.Report, cfg config.Config, now time.Time) Document`, `WriteJSON(w io.Writer, rep validator.Report, cfg config.Config) error`, `WriteErrorJSON(w io.Writer, msg, apiKey string) error`. Const: `SchemaVersion = "1.0"`.

---

## Task 1: View-model types + pure helpers

**Files:**
- Create: `report/document.go`
- Test: `report/document_test.go`

- [ ] **Step 1: Write the failing test**

Create `report/document_test.go`:

```go
package report

import (
	"testing"
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./report/ -run 'TestSplitCheck|TestRedactString' -v`
Expected: FAIL — compile error, `undefined: splitCheck`, `undefined: redactString`.

- [ ] **Step 3: Write minimal implementation**

Create `report/document.go`:

```go
package report

import "strings"

// SchemaVersion is the version of the JSON document shape emitted by WriteJSON.
// It appears as the top-level "schemaVersion" field of every document.
const SchemaVersion = "1.0"

// Document is the UI-oriented JSON output of a successful validation run.
type Document struct {
	SchemaVersion string  `json:"schemaVersion"`
	Meta          Meta    `json:"meta"`
	Summary       Summary `json:"summary"`
	Groups        []Group `json:"groups"`
}

// Meta echoes the run inputs (never the apiKey) so a UI can show what was checked.
type Meta struct {
	GeneratedAt  string       `json:"generatedAt"` // RFC3339, UTC
	OBAServerURL string       `json:"obaServerURL"`
	DataSources  []MetaSource `json:"dataSources"`
}

// MetaSource echoes one configured data source. ID joins to Group.ID.
type MetaSource struct {
	ID                  string            `json:"id"`
	Index               int               `json:"index"`
	StaticGtfsFeedURL   string            `json:"staticGtfsFeedURL,omitempty"`
	VehiclePositionsURL string            `json:"vehiclePositionsURL,omitempty"`
	TripUpdatesURL      string            `json:"tripUpdatesURL,omitempty"`
	ServiceAlertsURL    string            `json:"serviceAlertsURL,omitempty"`
	AgencyMapping       map[string]string `json:"agencyMapping,omitempty"`
}

// Summary is the run-wide verdict and tallies.
type Summary struct {
	Verdict  string `json:"verdict"` // PASS | FAIL
	ExitCode int    `json:"exitCode"`
	Total    int    `json:"total"`
	Counts   Counts `json:"counts"`
}

// Counts tallies results by status. Keys are lowercase; values always present.
type Counts struct {
	Pass int `json:"pass"`
	Warn int `json:"warn"`
	Fail int `json:"fail"`
	Skip int `json:"skip"`
}

// add increments the tally for an uppercase status string (PASS/WARN/FAIL/SKIP).
func (c *Counts) add(status string) {
	switch status {
	case "PASS":
		c.Pass++
	case "WARN":
		c.Warn++
	case "FAIL":
		c.Fail++
	case "SKIP":
		c.Skip++
	}
}

// Group is one section of results: the server, or one data source.
type Group struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Counts  Counts `json:"counts"`
	Results []Item `json:"results"`
}

// Item is one check result, with check name pre-split into category/step.
type Item struct {
	Check    string         `json:"check"`
	Category string         `json:"category"`
	Step     string         `json:"step,omitempty"`
	Status   string         `json:"status"`
	Message  string         `json:"message"`
	Details  map[string]any `json:"details,omitempty"`
}

// ErrorDocument is emitted to stdout when a --json run fails before producing a report.
type ErrorDocument struct {
	SchemaVersion string `json:"schemaVersion"`
	Error         string `json:"error"`
}

// splitCheck splits a check name on the first '/' into category and step.
// A name without '/' yields (name, "").
func splitCheck(check string) (category, step string) {
	if i := strings.IndexByte(check, '/'); i >= 0 {
		return check[:i], check[i+1:]
	}
	return check, ""
}

// redactString replaces the apiKey substring with "*** " (matching the
// validator's redact convention) so a secret never reaches output. A no-op
// when apiKey is empty.
func redactString(s, apiKey string) string {
	if apiKey == "" {
		return s
	}
	return strings.ReplaceAll(s, apiKey, "***")
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./report/ -run 'TestSplitCheck|TestRedactString' -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add report/document.go report/document_test.go
git commit -m "feat(report): view-model types and pure helpers for JSON document"
```

---

## Task 2: BuildDocument transform

**Files:**
- Modify: `report/document.go`
- Test: `report/document_test.go`

- [ ] **Step 1: Write the failing test**

Append to `report/document_test.go` (add imports `encoding/json`, `strings`, `time`, `github.com/onebusaway/oba-validator/config`, `github.com/onebusaway/oba-validator/validator`):

```go
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
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./report/ -run TestBuildDocument -v`
Expected: FAIL — `undefined: BuildDocument`.

- [ ] **Step 3: Write minimal implementation**

Append to `report/document.go` (add imports `fmt`, `sort`, `time`, `github.com/onebusaway/oba-validator/config`, `github.com/onebusaway/oba-validator/validator`; keep `strings`):

```go
// BuildDocument transforms a validation report and its config into the
// UI-oriented Document. It is pure: pass time.Now().UTC() for now in production
// and a fixed time in tests. The apiKey is never echoed; URLs are redacted.
func BuildDocument(rep validator.Report, cfg config.Config, now time.Time) Document {
	bySource := map[string][]validator.Result{}
	for _, r := range rep.Results {
		bySource[r.Source] = append(bySource[r.Source], r)
	}

	groups := []Group{buildGroup("server", "Server", bySource[""])}
	delete(bySource, "")
	for i := range cfg.DataSources {
		id := fmt.Sprintf("dataSource[%d]", i)
		groups = append(groups, buildGroup(id, fmt.Sprintf("Data source %d", i), bySource[id]))
		delete(bySource, id)
	}
	// Any result with an unrecognized source is emitted in a trailing group
	// (sorted) so no data is ever dropped.
	leftover := make([]string, 0, len(bySource))
	for k := range bySource {
		leftover = append(leftover, k)
	}
	sort.Strings(leftover)
	for _, k := range leftover {
		groups = append(groups, buildGroup(k, k, bySource[k]))
	}

	return Document{
		SchemaVersion: SchemaVersion,
		Meta:          buildMeta(cfg, now),
		Summary:       buildSummary(rep, groups),
		Groups:        groups,
	}
}

func buildGroup(id, label string, results []validator.Result) Group {
	g := Group{ID: id, Label: label, Results: []Item{}}
	for _, r := range results {
		cat, step := splitCheck(r.Check)
		status := r.Status.String()
		g.Results = append(g.Results, Item{
			Check:    r.Check,
			Category: cat,
			Step:     step,
			Status:   status,
			Message:  r.Message,
			Details:  r.Details,
		})
		g.Counts.add(status)
	}
	return g
}

func buildMeta(cfg config.Config, now time.Time) Meta {
	m := Meta{
		GeneratedAt:  now.UTC().Format(time.RFC3339),
		OBAServerURL: redactString(cfg.OBAServerURL, cfg.APIKey),
		DataSources:  make([]MetaSource, 0, len(cfg.DataSources)),
	}
	for i, ds := range cfg.DataSources {
		m.DataSources = append(m.DataSources, MetaSource{
			ID:                  fmt.Sprintf("dataSource[%d]", i),
			Index:               i,
			StaticGtfsFeedURL:   redactString(ds.StaticGtfsFeedURL, cfg.APIKey),
			VehiclePositionsURL: redactString(ds.VehiclePositionsURL, cfg.APIKey),
			TripUpdatesURL:      redactString(ds.TripUpdatesURL, cfg.APIKey),
			ServiceAlertsURL:    redactString(ds.ServiceAlertsURL, cfg.APIKey),
			AgencyMapping:       ds.AgencyMapping,
		})
	}
	return m
}

func buildSummary(rep validator.Report, groups []Group) Summary {
	var total Counts
	for _, g := range groups {
		total.Pass += g.Counts.Pass
		total.Warn += g.Counts.Warn
		total.Fail += g.Counts.Fail
		total.Skip += g.Counts.Skip
	}
	verdict := "PASS"
	if rep.Worst() == validator.Fail {
		verdict = "FAIL"
	}
	return Summary{
		Verdict:  verdict,
		ExitCode: rep.ExitCode(),
		Total:    total.Pass + total.Warn + total.Fail + total.Skip,
		Counts:   total,
	}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./report/ -run TestBuildDocument -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add report/document.go report/document_test.go
git commit -m "feat(report): BuildDocument transform from Report to UI document"
```

---

## Task 3: WriteJSON (new shape) + WriteErrorJSON

**Files:**
- Modify: `report/report.go`
- Modify: `report/report_test.go`
- Modify: `cmd/oba-validator/main.go:84` (call-site only, to keep the build green)

- [ ] **Step 1: Write the failing test**

Replace the body of `report/report_test.go` with (note: `sampleReport` now lives in `document_test.go`, so it is removed here):

```go
package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebusaway/oba-validator/validator"
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

var _ = validator.Pass // keep validator import even if unused above
```

Note the `TestWriteTextSummaryLine` expectation changed to `(4 checks, 1 failed, 1 warnings)` to match the new `sampleReport`. Remove the `var _ = validator.Pass` line if `validator` ends up referenced elsewhere in the file; it exists only to avoid an unused-import error.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./report/ -run 'TestWriteJSON|TestWriteErrorJSON' -v`
Expected: FAIL — `WriteJSON` arg count mismatch / `undefined: WriteErrorJSON`.

- [ ] **Step 3: Write minimal implementation**

In `report/report.go`, replace `WriteJSON` and add `WriteErrorJSON`. New file body:

```go
// Package report renders a validator.Report as JSON or human-readable text.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/onebusaway/oba-validator/config"
	"github.com/onebusaway/oba-validator/validator"
)

// WriteJSON writes the report as an indented, UI-oriented JSON Document.
func WriteJSON(w io.Writer, rep validator.Report, cfg config.Config) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(BuildDocument(rep, cfg, time.Now().UTC()))
}

// WriteErrorJSON writes an indented ErrorDocument to w, redacting apiKey from msg.
func WriteErrorJSON(w io.Writer, msg, apiKey string) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(ErrorDocument{SchemaVersion: SchemaVersion, Error: redactString(msg, apiKey)})
}

// WriteText writes a human-readable, grouped report with a summary line.
func WriteText(w io.Writer, rep validator.Report) error {
	var fails, warns int
	for _, r := range rep.Results {
		group := r.Source
		if group == "" {
			group = "server"
		}
		if _, err := fmt.Fprintf(w, "%s [%s] %s — %s\n", r.Status.Glyph(), group, r.Check, r.Message); err != nil {
			return err
		}
		switch r.Status {
		case validator.Fail:
			fails++
		case validator.Warn:
			warns++
		}
	}
	verdict := "PASS"
	if rep.Worst() == validator.Fail {
		verdict = "FAIL"
	}
	_, err := fmt.Fprintf(w, "\n%s (%d checks, %d failed, %d warnings)\n", verdict, len(rep.Results), fails, warns)
	return err
}
```

Then update the call site in `cmd/oba-validator/main.go` (currently line 84) from:

```go
		werr = report.WriteJSON(stdout, rep)
```

to:

```go
		werr = report.WriteJSON(stdout, rep, cfg)
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go build ./... && go test ./report/ -v`
Expected: build OK; all `report` tests PASS.

- [ ] **Step 5: Commit**

```bash
git add report/report.go report/report_test.go cmd/oba-validator/main.go
git commit -m "feat(report): WriteJSON emits UI document; add WriteErrorJSON"
```

---

## Task 4: Wire CLI error-JSON path + CLI tests

**Files:**
- Modify: `cmd/oba-validator/main.go`
- Test: `cmd/oba-validator/main_test.go`

- [ ] **Step 1: Write the failing test**

Append to `cmd/oba-validator/main_test.go` (add imports `encoding/json`, `net/http`, `net/http/httptest`, `strings`):

```go
func TestRunJSONConfigErrorEmitsErrorJSON(t *testing.T) {
	var stdout, stderr bytes.Buffer
	code := run([]string{"oba-validator", "--json", `{"dataSources":[]}`}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit=%d want 2", code)
	}
	var ed struct {
		SchemaVersion string `json:"schemaVersion"`
		Error         string `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &ed); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, stdout.String())
	}
	if ed.Error == "" || ed.SchemaVersion == "" {
		t.Errorf("missing fields in error doc: %s", stdout.String())
	}
}

func TestRunJSONOutputShape(t *testing.T) {
	obaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "current-time"):
			w.Write([]byte(`{"data":{"entry":{"time":1716000000000}}}`))
		case strings.Contains(r.URL.Path, "agencies-with-coverage"):
			w.Write([]byte(`{"data":{"list":[],"references":{"agencies":[]}}}`))
		default:
			w.Write([]byte(`{"data":{"list":[],"entry":{"arrivalsAndDepartures":[]}}}`))
		}
	}))
	defer obaSrv.Close()
	feedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte{}) // empty payload -> prep error recorded, run still completes
	}))
	defer feedSrv.Close()

	cfg := `{"obaServerURL":"` + obaSrv.URL + `","apiKey":"test","dataSources":[{"staticGtfsFeedURL":"` + feedSrv.URL + `/gtfs.zip"}]}`
	var stdout, stderr bytes.Buffer
	run([]string{"oba-validator", "--json", "--no-cache", cfg}, &stdout, &stderr)

	var doc map[string]json.RawMessage
	if err := json.Unmarshal(stdout.Bytes(), &doc); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, stdout.String())
	}
	for _, k := range []string{"schemaVersion", "meta", "summary", "groups"} {
		if _, ok := doc[k]; !ok {
			t.Errorf("missing key %q in output:\n%s", k, stdout.String())
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/... -run TestRunJSON -v`
Expected: FAIL — `TestRunJSONConfigErrorEmitsErrorJSON` gets text on stdout (or empty), not JSON.

- [ ] **Step 3: Write minimal implementation**

In `cmd/oba-validator/main.go`, replace the config-error and run-error blocks (currently lines ~69–80) so that, under `--json`, errors are emitted as JSON to stdout. The relevant section of `run` becomes:

```go
	cfg, err := config.Load(fs.Arg(0))
	if err != nil {
		if o.jsonOut {
			if werr := report.WriteErrorJSON(stdout, err.Error(), os.Getenv("ONEBUSAWAY_API_KEY")); werr != nil {
				fmt.Fprintln(stderr, "output error:", werr)
			}
		} else {
			fmt.Fprintln(stderr, "config error:", err)
		}
		return 2
	}
	applyOverrides(&cfg, o)

	rep, err := validator.Run(context.Background(), cfg)
	if err != nil {
		if o.jsonOut {
			if werr := report.WriteErrorJSON(stdout, err.Error(), cfg.APIKey); werr != nil {
				fmt.Fprintln(stderr, "output error:", werr)
			}
		} else {
			fmt.Fprintln(stderr, "run error:", err)
		}
		return 2
	}
```

(`os` is already imported in `main.go`.)

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/... -v`
Expected: all PASS.

- [ ] **Step 5: Commit**

```bash
git add cmd/oba-validator/main.go cmd/oba-validator/main_test.go
git commit -m "feat(cli): emit JSON error document on failure under --json"
```

---

## Task 5: JSON Schema file + conformance test

**Files:**
- Create: `schema/oba-validator-report.schema.json`
- Create: `report/schema_test.go`
- Modify: `go.mod`, `go.sum`

- [ ] **Step 1: Add the test-only dependency**

Run:

```bash
go get github.com/santhosh-tekuri/jsonschema/v6
```

Expected: `go.mod`/`go.sum` updated with the `jsonschema/v6` module.

- [ ] **Step 2: Write the failing test**

Create `report/schema_test.go`:

```go
package report

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

func compileSchema(t *testing.T) *jsonschema.Schema {
	t.Helper()
	path := filepath.Join("..", "schema", "oba-validator-report.schema.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read schema: %v", err)
	}
	var doc any
	if err := json.Unmarshal(data, &doc); err != nil {
		t.Fatalf("schema is not valid JSON: %v", err)
	}
	c := jsonschema.NewCompiler()
	if err := c.AddResource("report.json", doc); err != nil {
		t.Fatalf("add resource: %v", err)
	}
	sch, err := c.Compile("report.json")
	if err != nil {
		t.Fatalf("compile schema: %v", err)
	}
	return sch
}

func validateAgainst(t *testing.T, sch *jsonschema.Schema, v any) error {
	t.Helper()
	b, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	var inst any
	if err := json.Unmarshal(b, &inst); err != nil {
		t.Fatal(err)
	}
	return sch.Validate(inst)
}

func TestSchema_SuccessDocumentConforms(t *testing.T) {
	sch := compileSchema(t)
	if err := validateAgainst(t, sch, BuildDocument(sampleReport(), sampleConfig(), fixedTime())); err != nil {
		t.Errorf("success document failed schema:\n%v", err)
	}
}

func TestSchema_ErrorDocumentConforms(t *testing.T) {
	sch := compileSchema(t)
	ed := ErrorDocument{SchemaVersion: SchemaVersion, Error: "boom"}
	if err := validateAgainst(t, sch, ed); err != nil {
		t.Errorf("error document failed schema:\n%v", err)
	}
}

func TestSchema_RejectsMalformed(t *testing.T) {
	sch := compileSchema(t)
	// Has schemaVersion but is neither a valid report (missing meta/summary/groups)
	// nor a valid error (no "error"); oneOf must match zero variants.
	bad := map[string]any{"schemaVersion": "1.0", "summary": map[string]any{}}
	if err := validateAgainst(t, sch, bad); err == nil {
		t.Error("expected malformed document to fail schema validation")
	}
}
```

- [ ] **Step 3: Run test to verify it fails**

Run: `go test ./report/ -run TestSchema -v`
Expected: FAIL — schema file does not exist (`read schema: ... no such file`).

- [ ] **Step 4: Write the schema**

Create `schema/oba-validator-report.schema.json`:

```json
{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "$id": "https://github.com/onebusaway/oba-validator/schema/oba-validator-report.schema.json",
  "title": "OBA Validator Output",
  "description": "Output of `oba-validator --json`: either a validation report document or an error document. Both carry a top-level schemaVersion.",
  "oneOf": [
    { "$ref": "#/$defs/reportDocument" },
    { "$ref": "#/$defs/errorDocument" }
  ],
  "$defs": {
    "status": {
      "type": "string",
      "enum": ["PASS", "WARN", "FAIL", "SKIP"]
    },
    "counts": {
      "type": "object",
      "description": "Result tallies by status.",
      "additionalProperties": false,
      "required": ["pass", "warn", "fail", "skip"],
      "properties": {
        "pass": { "type": "integer", "minimum": 0 },
        "warn": { "type": "integer", "minimum": 0 },
        "fail": { "type": "integer", "minimum": 0 },
        "skip": { "type": "integer", "minimum": 0 }
      }
    },
    "item": {
      "type": "object",
      "description": "One check result. category/step are the check name split on the first '/'.",
      "additionalProperties": false,
      "required": ["check", "category", "status", "message"],
      "properties": {
        "check": { "type": "string" },
        "category": { "type": "string" },
        "step": { "type": "string" },
        "status": { "$ref": "#/$defs/status" },
        "message": { "type": "string" },
        "details": { "type": "object", "additionalProperties": true }
      }
    },
    "group": {
      "type": "object",
      "description": "A section of results: the server, or one data source.",
      "additionalProperties": false,
      "required": ["id", "label", "counts", "results"],
      "properties": {
        "id": { "type": "string", "description": "'server' or 'dataSource[N]'; joins to meta.dataSources[].id." },
        "label": { "type": "string" },
        "counts": { "$ref": "#/$defs/counts" },
        "results": { "type": "array", "items": { "$ref": "#/$defs/item" } }
      }
    },
    "metaSource": {
      "type": "object",
      "description": "Echo of one configured data source. The apiKey is never present.",
      "additionalProperties": false,
      "required": ["id", "index"],
      "properties": {
        "id": { "type": "string" },
        "index": { "type": "integer", "minimum": 0 },
        "staticGtfsFeedURL": { "type": "string" },
        "vehiclePositionsURL": { "type": "string" },
        "tripUpdatesURL": { "type": "string" },
        "serviceAlertsURL": { "type": "string" },
        "agencyMapping": { "type": "object", "additionalProperties": { "type": "string" } }
      }
    },
    "meta": {
      "type": "object",
      "additionalProperties": false,
      "required": ["generatedAt", "obaServerURL", "dataSources"],
      "properties": {
        "generatedAt": { "type": "string", "format": "date-time" },
        "obaServerURL": { "type": "string" },
        "dataSources": { "type": "array", "items": { "$ref": "#/$defs/metaSource" } }
      }
    },
    "summary": {
      "type": "object",
      "additionalProperties": false,
      "required": ["verdict", "exitCode", "total", "counts"],
      "properties": {
        "verdict": { "type": "string", "enum": ["PASS", "FAIL"] },
        "exitCode": { "type": "integer", "enum": [0, 1] },
        "total": { "type": "integer", "minimum": 0 },
        "counts": { "$ref": "#/$defs/counts" }
      }
    },
    "reportDocument": {
      "type": "object",
      "additionalProperties": false,
      "required": ["schemaVersion", "meta", "summary", "groups"],
      "properties": {
        "schemaVersion": { "type": "string" },
        "meta": { "$ref": "#/$defs/meta" },
        "summary": { "$ref": "#/$defs/summary" },
        "groups": { "type": "array", "items": { "$ref": "#/$defs/group" } }
      }
    },
    "errorDocument": {
      "type": "object",
      "additionalProperties": false,
      "required": ["schemaVersion", "error"],
      "properties": {
        "schemaVersion": { "type": "string" },
        "error": { "type": "string" }
      }
    }
  }
}
```

- [ ] **Step 5: Run test to verify it passes; tidy modules**

Run: `go mod tidy && go test ./report/ -run TestSchema -v`
Expected: all `TestSchema*` PASS.

- [ ] **Step 6: Commit**

```bash
git add schema/oba-validator-report.schema.json report/schema_test.go go.mod go.sum
git commit -m "feat: publish JSON Schema and conformance test for --json output"
```

---

## Task 6: Documentation

**Files:**
- Modify: `README.md`
- Modify: `CLAUDE.md`
- Modify: `docs/superpowers/specs/2026-05-25-json-ui-output-design.md` (status line)

- [ ] **Step 1: Update README**

In `README.md`, after the "Library" section (line ~46), add a section:

```markdown
## JSON output

`--json` emits a single structured document to stdout, designed for building a UI
visualization. It contains `meta` (run inputs — never the apiKey), `summary`
(verdict + status counts), and `groups` (a `server` group plus one per data
source, each with its results). On failure before a report is produced, a
`{ "schemaVersion, error }` object is emitted to stdout and the process exits 2.

The full contract is published as a JSON Schema (draft 2020-12) at
[`schema/oba-validator-report.schema.json`](schema/oba-validator-report.schema.json).
This is the recommended format for the Render one-off-job workflow: the job
prints the document to stdout and the caller reads it from the job output.
```

Also update the Library snippet line `report.WriteJSON(os.Stdout, rep)` to `report.WriteJSON(os.Stdout, rep, cfg)`.

- [ ] **Step 2: Update CLAUDE.md**

In `CLAUDE.md`, in the `report` bullet of the Architecture section, change the description of JSON output from "indented JSON (`WriteJSON`)" to note the new shape:

```markdown
4. **`report`** — renders a `Report` as grouped text (`WriteText`) or, via
   `WriteJSON`, a UI-oriented JSON `Document` (meta + summary + grouped results;
   schema at `schema/oba-validator-report.schema.json`). `WriteErrorJSON` emits
   the error variant. The `Document` view model is built by the pure
   `BuildDocument(report, config, now)` so output is deterministic in tests.
```

- [ ] **Step 3: Update spec status**

In `docs/superpowers/specs/2026-05-25-json-ui-output-design.md`, change the status line to:

```markdown
**Status:** Implemented
```

- [ ] **Step 4: Verify full suite + build**

Run: `go build ./... && go vet ./... && go test ./...`
Expected: build OK, vet clean, all tests PASS.

- [ ] **Step 5: Commit**

```bash
git add README.md CLAUDE.md docs/superpowers/specs/2026-05-25-json-ui-output-design.md
git commit -m "docs: document --json UI output and schema"
```

---

## Self-Review

**Spec coverage:**
- Replace flat `--json` → Task 3 (WriteJSON new shape) + Task 3 call-site update. ✓
- Grouped-by-section shape → Task 2 (BuildDocument grouping). ✓
- Rich meta + input echo, no apiKey → Task 2 (buildMeta) + redaction test. ✓
- Error-as-JSON on stdout → Task 4. ✓
- JSON Schema file → Task 5. ✓
- Schema-conformance test (test-only dep) → Task 5. ✓
- category/step split, deterministic ordering, counts/verdict/exit → Task 2. ✓
- Docs → Task 6. ✓

**Placeholder scan:** No TBD/TODO; every code/test step contains complete code and exact run commands. ✓

**Type consistency:** `BuildDocument`/`WriteJSON`/`WriteErrorJSON` signatures, the `Document`/`Meta`/`MetaSource`/`Summary`/`Counts`/`Group`/`Item`/`ErrorDocument` field names, and `SchemaVersion` are identical across the file-structure contract, Tasks 1–5, the schema, and the tests. `Counts.add` consumes the uppercase status from `validator.Status.String()`. ✓

**Note on existing tests:** `report_test.go`'s `sampleReport` is moved to `document_test.go` and enriched (4 results, 2 sources), so `TestWriteTextSummaryLine`'s expected line is updated to `(4 checks, 1 failed, 1 warnings)` in Task 3.
