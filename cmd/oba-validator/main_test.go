package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebusaway/oba-validator/config"
	"github.com/onebusaway/oba-validator/sink"
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

func TestRunJSONConfigErrorRedactsInlineAPIKey(t *testing.T) {
	t.Setenv("ONEBUSAWAY_API_KEY", "")
	var stdout, stderr bytes.Buffer
	code := run([]string{"oba-validator", "--json", `[{"obaServerURL":"https://x","apiKey":"SUPER-SECRET-KEY"}]`}, &stdout, &stderr)
	if code != 2 {
		t.Fatalf("exit=%d want 2", code)
	}
	if strings.Contains(stdout.String(), "SUPER-SECRET-KEY") {
		t.Errorf("apiKey leaked to stdout:\n%s", stdout.String())
	}
	var ed struct {
		Error string `json:"error"`
	}
	if err := json.Unmarshal(stdout.Bytes(), &ed); err != nil {
		t.Fatalf("stdout not JSON: %v\n%s", err, stdout.String())
	}
	if ed.Error == "" {
		t.Errorf("expected an error message: %s", stdout.String())
	}
}

func TestRunTextConfigErrorRedactsInlineAPIKey(t *testing.T) {
	t.Setenv("ONEBUSAWAY_API_KEY", "")
	var stdout, stderr bytes.Buffer
	run([]string{"oba-validator", `[{"obaServerURL":"https://x","apiKey":"SUPER-SECRET-KEY"}]`}, &stdout, &stderr)
	if strings.Contains(stderr.String(), "SUPER-SECRET-KEY") {
		t.Errorf("apiKey leaked to stderr:\n%s", stderr.String())
	}
}

func TestRunInvokesSinkOnCompleted(t *testing.T) {
	t.Setenv("ONEBUSAWAY_API_KEY", "")

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
		w.Write([]byte{})
	}))
	defer feedSrv.Close()

	type captured struct {
		status, data, errMsg string
		corrID, table        string
	}
	var got captured
	prev := sinkWrite
	sinkWrite = func(ctx context.Context, c sink.Config, status, data, errMsg string) error {
		got = captured{status: status, data: data, errMsg: errMsg, corrID: c.CorrelationID, table: c.ResultTable}
		return nil
	}
	defer func() { sinkWrite = prev }()

	cfg := `{
	  "obaServerURL":"` + obaSrv.URL + `",
	  "apiKey":"k",
	  "dataSources":[{"staticGtfsFeedURL":"` + feedSrv.URL + `/gtfs.zip"}],
	  "db_url":"jdbc:postgresql://h/d",
	  "db_user":"u",
	  "db_pass":"p",
	  "correlation_id":"abc-123",
	  "result_table":"oba_validator_results"
	}`
	var stdout, stderr bytes.Buffer
	run([]string{"oba-validator", "--json", "--no-cache", cfg}, &stdout, &stderr)

	if got.status != "completed" {
		t.Errorf("sink status = %q, want %q", got.status, "completed")
	}
	if got.corrID != "abc-123" || got.table != "oba_validator_results" {
		t.Errorf("sink config not threaded: corr=%q table=%q", got.corrID, got.table)
	}
	if got.errMsg != "" {
		t.Errorf("error_message should be empty on completed: %q", got.errMsg)
	}
	if !strings.Contains(got.data, `"schemaVersion"`) {
		t.Errorf("result_data should be the report JSON, got %q", got.data)
	}
	// stdout must still carry the full report — sink is purely additive.
	if !strings.Contains(stdout.String(), `"schemaVersion"`) {
		t.Errorf("stdout missing report JSON: %s", stdout.String())
	}
}

func TestRunSkipsSinkWhenNotConfigured(t *testing.T) {
	t.Setenv("ONEBUSAWAY_API_KEY", "")
	called := false
	prev := sinkWrite
	sinkWrite = func(ctx context.Context, c sink.Config, status, data, errMsg string) error {
		called = true
		return nil
	}
	defer func() { sinkWrite = prev }()

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
	feedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte{}) }))
	defer feedSrv.Close()

	cfg := `{"obaServerURL":"` + obaSrv.URL + `","apiKey":"k","dataSources":[{"staticGtfsFeedURL":"` + feedSrv.URL + `/gtfs.zip"}]}`
	var stdout, stderr bytes.Buffer
	run([]string{"oba-validator", "--json", "--no-cache", cfg}, &stdout, &stderr)

	if called {
		t.Errorf("sinkWrite must not be called when fields are absent")
	}
}

func TestRunSinkErrorDoesNotAlterExitCode(t *testing.T) {
	t.Setenv("ONEBUSAWAY_API_KEY", "")

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
	feedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte{}) }))
	defer feedSrv.Close()

	prev := sinkWrite
	sinkWrite = func(ctx context.Context, c sink.Config, status, data, errMsg string) error {
		return fmt.Errorf("simulated db failure")
	}
	defer func() { sinkWrite = prev }()

	cfg := `{
	  "obaServerURL":"` + obaSrv.URL + `",
	  "apiKey":"k",
	  "dataSources":[{"staticGtfsFeedURL":"` + feedSrv.URL + `/gtfs.zip"}],
	  "db_url":"jdbc:postgresql://h/d",
	  "db_user":"u",
	  "db_pass":"p",
	  "correlation_id":"abc",
	  "result_table":"oba_validator_results"
	}`
	var stdout, stderr bytes.Buffer
	code := run([]string{"oba-validator", "--json", "--no-cache", cfg}, &stdout, &stderr)

	// Sink failure must not change the validator exit code (0 or 1, never 2).
	if code == 2 {
		t.Errorf("sink failure should not produce exit code 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), "result sink write failed") {
		t.Errorf("stderr should log the sink failure: %s", stderr.String())
	}
}
