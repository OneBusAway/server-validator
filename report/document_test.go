package report

import (
	"testing"
)

func TestSplitCheck(t *testing.T) {
	cases := []struct {
		in       string
		category string
		step     string
	}{
		{"basic-endpoints/current-time", "basic-endpoints", "current-time"},
		{"vehicle-positions-sampling/trip-for-vehicle", "vehicle-positions-sampling", "trip-for-vehicle"},
		{"agency-union", "agency-union", ""},
		{"a/b/c", "a", "b/c"},
	}
	for _, c := range cases {
		cat, step := splitCheck(c.in)
		if cat != c.category || step != c.step {
			t.Errorf("splitCheck(%q) = (%q,%q) want (%q,%q)", c.in, cat, step, c.category, c.step)
		}
	}
}

func TestRedactString(t *testing.T) {
	if got := redactString("https://x/?key=SEKRET", "SEKRET"); got != "https://x/?key=***" {
		t.Errorf("redactString did not redact: %q", got)
	}
	if got := redactString("no-key-here", "SEKRET"); got != "no-key-here" {
		t.Errorf("redactString altered non-matching string: %q", got)
	}
	if got := redactString("anything", ""); got != "anything" {
		t.Errorf("empty apiKey should be a no-op: %q", got)
	}
}
