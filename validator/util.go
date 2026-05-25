package validator

import (
	"sort"
	"strings"
)

// redact removes the apiKey from an error string so secrets never reach output.
func redact(err error, apiKey string) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if apiKey != "" {
		s = strings.ReplaceAll(s, apiKey, "***")
	}
	return s
}

// sampleByID deterministically selects up to n items: it sorts by keyFn(item)
// and returns the first n, so repeated runs sample the same entities.
func sampleByID[T any](items []T, n int, keyFn func(T) string) []T {
	cp := make([]T, len(items))
	copy(cp, items)
	sort.SliceStable(cp, func(i, j int) bool { return keyFn(cp[i]) < keyFn(cp[j]) })
	if n < len(cp) {
		cp = cp[:n]
	}
	return cp
}
