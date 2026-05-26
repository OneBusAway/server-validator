// Package sink writes a validator run's result to a Postgres "results" table so
// a caller (obacloud's ServerValidationJob) can read it back after a Render
// one-off job finishes. The DB write is purely additive — when Configured()
// returns false, callers must skip the write and the validator behaves exactly
// as today.
//
// The status column vocabulary is deliberately narrow:
//
//	"completed" — the validator produced a report (PASS or FAIL verdict);
//	              result_data holds the report JSON.
//	"error"     — the validator could not produce a report (errorDocument variant);
//	              error_message carries the cause.
//
// The verdict lives inside result_data at summary.verdict, NOT in status.
package sink

import (
	"fmt"
	"sort"
	"strings"
)

// Config holds the five invocation inputs that activate the sink. All five must
// be present and non-blank together; partial config is rejected by Validate.
type Config struct {
	DBURL         string `json:"db_url"`
	DBUser        string `json:"db_user"`
	DBPass        string `json:"db_pass"`
	CorrelationID string `json:"correlation_id"`
	ResultTable   string `json:"result_table"`
}

// Configured reports whether the sink is active. DBURL is the activation flag:
// if absent or blank (whitespace counts as blank), the sink is disabled and the
// validator behaves exactly as today.
func (c Config) Configured() bool {
	return strings.TrimSpace(c.DBURL) != ""
}

// allowedTables is the closed allow-list of result table names the sink may
// write to. result_table is caller-controlled and gets interpolated into a
// CREATE/INSERT, so it MUST NOT be a free-form string — mirror the obacloud-side
// allow-list (`%w[api_key_results oba_validator_results]`); the validator only
// ever writes to its own table, so the list is a single entry.
var allowedTables = map[string]bool{
	"oba_validator_results": true,
}

// allowedTableNames returns the sorted list of allowed table names so error
// messages stay in sync with the allowedTables map (the single source of truth).
func allowedTableNames() []string {
	names := make([]string, 0, len(allowedTables))
	for k := range allowedTables {
		names = append(names, k)
	}
	sort.Strings(names)
	return names
}

// Validate returns nil when the sink is disabled (Configured() == false) or
// when all five fields are present AND result_table is on the allow-list.
// Otherwise it returns a descriptive error naming the first offending field.
// Callers should run Validate at config-load time so a partial sink config
// surfaces through the normal config-error pipeline (status: "error", exit 2)
// rather than producing a half-written run.
func (c Config) Validate() error {
	if !c.Configured() {
		return nil
	}
	// db_url is present; every sibling must be too.
	for _, f := range []struct {
		name string
		val  string
	}{
		{"db_user", c.DBUser},
		{"db_pass", c.DBPass},
		{"correlation_id", c.CorrelationID},
		{"result_table", c.ResultTable},
	} {
		if strings.TrimSpace(f.val) == "" {
			return fmt.Errorf("result sink: %s is required when db_url is set", f.name)
		}
	}
	if !allowedTables[c.ResultTable] {
		return fmt.Errorf("result sink: unsupported result_table %q (allowed: %s)",
			c.ResultTable, strings.Join(allowedTableNames(), ", "))
	}
	return nil
}
