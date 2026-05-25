package config

import (
	"os"
	"path/filepath"
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
