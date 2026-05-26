package validator

import "testing"

func TestReportWorst(t *testing.T) {
	cases := []struct {
		name     string
		statuses []Status
		worst    Status
	}{
		{"all pass", []Status{Pass, Pass}, Pass},
		{"warn only", []Status{Pass, Warn, Skip}, Warn},
		{"any fail", []Status{Pass, Warn, Fail}, Fail},
		{"empty", nil, Pass},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var r Report
			for _, s := range c.statuses {
				r.Results = append(r.Results, Result{Status: s})
			}
			if got := r.Worst(); got != c.worst {
				t.Errorf("Worst()=%v want %v", got, c.worst)
			}
		})
	}
}

// ExitCode is intentionally constant: a completed run always returns 0,
// including when checks failed. The FAIL verdict is conveyed via Worst() and
// the JSON summary.verdict, not the process exit code.
func TestReportExitCodeAlwaysZero(t *testing.T) {
	for _, s := range []Status{Pass, Warn, Skip, Fail} {
		r := Report{Results: []Result{{Status: s}}}
		if got := r.ExitCode(); got != 0 {
			t.Errorf("ExitCode() with %v result = %d, want 0", s, got)
		}
	}
}
