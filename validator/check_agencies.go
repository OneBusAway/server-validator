package validator

import (
	"context"
	"fmt"
)

type agencyUnionCheck struct{}

func (agencyUnionCheck) Name() string { return "agency-union" }

func (agencyUnionCheck) Run(ctx context.Context, vc *ValidationContext) []Result {
	const name = "agency-union"
	if vc.AgenciesErr != nil || vc.Agencies == nil {
		return []Result{{Check: name, Status: Fail, Message: "agencies-with-coverage unavailable: " + redact(vc.AgenciesErr, vc.Config.APIKey)}}
	}

	apiSet := map[string]bool{}
	for _, a := range vc.Agencies.Data.List {
		apiSet[a.AgencyID] = true
	}
	apiNamesByID := map[string]string{}
	for _, ra := range vc.Agencies.Data.References.Agencies {
		apiNamesByID[ra.ID] = ra.Name
	}

	type expected struct {
		obaID, gtfsID, name string
		mapped              bool
	}
	var exp []expected
	for _, src := range vc.Sources {
		if src.Static == nil {
			continue
		}
		for _, gid := range src.Static.AgencyIDs {
			oba, mapped := src.MapAgency(gid)
			exp = append(exp, expected{obaID: oba, gtfsID: gid, name: src.Static.AgencyNames[gid], mapped: mapped})
		}
	}

	var out []Result
	expectedSet := map[string]bool{}
	for _, e := range exp {
		expectedSet[e.obaID] = true
		if apiSet[e.obaID] {
			out = append(out, Result{Check: name, Status: Pass,
				Message: fmt.Sprintf("agency %q present in API as %q", e.gtfsID, e.obaID)})
			continue
		}
		det := map[string]any{"gtfsAgencyId": e.gtfsID, "expectedObaId": e.obaID}
		if hint := agencyHint(e.name, apiNamesByID, expectedSet); hint != "" {
			det["hint"] = hint
		}
		if e.mapped {
			out = append(out, Result{Check: name, Status: Fail,
				Message: fmt.Sprintf("mapped agency %q→%q not served by API", e.gtfsID, e.obaID), Details: det})
		} else {
			out = append(out, Result{Check: name, Status: Warn,
				Message: fmt.Sprintf("assumed identity mapping %q not served by API; add an agencyMapping entry if it is remapped", e.gtfsID), Details: det})
		}
	}

	for id := range apiSet {
		if !expectedSet[id] {
			out = append(out, Result{Check: name, Status: Warn,
				Message: fmt.Sprintf("API serves agency %q not present in any configured GTFS feed", id),
				Details: map[string]any{"apiAgencyId": id}})
		}
	}
	return out
}

// agencyHint suggests a mapping when an unmatched API agency shares the GTFS
// agency's name. Purely advisory; never affects pass/fail.
func agencyHint(gtfsName string, apiNamesByID map[string]string, expectedSet map[string]bool) string {
	if gtfsName == "" {
		return ""
	}
	for apiID, apiName := range apiNamesByID {
		if apiName == gtfsName && !expectedSet[apiID] {
			return fmt.Sprintf("API agency %q is named %q — did you mean to map it?", apiID, apiName)
		}
	}
	return ""
}
