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

// RenderJSON returns the indented JSON bytes for a successful run. Callers
// that need to send the same payload to multiple sinks (stdout, DB, log)
// should call RenderJSON once and write the returned slice repeatedly, rather
// than calling WriteJSON twice and risking inconsistent encodings.
func RenderJSON(rep validator.Report, cfg config.Config) ([]byte, error) {
	return marshalIndented(BuildDocument(rep, cfg, time.Now().UTC()))
}

// RenderErrorJSON returns the indented JSON bytes for the errorDocument
// variant, with apiKey redacted from msg.
func RenderErrorJSON(msg, apiKey string) ([]byte, error) {
	return marshalIndented(ErrorDocument{SchemaVersion: SchemaVersion, Error: redactString(msg, apiKey)})
}

// WriteJSON writes the report as an indented, UI-oriented JSON Document. The
// document is marshalled fully before writing so a mid-stream write failure
// can't leave partial, unparseable JSON on the consumer's stream.
func WriteJSON(w io.Writer, rep validator.Report, cfg config.Config) error {
	b, err := RenderJSON(rep, cfg)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// WriteErrorJSON writes an indented ErrorDocument to w, redacting apiKey from msg.
func WriteErrorJSON(w io.Writer, msg, apiKey string) error {
	b, err := RenderErrorJSON(msg, apiKey)
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// marshalIndented returns the JSON encoding of v as an indented byte slice
// terminated by a trailing newline so the caller can write it directly.
func marshalIndented(v any) ([]byte, error) {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
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
