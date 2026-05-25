package validator

import (
	"context"
	"testing"

	onebusaway "github.com/OneBusAway/go-sdk"

	"github.com/onebusaway/oba-validator/config"
	"github.com/onebusaway/oba-validator/feeds"
)

func agenciesResp(ids map[string]string) *onebusaway.AgenciesWithCoverageListResponse {
	r := &onebusaway.AgenciesWithCoverageListResponse{}
	for id, name := range ids {
		r.Data.List = append(r.Data.List, onebusaway.AgenciesWithCoverageListResponseDataList{AgencyID: id})
		r.Data.References.Agencies = append(r.Data.References.Agencies, onebusaway.ReferencesAgency{ID: id, Name: name})
	}
	return r
}

func staticWith(ids, names map[string]string) *feeds.ParsedStatic {
	p := &feeds.ParsedStatic{AgencyNames: map[string]string{}}
	for id := range ids {
		p.AgencyIDs = append(p.AgencyIDs, id)
		p.AgencyNames[id] = names[id]
	}
	return p
}

func sourceWith(mapping map[string]string, static *feeds.ParsedStatic) *SourceContext {
	return &SourceContext{Label: "ds0", Config: config.DataSource{AgencyMapping: mapping}, Static: static, PrepErrors: map[string]error{}}
}

func TestAgencyUnionMappedMatch(t *testing.T) {
	vc := &ValidationContext{
		Agencies: agenciesResp(map[string]string{"1": "Metro Transit"}),
		Sources:  []*SourceContext{sourceWith(map[string]string{"KCM": "1"}, staticWith(map[string]string{"KCM": ""}, map[string]string{"KCM": "Metro Transit"}))},
	}
	results := agencyUnionCheck{}.Run(context.Background(), vc)
	for _, r := range results {
		if r.Status != Pass {
			t.Errorf("expected Pass, got %v: %s", r.Status, r.Message)
		}
	}
}

func TestAgencyUnionMappedMissingFails(t *testing.T) {
	vc := &ValidationContext{
		Agencies: agenciesResp(map[string]string{"99": "Other"}),
		Sources:  []*SourceContext{sourceWith(map[string]string{"KCM": "1"}, staticWith(map[string]string{"KCM": ""}, map[string]string{"KCM": "Metro Transit"}))},
	}
	results := agencyUnionCheck{}.Run(context.Background(), vc)
	foundFail := false
	for _, r := range results {
		if r.Status == Fail {
			foundFail = true
		}
	}
	if !foundFail {
		t.Error("expected a Fail for mapped-but-missing agency")
	}
}

func TestAgencyUnionUnmappedMissingWarns(t *testing.T) {
	vc := &ValidationContext{
		Agencies: agenciesResp(map[string]string{"1": "Metro"}),
		Sources:  []*SourceContext{sourceWith(nil, staticWith(map[string]string{"KCM": ""}, map[string]string{"KCM": "Metro Transit"}))},
	}
	results := agencyUnionCheck{}.Run(context.Background(), vc)
	for _, r := range results {
		if r.Status == Fail {
			t.Errorf("unmapped-missing should Warn not Fail: %s", r.Message)
		}
	}
}
