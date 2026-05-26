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

func TestNormalizeDSN(t *testing.T) {
	cases := []struct {
		name, raw, user, pass string
		// substring assertions — DSN ordering of query params is not guaranteed
		// across url.URL.Query().Encode() implementations, so check pieces.
		mustContain    []string
		mustNotContain []string
		wantErr        bool
	}{
		{
			name: "jdbc prefix stripped, userinfo injected, defaults applied",
			raw:  "jdbc:postgresql://db.internal:5432/oba",
			user: "obauser",
			pass: "p@ss/word",
			mustContain: []string{
				"postgresql://",
				"obauser:",
				"@db.internal:5432/oba",
				"sslmode=require",
				"connect_timeout=5",
			},
			mustNotContain: []string{"jdbc:"},
		},
		{
			name:        "no jdbc prefix is fine",
			raw:         "postgresql://h:5432/d",
			user:        "u",
			pass:        "p",
			mustContain: []string{"postgresql://u:p@h:5432/d", "sslmode=require"},
		},
		{
			name:           "caller-specified sslmode is preserved",
			raw:            "jdbc:postgresql://h/d?sslmode=disable",
			user:           "u",
			pass:           "p",
			mustContain:    []string{"sslmode=disable"},
			mustNotContain: []string{"sslmode=require"},
		},
		{
			name:           "caller-specified connect_timeout is preserved",
			raw:            "jdbc:postgresql://h/d?connect_timeout=15",
			user:           "u",
			pass:           "p",
			mustContain:    []string{"connect_timeout=15"},
			mustNotContain: []string{"connect_timeout=5"},
		},
		{
			name:    "garbage URL fails",
			raw:     "://nope",
			user:    "u",
			pass:    "p",
			wantErr: true,
		},
		{
			name:    "empty url fails",
			raw:     "",
			user:    "u",
			pass:    "p",
			wantErr: true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := normalizeDSN(tc.raw, tc.user, tc.pass)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("want error, got %q", got)
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			for _, s := range tc.mustContain {
				if !strings.Contains(got, s) {
					t.Errorf("DSN %q missing %q", got, s)
				}
			}
			for _, s := range tc.mustNotContain {
				if strings.Contains(got, s) {
					t.Errorf("DSN %q should not contain %q", got, s)
				}
			}
		})
	}
}
