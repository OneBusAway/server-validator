package validator

import "strings"

// RawID strips an OBA agency prefix ("{agencyId}_") from an API id, returning
// the raw id. Splits on the first underscore only (raw ids may contain '_').
// An id with no underscore is returned unchanged.
func RawID(apiID string) string {
	if i := strings.IndexByte(apiID, '_'); i >= 0 {
		return apiID[i+1:]
	}
	return apiID
}

// PrefixedID builds an OBA API id "{agencyID}_{rawID}". A blank agencyID
// returns rawID unchanged (handles GTFS feeds with a blank agency_id).
func PrefixedID(agencyID, rawID string) string {
	if agencyID == "" {
		return rawID
	}
	return agencyID + "_" + rawID
}

// IDMatch reports whether an OBA API id refers to the same entity as a raw feed
// id, tolerant of the agency prefix. agencyID may be "" when unknown.
func IDMatch(apiID, rawFeedID, agencyID string) bool {
	if apiID == rawFeedID {
		return true
	}
	if RawID(apiID) == rawFeedID {
		return true
	}
	if agencyID != "" && apiID == PrefixedID(agencyID, rawFeedID) {
		return true
	}
	return false
}
