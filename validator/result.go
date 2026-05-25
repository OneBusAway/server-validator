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

// ExitCode is 1 if any result failed, else 0.
func (r Report) ExitCode() int {
	if r.Worst() == Fail {
		return 1
	}
	return 0
}
