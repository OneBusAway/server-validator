package report

import (
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/onebusaway/oba-validator/config"
	"github.com/onebusaway/oba-validator/validator"
)

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

// Counts tallies results by status. All four fields are always serialized (no
// omitempty), so a UI can rely on every count being present, including zeros.
type Counts struct {
	Pass int `json:"pass"`
	Warn int `json:"warn"`
	Fail int `json:"fail"`
	Skip int `json:"skip"`
}

// add increments the tally for a status. It takes the typed validator.Status so
// the compiler keeps this switch in sync with the enum; an out-of-range value is
// a programming error and panics rather than being silently dropped.
func (c *Counts) add(status validator.Status) {
	switch status {
	case validator.Pass:
		c.Pass++
	case validator.Warn:
		c.Warn++
	case validator.Fail:
		c.Fail++
	case validator.Skip:
		c.Skip++
	default:
		panic(fmt.Sprintf("report: unexpected status %v", status))
	}
}

// Group is one section of results: the server, or one data source.
type Group struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Counts  Counts `json:"counts"`
	Results []Item `json:"results"`
}

// Item is one check result, with the check name pre-split into category/step.
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

// redactString replaces the apiKey substring with "***" (matching the
// validator's redact convention in validator/util.go — keep the token in sync)
// so a secret never reaches output. A no-op when apiKey is empty.
func redactString(s, apiKey string) string {
	if apiKey == "" {
		return s
	}
	return strings.ReplaceAll(s, apiKey, "***")
}

// BuildDocument transforms a validation report and its config into the
// UI-oriented Document. It is pure: pass time.Now().UTC() for now in production
// and a fixed time in tests. The apiKey is never copied into the Document, and
// any occurrence of cfg.APIKey in echoed URLs and result messages is replaced
// with "***" (a no-op when the key is empty). Result Details pass through
// unchanged — checks are responsible for redacting them at the source.
func BuildDocument(rep validator.Report, cfg config.Config, now time.Time) Document {
	bySource := map[string][]validator.Result{}
	for _, r := range rep.Results {
		bySource[r.Source] = append(bySource[r.Source], r)
	}

	groups := []Group{buildGroup("server", "Server", bySource[""], cfg.APIKey)}
	delete(bySource, "")
	for i := range cfg.DataSources {
		id := fmt.Sprintf("dataSource[%d]", i)
		groups = append(groups, buildGroup(id, fmt.Sprintf("Data source %d", i), bySource[id], cfg.APIKey))
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
		groups = append(groups, buildGroup(k, k, bySource[k], cfg.APIKey))
	}

	return Document{
		SchemaVersion: SchemaVersion,
		Meta:          buildMeta(cfg, now),
		Summary:       buildSummary(rep, groups),
		Groups:        groups,
	}
}

func buildGroup(id, label string, results []validator.Result, apiKey string) Group {
	g := Group{ID: id, Label: label, Results: []Item{}}
	for _, r := range results {
		cat, step := splitCheck(r.Check)
		g.Results = append(g.Results, Item{
			Check:    r.Check,
			Category: cat,
			Step:     step,
			Status:   r.Status.String(),
			Message:  redactString(r.Message, apiKey), // defense in depth; checks redact upstream
			Details:  r.Details,
		})
		g.Counts.add(r.Status)
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
		Total:    len(rep.Results),
		Counts:   total,
	}
}
