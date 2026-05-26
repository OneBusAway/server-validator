package main

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

// apiKeyInJSON matches an "apiKey" string field in a (possibly malformed) JSON
// argument so its value can be scrubbed from error output.
var apiKeyInJSON = regexp.MustCompile(`"apiKey"\s*:\s*"((?:\\.|[^"\\])*)"`)

// dbPassInJSON matches a "db_pass" field in a (possibly malformed) JSON argument
// so its value can be scrubbed when config.Load echoes the raw input back to
// the user (see redactionKey's rationale for apiKey).
var dbPassInJSON = regexp.MustCompile(`"db_pass"\s*:\s*"((?:\\.|[^"\\])*)"`)

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

// sinkWriteFailedMsg is the stderr prefix used whenever a result-sink write
// returns an error. Centralized so all four call sites (validator-error path,
// JSON-render-failure fallback in both o.jsonOut branches, and the unified
// success-path write) stay in sync.
const sinkWriteFailedMsg = "result sink write failed:"

// renderJSON is the function used to render the report to JSON bytes. It is a
// package-level var so tests can replace it with a stub that returns an error,
// exercising the sink "error" fallback when rendering itself fails. Production
// callers use the default (report.RenderJSON).
var renderJSON = report.RenderJSON

// sinkWrite is the function used to write the run's result row to the optional
// Postgres sink. It is a package-level var so tests can replace it with a
// recorder, avoiding a real DB dependency in unit tests. Production callers
// use the default (sink.Config.Write).
var sinkWrite = func(ctx context.Context, c sink.Config, status, data, errMsg string) error {
	return c.Write(ctx, status, data, errMsg)
}

type overrides struct {
	jsonOut    bool
	sampleSize int
	freshness  int
	timeout    int
	cacheDir   string
	noCache    bool
	refresh    bool
}

func applyOverrides(cfg *config.Config, o overrides) {
	if o.sampleSize > 0 {
		cfg.SampleSize = o.sampleSize
	}
	if o.freshness > 0 {
		cfg.RTFreshnessSeconds = o.freshness
	}
	if o.timeout > 0 {
		cfg.TimeoutSeconds = o.timeout
	}
	if o.cacheDir != "" {
		cfg.CacheDir = o.cacheDir
	}
	if o.noCache {
		cfg.NoCache = true
	}
	if o.refresh {
		cfg.Refresh = true
	}
}

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
				fmt.Fprintln(stderr, sinkWriteFailedMsg, werr)
			}
		}
		return 2
	}

	// Success path: render once, write twice (stdout + optional sink).
	var reportBytes []byte
	if o.jsonOut {
		var renderErr error
		reportBytes, renderErr = renderJSON(rep, cfg)
		if renderErr != nil {
			fmt.Fprintln(stderr, "output error:", renderErr)
			// Render failed before stdout: fall back to a sink "error" row so the
			// caller doesn't poll until its 15-minute timeout. Stdout consumers
			// get nothing on this path, but Render logs will carry the stderr line.
			if sc := cfg.SinkConfig(); sc.Configured() {
				if werr := sinkWrite(ctx, sc, "error", "", "internal: render JSON failed: "+renderErr.Error()); werr != nil {
					fmt.Fprintln(stderr, sinkWriteFailedMsg, werr)
				}
			}
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
		// rendering failure here can't suppress the text output the user already
		// saw — but if it does fail, write a sink "error" row so the caller
		// doesn't poll until its 15-minute timeout.
		if sc := cfg.SinkConfig(); sc.Configured() {
			var renderErr error
			reportBytes, renderErr = renderJSON(rep, cfg)
			if renderErr != nil {
				fmt.Fprintln(stderr, "result sink: render JSON failed:", renderErr)
				if werr := sinkWrite(ctx, sc, "error", "", "internal: render JSON failed: "+renderErr.Error()); werr != nil {
					fmt.Fprintln(stderr, sinkWriteFailedMsg, werr)
				}
				return rep.ExitCode()
			}
		}
	}

	if sc := cfg.SinkConfig(); sc.Configured() && reportBytes != nil {
		if werr := sinkWrite(ctx, sc, "completed", string(reportBytes), ""); werr != nil {
			fmt.Fprintln(stderr, sinkWriteFailedMsg, werr)
		}
	}
	return rep.ExitCode()
}

func main() {
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}
