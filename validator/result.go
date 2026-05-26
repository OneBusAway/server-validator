package validator

// Result is the outcome of one check (or one step of a check).
type Result struct {
	Check   string         `json:"check"`
	Source  string         `json:"source,omitempty"` // data source label; empty for server-level
	Status  Status         `json:"status"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// Report aggregates every Result from a validation run.
type Report struct {
	Results []Result `json:"results"`
}

// Worst returns the most severe status in the report (Fail > Warn > Pass).
// Skip never outranks Pass.
func (r Report) Worst() Status {
	worst := Pass
	for _, res := range r.Results {
		switch res.Status {
		case Fail:
			return Fail
		case Warn:
			if worst == Pass {
				worst = Warn
			}
		}
	}
	return worst
}

// ExitCode is always 0 once a report has been produced. A FAIL verdict is
// reported via Worst() and the JSON document's summary.verdict; the process
// exit code is reserved for "the validator could not run" (config error in
// main.go returns 2). This keeps the Render cron status green when the
// validator successfully evaluated the OBA server, even if checks failed —
// callers read the verdict from the result-sink row.
func (r Report) ExitCode() int {
	return 0
}
