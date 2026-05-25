package feeds

import (
	"archive/zip"
	"bytes"
	"testing"
)

// buildZip writes a minimal GTFS zip with the given files.
func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestParseStaticIndexes(t *testing.T) {
	zip := buildZip(t, map[string]string{
		"agency.txt":     "agency_id,agency_name,agency_url,agency_timezone\nKCM,Metro Transit,https://kcm,America/Los_Angeles\n",
		"routes.txt":     "route_id,agency_id,route_short_name,route_long_name,route_type\nR1,KCM,1,One,3\n",
		"trips.txt":      "route_id,service_id,trip_id\nR1,S1,T1\n",
		"stops.txt":      "stop_id,stop_name,stop_lat,stop_lon\nST1,Stop 1,47.6,-122.3\n",
		"calendar.txt":   "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\nS1,1,1,1,1,1,0,0,20240101,20251231\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\nT1,08:00:00,08:00:00,ST1,1\n",
	})
	p, err := ParseStatic(zip)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.AgencyIDs) != 1 || p.AgencyIDs[0] != "KCM" {
		t.Errorf("AgencyIDs=%v", p.AgencyIDs)
	}
	if p.AgencyNames["KCM"] != "Metro Transit" {
		t.Errorf("name=%q", p.AgencyNames["KCM"])
	}
	if a, ok := p.AgencyForTrip("T1"); !ok || a != "KCM" {
		t.Errorf("AgencyForTrip=%q,%v", a, ok)
	}
	if a, ok := p.AgencyForRoute("R1"); !ok || a != "KCM" {
		t.Errorf("AgencyForRoute=%q,%v", a, ok)
	}
}
