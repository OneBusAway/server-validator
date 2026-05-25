package validator

// Status is the outcome of a single validation result.
type Status int

const (
	Pass Status = iota
	Warn
	Fail
	Skip
)

func (s Status) String() string {
	switch s {
	case Pass:
		return "PASS"
	case Warn:
		return "WARN"
	case Fail:
		return "FAIL"
	case Skip:
		return "SKIP"
	default:
		return "UNKNOWN"
	}
}

// Glyph returns the single-character marker used in terminal output.
func (s Status) Glyph() string {
	switch s {
	case Pass:
		return "✓"
	case Warn:
		return "⚠"
	case Fail:
		return "✗"
	case Skip:
		return "–"
	default:
		return "?"
	}
}

// MarshalJSON emits the status as its string name.
func (s Status) MarshalJSON() ([]byte, error) {
	return []byte(`"` + s.String() + `"`), nil
}
