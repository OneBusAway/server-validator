# OBA Validator — Design

**Date:** 2026-05-24
**Status:** Approved; revised after architect review (pending final user review)

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
`rtFreshnessSeconds` (300), `locationSpan` (0.01°), `maxConcurrency` (low,
e.g. 4), `cacheDir` (OS user cache dir). CLI flags override these and add run
controls: `--json`, `--sample-size`, `--freshness`, `--timeout` (default 120s),
`--cache-dir`, `--no-cache`, `--refresh`.

`apiKey` may be omitted from the config and supplied via the `ONEBUSAWAY_API_KEY`
environment variable (which the SDK already reads), so the key need not sit in a
file. It is never echoed to output (see Error handling).

Reference test config (King County Metro):

```json
{
    "obaServerURL": "https://api.pugetsound.onebusaway.org",
    "apiKey": "org.onebusaway.iphone",
    "dataSources": [
        {
            "agencyMapping": {"KCM": "1"},
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
  `TripForVehicle`, `TripsForLocation`.
  - **Client init:** `onebusaway.NewClient(option.WithAPIKey(key),
    option.WithBaseURL(url), option.WithRequestTimeout(d),
    option.WithMaxRetries(n), option.WithHTTPClient(c))`. The
    `ValidationContext` holds the constructed `*onebusaway.Client` (injected,
    not built internally) so checks can be exercised against an `httptest`
    server via `WithBaseURL`/`WithHTTPClient`.
  - **Param wrapping:** every query parameter is a `param.Field[T]` and must be
    wrapped with the SDK helpers (e.g. `onebusaway.Float(x)`); a check that
    passes lat/lon/time must construct these, not pass bare values.
  - **Verified param/response specifics** (read from SDK source, not assumed):
    - `TripsForLocationListParams` requires `Lat, LatSpan, Lon, LonSpan`
      (`param.Field[float64]`) — **there is no `Radius`** (see check 5).
    - `StopsForLocationListParams` has optional `Radius`/`LatSpan`/`LonSpan`.
    - Arrival/departure entries carry a per-arrival `SituationIDs []string`
      (`json:"situationIds"`) in addition to a global `References.Situations`
      (see check 7).
    - `current-time` returns epoch **milliseconds** (not seconds).
    - The `Time` param type is inconsistent across endpoints (some
      `param.Field[int64]`, some `param.Field[time.Time]`); confirm per call.
- **go-gtfs** (`github.com/OneBusAway/go-gtfs`) — parses both static GTFS zips
  (`ParseStatic(content []byte, opts ParseStaticOptions)`) and GTFS-realtime
  (`ParseRealtime(content []byte, opts *ParseRealtimeOptions)`). Both take the
  full payload as bytes (the whole zip in memory for static). **No
  hand-compiled `.proto` required.** Relevant types:
  - Static: `Static{Agencies []Agency, Routes []Route, Stops []Stop, Trips
    []ScheduledTrip}`; `Agency{Id string, Name string}`; `Stop{Id string,
    Latitude *float64, Longitude *float64}`; `ScheduledTrip{ID string, Route
    *Route}`, `Route{Id string, Agency *Agency}` (this trip→route→agency
    linkage is what the sampling checks use to resolve a feed entity's agency).
  - Realtime: `Realtime{CreatedAt time.Time, Trips []Trip, Vehicles []Vehicle,
    Alerts []Alert}`; `Vehicle{ID *VehicleID{ID, Label, LicensePlate}, Trip
    *Trip, Position *Position{Latitude/Longitude *float32}}`; `Trip{ID
    TripID{ID, RouteID}, StopTimeUpdates []StopTimeUpdate{StopID *string,
    Arrival/Departure *StopTimeEvent}}`; `Alert{ID string, InformedEntities
    []AlertInformedEntity{AgencyID *string, RouteID *string, TripID *TripID,
    StopID *string}}` — note `TripID` is a `*TripID` struct; use
    `entity.TripID.ID` for the raw trip id.
  - `ParseRealtimeOptions.Timezone` governs timestamp interpretation
    (nil ⇒ UTC). We pass `time.UTC` explicitly (GTFS-rt timestamps are
    POSIX/UTC by spec); `CreatedAt` derives from the feed header timestamp.

## Key invariants

- **Agency IDs are opaque strings.** `"MTS"` is as valid as `"1"`. Never
  integer-parse an agency ID anywhere — config, normalization, comparison, or
  output.
- **OBA namespaces entity IDs** as `{agencyId}_{rawId}` (e.g. API vehicle
  `1_4567`, trip `1_12345678`), while GTFS-realtime feeds carry the raw IDs.
  All API-vs-feed ID comparisons go through the smart prefix-aware normalizer
  (below). The `agencyId` used to build that prefix comes from the data
  source's `agencyMapping`.
  - This convention is **reliable for stops, routes, and trips**. It is **not
    reliable for vehicles** (OBA's `vehicleId` raw portion is not guaranteed to
    equal the GTFS-rt `VehicleDescriptor.id`/`label`) or for **situations**
    (OBA frequently synthesizes its own situation id rather than reusing the
    GTFS-rt `Alert.id`). Checks against vehicles and situations therefore use
    tolerant matching and downgrade an unconfirmed match to `Warn`, never a
    hard `Fail` on id shape alone (see checks 5 and 7).
- **`agency_id` may be blank.** GTFS permits an empty `agency_id` when a feed
  has exactly one agency. A blank id has no prefix to strip/build; the
  normalizer must handle it (treat the whole API id as raw when there is no
  agency prefix) and the operator can map `"": "<obaId>"` explicitly.
- **The operator declares agency remaps; the validator never guesses them.**
  GTFS↔OBA agency identity comes from the per-`dataSource` `agencyMapping`, not
  from name-matching heuristics.

## Architecture

A two-phase design behind `validator.Run(ctx, cfg) → Report`:

1. **Preparation phase** builds a shared `ValidationContext`: the OBA SDK
   client, the fetched `agencies-with-coverage` list, and — per data source — a
   `SourceContext` holding the parsed static GTFS, the three parsed RT feeds,
   and the resolved `agencyMapping`. All expensive downloads/parses happen
   exactly once here.
2. **Check phase** runs small `Check` units that read from the
   `ValidationContext` and emit `Result`s. No check re-downloads anything.
   Checks run in two groups: server-level (run once) and per-data-source.

### Package layout

This is a real importable library (no `internal/`):

```
config/      Config struct; Load(pathOrJSON) auto-detecting file vs raw JSON
validator/   Run orchestrator; ServerCheck/DataSourceCheck interfaces;
             Result/Status/Report; ValidationContext/SourceContext;
             ID normalizer (idnorm)
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

// Server-level checks run once against the whole server.
type ServerCheck interface {
    Name() string
    Run(ctx context.Context, vc *ValidationContext) []Result
}

// Per-data-source checks run once per dataSource; the orchestrator passes the
// specific source (parsed feeds + agencyMapping) so the check never loops over
// all sources itself.
type DataSourceCheck interface {
    Name() string
    Run(ctx context.Context, vc *ValidationContext, src *SourceContext) []Result
}
```

`SourceContext` holds one data source's parsed static GTFS, the three parsed RT
feeds, and its resolved `agencyMapping`. The orchestrator owns the
server-vs-source grouping; checks stay single-purpose. Each `Result` records
which data source it came from (index/label) so the reporter can group output.

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
- When there is no agency prefix (blank `agency_id`, or an API id with no
  underscore), compare the full ids directly.
- Agency IDs treated strictly as strings throughout.
- **Per-entity confidence:** the helper exposes the match outcome (matched / not
  matched) so callers decide severity. Stops/routes/trips treat a non-match as
  authoritative; vehicles and situations treat a non-match as inconclusive
  (`Warn`) because their id schemes are unreliable (see Key invariants).

## Download caching

A `FeedCache` keyed by `sha256(url)`, stored under
`<cacheDir>/oba-validator/`. Each entry is `{hash}.body` plus `{hash}.meta.json`
(url, etag, last-modified, fetched-at).

- **Static GTFS** is cached. Each run issues a conditional GET using the stored
  `ETag` / `Last-Modified`; a `304 Not Modified` reuses the cached zip.
  - **Validators take precedence over TTL:** the TTL fallback (default 1h)
    applies only when the cache entry has *no* `ETag`/`Last-Modified`. If a
    server sends validators but ignores them and returns `200` every time
    (common with S3/CDN), we simply accept the re-download.
  - `--no-cache` bypasses the cache; `--refresh` forces a re-download.
- **Realtime feeds are never cached** — always fetched fresh, so the freshness
  check is meaningful.

**Corruption / concurrency safety:** write `{hash}.body` to a temp file and
atomic-rename it into place, then write `{hash}.meta.json`; a body present
without valid meta is treated as a miss (guards against truncated writes from a
crash mid-download). A per-key lock (single-flight) prevents two data sources
that share a static feed URL from racing to write the same entry.

## The checks

### Server-level

**1. Basic endpoints** (a port of `docker/bin/validate.sh`). A dependency
chain, each step feeding the next:

1. `current-time` → epoch **milliseconds** returned, within a tolerance window
   of local now (default ±1h, to absorb clock skew without ignoring a wrong
   clock). Beyond the window → `Warn`.
2. `agencies-with-coverage` → at least one agency; capture first `agencyId`.
3. `routes-for-agency(agencyId)` → at least one route; capture first `routeId`.
4. `stops-for-route(routeId)` → `entry.routeId` matches; capture first `stopId`.
5. `stop(stopId)` → `entry.id` matches; capture `lat`/`lon`.
6. `stops-for-location(lat, lon)` → `outOfRange == false` and at least one stop.
   (No span/radius set → server default radius, matching `validate.sh`.)
7. `arrivals-and-departures-for-stop(stopId)` → `entry.stopId` matches; empty
   arrivals list → `Warn`.

Each step is its own `Result`. A failed step marks the remaining dependent
steps `Skip`.

**2. Agency union.** For every static GTFS feed, take its `agency_id` set and
translate each through that data source's `agencyMapping` (unmapped IDs pass
through as identity) to produce the set of **expected OBA `agencyId`s**. Union
these across all data sources and compare to the `agencies-with-coverage`
`agencyId` set.

- A **mapped** expected agency absent from the API → `Fail` (genuinely not
  served, or a wrong mapping value).
- An **unmapped (identity-assumed)** expected agency absent from the API →
  `Warn` ("assumed identity mapping `X`; add an `agencyMapping` entry if the
  server remaps it"). This prevents a guaranteed false `Fail` for the common
  case where the operator simply hasn't declared a remap — including a blank
  `agency_id` (mapping key `""`), which never matches a real OBA `agencyId`
  unmapped.
- API agency not in the expected set → `Warn` (the server may serve agencies
  from feeds not listed in this config).
- The `agencyMapping` is authoritative — no name-matching is used to decide
  pass/fail. As a convenience, a `Warn`/`Fail` *hint* may suggest a likely
  mapping when an unmatched API agency shares an `agency_name` with an unmatched
  GTFS agency ("API agency `1` is named 'Metro Transit' — did you mean to map
  `\"KCM\": \"1\"`?").

### Per data source

**3. Static GTFS sanity.** The parsed feed has non-empty agencies, routes,
stops, and trips; otherwise `Fail`. (Runs against the already-parsed feed —
nearly free.)

**4. RT feed freshness.** For each RT feed (vehicle positions, trip updates,
service alerts), `Realtime.CreatedAt` (parsed with `time.UTC`) is within
`rtFreshnessSeconds` (default 300) of now. Stale → `Fail`; missing/zero
timestamp → `Warn`; a timestamp dated meaningfully in the *future* → `Warn`
(clock/timezone problem rather than staleness).

**Deterministic sampling (applies to checks 5–7).** Candidate entities are
sorted by a stable key (raw id) before the first `sampleSize` are taken, so a
scheduled monitor samples the same entities run-to-run and `Warn`/`Fail`
signals don't flap.

**5. Vehicle sampling.** Take `sampleSize` (default 3) vehicles from the parsed
VehiclePositions feed, preferring vehicles that have both a trip and a position.

*Agency resolution.* The OBA `agencyId` to query is derived from the static
GTFS: feed `trip_id` → `ScheduledTrip.ID` → `.Route.Agency.Id`, falling back to
feed `route_id` → `Route.Id` → `.Route.Agency.Id` (RT vehicle positions often
omit `route_id`), then translated through the data source's `agencyMapping`. If
neither join resolves (RT references a trip/route absent from the cached static
feed — version skew, added trips), the vehicle is **not sampleable** → `Warn`
("could not resolve agency for vehicle X"), never `Fail`.

For each resolved vehicle:

- **vehicles-for-agency**: `vehicles-for-agency` returns no agency field, so
  agency identity is the path param. Match the feed vehicle against the returned
  `vehicleId`s using idnorm, trying `VehicleID.ID` and then `VehicleID.Label`.
  Match ⇒ `Pass`. List non-empty but no match ⇒ `Warn` (likely id-convention
  mismatch; surface both ids in `Details`). List empty while the feed is
  populated ⇒ `Fail`.
- **trip-for-vehicle**: returns a trip whose `tripId` matches (normalized) the
  feed vehicle's `trip_id`. No matching trip ⇒ `Fail`; empty/no current trip ⇒
  `Warn`.
- **trips-for-location**: queried with a bounding box around the vehicle's
  lat/lon — `Lat`, `Lon`, and required `LatSpan`/`LonSpan` (default span
  `0.01°` ≈ 1.1 km, configurable as `locationSpan`); **there is no radius
  param**. The vehicle's trip appears in results ⇒ `Pass`; absent ⇒ `Warn` (the
  vehicle may have moved out of the box between feed fetch and query — not a
  hard `Fail`). Vehicle lacked a position ⇒ `Warn` (can't query).

Feed empty ⇒ `Warn`.

**6. Trip-update sampling.** Take `sampleSize` trip updates from the parsed
TripUpdates feed. For a `StopTimeUpdate` with a predicted arrival/departure,
query **arrivals-and-departures-for-stop** for that (normalized) stop and
confirm an arrival/departure whose `tripId` matches the trip update's `trip_id`.
In-feed-but-absent ⇒ `Fail`. No usable stop-time-update ⇒ `Warn`. (v1 uses
`arrivals-and-departures-for-stop` only; `trip-details` deferred.)

**7. Service-alert cross-reference** (the flakiest check). Take `sampleSize`
active alerts, preferring those with an informed `stop_id`; resolve trip-scoped
(`InformedEntity.TripID.ID`) or route-scoped alerts to a representative stop via
the static GTFS. Query `arrivals-and-departures-for-stop` for that stop and
confirm the alert id appears in the relevant **per-arrival `SituationIDs`**
(scoped to the affected trip) — falling back to the response's global
`References.Situations`. Because OBA may synthesize situation ids that don't
equal the GTFS-rt `Alert.id` (see Key invariants), a non-match is `Warn`, not
`Fail`; a `Fail` is reserved for the case where the endpoint errors or returns
no situations at all for a stop the feed says is actively affected. No
cross-referenceable alert could be sampled ⇒ `Warn`.

## Concurrency, request volume & memory

Within a data source, the three+ RT feeds and the static GTFS are fetched
concurrently during preparation. Checks within a source run sequentially for
deterministic output ordering. v1 processes data sources sequentially; the
design permits per-source parallelism later without restructuring.

**API politeness.** The sampling checks fan out: roughly `3 × sampleSize`
(vehicles) + `sampleSize` (trip updates) + `sampleSize` (alerts) calls per data
source — ~20 calls/source at the default against a shared production server.
The SDK is constructed with an explicit `WithMaxRetries` (don't rely on the
default, which can silently multiply load) and a configurable concurrency cap
(`maxConcurrency`, default low) bounds in-flight API calls. Expected request
volume is documented for operators scheduling the tool.

**Memory.** `ParseStatic` holds the entire zip in memory and the parsed
`Static` (all trips/stop_times) for a large multi-agency feed can be hundreds of
MB; peak is the sum across data sources held in the `ValidationContext`. The raw
zip bytes are released after parsing. We keep `Trips`/`Routes` (needed for the
check-5 trip→route→agency join) but don't otherwise traverse `ScheduledStopTime`
data.

## Error handling

- A feed that won't download or parse is **breakage** → `Fail` with the
  underlying error in `Details`. This is distinct from a valid-but-empty feed
  (`Warn`).
- **Preparation failures** (a feed 404s, or the server is unreachable) produce
  `Fail` results that explicitly say preparation could not complete, so the
  report distinguishes "server/feed down" from "server up, one check failed."
  Exit code is still `1` (no separate code), but the message is unambiguous and
  dependent per-source checks are marked `Skip`.
- **Secret handling.** The `apiKey` must never appear in `Result.Details`, log
  output, or the JSON report. SDK errors and URLs that may embed the key as a
  query param are redacted before being stored or printed.
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

- **Unit (hermetic):** idnorm (table-driven: alphanumeric agency IDs, raw IDs
  containing underscores, blank `agency_id`/no-prefix, matched-vs-inconclusive
  outcome); config loader (file path vs raw JSON detection, malformed input,
  `agencyMapping` parsing, `apiKey` from env); the agency-union check across
  identity/remap/mapped-missing(`Fail`)/unmapped-missing(`Warn`)/blank-id cases;
  vehicle sampling's agency-resolution (trip_id join, route_id fallback,
  unresolvable → `Warn`) and graded vehicles-for-agency matching (match `Pass`,
  no-match-non-empty `Warn`, empty-list `Fail`); service-alert matching against
  per-arrival `SituationIDs`; secret redaction (apiKey never in `Details`/JSON);
  `FeedCache` (200 stores, 304 reuses, validator-vs-TTL precedence, atomic
  write / truncated-body-treated-as-miss, `--no-cache`/`--refresh`); each check
  against `httptest` servers (via `WithBaseURL`) plus small saved `.pb` and JSON
  fixtures.
- **Integration (live):** one end-to-end test gated by `OBA_VALIDATOR_LIVE=1`
  running the King County Metro config against the real server. Off by default
  in CI.

## Out of scope for v1

- `trip-details` cross-checks for sampled trips (trip-update sampling uses
  arrivals-and-departures only).
- Per-data-source parallel execution (designed for, not implemented).
- `--warn-as-error` exit-code mode.
- Caching of realtime feeds.
