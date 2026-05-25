package validator

import (
	"context"
	"fmt"
	"time"

	onebusaway "github.com/OneBusAway/go-sdk"
)

type endpointsCheck struct{}

func (endpointsCheck) Name() string { return "basic-endpoints" }

func (endpointsCheck) Run(ctx context.Context, vc *ValidationContext) []Result {
	key := vc.Config.APIKey
	var out []Result
	add := func(step string, st Status, msg string, det map[string]any) {
		out = append(out, Result{Check: "basic-endpoints/" + step, Status: st, Message: msg, Details: det})
	}
	remaining := []string{
		"agencies-with-coverage", "routes-for-agency", "stops-for-route",
		"stop", "stops-for-location", "arrivals-and-departures-for-stop",
	}
	skipRest := func(reason string) {
		for _, s := range remaining {
			add(s, Skip, "skipped: "+reason, nil)
		}
	}
	pop := func() { remaining = remaining[1:] }

	// 1. current-time
	ct, err := vc.Client.CurrentTime.Get(ctx)
	if err != nil || ct == nil {
		add("current-time", Fail, withReason("current-time failed", err, key), nil)
		skipRest("current-time failed")
		return out
	}
	skew := time.Now().UnixMilli() - ct.Data.Entry.Time
	if skew < 0 {
		skew = -skew
	}
	if skew > time.Hour.Milliseconds() {
		add("current-time", Warn, fmt.Sprintf("clock skew %dms", skew), map[string]any{"serverTimeMs": ct.Data.Entry.Time})
	} else {
		add("current-time", Pass, "current-time OK", nil)
	}

	// 2. agencies-with-coverage (pre-fetched into the context)
	if vc.Agencies == nil || vc.AgenciesErr != nil {
		add("agencies-with-coverage", Fail, withReason("agencies-with-coverage failed", vc.AgenciesErr, key), nil)
		pop()
		skipRest("agencies-with-coverage failed")
		return out
	}
	if len(vc.Agencies.Data.List) == 0 {
		add("agencies-with-coverage", Fail, "no agencies returned", nil)
		pop()
		skipRest("no agencies")
		return out
	}
	agencyID := vc.Agencies.Data.List[0].AgencyID
	add("agencies-with-coverage", Pass, fmt.Sprintf("%d agencies", len(vc.Agencies.Data.List)), map[string]any{"agencyId": agencyID})
	pop()

	// 3. routes-for-agency
	routes, err := vc.Client.RoutesForAgency.List(ctx, agencyID)
	if err != nil || routes == nil || len(routes.Data.List) == 0 {
		add("routes-for-agency", Fail, withReason("routes-for-agency empty/failed", err, key), map[string]any{"agencyId": agencyID})
		pop()
		skipRest("routes-for-agency failed")
		return out
	}
	routeID := routes.Data.List[0].ID
	add("routes-for-agency", Pass, fmt.Sprintf("%d routes", len(routes.Data.List)), map[string]any{"routeId": routeID})
	pop()

	// 4. stops-for-route
	sfr, err := vc.Client.StopsForRoute.List(ctx, routeID, onebusaway.StopsForRouteListParams{})
	if err != nil || sfr == nil || len(sfr.Data.Entry.StopIDs) == 0 {
		add("stops-for-route", Fail, withReason("stops-for-route empty/failed", err, key), map[string]any{"routeId": routeID})
		pop()
		skipRest("stops-for-route failed")
		return out
	}
	stopID := sfr.Data.Entry.StopIDs[0]
	add("stops-for-route", Pass, fmt.Sprintf("%d stops", len(sfr.Data.Entry.StopIDs)), map[string]any{"stopId": stopID})
	pop()

	// 5. stop
	st, err := vc.Client.Stop.Get(ctx, stopID)
	if err != nil || st == nil || st.Data.Entry.ID != stopID {
		add("stop", Fail, withReason("stop lookup failed/mismatch", err, key), map[string]any{"stopId": stopID})
		pop()
		skipRest("stop failed")
		return out
	}
	lat, lon := st.Data.Entry.Lat, st.Data.Entry.Lon
	add("stop", Pass, "stop OK", map[string]any{"lat": lat, "lon": lon})
	pop()

	// 6. stops-for-location
	loc, err := vc.Client.StopsForLocation.List(ctx, onebusaway.StopsForLocationListParams{
		Lat: onebusaway.Float(lat),
		Lon: onebusaway.Float(lon),
	})
	if err != nil || loc == nil || loc.Data.OutOfRange || len(loc.Data.List) == 0 {
		add("stops-for-location", Fail, withReason("stops-for-location empty/out-of-range/failed", err, key), nil)
		pop()
		skipRest("stops-for-location failed")
		return out
	}
	add("stops-for-location", Pass, fmt.Sprintf("%d stops near", len(loc.Data.List)), nil)
	pop()

	// 7. arrivals-and-departures-for-stop
	ad, err := vc.Client.ArrivalAndDeparture.List(ctx, stopID, onebusaway.ArrivalAndDepartureListParams{})
	if err != nil || ad == nil {
		add("arrivals-and-departures-for-stop", Fail, withReason("arrivals failed", err, key), map[string]any{"stopId": stopID})
		return out
	}
	n := len(ad.Data.Entry.ArrivalsAndDepartures)
	if n == 0 {
		add("arrivals-and-departures-for-stop", Warn, "endpoint OK but no arrivals at this time", map[string]any{"stopId": stopID})
	} else {
		add("arrivals-and-departures-for-stop", Pass, fmt.Sprintf("%d arrivals/departures", n), map[string]any{"stopId": stopID})
	}
	return out
}
