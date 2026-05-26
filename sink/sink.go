// Package sink writes a validator run's result to a Postgres "results" table so
// a caller (obacloud's ServerValidationJob) can read it back after a Render
// one-off job finishes. The DB write is purely additive — when Configured()
// returns false, callers must skip the write and the validator behaves exactly
// as today.
//
// The status column vocabulary is deliberately narrow:
//   "completed" — the validator produced a report (PASS or FAIL verdict);
//                 result_data holds the report JSON.
//   "error"     — the validator could not produce a report (errorDocument variant);
//                 error_message carries the cause.
// The verdict lives inside result_data at summary.verdict, NOT in status.
package sink

import "strings"

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
