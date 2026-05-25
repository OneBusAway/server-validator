package validator

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebusaway/oba-validator/config"
)

func miniGTFSZip(t *testing.T) []byte {
	t.Helper()
	files := map[string]string{
		"agency.txt":     "agency_id,agency_name,agency_url,agency_timezone\nKCM,Metro,https://k,America/Los_Angeles\n",
		"routes.txt":     "route_id,agency_id,route_short_name,route_long_name,route_type\nR1,KCM,1,One,3\n",
		"trips.txt":      "route_id,service_id,trip_id\nR1,S1,T1\n",
		"stops.txt":      "stop_id,stop_name,stop_lat,stop_lon\nST1,Stop,47.6,-122.3\n",
		"calendar.txt":   "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\nS1,1,1,1,1,1,0,0,20240101,20251231\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\nT1,08:00:00,08:00:00,ST1,1\n",
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, c := range files {
		w, _ := zw.Create(n)
		w.Write([]byte(c))
	}
	zw.Close()
	return buf.Bytes()
}

func TestRunEndToEndNoFail(t *testing.T) {
	zipBytes := miniGTFSZip(t)
	feedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "gtfs") {
			w.Write(zipBytes)
			return
		}
		w.Write([]byte{}) // empty realtime payload parses to an empty feed
	}))
	defer feedSrv.Close()

	obaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(p, "current-time"):
			w.Write([]byte(`{"data":{"entry":{"time":1716000000000}}}`))
		case strings.Contains(p, "agencies-with-coverage"):
			w.Write([]byte(`{"data":{"list":[{"agencyId":"1"}],"references":{"agencies":[{"id":"1","name":"Metro"}]}}}`))
		case strings.Contains(p, "routes-for-agency"):
			w.Write([]byte(`{"data":{"list":[{"id":"1_R1","agencyId":"1"}]}}`))
		case strings.Contains(p, "stops-for-route"):
			w.Write([]byte(`{"data":{"entry":{"routeId":"1_R1","stopIds":["1_ST1"]}}}`))
		case strings.Contains(p, "stops-for-location"):
			w.Write([]byte(`{"data":{"outOfRange":false,"list":[{"id":"1_ST1"}]}}`))
		case strings.Contains(p, "arrivals-and-departures-for-stop"):
			w.Write([]byte(`{"data":{"entry":{"arrivalsAndDepartures":[]}}}`))
		case strings.Contains(p, "/stop/"):
			w.Write([]byte(`{"data":{"entry":{"id":"1_ST1","lat":47.6,"lon":-122.3,"name":"Stop"}}}`))
		default:
			w.Write([]byte(`{"data":{"list":[]}}`))
		}
	}))
	defer obaSrv.Close()

	cfg := config.Config{
		OBAServerURL: obaSrv.URL, APIKey: "test",
		SampleSize: 3, RTFreshnessSeconds: 300, LocationSpan: 0.01, MaxConcurrency: 4, TimeoutSeconds: 30,
		NoCache: true,
		DataSources: []config.DataSource{{
			StaticGtfsFeedURL: feedSrv.URL + "/gtfs.zip",
			AgencyMapping:     map[string]string{"KCM": "1"},
		}},
	}
	rep, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Results) == 0 {
		t.Fatal("no results")
	}
	for _, r := range rep.Results {
		if r.Status == Fail {
			t.Errorf("unexpected Fail %s: %s", r.Check, r.Message)
		}
	}
}
