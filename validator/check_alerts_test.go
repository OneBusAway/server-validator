package validator

import (
	"context"
	"net/http"
	"testing"

	gtfs "github.com/OneBusAway/go-gtfs"

	"github.com/onebusaway/oba-validator/config"
)

func TestServiceAlertFoundInSituationIDs(t *testing.T) {
	client := arrivalsClient(t, `{"data":{"entry":{"arrivalsAndDepartures":[{"stopId":"1_ST1","tripId":"1_T1","situationIds":["1_ALERT1"]}]}}}`)
	vc := &ValidationContext{Config: cfgForTest("test"), Client: client}
	results := serviceAlertCheck{}.Run(context.Background(), vc, alertSrcForStop())
	assertFirstStatus(t, results, Pass, "alert in situationIds")
}

// A `null` arrivals response (nil SDK response, nil error) must not be mistaken
// for "stop has no situations" and Fail — it is an unconfirmed query, so Warn.
func TestServiceAlertNullArrivalsResponseWarns(t *testing.T) {
	client := arrivalsClient(t, `null`)
	vc := &ValidationContext{Config: cfgForTest("test"), Client: client}
	results := serviceAlertCheck{}.Run(context.Background(), vc, alertSrcForStop())
	assertFirstStatus(t, results, Warn, "null arrivals response")
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
	assertFirstStatus(t, results, Warn, "agency-only alert not stop-referenceable")
}

func TestServiceAlertNoSituationsFails(t *testing.T) {
	// Arrivals present but NO situations at all, though the feed says this stop is affected.
	client := arrivalsClient(t, `{"data":{"entry":{"arrivalsAndDepartures":[{"stopId":"1_ST1","tripId":"1_T1"}]}}}`)
	vc := &ValidationContext{Config: cfgForTest("test"), Client: client}
	results := serviceAlertCheck{}.Run(context.Background(), vc, alertSrcForStop())
	assertFirstStatus(t, results, Fail, "affected stop with no situations")
}

func TestServiceAlertSituationsButNoMatchWarns(t *testing.T) {
	// Situations exist but none match the feed alert id.
	client := arrivalsClient(t, `{"data":{"entry":{"arrivalsAndDepartures":[{"stopId":"1_ST1","tripId":"1_T1","situationIds":["1_DIFFERENT"]}]}}}`)
	vc := &ValidationContext{Config: cfgForTest("test"), Client: client}
	results := serviceAlertCheck{}.Run(context.Background(), vc, alertSrcForStop())
	assertFirstStatus(t, results, Warn, "situations present but no match")
}

func TestServiceAlertFoundInGlobalReferences(t *testing.T) {
	// situationIds empty on the arrival, but the alert IS in references.situations.
	client := arrivalsClient(t, `{"data":{"entry":{"arrivalsAndDepartures":[{"stopId":"1_ST1","tripId":"1_T1"}]},"references":{"situations":[{"id":"1_ALERT1"}]}}}`)
	vc := &ValidationContext{Config: cfgForTest("test"), Client: client}
	results := serviceAlertCheck{}.Run(context.Background(), vc, alertSrcForStop())
	assertFirstStatus(t, results, Pass, "alert in global references.situations")
}

func TestServiceAlert404StopWarns(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
	vc := &ValidationContext{Config: cfgForTest("test"), Client: client}
	results := serviceAlertCheck{}.Run(context.Background(), vc, alertSrcForStop())
	assertFirstStatus(t, results, Warn, "404 on stop should Warn not Fail")
}
