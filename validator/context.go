package validator

import (
	"context"
	"sync"

	gtfs "github.com/OneBusAway/go-gtfs"
	onebusaway "github.com/OneBusAway/go-sdk"

	"github.com/onebusaway/oba-validator/config"
	"github.com/onebusaway/oba-validator/feeds"
)

// SourceContext holds one data source's prepared feeds and config.
type SourceContext struct {
	Index            int
	Label            string
	Config           config.DataSource
	Static           *feeds.ParsedStatic
	VehiclePositions *gtfs.Realtime
	TripUpdates      *gtfs.Realtime
	ServiceAlerts    *gtfs.Realtime

	mu         sync.Mutex
	PrepErrors map[string]error // feed name -> preparation error
}

// prepErr safely records a preparation error during concurrent fetching.
func (s *SourceContext) prepErr(feed string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.PrepErrors[feed] = err
}

// MapAgency translates a GTFS agency id to its OBA agency id via the data
// source's agencyMapping. Returns (gtfsID, false) when unmapped (identity).
func (s *SourceContext) MapAgency(gtfsAgencyID string) (obaID string, mapped bool) {
	if v, ok := s.Config.AgencyMapping[gtfsAgencyID]; ok {
		return v, true
	}
	return gtfsAgencyID, false
}

// ValidationContext is the shared state for a validation run.
type ValidationContext struct {
	Config      config.Config
	Client      *onebusaway.Client
	Agencies    *onebusaway.AgenciesWithCoverageListResponse
	AgenciesErr error
	Sources     []*SourceContext
}

// realtimeResult carries a parsed realtime feed out of a fetch goroutine.
type realtimeResult struct{ rt *gtfs.Realtime }

// ServerCheck runs once against the whole server.
type ServerCheck interface {
	Name() string
	Run(ctx context.Context, vc *ValidationContext) []Result
}

// DataSourceCheck runs once per data source.
type DataSourceCheck interface {
	Name() string
	Run(ctx context.Context, vc *ValidationContext, src *SourceContext) []Result
}
