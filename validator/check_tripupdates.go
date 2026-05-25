package validator

import (
	"context"
	"fmt"

	gtfs "github.com/OneBusAway/go-gtfs"
)

type tripUpdateSamplingCheck struct{}

func (tripUpdateSamplingCheck) Name() string { return "trip-update-sampling" }

func (tripUpdateSamplingCheck) Run(ctx context.Context, vc *ValidationContext, src *SourceContext) []Result {
	const name = "trip-update-sampling"
	key := vc.Config.APIKey
	if err := src.PrepErrors["tripUpdates"]; err != nil {
		return []Result{{Check: name, Source: src.Label, Status: Fail, Message: "trip updates feed unavailable: " + redact(err, key)}}
	}
	if src.TripUpdates == nil || len(src.TripUpdates.Trips) == 0 {
		return []Result{{Check: name, Source: src.Label, Status: Warn, Message: "no trip updates in feed to sample"}}
	}

	var usable []gtfs.Trip
	for _, tr := range src.TripUpdates.Trips {
		for _, stu := range tr.StopTimeUpdates {
			if stu.StopID != nil && (stu.Arrival != nil || stu.Departure != nil) {
				usable = append(usable, tr)
				break
			}
		}
	}
	if len(usable) == 0 {
		return []Result{{Check: name, Source: src.Label, Status: Warn, Message: "no trip update has a predicted stop-time"}}
	}
	sample := sampleByID(usable, vc.Config.SampleSize, func(t gtfs.Trip) string { return t.ID.ID })

	var out []Result
	for _, tr := range sample {
		agency := ""
		if src.Static != nil {
			if gid, ok := src.Static.AgencyForTrip(tr.ID.ID); ok {
				agency, _ = src.MapAgency(gid)
			} else if tr.ID.RouteID != "" {
				if gid, ok := src.Static.AgencyForRoute(tr.ID.RouteID); ok {
					agency, _ = src.MapAgency(gid)
				}
			}
		}

		var rawStop string
		for _, stu := range tr.StopTimeUpdates {
			if stu.StopID != nil && (stu.Arrival != nil || stu.Departure != nil) {
				rawStop = *stu.StopID
				break
			}
		}
		obaStop := PrefixedID(agency, rawStop)

		ad, bad := queryArrivals(ctx, vc, name, src.Label, obaStop)
		if bad != nil {
			out = append(out, *bad)
			continue
		}
		found := false
		for _, adp := range ad.Data.Entry.ArrivalsAndDepartures {
			if IDMatch(adp.TripID, tr.ID.ID, agency) {
				found = true
				break
			}
		}
		if found {
			out = append(out, Result{Check: name, Source: src.Label, Status: Pass,
				Message: fmt.Sprintf("predicted trip %q present at stop %q", tr.ID.ID, rawStop)})
		} else {
			out = append(out, Result{Check: name, Source: src.Label, Status: Fail,
				Message: fmt.Sprintf("trip %q predicted in feed but absent from arrivals at stop %q", tr.ID.ID, rawStop),
				Details: map[string]any{"feedTripId": tr.ID.ID, "stopId": obaStop}})
		}
	}
	return out
}
