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
	"context"
	"fmt"
	"net/url"
	"sort"
	"strings"

	"github.com/jackc/pgx/v5"
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

// createTableSQL is parameterized by table name. The table name MUST come from
// the allow-list (see Validate) — never interpolate a caller-controlled string.
// Column shape is fixed by obacloud's reader (ObaDatabase::FetchResult); do not
// add columns without coordinating across both repos.
const createTableSQL = `CREATE TABLE IF NOT EXISTS %s (
  correlation_id TEXT PRIMARY KEY,
  status         TEXT NOT NULL,
  result_data    TEXT,
  error_message  TEXT
)`

// insertRowSQL uses ON CONFLICT DO NOTHING to keep the contract idempotent
// under retry without overwriting earlier writes. The table name is filled in
// from the allow-list at call time.
const insertRowSQL = `INSERT INTO %s (correlation_id, status, result_data, error_message)
VALUES ($1, $2, $3, $4)
ON CONFLICT (correlation_id) DO NOTHING`

// statementTimeoutSQL caps individual statement execution at 5s, matching
// obacloud's ObaDatabase::FetchResult. Anything longer makes the validator
// hang on bad creds or a misconfigured DB.
const statementTimeoutSQL = `SET statement_timeout = '5s'`

// redactErr returns an error whose message has DBPass replaced with "***" so
// pgx errors that echo the DSN cannot leak the password. The raw password is
// scrubbed first, then the URL-encoded form (normalizeDSN injects credentials
// via url.UserPassword which percent-encodes special characters) — a belt-and-
// suspenders second layer in case a future pgx error type echoes the DSN.
//
// Returns a fresh non-wrapping error so errors.Unwrap cannot recover the
// original un-redacted cause.
//
// Validate's own errors are static templates that never contain DBPass and
// intentionally bypass redactErr; if a future Validate path interpolates
// caller-controlled data, route it through this helper too.
func (c Config) redactErr(err error) error {
	if err == nil {
		return nil
	}
	s := err.Error()
	if c.DBPass != "" {
		s = strings.ReplaceAll(s, c.DBPass, "***")
		if enc := url.QueryEscape(c.DBPass); enc != c.DBPass {
			s = strings.ReplaceAll(s, enc, "***")
		}
	}
	return fmt.Errorf("%s", s)
}

// Write opens a single pgx connection to the configured Postgres, creates the
// table if it doesn't exist, and inserts one row keyed by CorrelationID. The
// status arg is "completed" or "error" (see package doc); resultData is the
// report JSON (empty on the error path); errorMessage is the cause (empty on
// the success path).
//
// Write is intentionally called AFTER stdout has been written by the caller —
// a DB error here must never prevent the report from reaching Render logs.
// The returned error is redacted (DBPass replaced with "***") so callers can
// safely log it to stderr.
func (c Config) Write(ctx context.Context, status, resultData, errorMessage string) error {
	if !c.Configured() {
		return fmt.Errorf("sink: Write called on unconfigured Config (programming error)")
	}
	// The status column vocabulary is fixed by obacloud's reader: only
	// "completed" (PASS or FAIL verdict) and "error" (errorDocument variant)
	// are accepted. Reject anything else before opening a DB connection so
	// arbitrary values can't be persisted by a buggy caller.
	if status != "completed" && status != "error" {
		return fmt.Errorf("sink: unsupported status %q (allowed: \"completed\", \"error\")", status)
	}
	if err := c.Validate(); err != nil {
		return err
	}
	// validateTable runs again here as defense in depth: Validate ran at
	// config-load time, but Write is also exported and may be called by tests
	// or future call sites that bypass the config pipeline.
	if !allowedTables[c.ResultTable] {
		return fmt.Errorf("result sink: unsupported result_table %q", c.ResultTable)
	}

	dsn, err := normalizeDSN(c.DBURL, c.DBUser, c.DBPass)
	if err != nil {
		return c.redactErr(err)
	}

	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		return c.redactErr(fmt.Errorf("connect: %w", err))
	}
	defer conn.Close(ctx)

	if _, err := conn.Exec(ctx, statementTimeoutSQL); err != nil {
		return c.redactErr(fmt.Errorf("set statement_timeout: %w", err))
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf(createTableSQL, c.ResultTable)); err != nil {
		return c.redactErr(fmt.Errorf("create table: %w", err))
	}
	if _, err := conn.Exec(ctx, fmt.Sprintf(insertRowSQL, c.ResultTable),
		c.CorrelationID, status, resultData, errorMessage); err != nil {
		return c.redactErr(fmt.Errorf("insert row: %w", err))
	}
	return nil
}
