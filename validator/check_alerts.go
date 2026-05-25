package validator

import (
	"context"
	"fmt"

	gtfs "github.com/OneBusAway/go-gtfs"
	onebusaway "github.com/OneBusAway/go-sdk"
)

type serviceAlertCheck struct{}

func (serviceAlertCheck) Name() string { return "service-alert-crossref" }

func (serviceAlertCheck) Run(ctx context.Context, vc *ValidationContext, src *SourceContext) []Result {
	const name = "service-alert-crossref"
	key := vc.Config.APIKey
	if err := src.PrepErrors["serviceAlerts"]; err != nil {
		return []Result{{Check: name, Source: src.Label, Status: Fail, Message: "service alerts feed unavailable: " + redact(err, key)}}
	}
	if src.ServiceAlerts == nil || len(src.ServiceAlerts.Alerts) == 0 {
		return []Result{{Check: name, Source: src.Label, Status: Warn, Message: "no alerts in feed to sample"}}
	}

	type sampleAlert struct {
		alert   gtfs.Alert
		rawStop string
	}
	var usable []sampleAlert
	for _, a := range src.ServiceAlerts.Alerts {
		for _, ie := range a.InformedEntities {
			if ie.StopID != nil && *ie.StopID != "" {
				usable = append(usable, sampleAlert{alert: a, rawStop: *ie.StopID})
				break
			}
		}
	}
	if len(usable) == 0 {
		return []Result{{Check: name, Source: src.Label, Status: Warn, Message: "no alert references a stop id we can cross-check"}}
	}
	sample := sampleByID(usable, vc.Config.SampleSize, func(s sampleAlert) string { return s.alert.ID })

	agency := ""
	if src.Static != nil && len(src.Static.AgencyIDs) > 0 {
		agency, _ = src.MapAgency(src.Static.AgencyIDs[0])
	}

	var out []Result
	for _, s := range sample {
		obaStop := PrefixedID(agency, s.rawStop)
		ad, bad := queryArrivals(ctx, vc, name, src.Label, obaStop)
		if bad != nil {
			out = append(out, *bad)
			continue
		}
		anySituation := false
		matched := false
		for _, adp := range ad.Data.Entry.ArrivalsAndDepartures {
			for _, sid := range adp.SituationIDs {
				anySituation = true
				if IDMatch(sid, s.alert.ID, agency) {
					matched = true
				}
			}
		}
		for _, sit := range ad.Data.References.Situations {
			anySituation = true
			if IDMatch(sit.ID, s.alert.ID, agency) {
				matched = true
			}
		}
		switch {
		case matched:
			out = append(out, Result{Check: name, Source: src.Label, Status: Pass,
				Message: fmt.Sprintf("alert %q surfaced at stop %q", s.alert.ID, s.rawStop)})
		case !anySituation:
			out = append(out, Result{Check: name, Source: src.Label, Status: Fail,
				Message: fmt.Sprintf("stop %q has no situations though feed alert %q affects it", s.rawStop, s.alert.ID),
				Details: map[string]any{"stopId": obaStop, "feedAlertId": s.alert.ID}})
		default:
			out = append(out, Result{Check: name, Source: src.Label, Status: Warn,
				Message: fmt.Sprintf("stop %q has situations but none matched feed alert %q (OBA may re-id situations)", s.rawStop, s.alert.ID),
				Details: map[string]any{"stopId": obaStop, "feedAlertId": s.alert.ID}})
		}
	}
	return out
}

// queryArrivals fetches arrivals-and-departures for a stop. It returns a non-nil
// *Result — a Warn the caller should record before skipping the stop — when the
// call errors or the server returns a null body, so the two cross-reference
// checks (alerts, trip-updates) share identical "couldn't read this stop"
// handling. A null body must never be read as "stop confirmed empty".
func queryArrivals(ctx context.Context, vc *ValidationContext, check, label, obaStop string) (*onebusaway.ArrivalAndDepartureListResponse, *Result) {
	ad, err := vc.Client.ArrivalAndDeparture.List(ctx, obaStop, onebusaway.ArrivalAndDepartureListParams{})
	switch {
	case err != nil:
		return nil, &Result{Check: check, Source: label, Status: Warn,
			Message: fmt.Sprintf("could not query stop %q (agency prefix may be wrong): %s", obaStop, redact(err, vc.Config.APIKey))}
	case ad == nil:
		return nil, &Result{Check: check, Source: label, Status: Warn,
			Message: fmt.Sprintf("arrivals query for stop %q returned a null response", obaStop),
			Details: map[string]any{"stopId": obaStop}}
	}
	return ad, nil
}
