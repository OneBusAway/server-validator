# Result Sink Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Add an optional Postgres sink so the validator writes its run result to a results table (keyed by `correlation_id`) for obacloud's `ServerValidationJob` to read, while keeping stdout/exit-code behavior identical to today.

**Architecture:** New `sink/` package owns the DB-write contract: `Config` (the five new invocation inputs), `Validate()` (allow-list + missing-sibling check), `normalizeDSN()` (strip `jdbc:` prefix + force `sslmode=require` + 5s `connect_timeout`), and `Write()` (open conn, `SET statement_timeout`, `CREATE TABLE IF NOT EXISTS`, `INSERT ... ON CONFLICT DO NOTHING`). `config.Config` gains five flat snake_case fields that build a `sink.Config` via a helper. `cmd/oba-validator/main.go` calls `sink.Write` **after** printing stdout on both the success and the validator-error paths; sink errors are logged to stderr and never alter the validator's exit code.

**Tech Stack:** Go 1.25, `github.com/jackc/pgx/v5` (single `pgx.Conn`, no pool), the existing config/report/validator packages.

---

## Reference: spec lives at

`docs/superpowers/specs/2026-05-25-result-sink-design.md` (in this repo). Read it before starting — the `status` column vocabulary (`"completed"` for both PASS *and* FAIL verdicts; `"error"` only for the `errorDocument` variant) is the single subtlest part of the contract.

## File map

**Create:**
- `sink/sink.go` — `Config`, `Configured()`, `Validate()`, `Write()`, `redactErr()`, SQL constants.
- `sink/dsn.go` — `normalizeDSN()` (jdbc-prefix strip, sslmode default, connect_timeout default, userinfo injection).
- `sink/sink_test.go` — unit tests for `Configured`, `Validate`, `normalizeDSN`, `redactErr`.
- `sink/sink_integration_test.go` — env-gated (`OBA_VALIDATOR_DB_DSN`) end-to-end test for `Write`.

**Modify:**
- `config/config.go` — five new flat fields, `SinkConfig()` helper, sink validation hook in `validate()`.
- `config/config_test.go` — new parse/validation cases.
- `report/report.go` — factor a `RenderJSON` / `RenderErrorJSON` that returns the marshaled bytes; keep `WriteJSON` / `WriteErrorJSON` as thin wrappers.
- `cmd/oba-validator/main.go` — call sink on both the success and the validator-error paths after stdout is written.
- `cmd/oba-validator/main_test.go` — regression: stdout unchanged when sink fields absent; new test that sink is invoked when configured (via an injected writer var).
- `go.mod` / `go.sum` — `pgx/v5` dependency.
- `CLAUDE.md` — one sentence under "What this is" mentioning the optional sink.

**Do not modify:**
- `schema/oba-validator-report.schema.json` — sink fields are invocation inputs, not part of the report.
- `entrypoint.sh` — already base64-decodes the full JSON blob, no change needed.
- `Dockerfile`, `render.yaml` — `go build ./...` picks up the new package automatically; image tag unchanged.

---

## Task 1: Scaffold the `sink` package with `Config` and `Configured()`

**Files:**
- Create: `sink/sink.go`
- Create: `sink/sink_test.go`

- [ ] **Step 1: Write the failing test**

Create `sink/sink_test.go` with:

```go
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
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
go test ./sink/ -run TestConfigured -v
```

Expected: `package sink/: no Go files` or build error (`Config` undefined).

- [ ] **Step 3: Write the minimal implementation**

Create `sink/sink.go`:

```go
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
```

- [ ] **Step 4: Run test to confirm it passes**

```bash
go test ./sink/ -run TestConfigured -v
```

Expected: PASS for all four cases.

- [ ] **Step 5: Commit**

```bash
git add sink/sink.go sink/sink_test.go
git commit -m "feat(sink): scaffold Config with Configured()"
```

---

## Task 2: Implement `Validate()` — allow-list + missing-sibling check

**Files:**
- Modify: `sink/sink.go`
- Modify: `sink/sink_test.go`

- [ ] **Step 1: Write the failing test**

Append to `sink/sink_test.go`:

```go
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
		name  string
		mutate func(*Config)
		want  string
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
```

Also add `"strings"` to the imports of `sink_test.go` if not already there:

```go
import (
	"strings"
	"testing"
)
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
go test ./sink/ -run TestValidate -v
```

Expected: build error (`Validate` undefined).

- [ ] **Step 3: Implement `Validate()`**

Append to `sink/sink.go` (also add `fmt` to the imports — change the import block to a parenthesized one):

```go
import (
	"fmt"
	"strings"
)

// allowedTables is the closed allow-list of result table names the sink may
// write to. result_table is caller-controlled and gets interpolated into a
// CREATE/INSERT, so it MUST NOT be a free-form string — mirror the obacloud-side
// allow-list (`%w[api_key_results oba_validator_results]`); the validator only
// ever writes to its own table, so the list is a single entry.
var allowedTables = map[string]bool{
	"oba_validator_results": true,
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
		return fmt.Errorf("result sink: unsupported result_table %q (allowed: oba_validator_results)", c.ResultTable)
	}
	return nil
}
```

- [ ] **Step 4: Run test to confirm it passes**

```bash
go test ./sink/ -v
```

Expected: PASS for `TestConfigured` and all `TestValidate` subtests.

- [ ] **Step 5: Commit**

```bash
git add sink/sink.go sink/sink_test.go
git commit -m "feat(sink): Validate enforces missing-sibling and table allow-list"
```

---

## Task 3: Implement `normalizeDSN()` — JDBC prefix strip + sslmode + connect_timeout

**Files:**
- Create: `sink/dsn.go`
- Modify: `sink/sink_test.go`

The validator receives `db_url` in JDBC form (`jdbc:postgresql://host:5432/db`) because obacloud formats it that way. pgx cannot parse `jdbc:`-prefixed URLs. We strip the prefix, inject userinfo, force `sslmode=require`, and default `connect_timeout=5`.

- [ ] **Step 1: Write the failing test**

Append to `sink/sink_test.go`:

```go
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
			name: "no jdbc prefix is fine",
			raw:  "postgresql://h:5432/d",
			user: "u",
			pass: "p",
			mustContain: []string{"postgresql://u:p@h:5432/d", "sslmode=require"},
		},
		{
			name: "caller-specified sslmode is preserved",
			raw:  "jdbc:postgresql://h/d?sslmode=disable",
			user: "u",
			pass: "p",
			mustContain:    []string{"sslmode=disable"},
			mustNotContain: []string{"sslmode=require"},
		},
		{
			name: "caller-specified connect_timeout is preserved",
			raw:  "jdbc:postgresql://h/d?connect_timeout=15",
			user: "u",
			pass: "p",
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
```

- [ ] **Step 2: Run test to confirm it fails**

```bash
go test ./sink/ -run TestNormalizeDSN -v
```

Expected: build error (`normalizeDSN` undefined).

- [ ] **Step 3: Implement `normalizeDSN`**

Create `sink/dsn.go`:

```go
package sink

import (
	"fmt"
	"net/url"
	"strings"
)

// normalizeDSN converts the JDBC-style URL obacloud sends (e.g.
// "jdbc:postgresql://host:5432/db") into a pgx-parseable DSN by:
//   1. stripping the "jdbc:" prefix if present (pgx rejects it),
//   2. injecting userinfo from dbUser/dbPass into the URL,
//   3. defaulting sslmode=require if the caller didn't set one,
//   4. defaulting connect_timeout=5 (seconds) if the caller didn't set one.
//
// The function never echoes dbPass into its return value's error path: parse
// errors come from url.Parse on rawURL alone (which never contains the
// password), so the password cannot leak into an error message.
func normalizeDSN(rawURL, dbUser, dbPass string) (string, error) {
	if strings.TrimSpace(rawURL) == "" {
		return "", fmt.Errorf("db_url is empty")
	}
	trimmed := strings.TrimPrefix(rawURL, "jdbc:")
	u, err := url.Parse(trimmed)
	if err != nil {
		return "", fmt.Errorf("parsing db_url: %w", err)
	}
	if u.Scheme == "" || u.Host == "" {
		return "", fmt.Errorf("db_url missing scheme or host")
	}
	u.User = url.UserPassword(dbUser, dbPass)
	q := u.Query()
	if q.Get("sslmode") == "" {
		q.Set("sslmode", "require")
	}
	if q.Get("connect_timeout") == "" {
		q.Set("connect_timeout", "5")
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}
```

- [ ] **Step 4: Run test to confirm it passes**

```bash
go test ./sink/ -v
```

Expected: all `TestNormalizeDSN` cases PASS, prior tests still PASS.

- [ ] **Step 5: Commit**

```bash
git add sink/dsn.go sink/sink_test.go
git commit -m "feat(sink): normalizeDSN strips jdbc prefix, defaults sslmode and connect_timeout"
```

---

## Task 4: Add `pgx/v5` dependency and the `Write()` implementation

**Files:**
- Modify: `sink/sink.go`
- Modify: `go.mod` / `go.sum`

There is no unit-test step for `Write` itself — it's an integration boundary (open conn, exec SQL). Task 5 adds the env-gated integration test that exercises it end-to-end. This task only adds the code and the dependency.

- [ ] **Step 1: Add the pgx/v5 dependency**

```bash
go get github.com/jackc/pgx/v5
go mod tidy
```

Expected: `go.mod` now lists `github.com/jackc/pgx/v5` under `require`; `go.sum` updated.

- [ ] **Step 2: Add SQL constants and the `redactErr` helper to `sink/sink.go`**

Append to `sink/sink.go`:

```go
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

// redactErr returns an error whose message has DBPass replaced with "***", so
// connection errors that echo the DSN (pgx sometimes does) cannot leak the
// password. apiKey redaction is handled upstream by the existing report/error
// pipeline; this redacts only the sink's own secret.
func (c Config) redactErr(err error) error {
	if err == nil {
		return nil
	}
	s := err.Error()
	if c.DBPass != "" {
		s = strings.ReplaceAll(s, c.DBPass, "***")
	}
	return fmt.Errorf("%s", s)
}
```

- [ ] **Step 3: Implement `Write`**

Append to `sink/sink.go`:

```go
import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)
```

Adjust the existing imports — merge the new ones into the single parenthesized block at the top of the file. The final import block should be exactly:

```go
import (
	"context"
	"fmt"
	"strings"

	"github.com/jackc/pgx/v5"
)
```

Then append the method:

```go
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
```

- [ ] **Step 4: Verify the package still builds**

```bash
go build ./sink/ && go vet ./sink/
```

Expected: no output (clean build + vet).

- [ ] **Step 5: Run existing unit tests to confirm no regression**

```bash
go test ./sink/ -v
```

Expected: `TestConfigured`, `TestValidate`, `TestNormalizeDSN` all PASS.

- [ ] **Step 6: Commit**

```bash
git add sink/sink.go go.mod go.sum
git commit -m "feat(sink): implement Write against pgx/v5"
```

---

## Task 5: Env-gated integration test for `Write()` against real Postgres

**Files:**
- Create: `sink/sink_integration_test.go`

This mirrors the existing `OBA_VALIDATOR_LIVE` pattern in `validator/integration_test.go`: skip when the env var is unset; require an existing Postgres when it is. The dev sets `OBA_VALIDATOR_DB_DSN` to something pgx can parse directly (no `jdbc:` prefix needed for this knob — it's only the validator's `db_url` input that's JDBC-shaped).

- [ ] **Step 1: Write the integration test**

Create `sink/sink_integration_test.go`:

```go
package sink

import (
	"context"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5"
)

// TestWriteIntegration exercises sink.Write end-to-end against a real Postgres.
// Gated by OBA_VALIDATOR_DB_DSN to keep `go test ./...` fully offline.
//
// Example:
//   OBA_VALIDATOR_DB_DSN="postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable" \
//     go test ./sink/ -run TestWriteIntegration -v
//
// The test:
//   1. Drops oba_validator_results so the run starts clean.
//   2. Calls Write twice (same correlation_id) and asserts ON CONFLICT DO NOTHING.
//   3. Reads the row back and asserts column contents.
//   4. Drops the table.
func TestWriteIntegration(t *testing.T) {
	dsn := os.Getenv("OBA_VALIDATOR_DB_DSN")
	if dsn == "" {
		t.Skip("OBA_VALIDATOR_DB_DSN not set; skipping integration test")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()

	// admin connection used only for setup/teardown and read-back.
	admin, err := pgx.Connect(ctx, dsn)
	if err != nil {
		t.Fatalf("admin connect: %v", err)
	}
	defer admin.Close(ctx)

	if _, err := admin.Exec(ctx, "DROP TABLE IF EXISTS oba_validator_results"); err != nil {
		t.Fatalf("teardown: %v", err)
	}
	t.Cleanup(func() {
		// Use a fresh context — the test ctx may already be cancelled.
		dctx, dcancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer dcancel()
		_, _ = admin.Exec(dctx, "DROP TABLE IF EXISTS oba_validator_results")
	})

	// Convert the test DSN into the JDBC shape Write expects, splitting userinfo
	// out into separate DBUser/DBPass fields so we exercise normalizeDSN.
	user, pass, jdbc := splitDSNForTest(t, dsn)

	cfg := Config{
		DBURL:         "jdbc:" + jdbc,
		DBUser:        user,
		DBPass:        pass,
		CorrelationID: "test-corr-id-001",
		ResultTable:   "oba_validator_results",
	}

	// First write — table does not exist; CREATE TABLE IF NOT EXISTS should create it.
	if err := cfg.Write(ctx, "completed", `{"summary":{"verdict":"PASS"}}`, ""); err != nil {
		t.Fatalf("first Write: %v", err)
	}

	// Second write — same correlation_id; ON CONFLICT DO NOTHING should silently no-op,
	// NOT overwrite the original row.
	if err := cfg.Write(ctx, "completed", `{"summary":{"verdict":"FAIL"}}`, "second attempt"); err != nil {
		t.Fatalf("second Write: %v", err)
	}

	var status, resultData, errMsg string
	err = admin.QueryRow(ctx, `SELECT status, result_data, error_message FROM oba_validator_results WHERE correlation_id = $1`,
		"test-corr-id-001").Scan(&status, &resultData, &errMsg)
	if err != nil {
		t.Fatalf("read back: %v", err)
	}
	if status != "completed" {
		t.Errorf("status = %q, want %q", status, "completed")
	}
	if !strings.Contains(resultData, `"verdict":"PASS"`) {
		t.Errorf("result_data lost on conflict: %q (second write should not overwrite)", resultData)
	}
	if errMsg != "" {
		t.Errorf("error_message = %q, want empty", errMsg)
	}
}

// splitDSNForTest takes a postgres:// URL and returns (user, pass, rest) where
// rest is the same URL with userinfo stripped — so the test can reassemble it
// in the JDBC shape that Write expects as input.
func splitDSNForTest(t *testing.T, dsn string) (user, pass, rest string) {
	t.Helper()
	u, err := url.Parse(dsn)
	if err != nil {
		t.Fatalf("parse OBA_VALIDATOR_DB_DSN: %v", err)
	}
	if u.User != nil {
		user = u.User.Username()
		pass, _ = u.User.Password()
	}
	u.User = nil
	return user, pass, u.String()
}
```

- [ ] **Step 2: Build to confirm it compiles**

```bash
go build ./sink/ && go vet ./sink/
```

Expected: clean.

- [ ] **Step 3: Run the test in skip mode**

```bash
go test ./sink/ -run TestWriteIntegration -v
```

Expected: `--- SKIP: TestWriteIntegration` (no DSN set).

- [ ] **Step 4: Optionally, run the test against a real Postgres**

If a local Postgres is available:

```bash
# Example using docker:
docker run --rm -d --name oba-pg-test -e POSTGRES_HOST_AUTH_METHOD=trust -p 5432:5432 postgres:16
sleep 3
OBA_VALIDATOR_DB_DSN="postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable" \
  go test ./sink/ -run TestWriteIntegration -v
docker stop oba-pg-test
```

Expected: `--- PASS: TestWriteIntegration`.

If you don't have Docker handy, leave this step skipped — the unit tests cover the pure logic; this test will run in CI/locally when the dev has Postgres available.

- [ ] **Step 5: Commit**

```bash
git add sink/sink_integration_test.go
git commit -m "test(sink): env-gated integration test for Write"
```

---

## Task 6: Wire the sink fields into `config.Config`

**Files:**
- Modify: `config/config.go`
- Modify: `config/config_test.go`

`config.Config` gains five flat snake_case JSON fields and a `SinkConfig()` helper that returns a `sink.Config`. `config.validate()` calls `c.SinkConfig().Validate()` so a partial sink config fails Load with a clear error (which surfaces through main.go's existing error pipeline).

- [ ] **Step 1: Write the failing tests**

Append to `config/config_test.go`:

```go
func TestLoadParsesSinkFields(t *testing.T) {
	raw := `{
	  "obaServerURL": "https://x",
	  "apiKey": "k",
	  "dataSources": [{"staticGtfsFeedURL":"u"}],
	  "db_url": "jdbc:postgresql://h:5432/d",
	  "db_user": "u",
	  "db_pass": "p",
	  "correlation_id": "abc-123",
	  "result_table": "oba_validator_results"
	}`
	cfg, err := Load(raw)
	if err != nil {
		t.Fatal(err)
	}
	sc := cfg.SinkConfig()
	if sc.DBURL != "jdbc:postgresql://h:5432/d" || sc.DBUser != "u" || sc.DBPass != "p" {
		t.Errorf("sink fields not parsed: %+v", sc)
	}
	if sc.CorrelationID != "abc-123" || sc.ResultTable != "oba_validator_results" {
		t.Errorf("correlation/table not parsed: %+v", sc)
	}
	if !sc.Configured() {
		t.Errorf("SinkConfig should be Configured()")
	}
}

func TestLoadWithoutSinkFieldsLeavesItDisabled(t *testing.T) {
	cfg, err := Load(sampleJSON)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SinkConfig().Configured() {
		t.Errorf("SinkConfig should be disabled when fields are absent")
	}
}

func TestLoadRejectsPartialSinkConfig(t *testing.T) {
	raw := `{
	  "obaServerURL": "https://x",
	  "apiKey": "k",
	  "dataSources": [{"staticGtfsFeedURL":"u"}],
	  "db_url": "jdbc:postgresql://h/d",
	  "db_user": "u"
	}`
	_, err := Load(raw)
	if err == nil {
		t.Fatal("want partial-sink error, got nil")
	}
	if !strings.Contains(err.Error(), "db_pass") {
		t.Errorf("error %q should mention the first missing field (db_pass)", err)
	}
}

func TestLoadRejectsUnknownResultTable(t *testing.T) {
	raw := `{
	  "obaServerURL": "https://x",
	  "apiKey": "k",
	  "dataSources": [{"staticGtfsFeedURL":"u"}],
	  "db_url": "jdbc:postgresql://h/d",
	  "db_user": "u",
	  "db_pass": "p",
	  "correlation_id": "abc",
	  "result_table": "evil"
	}`
	_, err := Load(raw)
	if err == nil || !strings.Contains(err.Error(), "unsupported result_table") {
		t.Errorf("want allow-list error, got %v", err)
	}
}
```

Also add a `"strings"` import to `config/config_test.go` (currently it only imports `os`, `path/filepath`, `testing`):

```go
import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./config/ -v
```

Expected: build errors for `cfg.SinkConfig` (undefined), and failures for the partial-sink / unknown-table assertions.

- [ ] **Step 3: Add the five fields and the `SinkConfig()` helper**

Modify `config/config.go`. First, add the sink import:

```go
import (
	"encoding/json"
	"fmt"
	"os"
	"strings"

	"github.com/onebusaway/oba-validator/sink"
)
```

Then add the five new fields to the `Config` struct (place them after `CacheDir`, before the `json:"-"` runtime-only fields):

```go
type Config struct {
	OBAServerURL       string       `json:"obaServerURL"`
	APIKey             string       `json:"apiKey"`
	DataSources        []DataSource `json:"dataSources"`
	SampleSize         int          `json:"sampleSize"`
	RTFreshnessSeconds int          `json:"rtFreshnessSeconds"`
	LocationSpan       float64      `json:"locationSpan"`
	MaxConcurrency     int          `json:"maxConcurrency"`
	TimeoutSeconds     int          `json:"timeoutSeconds"`
	CacheDir           string       `json:"cacheDir"`

	// Result sink — optional. Activated when DBURL is non-blank; all five must
	// then be present together (see sink.Config.Validate). These are invocation
	// inputs from obacloud's ServerValidationJob, not user-facing config; do
	// not surface them in --help or error messages.
	DBURL         string `json:"db_url,omitempty"`
	DBUser        string `json:"db_user,omitempty"`
	DBPass        string `json:"db_pass,omitempty"`
	CorrelationID string `json:"correlation_id,omitempty"`
	ResultTable   string `json:"result_table,omitempty"`

	NoCache bool `json:"-"`
	Refresh bool `json:"-"`
}
```

Then add the helper method and the validation hook. Place `SinkConfig` after `applyDefaults` and update `validate`:

```go
// SinkConfig assembles the optional result-sink configuration from the five
// flat invocation-input fields. The returned Config is value-copied, so
// downstream Write callers can hold it without aliasing config state.
func (c Config) SinkConfig() sink.Config {
	return sink.Config{
		DBURL:         c.DBURL,
		DBUser:        c.DBUser,
		DBPass:        c.DBPass,
		CorrelationID: c.CorrelationID,
		ResultTable:   c.ResultTable,
	}
}

func (c Config) validate() error {
	if c.OBAServerURL == "" {
		return fmt.Errorf("obaServerURL is required")
	}
	if c.APIKey == "" {
		return fmt.Errorf("apiKey is required (set in config or ONEBUSAWAY_API_KEY)")
	}
	if len(c.DataSources) == 0 {
		return fmt.Errorf("at least one dataSource is required")
	}
	if err := c.SinkConfig().Validate(); err != nil {
		return err
	}
	return nil
}
```

(Replace the existing `validate` body in full — the only change is the new `if err := c.SinkConfig().Validate()` block before `return nil`.)

- [ ] **Step 4: Run tests to confirm they pass**

```bash
go test ./config/ -v
```

Expected: all existing tests still PASS, plus four new tests PASS.

- [ ] **Step 5: Confirm no other package broke**

```bash
go build ./... && go vet ./...
```

Expected: clean.

- [ ] **Step 6: Commit**

```bash
git add config/config.go config/config_test.go
git commit -m "feat(config): parse and validate optional result-sink fields"
```

---

## Task 7: Expose marshaled-bytes helpers in the `report` package

**Files:**
- Modify: `report/report.go`

`main.go` needs to write the *same* JSON bytes to stdout and to the sink. Refactor `WriteJSON` / `WriteErrorJSON` so callers can get the bytes once and write them twice. The public API stays compatible.

- [ ] **Step 1: Refactor `report/report.go`**

Replace the existing body of `report/report.go` with:

```go
// Package report renders a validator.Report as JSON or human-readable text.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/onebusaway/oba-validator/config"
	"github.com/onebusaway/oba-validator/validator"
)

// RenderJSON returns the indented JSON bytes for a successful run. Callers
// that need to send the same payload to multiple sinks (stdout, DB, log)
// should call RenderJSON once and write the returned slice repeatedly, rather
// than calling WriteJSON twice and risking inconsistent encodings.
func RenderJSON(rep validator.Report, cfg config.Config) ([]byte, error) {
	return marshalIndented(BuildDocument(rep, cfg, time.Now().UTC()))
}

// RenderErrorJSON returns the indented JSON bytes for the errorDocument
// variant, with apiKey redacted from msg.
func RenderErrorJSON(msg, apiKey string) ([]byte, error) {
	return marshalIndented(ErrorDocument{SchemaVersion: SchemaVersion, Error: redactString(msg, apiKey)})
}

// WriteJSON writes the report as an indented, UI-oriented JSON Document. The
// document is marshalled fully before writing so a mid-stream write failure
// can't leave partial, unparseable JSON on the consumer's stream.
func WriteJSON(w io.Writer, rep validator.Report, cfg config.Config) error {
	b, err := RenderJSON(rep, cfg)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// WriteErrorJSON writes an indented ErrorDocument to w, redacting apiKey from msg.
func WriteErrorJSON(w io.Writer, msg, apiKey string) error {
	b, err := RenderErrorJSON(msg, apiKey)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// marshalIndented returns the JSON encoding of v as an indented byte slice
// terminated by a trailing newline so the caller can write it directly.
func marshalIndented(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

// WriteText writes a human-readable, grouped report with a summary line.
func WriteText(w io.Writer, rep validator.Report) error {
	var fails, warns int
	for _, r := range rep.Results {
		group := r.Source
		if group == "" {
			group = "server"
		}
		if _, err := fmt.Fprintf(w, "%s [%s] %s — %s\n", r.Status.Glyph(), group, r.Check, r.Message); err != nil {
			return err
		}
		switch r.Status {
		case validator.Fail:
			fails++
		case validator.Warn:
			warns++
		}
	}
	verdict := "PASS"
	if rep.Worst() == validator.Fail {
		verdict = "FAIL"
	}
	_, err := fmt.Fprintf(w, "\n%s (%d checks, %d failed, %d warnings)\n", verdict, len(rep.Results), fails, warns)
	return err
}
```

- [ ] **Step 2: Run the existing report tests to confirm no regression**

```bash
go test ./report/ -v
```

Expected: every existing test still PASS (the public API is unchanged in behavior; only the internals were factored).

- [ ] **Step 3: Commit**

```bash
git add report/report.go
git commit -m "refactor(report): expose RenderJSON / RenderErrorJSON bytes helpers"
```

---

## Task 8: Wire `sink.Write` into `cmd/oba-validator/main.go`

**Files:**
- Modify: `cmd/oba-validator/main.go`
- Modify: `cmd/oba-validator/main_test.go`

Wire the sink so that:

1. **Success path** (validator produced a report): print stdout, then if sink configured, write `status="completed"`, `result_data=<report JSON bytes>`, `error_message=""`.
2. **Validator-error path** (validator.Run returned err): print errorDocument, then if sink configured, write `status="error"`, `result_data=""`, `error_message=<redacted err>`.
3. **Config-load error path** (config.Load returned err, or `validate()` rejected partial sink config): we don't have a parsed sink config so no DB write is attempted — print errorDocument, exit 2.

A DB-write failure is logged to stderr (with `dbPass` already redacted by `sink.Write`) and never alters the validator's exit code.

To make this unit-testable without a real DB, introduce a package-level `sinkWrite` var so tests can swap it for a recorder.

- [ ] **Step 1: Write the failing tests**

Append to `cmd/oba-validator/main_test.go`:

```go
func TestRunInvokesSinkOnCompleted(t *testing.T) {
	t.Setenv("ONEBUSAWAY_API_KEY", "")

	obaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "current-time"):
			w.Write([]byte(`{"data":{"entry":{"time":1716000000000}}}`))
		case strings.Contains(r.URL.Path, "agencies-with-coverage"):
			w.Write([]byte(`{"data":{"list":[],"references":{"agencies":[]}}}`))
		default:
			w.Write([]byte(`{"data":{"list":[],"entry":{"arrivalsAndDepartures":[]}}}`))
		}
	}))
	defer obaSrv.Close()
	feedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte{})
	}))
	defer feedSrv.Close()

	type captured struct {
		status, data, errMsg string
		corrID, table        string
	}
	var got captured
	prev := sinkWrite
	sinkWrite = func(ctx context.Context, c sink.Config, status, data, errMsg string) error {
		got = captured{status: status, data: data, errMsg: errMsg, corrID: c.CorrelationID, table: c.ResultTable}
		return nil
	}
	defer func() { sinkWrite = prev }()

	cfg := `{
	  "obaServerURL":"` + obaSrv.URL + `",
	  "apiKey":"k",
	  "dataSources":[{"staticGtfsFeedURL":"` + feedSrv.URL + `/gtfs.zip"}],
	  "db_url":"jdbc:postgresql://h/d",
	  "db_user":"u",
	  "db_pass":"p",
	  "correlation_id":"abc-123",
	  "result_table":"oba_validator_results"
	}`
	var stdout, stderr bytes.Buffer
	run([]string{"oba-validator", "--json", "--no-cache", cfg}, &stdout, &stderr)

	if got.status != "completed" {
		t.Errorf("sink status = %q, want %q", got.status, "completed")
	}
	if got.corrID != "abc-123" || got.table != "oba_validator_results" {
		t.Errorf("sink config not threaded: corr=%q table=%q", got.corrID, got.table)
	}
	if got.errMsg != "" {
		t.Errorf("error_message should be empty on completed: %q", got.errMsg)
	}
	if !strings.Contains(got.data, `"schemaVersion"`) {
		t.Errorf("result_data should be the report JSON, got %q", got.data)
	}
	// stdout must still carry the full report — sink is purely additive.
	if !strings.Contains(stdout.String(), `"schemaVersion"`) {
		t.Errorf("stdout missing report JSON: %s", stdout.String())
	}
}

func TestRunSkipsSinkWhenNotConfigured(t *testing.T) {
	t.Setenv("ONEBUSAWAY_API_KEY", "")
	called := false
	prev := sinkWrite
	sinkWrite = func(ctx context.Context, c sink.Config, status, data, errMsg string) error {
		called = true
		return nil
	}
	defer func() { sinkWrite = prev }()

	obaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "current-time"):
			w.Write([]byte(`{"data":{"entry":{"time":1716000000000}}}`))
		case strings.Contains(r.URL.Path, "agencies-with-coverage"):
			w.Write([]byte(`{"data":{"list":[],"references":{"agencies":[]}}}`))
		default:
			w.Write([]byte(`{"data":{"list":[],"entry":{"arrivalsAndDepartures":[]}}}`))
		}
	}))
	defer obaSrv.Close()
	feedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte{}) }))
	defer feedSrv.Close()

	cfg := `{"obaServerURL":"` + obaSrv.URL + `","apiKey":"k","dataSources":[{"staticGtfsFeedURL":"` + feedSrv.URL + `/gtfs.zip"}]}`
	var stdout, stderr bytes.Buffer
	run([]string{"oba-validator", "--json", "--no-cache", cfg}, &stdout, &stderr)

	if called {
		t.Errorf("sinkWrite must not be called when fields are absent")
	}
}

func TestRunSinkErrorDoesNotAlterExitCode(t *testing.T) {
	t.Setenv("ONEBUSAWAY_API_KEY", "")

	obaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(r.URL.Path, "current-time"):
			w.Write([]byte(`{"data":{"entry":{"time":1716000000000}}}`))
		case strings.Contains(r.URL.Path, "agencies-with-coverage"):
			w.Write([]byte(`{"data":{"list":[],"references":{"agencies":[]}}}`))
		default:
			w.Write([]byte(`{"data":{"list":[],"entry":{"arrivalsAndDepartures":[]}}}`))
		}
	}))
	defer obaSrv.Close()
	feedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte{}) }))
	defer feedSrv.Close()

	prev := sinkWrite
	sinkWrite = func(ctx context.Context, c sink.Config, status, data, errMsg string) error {
		return fmt.Errorf("simulated db failure")
	}
	defer func() { sinkWrite = prev }()

	cfg := `{
	  "obaServerURL":"` + obaSrv.URL + `",
	  "apiKey":"k",
	  "dataSources":[{"staticGtfsFeedURL":"` + feedSrv.URL + `/gtfs.zip"}],
	  "db_url":"jdbc:postgresql://h/d",
	  "db_user":"u",
	  "db_pass":"p",
	  "correlation_id":"abc",
	  "result_table":"oba_validator_results"
	}`
	var stdout, stderr bytes.Buffer
	code := run([]string{"oba-validator", "--json", "--no-cache", cfg}, &stdout, &stderr)

	// Sink failure must not change the validator exit code (0 or 1, never 2).
	if code == 2 {
		t.Errorf("sink failure should not produce exit code 2, got %d", code)
	}
	if !strings.Contains(stderr.String(), "result sink write failed") {
		t.Errorf("stderr should log the sink failure: %s", stderr.String())
	}
}
```

Also add the new imports `context`, `fmt`, and the local `sink` import. The full import block at the top of `cmd/oba-validator/main_test.go` should be:

```go
import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebusaway/oba-validator/config"
	"github.com/onebusaway/oba-validator/sink"
)
```

- [ ] **Step 2: Run tests to confirm they fail**

```bash
go test ./cmd/oba-validator/ -v
```

Expected: build error (`sinkWrite` undefined, `sink` package unused-but-imported).

- [ ] **Step 3: Modify `cmd/oba-validator/main.go`**

Add imports and the `sinkWrite` package-level var. Replace the existing import block with:

```go
import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"regexp"
	"strings"

	"github.com/onebusaway/oba-validator/config"
	"github.com/onebusaway/oba-validator/report"
	"github.com/onebusaway/oba-validator/sink"
	"github.com/onebusaway/oba-validator/validator"
)
```

Add a second regex below the existing `apiKeyInJSON` so the same defense-in-depth sniff applies to db_pass:

```go
// dbPassInJSON matches a "db_pass" field in a (possibly malformed) JSON argument
// so its value can be scrubbed when config.Load echoes the raw input back to
// the user (see redactionKey's rationale for apiKey).
var dbPassInJSON = regexp.MustCompile(`"db_pass"\s*:\s*"((?:\\.|[^"\\])*)"`)
```

Replace the existing `redactionKey` function with `redactionSecrets`, which returns every secret the error pipeline should scrub:

```go
// redactionSecrets returns every secret value that must be removed from an
// error string. Inline credentials sniffed straight from the raw argument win
// over environment fallbacks because config.Load can fail before parsing the
// JSON (an os.ReadFile error wraps the input as a file path) and echo the raw
// blob — including any apiKey or db_pass inside it.
func redactionSecrets(arg string) []string {
	var out []string
	if m := apiKeyInJSON.FindStringSubmatch(arg); m != nil && m[1] != "" {
		out = append(out, m[1])
	} else if env := os.Getenv("ONEBUSAWAY_API_KEY"); env != "" {
		out = append(out, env)
	}
	if m := dbPassInJSON.FindStringSubmatch(arg); m != nil && m[1] != "" {
		out = append(out, m[1])
	}
	return out
}

// scrub replaces every non-empty secret in s with "***". Empty secrets are
// no-ops so callers don't need to filter before calling.
func scrub(s string, secrets []string) string {
	for _, sec := range secrets {
		if sec != "" {
			s = strings.ReplaceAll(s, sec, "***")
		}
	}
	return s
}
```

Then add the `sinkWrite` package-level var below those:

```go
// sinkWrite is the function used to write the run's result row to the optional
// Postgres sink. It is a package-level var so tests can replace it with a
// recorder, avoiding a real DB dependency in unit tests. Production callers
// use the default (sink.Config.Write).
var sinkWrite = func(ctx context.Context, c sink.Config, status, data, errMsg string) error {
	return c.Write(ctx, status, data, errMsg)
}
```

Then replace the existing `run()` function in `cmd/oba-validator/main.go` with the version below. The changes from the current code are:

1. Capture report bytes via `report.RenderJSON` / `report.RenderErrorJSON` so the same bytes go to stdout AND to the sink.
2. After printing stdout, call `sinkWrite` if `cfg.SinkConfig().Configured()`.
3. On the validator.Run error path, also call `sinkWrite` with `status="error"`.

```go
func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("oba-validator", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o overrides
	fs.BoolVar(&o.jsonOut, "json", false, "emit JSON instead of text")
	fs.IntVar(&o.sampleSize, "sample-size", 0, "vehicles/trip-updates to sample per source")
	fs.IntVar(&o.freshness, "freshness", 0, "max realtime feed age in seconds")
	fs.IntVar(&o.timeout, "timeout", 0, "per-request timeout in seconds")
	fs.StringVar(&o.cacheDir, "cache-dir", "", "static GTFS cache directory")
	fs.BoolVar(&o.noCache, "no-cache", false, "bypass the static GTFS cache")
	fs.BoolVar(&o.refresh, "refresh", false, "force re-download of static GTFS")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: oba-validator [flags] <config.json | raw-json>\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}

	cfg, err := config.Load(fs.Arg(0))
	if err != nil {
		secrets := redactionSecrets(fs.Arg(0))
		msg := scrub(err.Error(), secrets)
		if o.jsonOut {
			// WriteErrorJSON does an extra apiKey scrub of its own; passing
			// the already-scrubbed msg through is idempotent.
			if werr := report.WriteErrorJSON(stdout, msg, ""); werr != nil {
				fmt.Fprintln(stderr, "output error:", werr)
			}
		} else {
			fmt.Fprintln(stderr, "config error:", msg)
		}
		// No sink write here: the sink config could not be parsed, so there's
		// no correlation_id to key the row by. The caller's polling timeout
		// is the safety net (see spec §Deployment).
		return 2
	}
	applyOverrides(&cfg, o)

	ctx := context.Background()
	rep, err := validator.Run(ctx, cfg)
	if err != nil {
		errMsg := scrub(err.Error(), []string{cfg.APIKey, cfg.DBPass})
		if o.jsonOut {
			if werr := report.WriteErrorJSON(stdout, errMsg, ""); werr != nil {
				fmt.Fprintln(stderr, "output error:", werr)
			}
		} else {
			fmt.Fprintln(stderr, "run error:", errMsg)
		}
		// Validator-error path: we DO have a parsed sink config (config.Load
		// succeeded). Write status="error" so the caller learns the run failed
		// rather than timing out.
		if sc := cfg.SinkConfig(); sc.Configured() {
			if werr := sinkWrite(ctx, sc, "error", "", errMsg); werr != nil {
				fmt.Fprintln(stderr, "result sink write failed:", werr)
			}
		}
		return 2
	}

	// Success path: render once, write twice (stdout + optional sink).
	var reportBytes []byte
	if o.jsonOut {
		reportBytes, err = report.RenderJSON(rep, cfg)
		if err != nil {
			fmt.Fprintln(stderr, "output error:", err)
			return 2
		}
		if _, werr := stdout.Write(reportBytes); werr != nil {
			fmt.Fprintln(stderr, "output error:", werr)
			return 2
		}
	} else {
		if werr := report.WriteText(stdout, rep); werr != nil {
			fmt.Fprintln(stderr, "output error:", werr)
			return 2
		}
		// Text path still needs JSON bytes for the sink (the contract is
		// fixed: result_data is the JSON report). Render after stdout so a
		// rendering failure here can't suppress the text output the user sees.
		if sc := cfg.SinkConfig(); sc.Configured() {
			reportBytes, err = report.RenderJSON(rep, cfg)
			if err != nil {
				fmt.Fprintln(stderr, "result sink: render JSON failed:", err)
			}
		}
	}

	if sc := cfg.SinkConfig(); sc.Configured() && reportBytes != nil {
		if werr := sinkWrite(ctx, sc, "completed", string(reportBytes), ""); werr != nil {
			fmt.Fprintln(stderr, "result sink write failed:", werr)
		}
	}
	return rep.ExitCode()
}
```

- [ ] **Step 4: Run all tests**

```bash
go test ./... -v
```

Expected: all pre-existing tests still PASS; the three new `TestRunInvokesSinkOnCompleted`, `TestRunSkipsSinkWhenNotConfigured`, `TestRunSinkErrorDoesNotAlterExitCode` PASS.

- [ ] **Step 5: Run vet**

```bash
go vet ./...
```

Expected: no output.

- [ ] **Step 6: Commit**

```bash
git add cmd/oba-validator/main.go cmd/oba-validator/main_test.go
git commit -m "feat(cli): write result row to sink on completed and error paths"
```

---

## Task 9: Final verification + docs

**Files:**
- Modify: `CLAUDE.md`

- [ ] **Step 1: Smoke-test the binary builds**

```bash
make build
```

Expected: `bin/oba-validator` is rebuilt; no errors.

- [ ] **Step 2: Run the full test suite**

```bash
make test
```

Expected: all packages PASS, including `sink` (integration test SKIPs without DSN).

- [ ] **Step 3: Run vet and fmt**

```bash
make vet
make fmt
git diff --stat
```

Expected: vet clean; `make fmt` either no diff (good) or a small whitespace fix (commit it under `style: gofmt` if needed).

- [ ] **Step 4: Update `CLAUDE.md` to mention the sink**

Open `CLAUDE.md` and find the "What this is" section. Append the following sentence to the end of that paragraph (after "is this OBA server telling the truth about what the feeds say?"):

```
An optional Postgres result sink (`sink/`) writes one row per run keyed by `correlation_id` when the invocation payload includes `db_url` and its siblings — see `docs/superpowers/specs/2026-05-25-result-sink-design.md`.
```

Then add a new bullet under the "Architecture" section's `prepare → checks → report` flow, after the `report` bullet:

```
5. **`sink`** — optional Postgres writer. When the invocation payload includes `db_url`/`db_user`/`db_pass`/`correlation_id`/`result_table`, `main.go` calls `sink.Write` after stdout is written. `status` is `"completed"` for both PASS and FAIL verdicts (the verdict lives inside `result_data` at `summary.verdict`); `"error"` is reserved for the `errorDocument` variant. A sink write failure is logged to stderr and never changes the validator's exit code.
```

- [ ] **Step 5: Commit the docs**

```bash
git add CLAUDE.md
git commit -m "docs: mention optional result sink in CLAUDE.md"
```

- [ ] **Step 6: Confirm the diff stack matches the spec contract**

```bash
git log --oneline main..HEAD
```

You should see roughly nine commits, in order: scaffold → Validate → normalizeDSN → Write impl → integration test → config wiring → report refactor → main wiring → docs.

- [ ] **Step 7: Run the full check one more time**

```bash
make build && make test && make vet
```

Expected: all green.

---

## Out of scope (explicitly NOT in this plan)

- Modifying `entrypoint.sh`, `Dockerfile`, or `render.yaml`. The base64-decoded JSON channel already carries the new fields; the image rebuild picks up the new package; the Render service id is unchanged.
- Modifying `schema/oba-validator-report.schema.json`. The sink fields are invocation inputs, not part of the report contract.
- Connection pooling. The validator does one write per run; a single `pgx.Conn` is correct and simplest (see spec §Open questions).
- Upsert semantics (`ON CONFLICT DO UPDATE`). Spec recommends `DO NOTHING`; obacloud has not requested otherwise.
- Cross-repo coordination. The contract here matches obacloud PR #747; any contract change must sync with that PR before merging.
