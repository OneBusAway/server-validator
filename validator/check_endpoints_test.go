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
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(p, "current-time"):
			w.Write([]byte(`{"data":{"entry":{"time":1716000000000,"readableTime":"now"}}}`))
		case strings.Contains(p, "agencies-with-coverage"):
			w.Write([]byte(`{"data":{"list":[{"agencyId":"1"}],"references":{"agencies":[{"id":"1","name":"Metro"}]}}}`))
		case strings.Contains(p, "routes-for-agency"):
			w.Write([]byte(`{"data":{"list":[{"id":"1_R1","agencyId":"1"}]}}`))
		case strings.Contains(p, "stops-for-route"):
			w.Write([]byte(`{"data":{"entry":{"routeId":"1_R1","stopIds":["1_S1"]}}}`))
		case strings.Contains(p, "stops-for-location"):
			w.Write([]byte(`{"data":{"outOfRange":false,"list":[{"id":"1_S1"}]}}`))
		case strings.Contains(p, "arrivals-and-departures-for-stop"):
			w.Write([]byte(`{"data":{"entry":{"arrivalsAndDepartures":[{"stopId":"1_S1","tripId":"1_T1","vehicleId":"1_V1","routeId":"1_R1"}]}}}`))
		case strings.Contains(p, "/stop/"):
			w.Write([]byte(`{"data":{"entry":{"id":"1_S1","lat":47.6,"lon":-122.3,"name":"Stop"}}}`))
		default:
			t.Errorf("unexpected path %s", p)
		}
	})
	ag, err := client.AgenciesWithCoverage.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	vc := &ValidationContext{Config: cfgForTest("test"), Client: client, Agencies: ag}

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
