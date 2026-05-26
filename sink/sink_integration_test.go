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
//
//	OBA_VALIDATOR_DB_DSN="postgres://postgres@127.0.0.1:5432/postgres?sslmode=disable" \
//	  go test ./sink/ -run TestWriteIntegration -v
//
// The test:
//  1. Drops oba_validator_results so the run starts clean.
//  2. Calls Write twice (same correlation_id) and asserts ON CONFLICT DO NOTHING.
//  3. Reads the row back and asserts column contents.
//  4. Drops the table.
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
