package validator

import (
	"errors"
	"testing"
)

func TestRedact(t *testing.T) {
	err := errors.New("GET https://x?key=SECRET failed")
	got := redact(err, "SECRET")
	if got != "GET https://x?key=*** failed" {
		t.Errorf("redact=%q", got)
	}
	if redact(nil, "SECRET") != "" {
		t.Error("nil error should redact to empty string")
	}
}

func TestSampleByID(t *testing.T) {
	ids := []string{"c", "a", "b", "d"}
	got := sampleByID(ids, 2, func(s string) string { return s })
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("sampleByID=%v want [a b] (sorted, first 2)", got)
	}
	if all := sampleByID(ids, 99, func(s string) string { return s }); len(all) != 4 {
		t.Errorf("len=%d want 4", len(all))
	}
}
