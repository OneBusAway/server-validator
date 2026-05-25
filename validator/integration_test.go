package validator

import (
	"context"
	"os"
	"testing"

	"github.com/onebusaway/oba-validator/config"
)

// TestLiveKingCountyMetro runs the real King County Metro config against the
// live Puget Sound server. Gated by OBA_VALIDATOR_LIVE=1 so it never runs in CI.
func TestLiveKingCountyMetro(t *testing.T) {
	if os.Getenv("OBA_VALIDATOR_LIVE") != "1" {
		t.Skip("set OBA_VALIDATOR_LIVE=1 to run the live integration test")
	}
	raw := `{
      "obaServerURL": "https://api.pugetsound.onebusaway.org",
      "apiKey": "org.onebusaway.iphone",
      "dataSources": [{
        "agencyMapping": {"KCM": "1"},
        "staticGtfsFeedURL": "https://metro.kingcounty.gov/GTFS/google_transit.zip",
        "vehiclePositionsURL": "https://s3.amazonaws.com/kcm-alerts-realtime-prod/vehiclepositions.pb",
        "tripUpdatesURL": "https://s3.amazonaws.com/kcm-alerts-realtime-prod/tripupdates.pb",
        "serviceAlertsURL": "https://s3.amazonaws.com/kcm-alerts-realtime-prod/alerts.pb"
      }]
    }`
	cfg, err := config.Load(raw)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rep.Results {
		t.Logf("%s [%s] %s — %s", r.Status, r.Source, r.Check, r.Message)
	}
	// The agencyMapping key "KCM" may need adjustment once agency.txt is
	// inspected; this test is primarily for manual, observational runs.
}
