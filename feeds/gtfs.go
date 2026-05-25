package feeds

import (
	"sort"
	"time"

	gtfs "github.com/OneBusAway/go-gtfs"
)

// ParsedStatic wraps a parsed static GTFS feed with lookup indexes used by the
// validator checks.
type ParsedStatic struct {
	Static      *gtfs.Static
	AgencyIDs   []string          // sorted, unique agency ids
	AgencyNames map[string]string // agency id -> name
	tripAgency  map[string]string // raw trip id -> agency id
	routeAgency map[string]string // raw route id -> agency id
}

// ParseStatic parses a GTFS zip (bytes) and builds the agency indexes.
func ParseStatic(b []byte) (*ParsedStatic, error) {
	s, err := gtfs.ParseStatic(b, gtfs.ParseStaticOptions{})
	if err != nil {
		return nil, err
	}
	return parsedStaticFrom(s), nil
}

func parsedStaticFrom(s *gtfs.Static) *ParsedStatic {
	p := &ParsedStatic{
		Static:      s,
		AgencyNames: map[string]string{},
		tripAgency:  map[string]string{},
		routeAgency: map[string]string{},
	}
	seen := map[string]bool{}
	for i := range s.Agencies {
		a := &s.Agencies[i]
		p.AgencyNames[a.Id] = a.Name
		if !seen[a.Id] {
			seen[a.Id] = true
			p.AgencyIDs = append(p.AgencyIDs, a.Id)
		}
	}
	sort.Strings(p.AgencyIDs)
	for i := range s.Routes {
		r := &s.Routes[i]
		if r.Agency != nil {
			p.routeAgency[r.Id] = r.Agency.Id
		}
	}
	for i := range s.Trips {
		tr := &s.Trips[i]
		if tr.Route != nil && tr.Route.Agency != nil {
			p.tripAgency[tr.ID] = tr.Route.Agency.Id
		}
	}
	return p
}

// AgencyForTrip returns the GTFS agency id owning a raw trip id.
func (p *ParsedStatic) AgencyForTrip(tripID string) (string, bool) {
	a, ok := p.tripAgency[tripID]
	return a, ok
}

// AgencyForRoute returns the GTFS agency id owning a raw route id.
func (p *ParsedStatic) AgencyForRoute(routeID string) (string, bool) {
	a, ok := p.routeAgency[routeID]
	return a, ok
}

// ParseRealtime parses a GTFS-realtime feed, interpreting timestamps as UTC.
func ParseRealtime(b []byte) (*gtfs.Realtime, error) {
	return gtfs.ParseRealtime(b, &gtfs.ParseRealtimeOptions{Timezone: time.UTC})
}

// ParseStaticFromStruct builds a ParsedStatic from an already-constructed
// *gtfs.Static (used by tests; production code uses ParseStatic).
func ParseStaticFromStruct(s *gtfs.Static) (*ParsedStatic, error) {
	return parsedStaticFrom(s), nil
}
