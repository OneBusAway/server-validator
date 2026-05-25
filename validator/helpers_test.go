package validator

import (
	"net/http"
	"strings"
	"testing"

	gtfs "github.com/OneBusAway/go-gtfs"
	onebusaway "github.com/OneBusAway/go-sdk"

	"github.com/onebusaway/oba-validator/config"
)

// arrivalsClient returns a client that answers the arrivals-and-departures
// endpoint with body and rejects any other path. The cross-reference checks
// (alerts, trip-updates) only ever call that one endpoint.
func arrivalsClient(t *testing.T, body string) *onebusaway.Client {
	t.Helper()
	return newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "arrivals-and-departures-for-stop") {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(body))
			return
		}
		t.Errorf("unexpected path %s", r.URL.Path)
	})
}

// baseSrc returns a source for agency KCM (mapped to OBA id "1") with static
// GTFS but no realtime feeds; callers attach the one feed they exercise.
func baseSrc() *SourceContext {
	return &SourceContext{
		Label:      "ds0",
		Config:     config.DataSource{AgencyMapping: map[string]string{"KCM": "1"}},
		PrepErrors: map[string]error{},
		Static:     staticForVehicle(),
	}
}

// alertSrcForStop builds a source whose single alert affects stop ST1.
func alertSrcForStop() *SourceContext {
	s := baseSrc()
	s.ServiceAlerts = &gtfs.Realtime{Alerts: []gtfs.Alert{{
		ID:               "ALERT1",
		InformedEntities: []gtfs.AlertInformedEntity{{StopID: strp("ST1")}},
	}}}
	return s
}

// tripUpdateSrcForStop builds a source predicting trip T1 at stop ST1.
func tripUpdateSrcForStop() *SourceContext {
	s := baseSrc()
	s.TripUpdates = &gtfs.Realtime{Trips: []gtfs.Trip{{
		ID:              gtfs.TripID{ID: "T1", RouteID: "R1"},
		StopTimeUpdates: []gtfs.StopTimeUpdate{{StopID: strp("ST1"), Arrival: &gtfs.StopTimeEvent{}}},
	}}}
	return s
}

// assertFirstStatus checks the first result's status, the common shape for the
// single-sample cross-reference tests.
func assertFirstStatus(t *testing.T, results []Result, want Status, desc string) {
	t.Helper()
	if len(results) == 0 || results[0].Status != want {
		t.Errorf("%s: want %v, got %+v", desc, want, results)
	}
}

// vehicleValidBodies returns happy-path JSON keyed by endpoint path fragment for
// the vehicle-sampling check's three OBA calls. Callers override one entry to
// simulate an empty/null/mismatched response.
func vehicleValidBodies() map[string]string {
	return map[string]string{
		"vehicles-for-agency": `{"data":{"list":[{"vehicleId":"1_V1","tripId":"1_T1"}]}}`,
		"trip-for-vehicle":    `{"data":{"entry":{"tripId":"1_T1"}}}`,
		"trips-for-location":  `{"data":{"list":[{"tripId":"1_T1"}]}}`,
	}
}

// vehicleClient returns a client that answers each vehicle-sampling endpoint
// with the matching body from bodies, rejecting any unmapped path.
func vehicleClient(t *testing.T, bodies map[string]string) *onebusaway.Client {
	t.Helper()
	return newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		for path, body := range bodies {
			if strings.Contains(r.URL.Path, path) {
				w.Write([]byte(body))
				return
			}
		}
		t.Errorf("unexpected path %s", r.URL.Path)
	})
}
