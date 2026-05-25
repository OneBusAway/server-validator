package validator

import (
	"context"
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

// vehicleSrcForTest builds a SourceContext with one sampled vehicle on trip T1.
func vehicleSrcForTest() *SourceContext {
	s := baseSrc()
	s.VehiclePositions = &gtfs.Realtime{Vehicles: []gtfs.Vehicle{{
		ID:       &gtfs.VehicleID{ID: "V1"},
		Trip:     &gtfs.Trip{ID: gtfs.TripID{ID: "T1", RouteID: "R1"}},
		Position: &gtfs.Position{Latitude: f32(47.6), Longitude: f32(-122.3)},
	}}}
	return s
}

func TestVehicleSamplingHappyPath(t *testing.T) {
	client := vehicleClient(t, vehicleValidBodies())
	vc := &ValidationContext{Config: cfgForTest("test"), Client: client}
	results := vehicleSamplingCheck{}.Run(context.Background(), vc, vehicleSrcForTest())
	assertNoFail(t, results)
	if len(results) == 0 {
		t.Fatal("expected sub-results")
	}
}

func TestVehicleSamplingEmptyTripForVehicleWarns(t *testing.T) {
	bodies := vehicleValidBodies()
	bodies["trip-for-vehicle"] = `{"data":{"entry":{"tripId":""}}}` // no current trip
	client := vehicleClient(t, bodies)
	vc := &ValidationContext{Config: cfgForTest("test"), Client: client}
	results := vehicleSamplingCheck{}.Run(context.Background(), vc, vehicleSrcForTest())
	if !hasWarn(results, "trip-for-vehicle") {
		t.Errorf("empty current trip should Warn, got %+v", results)
	}
	assertNoFail(t, results)
}

// The OBA server returns a literal `null` body (HTTP 200) for some queries; the
// SDK decodes that into a nil response with a nil error. The check must not
// dereference it — each of the three OBA calls is covered here.
func TestVehicleSamplingNullResponses(t *testing.T) {
	cases := []struct {
		nullPath, wantCheck string
		want                Status
	}{
		// vehicles-for-agency is the agency-wide roster: the feed proves the
		// agency has vehicles, so a null (like an empty) response is the API
		// missing data the feed proves exists — Fail, matching the empty branch.
		{"vehicles-for-agency", "vehicles-for-agency", Fail},
		// Per-vehicle cross-refs are unconfirmed on a null response — Warn.
		{"trip-for-vehicle", "trip-for-vehicle", Warn},
		{"trips-for-location", "trips-for-location", Warn},
	}
	for _, tc := range cases {
		t.Run(tc.nullPath, func(t *testing.T) {
			bodies := vehicleValidBodies()
			bodies[tc.nullPath] = `null`
			vc := &ValidationContext{Config: cfgForTest("test"), Client: vehicleClient(t, bodies)}
			results := vehicleSamplingCheck{}.Run(context.Background(), vc, vehicleSrcForTest())
			if !hasStatus(results, tc.wantCheck, tc.want) {
				t.Errorf("null %s: want %v on %s, got %+v", tc.nullPath, tc.want, tc.wantCheck, results)
			}
		})
	}
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
