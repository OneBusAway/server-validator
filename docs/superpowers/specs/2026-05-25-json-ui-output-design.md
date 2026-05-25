# JSON-for-UI Output Mode — Design

**Date:** 2026-05-25
**Status:** Approved (design + both sign-off items); pending spec review

## Purpose

Add a JSON output mode whose shape is built for **UI visualization** on the
consumer side, and ship a **JSON Schema** file so consumers know exactly what to
expect. The output carries *all the same data* the text report does, but
restructured into metadata + summary + grouped results instead of a flat list.

The driving use case is the Render **one-off job** pattern (see
`api-key-service`): the job prints a single well-formed JSON object to **stdout**,
the caller reads it from the job output, and the exit code signals pass/fail.
There is no database recording in this project — stdout is the channel.

## Decisions (locked with the user)

1. **Replace the existing flat `--json`.** The new structured document becomes
   the one and only JSON output. The previous behavior (marshalling
   `validator.Report` directly as `{ "results": [...] }`) is removed.
2. **Grouped by section.** Top level is `meta` + `summary` + `groups`, where
   `groups` is the `server` group followed by one group per data source.
3. **Rich meta + input echo.** `meta` echoes `obaServerURL` and per-data-source
   labels + feed URLs + `agencyMapping`. **The `apiKey` is never echoed.**
4. **Error-as-JSON on stdout.** When `--json` is requested and config load or the
   run fails, a JSON error object is printed to stdout (not only stderr), exit 2.
5. **Schema-conformance test** with a test-only dependency
   (`github.com/santhosh-tekuri/jsonschema/v6`) asserts generated output
   validates against the committed schema, preventing schema rot.

## Architecture

Rendering is a presentation concern, so the transform lives in the **`report`**
package (which already imports `validator`; adding `config` introduces no import
cycle — `config` depends on neither).

- A **pure** builder `BuildDocument(rep validator.Report, cfg config.Config, now
  time.Time) Document` produces the view model. Purity (an injected `now`) keeps
  tests deterministic, consistent with the repo's determinism convention.
- `WriteJSON(w io.Writer, rep validator.Report, cfg config.Config) error` calls
  `BuildDocument` with `time.Now().UTC()` and encodes it indented.
- A small `ErrorDocument` + `WriteErrorJSON(w, msg, apiKey)` covers the error
  contract.

`WriteText` is unchanged. `main.go`'s `--json` branch switches to the new
`WriteJSON` signature and gains the error-JSON path.

### Flow

```
config.Load ──err──▶ (--json? WriteErrorJSON : text) ; exit 2
     │ ok
validator.Run ──err──▶ (--json? WriteErrorJSON : text) ; exit 2
     │ ok
   Report ──▶ (--json? WriteJSON=BuildDocument : WriteText) ; exit rep.ExitCode()
```

## Output shape (success)

```jsonc
{
  "schemaVersion": "1.0",
  "meta": {
    "generatedAt": "2026-05-25T17:04:00Z",          // RFC3339, UTC
    "obaServerURL": "https://api.pugetsound.onebusaway.org",
    "dataSources": [
      {
        "id": "dataSource[0]",                       // joins to groups[].id
        "index": 0,
        "staticGtfsFeedURL": "https://.../gtfs.zip",
        "vehiclePositionsURL": "https://.../vp.pb",
        "tripUpdatesURL": "https://.../tu.pb",       // omitted when not configured
        "serviceAlertsURL": "https://.../alerts.pb", // omitted when not configured
        "agencyMapping": { "KCM": "1" }              // omitted when empty
      }
    ]
  },
  "summary": {
    "verdict": "FAIL",                               // PASS | FAIL (mirrors Report.Worst())
    "exitCode": 1,                                   // mirrors Report.ExitCode()
    "total": 9,
    "counts": { "pass": 6, "warn": 1, "fail": 2, "skip": 0 }
  },
  "groups": [
    {
      "id": "server",
      "label": "Server",
      "counts": { "pass": 2, "warn": 0, "fail": 0, "skip": 0 },
      "results": [
        {
          "check": "basic-endpoints/current-time",
          "category": "basic-endpoints",
          "step": "current-time",
          "status": "PASS",
          "message": "OK"
        }
      ]
    },
    {
      "id": "dataSource[0]",
      "label": "Data source 0",
      "counts": { "pass": 4, "warn": 1, "fail": 2, "skip": 0 },
      "results": [
        {
          "check": "vehicle-positions-sampling/trip-for-vehicle",
          "category": "vehicle-positions-sampling",
          "step": "trip-for-vehicle",
          "status": "FAIL",
          "message": "...",
          "details": { "vehicleId": "1_1234" }       // omitted when nil
        }
      ]
    }
  ]
}
```

### Field rules

- **`status`** values stay uppercase (`PASS`/`WARN`/`FAIL`/`SKIP`) to match
  `Status.String()` and the prior JSON. **`counts`** keys are lowercase
  (`pass`/`warn`/`fail`/`skip`) and always present (zeros included).
- **`category` / `step`** are split from `check` on the **first** `/`. A check
  with no `/` (e.g. `agency-union`) yields `category = check`, `step` omitted.
  This gives the UI a stable grouping key without re-parsing the `check` string.
- **Grouping** is deterministic: `server` group first (all results with empty
  `Source`), then one group per data source in **config order**, keyed by the
  source label (`dataSource[N]`). A result whose `Source` matches no known data
  source is emitted in a trailing group keyed by that source (sorted) so no data
  is ever dropped.
- **`label`** is human-friendly: `"Server"`, `"Data source N"`.
- **`verdict`** is `FAIL` iff `Report.Worst() == Fail`, else `PASS` (matches the
  text report). `exitCode` mirrors `Report.ExitCode()` (0 or 1).

### Security

`apiKey` is never placed in the document. As defense in depth, every echoed URL
in `meta` is passed through a redactor that replaces the apiKey substring with
`REDACTED` if it ever appears (mirrors the existing `redact(err, key)` rule in
the validator). `Details` values pass through unchanged — checks are already
responsible for redacting them at the source.

## Output shape (error)

```json
{ "schemaVersion": "1.0", "error": "obaServerURL is required" }
```

Emitted to stdout when `--json` is set and either `config.Load` or
`validator.Run` returns an error; the process exits 2. The message is passed
through the apiKey redactor before printing.

## JSON Schema file

`schema/oba-validator-report.schema.json` — JSON Schema **draft 2020-12** with
`$id`, `title`, property `description`s, and `required` arrays. Top level is
`oneOf: [reportDocument, errorDocument]`, both requiring `schemaVersion`, so a
consumer can validate either variant against one file. `additionalProperties`
is `false` on the closed objects (`summary`, `counts`, result items) to catch
drift; `details` is left open (`additionalProperties: true`).

## View-model types (in `report`)

```go
type Document struct {
    SchemaVersion string  `json:"schemaVersion"`
    Meta          Meta    `json:"meta"`
    Summary       Summary `json:"summary"`
    Groups        []Group `json:"groups"`
}
type Meta struct {
    GeneratedAt  string       `json:"generatedAt"`
    OBAServerURL string       `json:"obaServerURL"`
    DataSources  []MetaSource `json:"dataSources"`
}
type MetaSource struct {
    ID                  string            `json:"id"`
    Index               int               `json:"index"`
    StaticGtfsFeedURL   string            `json:"staticGtfsFeedURL,omitempty"`
    VehiclePositionsURL string            `json:"vehiclePositionsURL,omitempty"`
    TripUpdatesURL      string            `json:"tripUpdatesURL,omitempty"`
    ServiceAlertsURL    string            `json:"serviceAlertsURL,omitempty"`
    AgencyMapping       map[string]string `json:"agencyMapping,omitempty"`
}
type Summary struct {
    Verdict  string `json:"verdict"`
    ExitCode int    `json:"exitCode"`
    Total    int    `json:"total"`
    Counts   Counts `json:"counts"`
}
type Counts struct {
    Pass int `json:"pass"`
    Warn int `json:"warn"`
    Fail int `json:"fail"`
    Skip int `json:"skip"`
}
type Group struct {
    ID      string `json:"id"`
    Label   string `json:"label"`
    Counts  Counts `json:"counts"`
    Results []Item `json:"results"`
}
type Item struct {
    Check    string         `json:"check"`
    Category string         `json:"category"`
    Step     string         `json:"step,omitempty"`
    Status   string         `json:"status"`
    Message  string         `json:"message"`
    Details  map[string]any `json:"details,omitempty"`
}
type ErrorDocument struct {
    SchemaVersion string `json:"schemaVersion"`
    Error         string `json:"error"`
}
```

## Testing (TDD red → green)

`report` package (no network, table/`httptest` style per repo convention):

- `BuildDocument`: server vs data-source grouping and ordering; `category`/`step`
  split (incl. no-slash case); per-group and summary `counts`; `verdict` and
  `exitCode`; `generatedAt` RFC3339 formatting via injected `now`; `meta` echo
  of URLs + `agencyMapping`; `omitempty` on unconfigured URLs.
- **apiKey never present:** assert the full marshalled output contains neither
  the apiKey nor a URL with the apiKey embedded (URL-redaction test).
- `WriteJSON` produces valid, indented JSON with the expected top-level keys.
- `WriteErrorJSON` produces `{schemaVersion, error}` with the message redacted.
- **Schema conformance:** marshal a representative success `Document` and an
  `ErrorDocument`, then validate both against
  `schema/oba-validator-report.schema.json` using `santhosh-tekuri/jsonschema/v6`.
  Also assert a deliberately-malformed document fails validation (guards that
  the schema is actually constraining).

`cmd` package (existing `httptest` harness style):

- `--json` against a stubbed run yields top-level `meta`/`summary`/`groups`.
- Config error under `--json` prints `{"error": ...}` to stdout and exits 2.

The existing flat-output `TestWriteJSON` in `report/report_test.go` is rewritten
to assert the new structure.

## Out of scope (YAGNI)

- No database / result-table recording (unlike `api-key-service`); stdout only.
- No new CLI flag — `--json` is repurposed, not added alongside.
- No per-check schema beyond `details: object` (details are check-specific and
  intentionally open).
