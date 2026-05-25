package validator

import "testing"

func TestReportWorstAndExitCode(t *testing.T) {
	cases := []struct {
		name     string
		statuses []Status
		worst    Status
		exit     int
	}{
		{"all pass", []Status{Pass, Pass}, Pass, 0},
		{"warn only", []Status{Pass, Warn, Skip}, Warn, 0},
		{"any fail", []Status{Pass, Warn, Fail}, Fail, 1},
		{"empty", nil, Pass, 0},
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
			if got := r.ExitCode(); got != c.exit {
				t.Errorf("ExitCode()=%d want %d", got, c.exit)
			}
		})
	}
}
