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

func TestVehicleSamplingEmptyTripForVehicleWarns(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "vehicles-for-agency"):
			w.Write([]byte(`{"data":{"list":[{"vehicleId":"1_V1","tripId":"1_T1"}]}}`))
		case strings.Contains(r.URL.Path, "trip-for-vehicle"):
			w.Write([]byte(`{"data":{"entry":{"tripId":""}}}`)) // no current trip
		case strings.Contains(r.URL.Path, "trips-for-location"):
			w.Write([]byte(`{"data":{"list":[{"tripId":"1_T1"}]}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
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
		if strings.Contains(r.Check, "trip-for-vehicle") && r.Status != Warn {
			t.Errorf("empty current trip should Warn, got %v: %s", r.Status, r.Message)
		}
		if r.Status == Fail {
			t.Errorf("unexpected Fail: %s %s", r.Check, r.Message)
		}
	}
}

// vehicleSrcForTest builds a SourceContext with one sampled vehicle on trip T1.
func vehicleSrcForTest() *SourceContext {
	return &SourceContext{
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
}

// The OBA server returns a literal `null` body (HTTP 200) for some queries; the
// SDK decodes that into a nil response with a nil error. The check must not
// dereference it. Each of the three OBA calls is covered below.

func TestVehicleSamplingNullTripsForLocationResponse(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "vehicles-for-agency"):
			w.Write([]byte(`{"data":{"list":[{"vehicleId":"1_V1","tripId":"1_T1"}]}}`))
		case strings.Contains(r.URL.Path, "trip-for-vehicle"):
			w.Write([]byte(`{"data":{"entry":{"tripId":"1_T1"}}}`))
		case strings.Contains(r.URL.Path, "trips-for-location"):
			w.Write([]byte(`null`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})
	vc := &ValidationContext{Config: cfgForTest("test"), Client: client}
	results := vehicleSamplingCheck{}.Run(context.Background(), vc, vehicleSrcForTest())
	if !hasWarn(results, "trips-for-location") {
		t.Errorf("null trips-for-location response should Warn, got %v", results)
	}
	assertNoFail(t, results)
}

// vehicles-for-agency is the agency-wide roster: the feed proves the agency has
// vehicles, so a null (like an empty) response is the API missing data the feed
// proves exists — a Fail, matching the sibling empty-list branch.
func TestVehicleSamplingNullVehiclesForAgencyFails(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "vehicles-for-agency"):
			w.Write([]byte(`null`))
		case strings.Contains(r.URL.Path, "trip-for-vehicle"):
			w.Write([]byte(`{"data":{"entry":{"tripId":"1_T1"}}}`))
		case strings.Contains(r.URL.Path, "trips-for-location"):
			w.Write([]byte(`{"data":{"list":[{"tripId":"1_T1"}]}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})
	vc := &ValidationContext{Config: cfgForTest("test"), Client: client}
	results := vehicleSamplingCheck{}.Run(context.Background(), vc, vehicleSrcForTest())
	if !hasStatus(results, "vehicles-for-agency", Fail) {
		t.Errorf("null vehicles-for-agency response should Fail, got %v", results)
	}
}

func TestVehicleSamplingNullTripForVehicleResponse(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "vehicles-for-agency"):
			w.Write([]byte(`{"data":{"list":[{"vehicleId":"1_V1","tripId":"1_T1"}]}}`))
		case strings.Contains(r.URL.Path, "trip-for-vehicle"):
			w.Write([]byte(`null`))
		case strings.Contains(r.URL.Path, "trips-for-location"):
			w.Write([]byte(`{"data":{"list":[{"tripId":"1_T1"}]}}`))
		default:
			t.Errorf("unexpected path %s", r.URL.Path)
		}
	})
	vc := &ValidationContext{Config: cfgForTest("test"), Client: client}
	results := vehicleSamplingCheck{}.Run(context.Background(), vc, vehicleSrcForTest())
	if !hasWarn(results, "trip-for-vehicle") {
		t.Errorf("null trip-for-vehicle response should Warn, got %v", results)
	}
	assertNoFail(t, results)
}

func hasWarn(results []Result, checkSubstr string) bool {
	return hasStatus(results, checkSubstr, Warn)
}

func hasStatus(results []Result, checkSubstr string, status Status) bool {
	for _, r := range results {
		if strings.Contains(r.Check, checkSubstr) && r.Status == status {
			return true
		}
	}
	return false
}

func assertNoFail(t *testing.T, results []Result) {
	t.Helper()
	for _, r := range results {
		if r.Status == Fail {
			t.Errorf("unexpected Fail: %s %s", r.Check, r.Message)
		}
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
