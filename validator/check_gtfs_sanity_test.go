package validator

import (
	"context"
	"testing"

	gtfs "github.com/OneBusAway/go-gtfs"

	"github.com/onebusaway/oba-validator/feeds"
)

func TestGtfsSanityPassAndFail(t *testing.T) {
	good := &SourceContext{Label: "ds0", PrepErrors: map[string]error{}, Static: &feeds.ParsedStatic{Static: &gtfs.Static{
		Agencies: []gtfs.Agency{{Id: "1"}},
		Routes:   []gtfs.Route{{Id: "R"}},
		Stops:    []gtfs.Stop{{Id: "S"}},
		Trips:    []gtfs.ScheduledTrip{{ID: "T"}},
	}}}
	for _, r := range (gtfsSanityCheck{}).Run(context.Background(), &ValidationContext{}, good) {
		if r.Status != Pass {
			t.Errorf("good feed: %v %s", r.Status, r.Message)
		}
	}

	empty := &SourceContext{Label: "ds1", PrepErrors: map[string]error{}, Static: &feeds.ParsedStatic{Static: &gtfs.Static{}}}
	res := gtfsSanityCheck{}.Run(context.Background(), &ValidationContext{}, empty)
	if res[0].Status != Fail {
		t.Errorf("empty feed should Fail, got %v", res[0].Status)
	}
}
