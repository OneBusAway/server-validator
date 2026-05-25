package validator

import (
	"context"
	"fmt"

	gtfs "github.com/OneBusAway/go-gtfs"
	onebusaway "github.com/OneBusAway/go-sdk"
)

type vehicleSamplingCheck struct{}

func (vehicleSamplingCheck) Name() string { return "vehicle-positions-sampling" }

func (vehicleSamplingCheck) Run(ctx context.Context, vc *ValidationContext, src *SourceContext) []Result {
	const name = "vehicle-positions-sampling"
	key := vc.Config.APIKey
	if err := src.PrepErrors["vehiclePositions"]; err != nil {
		return []Result{{Check: name, Source: src.Label, Status: Fail, Message: "vehicle positions feed unavailable: " + redact(err, key)}}
	}
	if src.VehiclePositions == nil || len(src.VehiclePositions.Vehicles) == 0 {
		return []Result{{Check: name, Source: src.Label, Status: Warn, Message: "no vehicles in feed to sample"}}
	}

	var candidates []gtfs.Vehicle
	for _, v := range src.VehiclePositions.Vehicles {
		if v.Trip != nil && v.Position != nil {
			candidates = append(candidates, v)
		}
	}
	if len(candidates) == 0 {
		candidates = src.VehiclePositions.Vehicles
	}
	sample := sampleByID(candidates, vc.Config.SampleSize, func(v gtfs.Vehicle) string {
		if v.ID != nil {
			return v.ID.ID
		}
		return ""
	})

	var out []Result
	for _, v := range sample {
		rawVeh := ""
		label := ""
		if v.ID != nil {
			rawVeh, label = v.ID.ID, v.ID.Label
		}
		agency, ok := resolveVehicleAgency(src, v)
		if !ok {
			out = append(out, Result{Check: name, Source: src.Label, Status: Warn,
				Message: fmt.Sprintf("could not resolve agency for vehicle %q (trip/route not in static GTFS)", rawVeh),
				Details: map[string]any{"vehicleId": rawVeh}})
			continue
		}

		// (a) vehicles-for-agency
		vfa, err := vc.Client.VehiclesForAgency.List(ctx, agency, onebusaway.VehiclesForAgencyListParams{})
		switch {
		case err != nil:
			out = append(out, Result{Check: name + "/vehicles-for-agency", Source: src.Label, Status: Fail,
				Message: "vehicles-for-agency failed: " + redact(err, key), Details: map[string]any{"agencyId": agency}})
		case len(vfa.Data.List) == 0:
			out = append(out, Result{Check: name + "/vehicles-for-agency", Source: src.Label, Status: Fail,
				Message: fmt.Sprintf("vehicles-for-agency %q empty while feed has vehicles", agency)})
		default:
			matched := false
			for _, item := range vfa.Data.List {
				if IDMatch(item.VehicleID, rawVeh, agency) || (label != "" && IDMatch(item.VehicleID, label, agency)) {
					matched = true
					break
				}
			}
			if matched {
				out = append(out, Result{Check: name + "/vehicles-for-agency", Source: src.Label, Status: Pass,
					Message: fmt.Sprintf("vehicle %q present", rawVeh)})
			} else {
				out = append(out, Result{Check: name + "/vehicles-for-agency", Source: src.Label, Status: Warn,
					Message: fmt.Sprintf("vehicle %q not found among %d vehicles (possible id-convention mismatch)", rawVeh, len(vfa.Data.List)),
					Details: map[string]any{"vehicleId": rawVeh, "agencyId": agency}})
			}
		}

		// (b) trip-for-vehicle
		obaVeh := PrefixedID(agency, rawVeh)
		tfv, err := vc.Client.TripForVehicle.Get(ctx, obaVeh, onebusaway.TripForVehicleGetParams{})
		rawTrip := v.Trip.ID.ID
		switch {
		case err != nil:
			out = append(out, Result{Check: name + "/trip-for-vehicle", Source: src.Label, Status: Warn,
				Message: "trip-for-vehicle returned no current trip: " + redact(err, key), Details: map[string]any{"vehicleId": obaVeh}})
		case IDMatch(tfv.Data.Entry.TripID, rawTrip, agency):
			out = append(out, Result{Check: name + "/trip-for-vehicle", Source: src.Label, Status: Pass,
				Message: fmt.Sprintf("vehicle %q on expected trip %q", rawVeh, rawTrip)})
		default:
			out = append(out, Result{Check: name + "/trip-for-vehicle", Source: src.Label, Status: Fail,
				Message: fmt.Sprintf("trip-for-vehicle returned %q, feed says %q", tfv.Data.Entry.TripID, rawTrip),
				Details: map[string]any{"apiTripId": tfv.Data.Entry.TripID, "feedTripId": rawTrip}})
		}

		// (c) trips-for-location
		if v.Position == nil || v.Position.Latitude == nil || v.Position.Longitude == nil {
			out = append(out, Result{Check: name + "/trips-for-location", Source: src.Label, Status: Warn,
				Message: fmt.Sprintf("vehicle %q has no position to query", rawVeh)})
			continue
		}
		tfl, err := vc.Client.TripsForLocation.List(ctx, onebusaway.TripsForLocationListParams{
			Lat:     onebusaway.Float(float64(*v.Position.Latitude)),
			Lon:     onebusaway.Float(float64(*v.Position.Longitude)),
			LatSpan: onebusaway.Float(vc.Config.LocationSpan),
			LonSpan: onebusaway.Float(vc.Config.LocationSpan),
		})
		if err != nil {
			out = append(out, Result{Check: name + "/trips-for-location", Source: src.Label, Status: Warn,
				Message: "trips-for-location failed: " + redact(err, key)})
			continue
		}
		found := false
		for _, item := range tfl.Data.List {
			if IDMatch(item.TripID, rawTrip, agency) {
				found = true
				break
			}
		}
		if found {
			out = append(out, Result{Check: name + "/trips-for-location", Source: src.Label, Status: Pass,
				Message: fmt.Sprintf("trip %q present near vehicle", rawTrip)})
		} else {
			out = append(out, Result{Check: name + "/trips-for-location", Source: src.Label, Status: Warn,
				Message: fmt.Sprintf("trip %q not in trips-for-location box (vehicle may have moved)", rawTrip)})
		}
	}
	return out
}

// resolveVehicleAgency finds the OBA agency id for a feed vehicle via the static
// GTFS trip→route→agency linkage, then applies the agencyMapping.
func resolveVehicleAgency(src *SourceContext, v gtfs.Vehicle) (string, bool) {
	if v.Trip == nil || src.Static == nil {
		return "", false
	}
	if gid, ok := src.Static.AgencyForTrip(v.Trip.ID.ID); ok {
		oba, _ := src.MapAgency(gid)
		return oba, true
	}
	if v.Trip.ID.RouteID != "" {
		if gid, ok := src.Static.AgencyForRoute(v.Trip.ID.RouteID); ok {
			oba, _ := src.MapAgency(gid)
			return oba, true
		}
	}
	return "", false
}
