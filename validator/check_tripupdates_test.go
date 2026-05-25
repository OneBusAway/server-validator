package validator

import (
	"context"
	"net/http"
	"strings"
	"testing"

	gtfs "github.com/OneBusAway/go-gtfs"

	"github.com/onebusaway/oba-validator/config"
)

func TestTripUpdateSamplingFound(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "arrivals-and-departures-for-stop") {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"data":{"entry":{"arrivalsAndDepartures":[{"stopId":"1_ST1","tripId":"1_T1","vehicleId":"1_V1","routeId":"1_R1"}]}}}`))
			return
		}
		t.Errorf("unexpected path %s", r.URL.Path)
	})
	src := &SourceContext{
		Label:      "ds0",
		Config:     config.DataSource{AgencyMapping: map[string]string{"KCM": "1"}},
		PrepErrors: map[string]error{},
		Static:     staticForVehicle(), // trip T1 -> agency KCM
		TripUpdates: &gtfs.Realtime{Trips: []gtfs.Trip{{
			ID:              gtfs.TripID{ID: "T1", RouteID: "R1"},
			StopTimeUpdates: []gtfs.StopTimeUpdate{{StopID: strp("ST1"), Arrival: &gtfs.StopTimeEvent{}}},
		}}},
	}
	vc := &ValidationContext{Config: cfgForTest("test"), Client: client}
	results := tripUpdateSamplingCheck{}.Run(context.Background(), vc, src)
	if len(results) == 0 || results[0].Status != Pass {
		t.Errorf("want Pass, got %+v", results)
	}
}

func TestTripUpdateSamplingAbsentFails(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "arrivals-and-departures-for-stop") {
			w.Header().Set("Content-Type", "application/json")
			// Arrivals exist, but for a DIFFERENT trip than the feed predicts.
			w.Write([]byte(`{"data":{"entry":{"arrivalsAndDepartures":[{"stopId":"1_ST1","tripId":"1_OTHER"}]}}}`))
			return
		}
		t.Errorf("unexpected path %s", r.URL.Path)
	})
	src := &SourceContext{
		Label:      "ds0",
		Config:     config.DataSource{AgencyMapping: map[string]string{"KCM": "1"}},
		PrepErrors: map[string]error{},
		Static:     staticForVehicle(),
		TripUpdates: &gtfs.Realtime{Trips: []gtfs.Trip{{
			ID:              gtfs.TripID{ID: "T1", RouteID: "R1"},
			StopTimeUpdates: []gtfs.StopTimeUpdate{{StopID: strp("ST1"), Arrival: &gtfs.StopTimeEvent{}}},
		}}},
	}
	vc := &ValidationContext{Config: cfgForTest("test"), Client: client}
	results := tripUpdateSamplingCheck{}.Run(context.Background(), vc, src)
	if len(results) == 0 || results[0].Status != Fail {
		t.Errorf("predicted trip absent from arrivals should Fail, got %+v", results)
	}
}

func TestTripUpdateSamplingNilStaticNoPanic(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"data":{"entry":{"arrivalsAndDepartures":[]}}}`))
	})
	src := &SourceContext{
		Label:      "ds0",
		PrepErrors: map[string]error{},
		Static:     nil, // no static GTFS for this source
		TripUpdates: &gtfs.Realtime{Trips: []gtfs.Trip{{
			ID:              gtfs.TripID{ID: "T1"},
			StopTimeUpdates: []gtfs.StopTimeUpdate{{StopID: strp("ST1"), Arrival: &gtfs.StopTimeEvent{}}},
		}}},
	}
	vc := &ValidationContext{Config: cfgForTest("test"), Client: client}
	// Must not panic.
	_ = tripUpdateSamplingCheck{}.Run(context.Background(), vc, src)
}
