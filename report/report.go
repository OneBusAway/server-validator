// Package report renders a validator.Report as JSON or human-readable text.
package report

import (
	"encoding/json"
	"fmt"
	"io"
	"time"

	"github.com/onebusaway/oba-validator/config"
	"github.com/onebusaway/oba-validator/validator"
)

// WriteJSON writes the report as an indented, UI-oriented JSON Document. The
// document is marshalled fully before writing so a mid-stream write failure
// can't leave partial, unparseable JSON on the consumer's stream.
func WriteJSON(w io.Writer, rep validator.Report, cfg config.Config) error {
	return writeIndentedJSON(w, BuildDocument(rep, cfg, time.Now().UTC()))
}

// WriteErrorJSON writes an indented ErrorDocument to w, redacting apiKey from msg.
func WriteErrorJSON(w io.Writer, msg, apiKey string) error {
	return writeIndentedJSON(w, ErrorDocument{SchemaVersion: SchemaVersion, Error: redactString(msg, apiKey)})
}

// writeIndentedJSON marshals v fully, then writes it in a single call so output
// is all-or-nothing rather than incrementally streamed.
func writeIndentedJSON(w io.Writer, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = w.Write(append(b, '\n'))
	return err
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
