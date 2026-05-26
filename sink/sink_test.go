package sink

import (
	"strings"
	"testing"
)

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

func TestValidate(t *testing.T) {
	full := Config{
		DBURL:         "jdbc:postgresql://h/d",
		DBUser:        "u",
		DBPass:        "p",
		CorrelationID: "abc",
		ResultTable:   "oba_validator_results",
	}

	t.Run("disabled is always valid", func(t *testing.T) {
		if err := (Config{}).Validate(); err != nil {
			t.Errorf("empty Config: %v", err)
		}
	})

	t.Run("fully configured is valid", func(t *testing.T) {
		if err := full.Validate(); err != nil {
			t.Errorf("full Config: %v", err)
		}
	})

	missingFields := []struct {
		name   string
		mutate func(*Config)
		want   string
	}{
		{"db_user", func(c *Config) { c.DBUser = "" }, "db_user"},
		{"db_pass", func(c *Config) { c.DBPass = "" }, "db_pass"},
		{"correlation_id", func(c *Config) { c.CorrelationID = "" }, "correlation_id"},
		{"result_table", func(c *Config) { c.ResultTable = "" }, "result_table"},
	}
	for _, tc := range missingFields {
		t.Run("missing "+tc.name, func(t *testing.T) {
			c := full
			tc.mutate(&c)
			err := c.Validate()
			if err == nil {
				t.Fatalf("want error, got nil")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Errorf("error %q should mention %q", err, tc.want)
			}
		})
	}

	t.Run("unsupported result_table", func(t *testing.T) {
		c := full
		c.ResultTable = "evil_table; DROP TABLE users; --"
		err := c.Validate()
		if err == nil || !strings.Contains(err.Error(), "unsupported result_table") {
			t.Errorf("want unsupported-table error, got %v", err)
		}
	})
}
