# OBA Validator

Validates that a OneBusAway server is functioning properly by cross-referencing
its REST API against the authoritative static GTFS and GTFS-realtime feeds
(vehicle positions, trip updates, service alerts).

## Usage

    oba-validator [flags] <config.json | raw-json-string>

Flags: `--json`, `--sample-size`, `--freshness`, `--timeout`, `--cache-dir`,
`--no-cache`, `--refresh`.

Exit codes: `0` = the validator produced a report (read `summary.verdict` for
PASS/FAIL); `2` = the validator could not run (config/usage error). The process
exit code is intentionally not used to convey a FAIL verdict — that's what the
JSON report's `summary.verdict` and the result-sink row are for.

## Config

```json
{
    "obaServerURL": "https://api.pugetsound.onebusaway.org",
    "apiKey": "org.onebusaway.iphone",
    "dataSources": [
        {
            "agencyMapping": { "KCM": "1" },
            "staticGtfsFeedURL": "https://metro.kingcounty.gov/GTFS/google_transit.zip",
            "vehiclePositionsURL": "https://s3.amazonaws.com/kcm-alerts-realtime-prod/vehiclepositions.pb",
            "tripUpdatesURL": "https://s3.amazonaws.com/kcm-alerts-realtime-prod/tripupdates.pb",
            "serviceAlertsURL": "https://s3.amazonaws.com/kcm-alerts-realtime-prod/alerts.pb"
        }
    ]
}
```

`apiKey` may instead be supplied via the `ONEBUSAWAY_API_KEY` environment
variable. `agencyMapping` (optional, per data source) maps each GTFS `agency_id`
to the `agencyId` the OBA server exposes; unmapped agencies default to identity.

Five optional top-level fields (`db_url`, `db_user`, `db_pass`, `correlation_id`,
`result_table`) activate the result sink described under [Reading the result from
a database](#reading-the-result-from-a-database) below; when absent the validator
behaves exactly as today.

## Library

```go
cfg, _ := config.Load("config.json")  // or a raw JSON string
rep, _ := validator.Run(ctx, cfg)
report.WriteText(os.Stdout, rep)       // or report.WriteJSON(os.Stdout, rep, cfg)
os.Exit(rep.ExitCode())
```

## JSON output

`--json` emits a single structured document to stdout, designed for building a UI
visualization. It contains `meta` (run inputs — never the apiKey), `summary`
(verdict + status counts), and `groups` (a `server` group plus one per data
source, each with its results). On failure before a report is produced, a
an object like `{ "schemaVersion": "1.0", "error": "..." }` is emitted to stdout and the process exits 2.

The full contract is published as a JSON Schema (draft 2020-12) at
[`schema/oba-validator-report.schema.json`](schema/oba-validator-report.schema.json).
This is the recommended format for the Render one-off-job workflow: the job
prints the document to stdout and the caller reads it from the job output.

## Development

    make build         # compile to bin/oba-validator
    make test          # run unit tests (no network)
    make run ARGS=config.json
    make test-live     # env-gated live test against the real server
    make docker-build  # build the deployment image

Run `make` with no target to build. See the `Makefile` for all targets.

## Deploying to Render

The validator deploys to [Render](https://render.com) as a Docker **cron job**
whose schedule (`0 2 29 2 *`, Feb 29 02:00 UTC) makes it effectively never run on
its own. Real validations are launched on demand as **one-off jobs** against the
service, with the entire config — including `apiKey` — passed as the start
command. Nothing server-specific is baked into the image, so one deployment can
validate any OBA server.

Build the image locally to verify it (the container accepts raw JSON directly):

    make docker-build
    docker run --rm oba-validator '{"obaServerURL":"https://api.pugetsound.onebusaway.org","apiKey":"org.onebusaway.iphone","dataSources":[{"agencyMapping":{"KCM":"1"},"staticGtfsFeedURL":"https://metro.kingcounty.gov/GTFS/google_transit.zip","vehiclePositionsURL":"https://s3.amazonaws.com/kcm-alerts-realtime-prod/vehiclepositions.pb"}]}'

Deploy by pointing a Render Blueprint at `render.yaml`, or create the cron job by
hand in the dashboard (Docker runtime, the schedule above, no environment
variables needed).

### Triggering a validation (one-off job)

Render runs a one-off job's start command by **splitting it on whitespace and
passing it as argv — there is no shell**, and the first token is used as the
executable. A JSON config can't be passed inline (it has spaces and special
characters), so **base64-encode the config** into a single token and let the
image's `entrypoint.sh` decode it. The start command is:

    /app/entrypoint.sh <base64-of-compact-config-json>

Produce the token from your config (note the `apiKey` lives *in* the config — it
is never baked into the image, since keys differ per server):

    printf '%s' '{"obaServerURL":"https://api.example.org","apiKey":"your-key","dataSources":[{"agencyMapping":{"X":"1"},"staticGtfsFeedURL":"https://.../gtfs.zip","vehiclePositionsURL":"https://.../vp.pb"}]}' | base64 | tr -d '\n'

Trigger it via the API (base64 is plain `[A-Za-z0-9+/=]`, so no escaping needed):

    curl --request POST 'https://api.render.com/v1/services/<service-id>/jobs' \
      --header 'Authorization: Bearer <render-api-key>' \
      --header 'Content-Type: application/json' \
      --data-raw '{"startCommand": "/app/entrypoint.sh <base64-config>"}'

From Ruby (e.g. an obacloud job), this mirrors the existing pattern:

    encoded = Base64.strict_encode64(config.to_json)
    start_command = "/app/entrypoint.sh #{encoded}"

Validator flags go after the token (`/app/entrypoint.sh <base64> --json`). The
job's exit status is the validator's exit code — `0` when a report was produced
(PASS *or* FAIL verdict), `2` only when the validator couldn't run (config/usage
error). A FAIL verdict therefore shows as a *succeeded* Render run; the caller
reads the verdict from `summary.verdict` in the JSON report or the sink row.

See `docs/superpowers/specs/2026-05-24-oba-validator-design.md` for the validator
design and `docs/superpowers/specs/2026-05-25-render-deployment-design.md` for the
deployment design.

### Reading the result from a database

When the validator runs as a Render one-off job, the job status exposes whether
it succeeded but not the report itself. To let a caller (e.g. obacloud's
`ServerValidationJob`) read the report back without scraping stdout, supply five
additional fields in the same JSON payload and the validator will write one row
to a Postgres "results" table after stdout, keyed by `correlation_id`:

| Field | Description |
|---|---|
| `db_url` | JDBC-style URL, e.g. `jdbc:postgresql://host:5432/dbname`. Activates the sink when non-blank. |
| `db_user` / `db_pass` | DB credentials. |
| `correlation_id` | UUID the caller chooses; row key. |
| `result_table` | Table name. Must be `oba_validator_results` (allow-listed). |

The validator creates the table on first write (`CREATE TABLE IF NOT EXISTS`)
with columns `correlation_id TEXT PRIMARY KEY, status TEXT NOT NULL, result_data
TEXT, error_message TEXT`, then `INSERT ... ON CONFLICT (correlation_id) DO
NOTHING` — so retries are idempotent.

Behavior is purely additive: when `db_url` is absent the validator behaves
exactly as today. A row is always written after stdout, with `status="completed"`
for both PASS and FAIL verdicts (the verdict lives at `summary.verdict` inside
`result_data`) and `status="error"` reserved for the `errorDocument` variant.
Sink failures are logged to stderr but never change the exit code.

See `docs/superpowers/specs/2026-05-25-result-sink-design.md` for the full
contract.
