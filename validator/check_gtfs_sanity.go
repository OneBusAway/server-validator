package validator

import (
	"context"
	"fmt"
)

type gtfsSanityCheck struct{}

func (gtfsSanityCheck) Name() string { return "gtfs-sanity" }

func (gtfsSanityCheck) Run(ctx context.Context, vc *ValidationContext, src *SourceContext) []Result {
	const name = "gtfs-sanity"
	if err := src.PrepErrors["staticGtfs"]; err != nil {
		return []Result{{Check: name, Source: src.Label, Status: Fail, Message: "static GTFS unavailable: " + redact(err, vc.Config.APIKey)}}
	}
	if src.Static == nil || src.Static.Static == nil {
		return []Result{{Check: name, Source: src.Label, Status: Fail, Message: "static GTFS not parsed"}}
	}
	s := src.Static.Static
	var missing []string
	if len(s.Agencies) == 0 {
		missing = append(missing, "agencies")
	}
	if len(s.Routes) == 0 {
		missing = append(missing, "routes")
	}
	if len(s.Stops) == 0 {
		missing = append(missing, "stops")
	}
	if len(s.Trips) == 0 {
		missing = append(missing, "trips")
	}
	if len(missing) > 0 {
		return []Result{{Check: name, Source: src.Label, Status: Fail,
			Message: fmt.Sprintf("static GTFS missing: %v", missing), Details: map[string]any{"missing": missing}}}
	}
	return []Result{{Check: name, Source: src.Label, Status: Pass,
		Message: fmt.Sprintf("%d agencies, %d routes, %d stops, %d trips", len(s.Agencies), len(s.Routes), len(s.Stops), len(s.Trips))}}
}
