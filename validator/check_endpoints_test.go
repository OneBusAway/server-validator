package validator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	onebusaway "github.com/OneBusAway/go-sdk"
	"github.com/OneBusAway/go-sdk/option"

	"github.com/onebusaway/oba-validator/config"
)

// newTestClient returns an SDK client pointed at a handler. Because each test
// drives one endpoint chain, the handler dispatches on the request path.
func newTestClient(t *testing.T, h http.HandlerFunc) *onebusaway.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return onebusaway.NewClient(option.WithAPIKey("test"), option.WithBaseURL(srv.URL))
}

func cfgForTest(apiKey string) config.Config {
	return config.Config{APIKey: apiKey, SampleSize: 3, LocationSpan: 0.01, RTFreshnessSeconds: 300}
}

func TestEndpointsCheckHappyPath(t *testing.T) {
	vc := endpointsVC(t, endpointsClient(t, ""))
	results := endpointsCheck{}.Run(context.Background(), vc)
	for _, r := range results {
		if r.Status == Fail || r.Status == Skip {
			t.Errorf("%s: status %v msg %q", r.Check, r.Status, r.Message)
		}
	}
	if len(results) != 7 {
		t.Errorf("got %d results want 7", len(results))
	}
}

// validEndpointBody returns the happy-path JSON for whichever endpoint the path
// names, so a test can null out one step while the rest of the chain succeeds.
func validEndpointBody(p string) string {
	switch {
	case strings.Contains(p, "current-time"):
		return `{"data":{"entry":{"time":1716000000000,"readableTime":"now"}}}`
	case strings.Contains(p, "agencies-with-coverage"):
		return `{"data":{"list":[{"agencyId":"1"}],"references":{"agencies":[{"id":"1","name":"Metro"}]}}}`
	case strings.Contains(p, "routes-for-agency"):
		return `{"data":{"list":[{"id":"1_R1","agencyId":"1"}]}}`
	case strings.Contains(p, "stops-for-route"):
		return `{"data":{"entry":{"routeId":"1_R1","stopIds":["1_S1"]}}}`
	case strings.Contains(p, "stops-for-location"):
		return `{"data":{"outOfRange":false,"list":[{"id":"1_S1"}]}}`
	case strings.Contains(p, "arrivals-and-departures-for-stop"):
		return `{"data":{"entry":{"arrivalsAndDepartures":[{"stopId":"1_S1","tripId":"1_T1"}]}}}`
	case strings.Contains(p, "/stop/"):
		return `{"data":{"entry":{"id":"1_S1","lat":47.6,"lon":-122.3,"name":"Stop"}}}`
	}
	return ""
}

// endpointsClient answers every endpoint with its happy-path body, except the
// step whose path contains nullStep (when non-empty), which returns `null`.
func endpointsClient(t *testing.T, nullStep string) *onebusaway.Client {
	t.Helper()
	return newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		if nullStep != "" && strings.Contains(r.URL.Path, nullStep) {
			w.Write([]byte(`null`))
			return
		}
		if body := validEndpointBody(r.URL.Path); body != "" {
			w.Write([]byte(body))
			return
		}
		t.Errorf("unexpected path %s", r.URL.Path)
	})
}

// endpointsVC pre-fetches agencies-with-coverage (as Run expects) and returns a
// context wired to client.
func endpointsVC(t *testing.T, client *onebusaway.Client) *ValidationContext {
	t.Helper()
	ag, err := client.AgenciesWithCoverage.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	return &ValidationContext{Config: cfgForTest("test"), Client: client, Agencies: ag}
}

// OBA can return a literal `null` body (HTTP 200) for a working-looking
// endpoint; the SDK decodes that into a nil response with a nil error. A core
// endpoint returning null is a server fault, so this smoke check must Fail
// rather than panic — for every SDK call in the chain. Each call dereferences
// the response differently (list length, scalar field, skew math), so all are
// covered, not just a representative one.
func TestEndpointsNullResponseFailsPerStep(t *testing.T) {
	steps := []struct{ step, pathSubstr string }{
		{"current-time", "current-time"},
		{"routes-for-agency", "routes-for-agency"},
		{"stops-for-route", "stops-for-route"},
		{"stop", "/stop/"},
		{"stops-for-location", "stops-for-location"},
		{"arrivals-and-departures-for-stop", "arrivals-and-departures-for-stop"},
	}
	for _, tc := range steps {
		t.Run(tc.step, func(t *testing.T) {
			vc := endpointsVC(t, endpointsClient(t, tc.pathSubstr))
			results := endpointsCheck{}.Run(context.Background(), vc)
			var got Result
			for _, r := range results {
				if r.Check == "basic-endpoints/"+tc.step {
					got = r
				}
			}
			if got.Status != Fail {
				t.Errorf("null %s response: want Fail got %v (%q)", tc.step, got.Status, got.Message)
			}
			// withReason must not leave a dangling colon when the cause is a null
			// body (nil error) rather than a transport error.
			if strings.HasSuffix(strings.TrimRight(got.Message, " "), ":") {
				t.Errorf("null %s message has a dangling colon: %q", tc.step, got.Message)
			}
		})
	}
}

func TestEndpointsCheckCurrentTimeFailureSkipsRest(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	vc := &ValidationContext{Config: cfgForTest("test"), Client: client}
	results := endpointsCheck{}.Run(context.Background(), vc)
	if results[0].Status != Fail {
		t.Errorf("first status %v want Fail", results[0].Status)
	}
	for _, r := range results[1:] {
		if r.Status != Skip {
			t.Errorf("%s status %v want Skip", r.Check, r.Status)
		}
	}
}
