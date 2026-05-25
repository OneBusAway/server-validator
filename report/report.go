// Package report renders a validator.Report as JSON or human-readable text.
package report

import (
	"encoding/json"
	"fmt"
	"io"

	"github.com/onebusaway/oba-validator/validator"
)

// WriteJSON writes the report as indented JSON.
func WriteJSON(w io.Writer, rep validator.Report) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(rep)
}

// WriteText writes a human-readable, grouped report with a summary line.
func WriteText(w io.Writer, rep validator.Report) error {
	var fails, warns int
	for _, r := range rep.Results {
		group := r.Source
		if group == "" {
			group = "server"
		}
		if _, err := fmt.Fprintf(w, "%s [%s] %s — %s\n", r.Status.Glyph(), group, r.Check, r.Message); err != nil {
			return err
		}
		switch r.Status {
		case validator.Fail:
			fails++
		case validator.Warn:
			warns++
		}
	}
	verdict := "PASS"
	if rep.Worst() == validator.Fail {
		verdict = "FAIL"
	}
	_, err := fmt.Fprintf(w, "\n%s (%d checks, %d failed, %d warnings)\n", verdict, len(rep.Results), fails, warns)
	return err
}
