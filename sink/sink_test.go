package sink

import "testing"

func TestConfigured(t *testing.T) {
	cases := []struct {
		name string
		c    Config
		want bool
	}{
		{"all empty", Config{}, false},
		{"only db_url set", Config{DBURL: "jdbc:postgresql://h/d"}, true},
		{"all five set", Config{
			DBURL:         "jdbc:postgresql://h/d",
			DBUser:        "u",
			DBPass:        "p",
			CorrelationID: "abc",
			ResultTable:   "oba_validator_results",
		}, true},
		{"db_url whitespace", Config{DBURL: "   "}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.c.Configured(); got != tc.want {
				t.Errorf("Configured() = %v, want %v", got, tc.want)
			}
		})
	}
}
