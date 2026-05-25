# OBA Validator

Validates that a OneBusAway server is functioning properly by cross-referencing
its REST API against the authoritative static GTFS and GTFS-realtime feeds
(vehicle positions, trip updates, service alerts).

## Usage

    oba-validator [flags] <config.json | raw-json-string>

Flags: `--json`, `--sample-size`, `--freshness`, `--timeout`, `--cache-dir`,
`--no-cache`, `--refresh`.

Exit codes: `0` = no failures, `1` = at least one failure, `2` = config/usage
error. Warnings and skips do not affect the exit code.

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

## Library

```go
cfg, _ := config.Load("config.json")  // or a raw JSON string
rep, _ := validator.Run(ctx, cfg)
report.WriteText(os.Stdout, rep)       // or report.WriteJSON(os.Stdout, rep)
os.Exit(rep.ExitCode())
```

See `docs/superpowers/specs/2026-05-24-oba-validator-design.md` for the full
design.
