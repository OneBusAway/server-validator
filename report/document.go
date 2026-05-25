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
// validator's redact convention) so a secret never reaches output. A no-op when
// apiKey is empty.
func redactString(s, apiKey string) string {
	if apiKey == "" {
		return s
	}
	return strings.ReplaceAll(s, apiKey, "***")
}
