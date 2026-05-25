# OBA Validator — Design

**Date:** 2026-05-24
**Status:** Approved (pending spec review)

## Purpose

A Go **library** with a thin **CLI** that validates whether a OneBusAway (OBA)
server is functioning correctly. It cross-references a server's REST API against
the authoritative source data — the static GTFS feeds and the GTFS-realtime
feeds (vehicle positions, trip updates, service alerts) — and reports per-check
results.

The tool is meant to serve two audiences at once: a human debugging a
deployment, and an unattended monitor running on a schedule. That dual purpose
drives the output model (structured + pretty) and the severity model
(evidence-based, low false-positive).

## Inputs

A JSON config, supplied either as a **file path** or a **raw JSON string**
(auto-detected). Schema:

```json
{
    "obaServerURL": "https://example.com",
    "apiKey": "apiKey",
    "dataSources": [
        {
            "staticGtfsFeedURL": "https://example.com",
            "vehiclePositionsURL": "https://example.com",
            "tripUpdatesURL": "https://example.com",
            "serviceAlertsURL": "https://example.com",
            "agencyMapping": { "<gtfs_agency_id>": "<oba_agency_id>" }
        }
    ]
}
```

**`agencyMapping`** (optional, per `dataSource`) declares how the GTFS
`agency_id` values in *this* feed map to the `agencyId` values the OBA server
exposes — e.g. `{ "KCM": "1" }`. Keys are the GTFS `agency_id` exactly as it
appears in `agency.txt` (case-sensitive, opaque string); values are the OBA
`agencyId`. Any GTFS agency not listed defaults to **identity** (used as-is
against the API), so single-agency feeds whose IDs already match need no
mapping. The mapping is **authoritative** wherever the validator must bridge a
GTFS agency to its OBA counterpart (see the agency-union and sampling checks).

Other optional config fields (with defaults): `sampleSize` (3),
`rtFreshnessSeconds` (300), `cacheDir` (OS user cache dir). CLI flags override
these and add run controls: `--json`, `--sample-size`, `--freshness`,
`--timeout` (default 120s), `--cache-dir`, `--no-cache`, `--refresh`.

Reference test config (King County Metro):

```json
{
    "obaServerURL": "https://api.pugetsound.onebusaway.org",
    "apiKey": "org.onebusaway.iphone",
    "dataSources": [
        {
            "staticGtfsFeedURL": "https://metro.kingcounty.gov/GTFS/google_transit.zip",
            "vehiclePositionsURL": "https://s3.amazonaws.com/kcm-alerts-realtime-prod/vehiclepositions.pb",
            "tripUpdatesURL": "https://s3.amazonaws.com/kcm-alerts-realtime-prod/tripupdates.pb",
            "serviceAlertsURL": "https://s3.amazonaws.com/kcm-alerts-realtime-prod/alerts.pb"
        }
    ]
}
```

KCM's GTFS `agency_id` almost certainly differs from the `1` the Puget Sound
server exposes, so this config will likely need an `agencyMapping` (e.g.
`{ "KCM": "1" }`) for the agency-union check to pass. The exact key is confirmed
by inspecting `agency.txt` at implementation time.

## Dependencies

- **OBA Go SDK** (`github.com/OneBusAway/go-sdk`) — client for the REST API.
  Exposes one service per endpoint we use: `CurrentTime`,
  `AgenciesWithCoverage`, `RoutesForAgency`, `StopsForRoute`, `Stop`,
  `StopsForLocation`, `ArrivalsAndDeparturesForStop`, `VehiclesForAgency`,
  `TripForVehicle`, `TripsForLocation`. Client init takes base URL + API key.
  *Exact method parameter structs to be confirmed against `api.md` at
  implementation time.*
- **go-gtfs** (`github.com/onebusaway/go-gtfs`) — parses both static GTFS zips
  (`ParseStatic`) and GTFS-realtime (`ParseRealtime`). Verified to expose
  everything we need; **no hand-compiled `.proto` required.** Relevant types:
  - Static: `Static{Agencies []Agency, Routes []Route, Stops []Stop, Trips
    []ScheduledTrip}`; `Agency{Id string, Name string}`; `Stop{Id string,
    Latitude *float64, Longitude *float64}`.
  - Realtime: `Realtime{CreatedAt time.Time, Trips []Trip, Vehicles []Vehicle,
    Alerts []Alert}`; `Vehicle{ID *VehicleID, Trip *Trip, Position
    *Position{Latitude/Longitude *float32}}`; `Trip{ID TripID{ID, RouteID},
    StopTimeUpdates []StopTimeUpdate{StopID *string, Arrival/Departure}}`;
    `Alert{ID string, InformedEntities []AlertInformedEntity{AgencyID, RouteID,
    TripID, StopID}}`.

## Key invariants

- **Agency IDs are opaque strings.** `"MTS"` is as valid as `"1"`. Never
  integer-parse an agency ID anywhere — config, normalization, comparison, or
  output.
- **OBA namespaces entity IDs** as `{agencyId}_{rawId}` (e.g. API vehicle
  `1_4567`, trip `1_12345678`), while GTFS-realtime feeds carry the raw IDs.
  All API-vs-feed ID comparisons go through the smart prefix-aware normalizer
  (below). The `agencyId` used to build that prefix comes from the data
  source's `agencyMapping`.
- **The operator declares agency remaps; the validator never guesses them.**
  GTFS↔OBA agency identity comes from the per-`dataSource` `agencyMapping`, not
  from name-matching heuristics.

## Architecture

A two-phase design behind `validator.Run(ctx, cfg) → Report`:

1. **Preparation phase** builds a shared `ValidationContext`: the OBA SDK
   client, the fetched `agencies-with-coverage` list, and — per data source —
   the parsed static GTFS plus the three parsed RT feeds. All expensive
   downloads/parses happen exactly once here.
2. **Check phase** runs small `Check` units that read from the
   `ValidationContext` and emit `Result`s. No check re-downloads anything.
   Checks run in two groups: server-level (run once) and per-data-source.

### Package layout

This is a real importable library (no `internal/`):

```
config/      Config struct; Load(pathOrJSON) auto-detecting file vs raw JSON
validator/   Run orchestrator; Check interface; Result/Status/Report;
             ValidationContext; ID normalizer (idnorm)
feeds/       HTTP fetch; FeedCache (conditional GET); static + realtime
             parse wrappers
checks/      one file per check: endpoints, agencies, gtfs_sanity, freshness,
             vehicle_positions, trip_updates, service_alerts
report/      pretty (terminal) + json reporters
cmd/oba-validator/   CLI entry point
```

Library usage:

```go
cfg, _ := config.Load("config.json")  // or a raw JSON string
rep, _ := validator.Run(ctx, cfg)
rep.WriteText(os.Stdout)              // or rep.WriteJSON(os.Stdout)
os.Exit(rep.ExitCode())
```

### Core types

```go
type Status int // Pass, Warn, Fail, Skip

type Result struct {
    Check   string         // e.g. "vehicle-positions sampling"
    Status  Status
    Message string         // human summary
    Details map[string]any // structured extras: ids checked, counts, errors
}

type Report struct { Results []Result } // + Worst(), ExitCode(), WriteText, WriteJSON

type Check interface {
    Name() string
    Run(ctx context.Context, vc *ValidationContext) []Result
}
```

### Severity model (evidence-based)

- **Fail** only when the feed *has* an entity but the API is missing or
  contradicts it, or when an endpoint errors / returns invalid data, or a feed
  won't download/parse.
- **Warn** for valid-but-empty or unsamplable conditions: feed currently has
  zero vehicles, stop has no current arrivals, no cross-referenceable alert
  could be drawn, API agency absent from the GTFS union.
- **Skip** for checks whose prerequisites failed earlier in a dependent chain.
- **Pass** otherwise.

### Exit codes

`0` = no `Fail`. `1` = at least one `Fail`. `2` = config/usage error (exit
before any checks run). `Warn`/`Skip` do not affect the exit code.

### ID normalization (idnorm)

Smart, prefix-aware matching for comparing a raw feed ID against an OBA API ID:

- Split the API ID on the **first** underscore; compare the suffix to the raw
  feed ID. (Raw IDs may themselves contain underscores, so only the first split
  matters.)
- Also support the reverse: prefix a raw feed ID with the OBA `agencyId` (from
  the data source's `agencyMapping`) to build the expected API ID.
- Agency IDs treated strictly as strings throughout.

## Download caching

A `FeedCache` keyed by `sha256(url)`, stored under
`<cacheDir>/oba-validator/`. Each entry is `{hash}.body` plus `{hash}.meta.json`
(url, etag, last-modified, fetched-at).

- **Static GTFS** is cached. Each run issues a conditional GET using the stored
  `ETag` / `Last-Modified`; a `304 Not Modified` reuses the cached zip. When the
  server provides no validators, fall back to a TTL (default 1h) to skip the
  network. `--no-cache` bypasses the cache; `--refresh` forces a re-download.
- **Realtime feeds are never cached** — always fetched fresh, so the freshness
  check is meaningful.

## The checks

### Server-level

**1. Basic endpoints** (a port of `docker/bin/validate.sh`). A dependency
chain, each step feeding the next:

1. `current-time` → numeric epoch returned (sanity: roughly near now).
2. `agencies-with-coverage` → at least one agency; capture first `agencyId`.
3. `routes-for-agency(agencyId)` → at least one route; capture first `routeId`.
4. `stops-for-route(routeId)` → `entry.routeId` matches; capture first `stopId`.
5. `stop(stopId)` → `entry.id` matches; capture `lat`/`lon`.
6. `stops-for-location(lat, lon)` → `outOfRange == false` and at least one stop.
7. `arrivals-and-departures-for-stop(stopId)` → `entry.stopId` matches; empty
   arrivals list → `Warn`.

Each step is its own `Result`. A failed step marks the remaining dependent
steps `Skip`.

**2. Agency union.** For every static GTFS feed, take its `agency_id` set and
translate each through that data source's `agencyMapping` (unmapped IDs pass
through as identity) to produce the set of **expected OBA `agencyId`s**. Union
these across all data sources and compare to the `agencies-with-coverage`
`agencyId` set.

- Expected agency absent from the API → `Fail` (genuinely not served, or a
  missing/incorrect mapping entry).
- API agency not in the expected set → `Warn` (the server may serve agencies
  from feeds not listed in this config).
- The `agencyMapping` is authoritative — no name-matching is used to decide
  pass/fail. As a convenience, a `Fail` *hint* may suggest a likely mapping
  when an unmatched API agency shares an `agency_name` with an unmatched GTFS
  agency ("API agency `1` is named 'Metro Transit' — did you mean to map
  `\"KCM\": \"1\"`?").

### Per data source

**3. Static GTFS sanity.** The parsed feed has non-empty agencies, routes,
stops, and trips; otherwise `Fail`. (Runs against the already-parsed feed —
nearly free.)

**4. RT feed freshness.** For each RT feed (vehicle positions, trip updates,
service alerts), `Realtime.CreatedAt` is within `rtFreshnessSeconds` (default
300) of now. Stale → `Fail`; missing/zero timestamp → `Warn`.

**5. Vehicle sampling.** Take `sampleSize` (default 3) vehicles from the parsed
VehiclePositions feed, preferring vehicles that have both a trip and a position.
The OBA `agencyId` to query (and to build the `{agencyId}_{rawId}` prefix) is
resolved from the data source's `agencyMapping`, via the agency that owns the
vehicle's trip/route in the static GTFS. For each sampled vehicle:

- **vehicles-for-agency**: the vehicle (normalized) appears in the agency's
  vehicle list.
- **trip-for-vehicle**: returns a trip whose `tripId` matches (normalized) the
  feed vehicle's `trip_id`.
- **trips-for-location**: queried at the vehicle's lat/lon (small radius), the
  vehicle's trip appears in results.

Found ⇒ `Pass`. In-feed-but-not-in-API ⇒ `Fail`. Vehicle lacked a position (for
the location check) ⇒ `Warn`. Feed empty ⇒ `Warn`.

**6. Trip-update sampling.** Take `sampleSize` trip updates from the parsed
TripUpdates feed. For a `StopTimeUpdate` with a predicted arrival/departure,
query **arrivals-and-departures-for-stop** for that (normalized) stop and
confirm an arrival/departure whose `tripId` matches the trip update's `trip_id`.
In-feed-but-absent ⇒ `Fail`. No usable stop-time-update ⇒ `Warn`. (v1 uses
`arrivals-and-departures-for-stop` only; `trip-details` deferred.)

**7. Service-alert cross-reference** (the flakiest check). Take `sampleSize`
active alerts, preferring those with an informed `stop_id`; resolve trip- or
route-scoped alerts to a representative stop via the static GTFS. Query
`arrivals-and-departures-for-stop` for that stop and confirm the alert id
(normalized) appears in the response's `references.situations`. Absent ⇒ `Fail`.
No cross-referenceable alert could be sampled ⇒ `Warn`.

## Concurrency

Within a data source, the three+ RT feeds and the static GTFS are fetched
concurrently during preparation. Checks within a source run sequentially for
deterministic output ordering. v1 processes data sources sequentially; the
design permits per-source parallelism later without restructuring.

## Error handling

- A feed that won't download or parse is **breakage** → `Fail` with the
  underlying error in `Details`. This is distinct from a valid-but-empty feed
  (`Warn`).
- Per-request timeout via `context` (default 120s, `--timeout`).
- Config load/parse errors exit `2` with a clear message before any checks run.

## Output

- **Pretty reporter** (default): results grouped by server-level and per-data-
  source, with `✓` / `⚠` / `✗` / `–` glyphs and a summary line
  (`FAIL (1 of 4 checks failed)`).
- **JSON reporter** (`--json`): the full structured `Report` to stdout for
  automation; humans can pipe through `jq`.

## Testing

TDD throughout.

- **Unit (hermetic):** idnorm (table-driven, including alphanumeric agency IDs
  and raw IDs containing underscores); config loader (file path vs raw JSON
  detection, malformed input, `agencyMapping` parsing); the agency-union check
  with and without an `agencyMapping` (identity, remap, missing-mapping `Fail`
  with hint); `FeedCache` (200 stores, 304 reuses, TTL skip,
  `--no-cache`/`--refresh`); each check against `httptest` servers plus small
  saved `.pb` and JSON fixtures.
- **Integration (live):** one end-to-end test gated by `OBA_VALIDATOR_LIVE=1`
  running the King County Metro config against the real server. Off by default
  in CI.

## Out of scope for v1

- `trip-details` cross-checks for sampled trips (trip-update sampling uses
  arrivals-and-departures only).
- Per-data-source parallel execution (designed for, not implemented).
- `--warn-as-error` exit-code mode.
- Caching of realtime feeds.
