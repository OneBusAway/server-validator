package validator

import (
	"context"
	"net/http"
	"testing"

	gtfs "github.com/OneBusAway/go-gtfs"
)

func TestTripUpdateSamplingFound(t *testing.T) {
	client := arrivalsClient(t, `{"data":{"entry":{"arrivalsAndDepartures":[{"stopId":"1_ST1","tripId":"1_T1","vehicleId":"1_V1","routeId":"1_R1"}]}}}`)
	vc := &ValidationContext{Config: cfgForTest("test"), Client: client}
	results := tripUpdateSamplingCheck{}.Run(context.Background(), vc, tripUpdateSrcForStop())
	assertFirstStatus(t, results, Pass, "predicted trip present at stop")
}

func TestTripUpdateSamplingAbsentFails(t *testing.T) {
	// Arrivals exist, but for a DIFFERENT trip than the feed predicts.
	client := arrivalsClient(t, `{"data":{"entry":{"arrivalsAndDepartures":[{"stopId":"1_ST1","tripId":"1_OTHER"}]}}}`)
	vc := &ValidationContext{Config: cfgForTest("test"), Client: client}
	results := tripUpdateSamplingCheck{}.Run(context.Background(), vc, tripUpdateSrcForStop())
	assertFirstStatus(t, results, Fail, "predicted trip absent from arrivals")
}

func TestTripUpdateSamplingNilStaticNoPanic(t *testing.T) {
	client := arrivalsClient(t, `{"data":{"entry":{"arrivalsAndDepartures":[]}}}`)
	src := &SourceContext{
		Label:      "ds0",
		PrepErrors: map[string]error{},
		Static:     nil, // no static GTFS for this source
		TripUpdates: &gtfs.Realtime{Trips: []gtfs.Trip{{
			ID:              gtfs.TripID{ID: "T1"},
			StopTimeUpdates: []gtfs.StopTimeUpdate{{StopID: strp("ST1"), Arrival: &gtfs.StopTimeEvent{}}},
		}}},
	}
	vc := &ValidationContext{Config: cfgForTest("test"), Client: client}
	// Must not panic.
	_ = tripUpdateSamplingCheck{}.Run(context.Background(), vc, src)
}

func TestTripUpdateSampling404StopWarns(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(http.StatusNotFound) })
	vc := &ValidationContext{Config: cfgForTest("test"), Client: client}
	results := tripUpdateSamplingCheck{}.Run(context.Background(), vc, tripUpdateSrcForStop())
	assertFirstStatus(t, results, Warn, "404 on stop should Warn not Fail")
}

// A `null` arrivals response (nil SDK response, nil error) must not be mistaken
// for "predicted trip absent" and Fail — it is an unconfirmed query, so Warn.
func TestTripUpdateNullArrivalsResponseWarns(t *testing.T) {
	client := arrivalsClient(t, `null`)
	vc := &ValidationContext{Config: cfgForTest("test"), Client: client}
	results := tripUpdateSamplingCheck{}.Run(context.Background(), vc, tripUpdateSrcForStop())
	assertFirstStatus(t, results, Warn, "null arrivals response")
}
