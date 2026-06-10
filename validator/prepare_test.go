package validator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"

	"github.com/onebusaway/oba-validator/config"
)

// TestPrepareSendsRealtimeAuthHeaders verifies that a data source's
// RealtimeHeaders are sent when fetching its GTFS-RT feeds, so protected feeds
// (e.g. Swiftly, which requires an Authorization header) don't 401 — and that
// the same headers are NOT sent to the static GTFS feed, which lives on an
// unrelated host (the bundle URL) and must not receive the feed's credential.
func TestPrepareSendsRealtimeAuthHeaders(t *testing.T) {
	var mu sync.Mutex
	auth := map[string]string{} // request path -> Authorization header seen
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		auth[r.URL.Path] = r.Header.Get("Authorization")
		mu.Unlock()
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()

	cfg := config.Config{
		OBAServerURL:   srv.URL,
		APIKey:         "k",
		MaxConcurrency: 2,
		TimeoutSeconds: 5,
		NoCache:        true,
		DataSources: []config.DataSource{{
			StaticGtfsFeedURL:   srv.URL + "/static",
			VehiclePositionsURL: srv.URL + "/vp",
			RealtimeHeaders:     map[string]string{"Authorization": "secret-rt-key"},
		}},
	}

	if _, err := prepare(context.Background(), cfg); err != nil {
		t.Fatal(err)
	}

	mu.Lock()
	defer mu.Unlock()
	if auth["/vp"] != "secret-rt-key" {
		t.Errorf("vehiclePositions Authorization = %q, want secret-rt-key", auth["/vp"])
	}
	staticAuth, staticHit := auth["/static"]
	if !staticHit {
		t.Fatal("static feed was never requested; the empty-Authorization assertion below would pass vacuously")
	}
	if staticAuth != "" {
		t.Errorf("static feed Authorization = %q, want empty (credential must not leak to the bundle host)", staticAuth)
	}
}
