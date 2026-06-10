package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const sampleJSON = `{
  "obaServerURL": "https://example.com",
  "apiKey": "k",
  "dataSources": [{"staticGtfsFeedURL":"https://s/gtfs.zip","agencyMapping":{"KCM":"1"}}]
}`

func TestLoadFromRawJSONAppliesDefaults(t *testing.T) {
	cfg, err := Load(sampleJSON)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SampleSize != 3 || cfg.RTFreshnessSeconds != 300 || cfg.TimeoutSeconds != 120 {
		t.Errorf("defaults not applied: %+v", cfg)
	}
	if cfg.LocationSpan != 0.01 || cfg.MaxConcurrency != 4 {
		t.Errorf("span/concurrency defaults wrong: %+v", cfg)
	}
	if cfg.DataSources[0].AgencyMapping["KCM"] != "1" {
		t.Errorf("agencyMapping not parsed")
	}
}

func TestLoadParsesRealtimeHeaders(t *testing.T) {
	cfg, err := Load(`{"obaServerURL":"https://x","apiKey":"k","dataSources":[{"staticGtfsFeedURL":"u","realtimeHeaders":{"Authorization":"feed-key"}}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if got := cfg.DataSources[0].RealtimeHeaders["Authorization"]; got != "feed-key" {
		t.Errorf("RealtimeHeaders[Authorization] = %q, want feed-key", got)
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.json")
	if err := os.WriteFile(p, []byte(sampleJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OBAServerURL != "https://example.com" {
		t.Errorf("got %q", cfg.OBAServerURL)
	}
}

func TestLoadAPIKeyFromEnv(t *testing.T) {
	t.Setenv("ONEBUSAWAY_API_KEY", "envkey")
	cfg, err := Load(`{"obaServerURL":"https://x","dataSources":[{"staticGtfsFeedURL":"u"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "envkey" {
		t.Errorf("APIKey=%q want envkey", cfg.APIKey)
	}
}

func TestLoadErrors(t *testing.T) {
	if _, err := Load(`{bad json`); err == nil {
		t.Error("expected parse error")
	}
	if _, err := Load(`{"apiKey":"k","dataSources":[{}]}`); err == nil {
		t.Error("expected missing obaServerURL error")
	}
}

func TestLoadValidationErrors(t *testing.T) {
	// Ensure the env fallback can't satisfy apiKey for the missing-apiKey case.
	t.Setenv("ONEBUSAWAY_API_KEY", "")

	if _, err := Load(`{"obaServerURL":"https://x","dataSources":[{"staticGtfsFeedURL":"u"}]}`); err == nil {
		t.Error("expected missing-apiKey error")
	}
	if _, err := Load(`{"obaServerURL":"https://x","apiKey":"k"}`); err == nil {
		t.Error("expected missing-dataSources error")
	}
}

func TestLoadParsesSinkFields(t *testing.T) {
	raw := `{
	  "obaServerURL": "https://x",
	  "apiKey": "k",
	  "dataSources": [{"staticGtfsFeedURL":"u"}],
	  "db_url": "jdbc:postgresql://h:5432/d",
	  "db_user": "u",
	  "db_pass": "p",
	  "correlation_id": "abc-123",
	  "result_table": "oba_validator_results"
	}`
	cfg, err := Load(raw)
	if err != nil {
		t.Fatal(err)
	}
	sc := cfg.SinkConfig()
	if sc.DBURL != "jdbc:postgresql://h:5432/d" || sc.DBUser != "u" || sc.DBPass != "p" {
		t.Errorf("sink fields not parsed: %+v", sc)
	}
	if sc.CorrelationID != "abc-123" || sc.ResultTable != "oba_validator_results" {
		t.Errorf("correlation/table not parsed: %+v", sc)
	}
	if !sc.Configured() {
		t.Errorf("SinkConfig should be Configured()")
	}
}

func TestLoadWithoutSinkFieldsLeavesItDisabled(t *testing.T) {
	cfg, err := Load(sampleJSON)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SinkConfig().Configured() {
		t.Errorf("SinkConfig should be disabled when fields are absent")
	}
}

func TestLoadRejectsPartialSinkConfig(t *testing.T) {
	raw := `{
	  "obaServerURL": "https://x",
	  "apiKey": "k",
	  "dataSources": [{"staticGtfsFeedURL":"u"}],
	  "db_url": "jdbc:postgresql://h/d",
	  "db_user": "u"
	}`
	_, err := Load(raw)
	if err == nil {
		t.Fatal("want partial-sink error, got nil")
	}
	if !strings.Contains(err.Error(), "db_pass") {
		t.Errorf("error %q should mention the first missing field (db_pass)", err)
	}
}

func TestLoadRejectsUnknownResultTable(t *testing.T) {
	raw := `{
	  "obaServerURL": "https://x",
	  "apiKey": "k",
	  "dataSources": [{"staticGtfsFeedURL":"u"}],
	  "db_url": "jdbc:postgresql://h/d",
	  "db_user": "u",
	  "db_pass": "p",
	  "correlation_id": "abc",
	  "result_table": "evil"
	}`
	_, err := Load(raw)
	if err == nil || !strings.Contains(err.Error(), "unsupported result_table") {
		t.Errorf("want allow-list error, got %v", err)
	}
}
