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
	"github.com/onebusaway/oba-validator/validator"
)

// apiKeyInJSON matches an "apiKey" string field in a (possibly malformed) JSON
// argument so its value can be scrubbed from error output.
var apiKeyInJSON = regexp.MustCompile(`"apiKey"\s*:\s*"((?:\\.|[^"\\])*)"`)

// redactionKey returns the apiKey to scrub from a config-load error. config.Load
// can fail before it parses the key — notably when a raw-JSON argument that does
// not start with '{' is misread as a file path, whose os.ReadFile error echoes
// the raw input (and thus an inline apiKey) verbatim. The parsed cfg is empty in
// that case, so prefer a key sniffed straight from the argument, falling back to
// the environment.
func redactionKey(arg string) string {
	if m := apiKeyInJSON.FindStringSubmatch(arg); m != nil && m[1] != "" {
		return m[1]
	}
	return os.Getenv("ONEBUSAWAY_API_KEY")
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
		key := redactionKey(fs.Arg(0))
		if o.jsonOut {
			if werr := report.WriteErrorJSON(stdout, err.Error(), key); werr != nil {
				fmt.Fprintln(stderr, "output error:", werr)
			}
		} else {
			msg := err.Error()
			if key != "" {
				msg = strings.ReplaceAll(msg, key, "***")
			}
			fmt.Fprintln(stderr, "config error:", msg)
		}
		return 2
	}
	applyOverrides(&cfg, o)

	rep, err := validator.Run(context.Background(), cfg)
	if err != nil {
		if o.jsonOut {
			if werr := report.WriteErrorJSON(stdout, err.Error(), cfg.APIKey); werr != nil {
				fmt.Fprintln(stderr, "output error:", werr)
			}
		} else {
			fmt.Fprintln(stderr, "run error:", err)
		}
		return 2
	}

	var werr error
	if o.jsonOut {
		werr = report.WriteJSON(stdout, rep, cfg)
	} else {
		werr = report.WriteText(stdout, rep)
	}
	if werr != nil {
		fmt.Fprintln(stderr, "output error:", werr)
		return 2
	}
	return rep.ExitCode()
}

func main() {
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}
