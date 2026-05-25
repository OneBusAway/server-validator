package validator

import (
	"context"
	"testing"
	"time"

	gtfs "github.com/OneBusAway/go-gtfs"
)

func TestFreshnessFreshStaleAndMissing(t *testing.T) {
	vc := &ValidationContext{Config: cfgForTest("k")}
	src := &SourceContext{
		Label:            "ds0",
		PrepErrors:       map[string]error{},
		VehiclePositions: &gtfs.Realtime{CreatedAt: time.Now()},
		TripUpdates:      &gtfs.Realtime{CreatedAt: time.Now().Add(-30 * time.Minute)},
		ServiceAlerts:    &gtfs.Realtime{}, // zero CreatedAt -> missing
	}
	byCheck := map[string]Status{}
	for _, r := range (freshnessCheck{}).Run(context.Background(), vc, src) {
		byCheck[r.Check] = r.Status
	}
	if byCheck["rt-freshness/vehiclePositions"] != Pass {
		t.Errorf("vehiclePositions want Pass got %v", byCheck["rt-freshness/vehiclePositions"])
	}
	if byCheck["rt-freshness/tripUpdates"] != Fail {
		t.Errorf("tripUpdates (stale) want Fail got %v", byCheck["rt-freshness/tripUpdates"])
	}
	if byCheck["rt-freshness/serviceAlerts"] != Warn {
		t.Errorf("serviceAlerts (missing) want Warn got %v", byCheck["rt-freshness/serviceAlerts"])
	}
}
