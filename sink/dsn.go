package sink

import (
	"fmt"
	"net/url"
	"strings"
)

// normalizeDSN converts the JDBC-style URL obacloud sends (e.g.
// "jdbc:postgresql://host:5432/db") into a pgx-parseable DSN by:
//  1. stripping the "jdbc:" prefix if present (pgx rejects it),
//  2. injecting userinfo from dbUser/dbPass into the URL,
//  3. defaulting sslmode=require if the caller didn't set one,
//  4. defaulting connect_timeout=5 (seconds) if the caller didn't set one.
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
