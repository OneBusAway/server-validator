package validator

import (
	"context"
	"fmt"
	"time"

	gtfs "github.com/OneBusAway/go-gtfs"
)

type freshnessCheck struct{}

func (freshnessCheck) Name() string { return "rt-freshness" }

func (freshnessCheck) Run(ctx context.Context, vc *ValidationContext, src *SourceContext) []Result {
	maxAge := time.Duration(vc.Config.RTFreshnessSeconds) * time.Second
	feedList := []struct {
		name string
		rt   *gtfs.Realtime
		key  string
	}{
		{"vehiclePositions", src.VehiclePositions, "vehiclePositions"},
		{"tripUpdates", src.TripUpdates, "tripUpdates"},
		{"serviceAlerts", src.ServiceAlerts, "serviceAlerts"},
	}
	var out []Result
	for _, f := range feedList {
		check := "rt-freshness/" + f.name
		if err := src.PrepErrors[f.key]; err != nil {
			out = append(out, Result{Check: check, Source: src.Label, Status: Fail, Message: f.name + " unavailable: " + redact(err, vc.Config.APIKey)})
			continue
		}
		if f.rt == nil || f.rt.CreatedAt.IsZero() {
			out = append(out, Result{Check: check, Source: src.Label, Status: Warn, Message: f.name + " has no feed timestamp"})
			continue
		}
		age := time.Since(f.rt.CreatedAt)
		switch {
		case age > maxAge:
			out = append(out, Result{Check: check, Source: src.Label, Status: Fail,
				Message: fmt.Sprintf("%s stale by %s", f.name, age.Round(time.Second)), Details: map[string]any{"ageSeconds": int(age.Seconds())}})
		case age < -maxAge:
			out = append(out, Result{Check: check, Source: src.Label, Status: Warn,
				Message: fmt.Sprintf("%s timestamp is in the future (clock/timezone?)", f.name)})
		default:
			out = append(out, Result{Check: check, Source: src.Label, Status: Pass,
				Message: fmt.Sprintf("%s fresh (%s old)", f.name, age.Round(time.Second))})
		}
	}
	return out
}
