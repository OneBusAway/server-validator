package validator

import (
	"context"
	"net/http"
	"strings"
	"testing"

	gtfs "github.com/OneBusAway/go-gtfs"

	"github.com/onebusaway/oba-validator/config"
	"github.com/onebusaway/oba-validator/feeds"
)

func f32(v float32) *float32 { return &v }
func strp(s string) *string  { return &s }

// staticForVehicle builds a ParsedStatic where trip T1 (route R1) belongs to KCM.
func staticForVehicle() *feeds.ParsedStatic {
	s := &gtfs.Static{
		Agencies: []gtfs.Agency{{Id: "KCM", Name: "Metro"}},
		Routes:   []gtfs.Route{{Id: "R1"}},
		Trips:    []gtfs.ScheduledTrip{{ID: "T1"}},
		Stops:    []gtfs.Stop{{Id: "ST1"}},
	}
	s.Routes[0].Agency = &s.Agencies[0]
	s.Trips[0].Route = &s.Routes[0]
	p, _ := feeds.ParseStaticFromStruct(s)
	return p
}

func TestVehicleSamplingHappyPath(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(p, "vehicles-for-agency"):
			w.Write([]byte(`{"data":{"list":[{"vehicleId":"1_V1","tripId":"1_T1"}]}}`))
		case strings.Contains(p, "trip-for-vehicle"):
			w.Write([]byte(`{"data":{"entry":{"tripId":"1_T1"}}}`))
		case strings.Contains(p, "trips-for-location"):
			w.Write([]byte(`{"data":{"list":[{"tripId":"1_T1"}]}}`))
		default:
			t.Errorf("unexpected path %s", p)
		}
	})
	src := &SourceContext{
		Label:      "ds0",
		Config:     config.DataSource{AgencyMapping: map[string]string{"KCM": "1"}},
		PrepErrors: map[string]error{},
		Static:     staticForVehicle(),
		VehiclePositions: &gtfs.Realtime{Vehicles: []gtfs.Vehicle{{
			ID:       &gtfs.VehicleID{ID: "V1"},
			Trip:     &gtfs.Trip{ID: gtfs.TripID{ID: "T1", RouteID: "R1"}},
			Position: &gtfs.Position{Latitude: f32(47.6), Longitude: f32(-122.3)},
		}}},
	}
	vc := &ValidationContext{Config: cfgForTest("test"), Client: client}
	results := vehicleSamplingCheck{}.Run(context.Background(), vc, src)
	for _, r := range results {
		if r.Status == Fail {
			t.Errorf("%s Fail: %s", r.Check, r.Message)
		}
	}
	if len(results) == 0 {
		t.Fatal("expected sub-results")
	}
}

func TestVehicleSamplingUnresolvableAgencyWarns(t *testing.T) {
	src := &SourceContext{
		Label:      "ds0",
		Config:     config.DataSource{},
		PrepErrors: map[string]error{},
		Static:     staticForVehicle(),
		VehiclePositions: &gtfs.Realtime{Vehicles: []gtfs.Vehicle{{
			ID:   &gtfs.VehicleID{ID: "V9"},
			Trip: &gtfs.Trip{ID: gtfs.TripID{ID: "UNKNOWN", RouteID: "ALSO_UNKNOWN"}},
		}}},
	}
	vc := &ValidationContext{Config: cfgForTest("test")}
	results := vehicleSamplingCheck{}.Run(context.Background(), vc, src)
	if results[0].Status != Warn {
		t.Errorf("unresolvable agency want Warn got %v", results[0].Status)
	}
}
