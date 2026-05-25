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

// withReason appends a redacted error to msg as ": <err>", or returns msg
// unchanged when err is nil — e.g. when a step failed because the server
// returned an empty list or a null body rather than a transport error, so the
// message reads cleanly instead of trailing a bare colon.
func withReason(msg string, err error, apiKey string) string {
	if err == nil {
		return msg
	}
	return msg + ": " + redact(err, apiKey)
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
