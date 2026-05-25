package validator

import (
	"encoding/json"
	"testing"
)

func TestStatusStringAndGlyph(t *testing.T) {
	cases := []struct {
		s     Status
		str   string
		glyph string
	}{
		{Pass, "PASS", "✓"},
		{Warn, "WARN", "⚠"},
		{Fail, "FAIL", "✗"},
		{Skip, "SKIP", "–"},
	}
	for _, c := range cases {
		if got := c.s.String(); got != c.str {
			t.Errorf("String()=%q want %q", got, c.str)
		}
		if got := c.s.Glyph(); got != c.glyph {
			t.Errorf("Glyph()=%q want %q", got, c.glyph)
		}
	}
}

func TestStatusMarshalJSON(t *testing.T) {
	b, err := json.Marshal(Fail)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `"FAIL"` {
		t.Errorf("MarshalJSON=%s want \"FAIL\"", b)
	}
}
