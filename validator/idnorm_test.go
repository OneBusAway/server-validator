package validator

import "testing"

func TestRawID(t *testing.T) {
	cases := map[string]string{
		"1_4567":   "4567",
		"40_12_34": "12_34", // split on FIRST underscore only
		"noprefix": "noprefix",
		"_x":       "x",
	}
	for in, want := range cases {
		if got := RawID(in); got != want {
			t.Errorf("RawID(%q)=%q want %q", in, got, want)
		}
	}
}

func TestPrefixedID(t *testing.T) {
	if got := PrefixedID("1", "4567"); got != "1_4567" {
		t.Errorf("got %q", got)
	}
	if got := PrefixedID("", "4567"); got != "4567" { // blank agency id
		t.Errorf("blank agency: got %q", got)
	}
}

func TestIDMatch(t *testing.T) {
	cases := []struct {
		api, feed, agency string
		want              bool
	}{
		{"1_4567", "4567", "1", true},
		{"1_4567", "4567", "", true},
		{"4567", "4567", "", true},
		{"1_4567", "9999", "1", false},
		{"MTS_ab_cd", "ab_cd", "MTS", true},
	}
	for _, c := range cases {
		if got := IDMatch(c.api, c.feed, c.agency); got != c.want {
			t.Errorf("IDMatch(%q,%q,%q)=%v want %v", c.api, c.feed, c.agency, got, c.want)
		}
	}
}
