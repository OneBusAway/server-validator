package validator

import (
	"context"
	"net/http"
	"strings"
	"testing"

	gtfs "github.com/OneBusAway/go-gtfs"

	"github.com/onebusaway/oba-validator/config"
)

func TestServiceAlertFoundInSituationIDs(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "arrivals-and-departures-for-stop") {
			w.Header().Set("Content-Type", "application/json")
			w.Write([]byte(`{"data":{"entry":{"arrivalsAndDepartures":[{"stopId":"1_ST1","tripId":"1_T1","situationIds":["1_ALERT1"]}]}}}`))
			return
		}
		t.Errorf("unexpected path %s", r.URL.Path)
	})
	src := &SourceContext{
		Label:      "ds0",
		Config:     config.DataSource{AgencyMapping: map[string]string{"KCM": "1"}},
		PrepErrors: map[string]error{},
		Static:     staticForVehicle(),
		ServiceAlerts: &gtfs.Realtime{Alerts: []gtfs.Alert{{
			ID:               "ALERT1",
			InformedEntities: []gtfs.AlertInformedEntity{{StopID: strp("ST1")}},
		}}},
	}
	vc := &ValidationContext{Config: cfgForTest("test"), Client: client}
	results := serviceAlertCheck{}.Run(context.Background(), vc, src)
	if len(results) == 0 || results[0].Status != Pass {
		t.Errorf("want Pass, got %+v", results)
	}
}

func TestServiceAlertNoSamplableWarns(t *testing.T) {
	src := &SourceContext{
		Label:         "ds0",
		Config:        config.DataSource{},
		PrepErrors:    map[string]error{},
		Static:        staticForVehicle(),
		ServiceAlerts: &gtfs.Realtime{Alerts: []gtfs.Alert{{ID: "A", InformedEntities: []gtfs.AlertInformedEntity{{AgencyID: strp("KCM")}}}}},
	}
	vc := &ValidationContext{Config: cfgForTest("test")}
	results := serviceAlertCheck{}.Run(context.Background(), vc, src)
	if results[0].Status != Warn {
		t.Errorf("agency-only alert not stop-referenceable: want Warn got %v", results[0].Status)
	}
}

func TestServiceAlertNoSituationsFails(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "arrivals-and-departures-for-stop") {
			w.Header().Set("Content-Type", "application/json")
			// Arrivals present but NO situations at all, though the feed says this stop is affected.
			w.Write([]byte(`{"data":{"entry":{"arrivalsAndDepartures":[{"stopId":"1_ST1","tripId":"1_T1"}]}}}`))
			return
		}
		t.Errorf("unexpected path %s", r.URL.Path)
	})
	src := &SourceContext{
		Label:      "ds0",
		Config:     config.DataSource{AgencyMapping: map[string]string{"KCM": "1"}},
		PrepErrors: map[string]error{},
		Static:     staticForVehicle(),
		ServiceAlerts: &gtfs.Realtime{Alerts: []gtfs.Alert{{
			ID:               "ALERT1",
			InformedEntities: []gtfs.AlertInformedEntity{{StopID: strp("ST1")}},
		}}},
	}
	vc := &ValidationContext{Config: cfgForTest("test"), Client: client}
	results := serviceAlertCheck{}.Run(context.Background(), vc, src)
	if len(results) == 0 || results[0].Status != Fail {
		t.Errorf("affected stop with no situations should Fail, got %+v", results)
	}
}

func TestServiceAlertSituationsButNoMatchWarns(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "arrivals-and-departures-for-stop") {
			w.Header().Set("Content-Type", "application/json")
			// Situations exist but none match the feed alert id.
			w.Write([]byte(`{"data":{"entry":{"arrivalsAndDepartures":[{"stopId":"1_ST1","tripId":"1_T1","situationIds":["1_DIFFERENT"]}]}}}`))
			return
		}
		t.Errorf("unexpected path %s", r.URL.Path)
	})
	src := &SourceContext{
		Label:      "ds0",
		Config:     config.DataSource{AgencyMapping: map[string]string{"KCM": "1"}},
		PrepErrors: map[string]error{},
		Static:     staticForVehicle(),
		ServiceAlerts: &gtfs.Realtime{Alerts: []gtfs.Alert{{
			ID:               "ALERT1",
			InformedEntities: []gtfs.AlertInformedEntity{{StopID: strp("ST1")}},
		}}},
	}
	vc := &ValidationContext{Config: cfgForTest("test"), Client: client}
	results := serviceAlertCheck{}.Run(context.Background(), vc, src)
	if len(results) == 0 || results[0].Status != Warn {
		t.Errorf("situations present but no match should Warn, got %+v", results)
	}
}
