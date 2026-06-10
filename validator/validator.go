package validator

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	onebusaway "github.com/OneBusAway/go-sdk"
	"github.com/OneBusAway/go-sdk/option"

	"github.com/onebusaway/oba-validator/config"
	"github.com/onebusaway/oba-validator/feeds"
)

// Run prepares the shared context and executes all checks, returning a Report.
func Run(ctx context.Context, cfg config.Config) (Report, error) {
	vc, err := prepare(ctx, cfg)
	if err != nil {
		return Report{}, err
	}
	var rep Report
	for _, c := range serverChecks() {
		rep.Results = append(rep.Results, c.Run(ctx, vc)...)
	}
	for _, src := range vc.Sources {
		for _, c := range dataSourceChecks() {
			rep.Results = append(rep.Results, c.Run(ctx, vc, src)...)
		}
	}
	return rep, nil
}

func serverChecks() []ServerCheck {
	return []ServerCheck{endpointsCheck{}, agencyUnionCheck{}}
}

func dataSourceChecks() []DataSourceCheck {
	return []DataSourceCheck{
		gtfsSanityCheck{},
		freshnessCheck{},
		vehicleSamplingCheck{},
		tripUpdateSamplingCheck{},
		serviceAlertCheck{},
	}
}

func prepare(ctx context.Context, cfg config.Config) (*ValidationContext, error) {
	if cfg.MaxConcurrency <= 0 {
		cfg.MaxConcurrency = 4
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	httpClient := &http.Client{Timeout: timeout}

	client := onebusaway.NewClient(
		option.WithAPIKey(cfg.APIKey),
		option.WithBaseURL(cfg.OBAServerURL),
		option.WithMaxRetries(2),
		option.WithRequestTimeout(timeout),
		option.WithHTTPClient(httpClient),
	)

	cacheDir := cfg.CacheDir
	if cacheDir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			base = os.TempDir()
		}
		cacheDir = filepath.Join(base, "oba-validator")
	}
	fetcher := feeds.NewFetcher(httpClient, feeds.NewCache(cacheDir, time.Hour), cfg.NoCache, cfg.Refresh)

	vc := &ValidationContext{Config: cfg, Client: client}
	vc.Agencies, vc.AgenciesErr = client.AgenciesWithCoverage.List(ctx)

	vc.Sources = make([]*SourceContext, len(cfg.DataSources))
	sem := make(chan struct{}, cfg.MaxConcurrency)
	var wg sync.WaitGroup

	for i := range cfg.DataSources {
		ds := cfg.DataSources[i]
		src := &SourceContext{Index: i, Label: fmt.Sprintf("dataSource[%d]", i), Config: ds, PrepErrors: map[string]error{}}
		vc.Sources[i] = src

		if ds.StaticGtfsFeedURL != "" {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				b, err := fetcher.FetchStatic(ctx, ds.StaticGtfsFeedURL)
				if err == nil {
					var p *feeds.ParsedStatic
					p, err = feeds.ParseStatic(b)
					if err == nil {
						src.Static = p
					}
				}
				if err != nil {
					src.prepErr("staticGtfs", err)
				}
			}()
		}

		rtHeaders := http.Header{}
		for k, v := range ds.RealtimeHeaders {
			rtHeaders.Set(k, v)
		}

		rt := func(feedName, url string, assign func(r *realtimeResult)) {
			if url == "" {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				b, err := fetcher.FetchRealtime(ctx, url, rtHeaders)
				if err == nil {
					parsed := &realtimeResult{}
					parsed.rt, err = feeds.ParseRealtime(b)
					if err == nil {
						assign(parsed)
					}
				}
				if err != nil {
					src.prepErr(feedName, err)
				}
			}()
		}
		rt("vehiclePositions", ds.VehiclePositionsURL, func(r *realtimeResult) { src.VehiclePositions = r.rt })
		rt("tripUpdates", ds.TripUpdatesURL, func(r *realtimeResult) { src.TripUpdates = r.rt })
		rt("serviceAlerts", ds.ServiceAlertsURL, func(r *realtimeResult) { src.ServiceAlerts = r.rt })
	}
	wg.Wait()
	return vc, nil
}
