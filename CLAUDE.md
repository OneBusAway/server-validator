# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

A Go CLI + library that validates a running OneBusAway (OBA) server by cross-referencing its REST API against the *authoritative* sources of truth: the operator's static GTFS feed and GTFS-realtime feeds (vehicle positions, trip updates, service alerts). It answers "is this OBA server telling the truth about what the feeds say?"

## Commands

Common tasks have `make` targets (`build`, `test`, `test-live`, `vet`, `fmt`, `run`, `tidy`, `install`, `clean`); the raw commands they wrap:

```sh
go build ./...                       # build everything (make build → bin/oba-validator)
go test ./...                        # run all unit tests (no network)
go test ./validator/ -run TestName   # run a single test
go vet ./...

# Run the CLI
go run ./cmd/oba-validator [flags] <config.json | raw-json-string>

# Live integration test (hits the real Puget Sound server; off by default)
OBA_VALIDATOR_LIVE=1 go test ./validator/ -run TestLiveKingCountyMetro -v
```

Exit codes (also returned by `Report.ExitCode()`): `0` = no failures, `1` = ≥1 failure, `2` = config/usage error. Warnings and skips never affect the exit code.

## Architecture

The flow is **config → prepare (fetch) → checks → report**:

1. **`config`** — `config.Load()` accepts a file path *or* a raw JSON string (auto-detected by a leading `{`). Applies defaults, validates required fields, and reads `apiKey` from `ONEBUSAWAY_API_KEY` if absent.
2. **`feeds`** — fetching + parsing. `Fetcher` downloads feeds; static GTFS goes through an on-disk **conditional-GET `Cache`** (ETag/Last-Modified, atomic body-then-meta writes), realtime feeds are always fetched fresh. `ParsedStatic` wraps go-gtfs's `Static` with the lookup indexes checks need (agency IDs/names, raw trip→agency, raw route→agency).
3. **`validator`** — the engine. `validator.Run()` calls `prepare()`, then runs every check.
4. **`report`** — renders a `Report` as grouped text (`WriteText`) or, via `WriteJSON`, a UI-oriented JSON `Document` (meta + summary + grouped results; schema at `schema/oba-validator-report.schema.json`). `WriteErrorJSON` emits the error variant. The `Document` view model is built by the pure `BuildDocument(report, config, now)` so output is deterministic in tests.

`prepare()` (`validator/validator.go`) builds the shared `ValidationContext`: it constructs the OBA SDK client, fetches `AgenciesWithCoverage` once, and **fans out concurrently** (bounded by `MaxConcurrency`, default 4) to download/parse each data source's feeds into a `SourceContext`. A per-feed fetch/parse failure is recorded in `SourceContext.PrepErrors[feedName]` rather than aborting the run — checks inspect that map and decide severity themselves.

### Checks

Two interfaces in `validator/context.go`:
- **`ServerCheck`** — runs once against the whole server (`endpointsCheck`, `agencyUnionCheck`).
- **`DataSourceCheck`** — runs once per data source (`gtfsSanityCheck`, `freshnessCheck`, `vehicleSamplingCheck`, `tripUpdateSamplingCheck`, `serviceAlertCheck`).

Each check is a small struct in its own `check_*.go` file, returns `[]Result`, and is registered in the `serverChecks()` / `dataSourceChecks()` slices in `validator.go`. **To add a check: create `check_foo.go` with a struct implementing the interface, then add it to the appropriate registry slice.** A single check may emit multiple `Result`s (e.g. the vehicle check emits a sub-result per OBA endpoint, named `vehicle-positions-sampling/trip-for-vehicle`).

### Severity model (read before touching any check)

This is the core design discipline. Severity is **evidence-based** — see `docs/superpowers/specs/2026-05-24-oba-validator-design.md`:

- **`Fail`** only when the feed *has* an entity but the API contradicts or is missing it (genuine server breakage). Failures set exit code 1.
- **`Warn`** for valid-but-empty / unsamplable / unconfirmed conditions: empty feed, vehicle that moved, or an ID that didn't match on shape alone.
- **`Skip`** when a prerequisite failed earlier in a dependent chain.

The cardinal rule: **never `Fail` on ID-convention mismatch alone.** OBA prefixes IDs as `{agencyId}_{rawId}`, and agency/stop/route/trip ID schemes vary by operator, so a non-match is a `Warn` unless the API genuinely lacks data the feed proves exists.

### ID handling and agency mapping

- **`validator/idnorm.go`** — `RawID` strips the agency prefix, `PrefixedID` adds it, `IDMatch` compares an API id against a raw feed id tolerant of the prefix. Use `IDMatch` for all cross-references; don't compare ID strings directly.
- **`agencyMapping`** (per data source, in config) maps a GTFS `agency_id` to the `agencyId` the OBA server exposes; unmapped agencies default to identity. This is **explicit config, deliberately not name-based inference** — do not add fuzzy agency-name matching. Checks resolve a feed entity's agency through the static GTFS trip→route→agency linkage, then apply `MapAgency`.

### Security

The API key must never appear in output. Wrap any error string that may contain a URL/key with `redact(err, key)` (`validator/util.go`) before putting it in a `Result.Message`.

## Conventions

- **Determinism:** sampling uses `sampleByID` (sort by id, take first N) so a scheduled monitor checks the same entities run-to-run. Preserve this when adding sampling.
- **Tests** are standard table/`httptest` style with no network. Use `httptest.NewServer` to stub both the OBA API and feed URLs; build static-GTFS fixtures in-memory via `feeds.ParseStaticFromStruct(*gtfs.Static)` rather than zipping real feeds. Network-dependent tests must be gated behind an env var like the `OBA_VALIDATOR_LIVE` integration test.
- Key dependencies: `github.com/OneBusAway/go-sdk` (OBA REST client) and `github.com/OneBusAway/go-gtfs` (static + realtime parsing).
