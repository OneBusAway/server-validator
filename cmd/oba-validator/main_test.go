package main

import (
	"bytes"
	"testing"

	"github.com/onebusaway/oba-validator/config"
)

func TestApplyFlagOverrides(t *testing.T) {
	cfg := config.Config{SampleSize: 3, NoCache: false}
	applyOverrides(&cfg, overrides{sampleSize: 5, noCache: true, freshness: 60})
	if cfg.SampleSize != 5 || !cfg.NoCache || cfg.RTFreshnessSeconds != 60 {
		t.Errorf("overrides not applied: %+v", cfg)
	}
}

func TestUsageWhenNoArgs(t *testing.T) {
	var stderr bytes.Buffer
	code := run([]string{"oba-validator"}, &bytes.Buffer{}, &stderr)
	if code != 2 {
		t.Errorf("exit=%d want 2", code)
	}
	if stderr.Len() == 0 {
		t.Error("expected usage on stderr")
	}
}

func TestUsageWhenTooManyArgs(t *testing.T) {
	var stderr bytes.Buffer
	code := run([]string{"oba-validator", "a", "b"}, &bytes.Buffer{}, &stderr)
	if code != 2 {
		t.Errorf("exit=%d want 2", code)
	}
	if stderr.Len() == 0 {
		t.Error("expected usage on stderr")
	}
}
