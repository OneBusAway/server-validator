package main

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebusaway/oba-validator/config"
)

func TestApplyFlagOverrides(t *testing.T) {
	cfg := config.Config{SampleSize: 3, NoCache: false}
	applyOverrides(&cfg, overrides{sampleSize: 5, noCache: true, freshness: 60})
	if cfg.SampleSize != 5 || !cfg.NoCache || cfg.RTFreshnessSeconds != 60 {
		t.Errorf("overrides not applied: %+v", cfg)
	}
}

func TestUsageWhenNoArgs(t *testing.T) {
	var stderr bytes.Buffer
	code := run([]string{"oba-validator"}, &bytes.Buffer{}, &stderr)
	if code != 2 {
		t.Errorf("exit=%d want 2", code)
	}
	if stderr.Len() == 0 {
		t.Error("expected usage on stderr")
	}
}

func TestUsageWhenTooManyArgs(t *testing.T) {
	var stderr bytes.Buffer
	code := run([]string{"oba-validator", "a", "b"}, &bytes.Buffer{}, &stderr)
	if code != 2 {
		t.Errorf("exit=%d want 2", code)
	}
	if stderr.Len() == 0 {
		t.Error("expected usage on stderr")
	}
}

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
