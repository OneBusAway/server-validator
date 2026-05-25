# OBA Validator Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Build a Go library + CLI that validates a OneBusAway server by cross-referencing its REST API against the authoritative static GTFS and GTFS-realtime feeds, emitting Pass/Warn/Fail/Skip results.

**Architecture:** Two phases behind `validator.Run(ctx, cfg) → Report`. A *preparation* phase builds a shared `ValidationContext` (SDK client, fetched agency list, and per-data-source `SourceContext` with parsed feeds). A *check* phase runs small `ServerCheck` / `DataSourceCheck` units that read the context and emit `Result`s. Costly downloads happen once; static GTFS is cached with conditional GET, realtime feeds are always fetched fresh.

**Tech Stack:** Go 1.25, `github.com/OneBusAway/go-sdk` (REST client), `github.com/OneBusAway/go-gtfs` (static + realtime parsing). Stdlib `net/http`, `net/http/httptest` for tests.

**Spec:** `docs/superpowers/specs/2026-05-24-oba-validator-design.md`

---

## Decomposition notes (read before starting)

Two deliberate deviations from the spec's package-layout sketch, locked in here to avoid Go import cycles:

1. **Checks live as files inside the `validator` package**, not a separate `checks/` package. The orchestrator (`validator.Run`) owns the default check list, and checks need `ValidationContext`/`SourceContext`/`Result` — a separate package would create a `validator ⇄ checks` cycle. Each check is still its own focused file (`check_endpoints.go`, `check_agencies.go`, …).
2. **The `report` package exposes free functions** `report.WriteText(w, rep)` and `report.WriteJSON(w, rep)` taking a `validator.Report` (so `report → validator`, no cycle). The spec's `rep.WriteText(os.Stdout)` shorthand becomes `report.WriteText(os.Stdout, rep)`.

**Final package set (all under module `github.com/onebusaway/oba-validator`):**

```
config/                config.go         Config/DataSource, Load(pathOrJSON), defaults, env apiKey
feeds/                 cache.go          Cache: conditional-GET store (atomic write, TTL, per-key lock)
                       fetch.go          Fetcher: FetchStatic (cached) / FetchRealtime (fresh)
                       gtfs.go           ParsedStatic (+agency indexes) / ParseRealtime wrappers
validator/             status.go         Status enum (+ String/Glyph/MarshalJSON)
                       result.go         Result, Report (Worst/ExitCode)
                       idnorm.go         RawID / PrefixedID / IDMatch
                       util.go           redact(), deterministic sampling helpers
                       context.go        ValidationContext, SourceContext, check interfaces
                       validator.go      Run orchestrator + prepare()
                       check_endpoints.go      basic-endpoints (ServerCheck)
                       check_agencies.go       agency-union (ServerCheck)
                       check_gtfs_sanity.go    (DataSourceCheck)
                       check_freshness.go      (DataSourceCheck)
                       check_vehicles.go       vehicle-positions sampling (DataSourceCheck)
                       check_tripupdates.go    trip-update sampling (DataSourceCheck)
                       check_alerts.go         service-alert cross-ref (DataSourceCheck)
report/                report.go         WriteText / WriteJSON (take validator.Report)
cmd/oba-validator/     main.go           flag parsing → cfg overrides → Run → report → exit
```

**Verified external API (use exactly these — confirmed against source):**

- Client: `onebusaway.NewClient(option.WithAPIKey(k), option.WithBaseURL(u), option.WithMaxRetries(n), option.WithRequestTimeout(d), option.WithHTTPClient(c))`.
- Param helpers: `onebusaway.Float(float64)`, `onebusaway.Int(int64)`, `onebusaway.String(string)`, `onebusaway.Bool(bool)`.
- Methods & response field paths:
  - `client.CurrentTime.Get(ctx)` → `res.Data.Entry.Time int64` (epoch **ms**), `.ReadableTime`.
  - `client.AgenciesWithCoverage.List(ctx)` → `res.Data.List[i].AgencyID string`; names via `res.Data.References.Agencies[j].{ID,Name}`.
  - `client.RoutesForAgency.List(ctx, agencyID)` → `res.Data.List[i].ID string`.
  - `client.StopsForRoute.List(ctx, routeID, onebusaway.StopsForRouteListParams{})` → `res.Data.Entry.RouteID string`, `res.Data.Entry.StopIDs []string`.
  - `client.Stop.Get(ctx, stopID)` → `res.Data.Entry.{ID string, Lat float64, Lon float64, Name string}`.
  - `client.StopsForLocation.List(ctx, onebusaway.StopsForLocationListParams{Lat:…, Lon:…})` → `res.Data.OutOfRange bool`, `res.Data.List []…`.
  - `client.ArrivalAndDeparture.List(ctx, stopID, onebusaway.ArrivalAndDepartureListParams{})` → `res.Data.Entry.ArrivalsAndDepartures[k].{StopID, TripID, VehicleID, RouteID, SituationIDs []string}`. **No entry-level stopId.**
  - `client.VehiclesForAgency.List(ctx, agencyID, onebusaway.VehiclesForAgencyListParams{})` → `res.Data.List[i].{VehicleID string, TripID string}`.
  - `client.TripForVehicle.Get(ctx, vehicleID, onebusaway.TripForVehicleGetParams{})` → `res.Data.Entry.TripID string`.
  - `client.TripsForLocation.List(ctx, onebusaway.TripsForLocationListParams{Lat,LatSpan,Lon,LonSpan})` → `res.Data.List[i].{TripID string, Status.VehicleID string}`. **LatSpan/LonSpan required; no Radius.**
- go-gtfs: `gtfs.ParseStatic(b []byte, gtfs.ParseStaticOptions{}) (*gtfs.Static, error)`; `gtfs.ParseRealtime(b []byte, &gtfs.ParseRealtimeOptions{Timezone: time.UTC}) (*gtfs.Realtime, error)`.
  - `gtfs.Static{Agencies []Agency, Routes []Route, Stops []Stop, Trips []ScheduledTrip}`; `Agency{Id, Name string}`; `Route{Id string, Agency *Agency}`; `ScheduledTrip{ID string, Route *Route}`; `Stop{Id string, Latitude, Longitude *float64}`.
  - `gtfs.Realtime{CreatedAt time.Time, Vehicles []Vehicle, Trips []Trip, Alerts []Alert}`; `Vehicle{ID *VehicleID, Trip *Trip, Position *Position}`; `VehicleID{ID, Label, LicensePlate string}`; `Position{Latitude, Longitude *float32}`; `Trip{ID TripID, StopTimeUpdates []StopTimeUpdate}`; `TripID{ID, RouteID string}`; `StopTimeUpdate{StopID *string, Arrival, Departure *StopTimeEvent}`; `StopTimeEvent{Time *time.Time}`; `Alert{ID string, InformedEntities []AlertInformedEntity}`; `AlertInformedEntity{AgencyID, RouteID, StopID *string, TripID *TripID}`.

---

## Task 1: Project dependencies & build sanity

**Files:**
- Modify: `go.mod`
- Create: `doc.go`

- [ ] **Step 1: Add dependencies**

Run:
```bash
cd /Users/aaron/repos/onebusaway/oba-validator
go get github.com/OneBusAway/go-sdk@latest
go get github.com/OneBusAway/go-gtfs@latest
```
Expected: `go.mod`/`go.sum` updated with both modules (go-gtfs pulls `google.golang.org/protobuf` transitively).

- [ ] **Step 2: Add a package doc file so the module compiles with no other code yet**

Create `doc.go`:
```go
// Package obavalidator is the module root for the OneBusAway server validator.
//
// The validation logic lives in the validator package; see cmd/oba-validator
// for the CLI.
package obavalidator
```

- [ ] **Step 3: Verify it builds**

Run: `go build ./...`
Expected: success, no output.

- [ ] **Step 4: Commit**

```bash
git add go.mod go.sum doc.go
git commit -m "chore: add go-sdk and go-gtfs dependencies"
```

---

## Task 2: Status type

**Files:**
- Create: `validator/status.go`
- Test: `validator/status_test.go`

- [ ] **Step 1: Write the failing test**

`validator/status_test.go`:
```go
package validator

import (
	"encoding/json"
	"testing"
)

func TestStatusStringAndGlyph(t *testing.T) {
	cases := []struct {
		s     Status
		str   string
		glyph string
	}{
		{Pass, "PASS", "✓"},
		{Warn, "WARN", "⚠"},
		{Fail, "FAIL", "✗"},
		{Skip, "SKIP", "–"},
	}
	for _, c := range cases {
		if got := c.s.String(); got != c.str {
			t.Errorf("String()=%q want %q", got, c.str)
		}
		if got := c.s.Glyph(); got != c.glyph {
			t.Errorf("Glyph()=%q want %q", got, c.glyph)
		}
	}
}

func TestStatusMarshalJSON(t *testing.T) {
	b, err := json.Marshal(Fail)
	if err != nil {
		t.Fatal(err)
	}
	if string(b) != `"FAIL"` {
		t.Errorf("MarshalJSON=%s want \"FAIL\"", b)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./validator/ -run TestStatus -v`
Expected: FAIL — `undefined: Status` / `undefined: Pass`.

- [ ] **Step 3: Write the implementation**

`validator/status.go`:
```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./validator/ -run TestStatus -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add validator/status.go validator/status_test.go
git commit -m "feat: add validator Status type"
```

---

## Task 3: Result and Report

**Files:**
- Create: `validator/result.go`
- Test: `validator/result_test.go`

- [ ] **Step 1: Write the failing test**

`validator/result_test.go`:
```go
package validator

import "testing"

func TestReportWorstAndExitCode(t *testing.T) {
	cases := []struct {
		name     string
		statuses []Status
		worst    Status
		exit     int
	}{
		{"all pass", []Status{Pass, Pass}, Pass, 0},
		{"warn only", []Status{Pass, Warn, Skip}, Warn, 0},
		{"any fail", []Status{Pass, Warn, Fail}, Fail, 1},
		{"empty", nil, Pass, 0},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			var r Report
			for _, s := range c.statuses {
				r.Results = append(r.Results, Result{Status: s})
			}
			if got := r.Worst(); got != c.worst {
				t.Errorf("Worst()=%v want %v", got, c.worst)
			}
			if got := r.ExitCode(); got != c.exit {
				t.Errorf("ExitCode()=%d want %d", got, c.exit)
			}
		})
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./validator/ -run TestReport -v`
Expected: FAIL — `undefined: Report` / `undefined: Result`.

- [ ] **Step 3: Write the implementation**

`validator/result.go`:
```go
package validator

// Result is the outcome of one check (or one step of a check).
type Result struct {
	Check   string         `json:"check"`
	Source  string         `json:"source,omitempty"` // data source label; empty for server-level
	Status  Status         `json:"status"`
	Message string         `json:"message"`
	Details map[string]any `json:"details,omitempty"`
}

// Report aggregates every Result from a validation run.
type Report struct {
	Results []Result `json:"results"`
}

// Worst returns the most severe status in the report (Fail > Warn > Pass).
// Skip never outranks Pass.
func (r Report) Worst() Status {
	worst := Pass
	for _, res := range r.Results {
		switch res.Status {
		case Fail:
			return Fail
		case Warn:
			if worst == Pass {
				worst = Warn
			}
		}
	}
	return worst
}

// ExitCode is 1 if any result failed, else 0.
func (r Report) ExitCode() int {
	if r.Worst() == Fail {
		return 1
	}
	return 0
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./validator/ -run TestReport -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add validator/result.go validator/result_test.go
git commit -m "feat: add Result and Report types"
```

---

## Task 4: ID normalization

**Files:**
- Create: `validator/idnorm.go`
- Test: `validator/idnorm_test.go`

- [ ] **Step 1: Write the failing test**

`validator/idnorm_test.go`:
```go
package validator

import "testing"

func TestRawID(t *testing.T) {
	cases := map[string]string{
		"1_4567":      "4567",
		"40_12_34":    "12_34", // split on FIRST underscore only
		"noprefix":    "noprefix",
		"_x":          "x",
	}
	for in, want := range cases {
		if got := RawID(in); got != want {
			t.Errorf("RawID(%q)=%q want %q", in, got, want)
		}
	}
}

func TestPrefixedID(t *testing.T) {
	if got := PrefixedID("1", "4567"); got != "1_4567" {
		t.Errorf("got %q", got)
	}
	if got := PrefixedID("", "4567"); got != "4567" { // blank agency id
		t.Errorf("blank agency: got %q", got)
	}
}

func TestIDMatch(t *testing.T) {
	cases := []struct {
		api, feed, agency string
		want              bool
	}{
		{"1_4567", "4567", "1", true},  // prefix strip
		{"1_4567", "4567", "", true},   // strip works without knowing agency
		{"4567", "4567", "", true},     // exact, no prefix
		{"1_4567", "9999", "1", false}, // genuine mismatch
		{"MTS_ab_cd", "ab_cd", "MTS", true},
	}
	for _, c := range cases {
		if got := IDMatch(c.api, c.feed, c.agency); got != c.want {
			t.Errorf("IDMatch(%q,%q,%q)=%v want %v", c.api, c.feed, c.agency, got, c.want)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./validator/ -run "TestRawID|TestPrefixedID|TestIDMatch" -v`
Expected: FAIL — `undefined: RawID`.

- [ ] **Step 3: Write the implementation**

`validator/idnorm.go`:
```go
package validator

import "strings"

// RawID strips an OBA agency prefix ("{agencyId}_") from an API id, returning
// the raw id. Splits on the first underscore only (raw ids may contain '_').
// An id with no underscore is returned unchanged.
func RawID(apiID string) string {
	if i := strings.IndexByte(apiID, '_'); i >= 0 {
		return apiID[i+1:]
	}
	return apiID
}

// PrefixedID builds an OBA API id "{agencyID}_{rawID}". A blank agencyID
// returns rawID unchanged (handles GTFS feeds with a blank agency_id).
func PrefixedID(agencyID, rawID string) string {
	if agencyID == "" {
		return rawID
	}
	return agencyID + "_" + rawID
}

// IDMatch reports whether an OBA API id refers to the same entity as a raw feed
// id, tolerant of the agency prefix. agencyID may be "" when unknown.
func IDMatch(apiID, rawFeedID, agencyID string) bool {
	if apiID == rawFeedID {
		return true
	}
	if RawID(apiID) == rawFeedID {
		return true
	}
	if agencyID != "" && apiID == PrefixedID(agencyID, rawFeedID) {
		return true
	}
	return false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./validator/ -run "TestRawID|TestPrefixedID|TestIDMatch" -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add validator/idnorm.go validator/idnorm_test.go
git commit -m "feat: add prefix-aware ID normalization"
```

---

## Task 5: Config loading

**Files:**
- Create: `config/config.go`
- Test: `config/config_test.go`

- [ ] **Step 1: Write the failing test**

`config/config_test.go`:
```go
package config

import (
	"os"
	"path/filepath"
	"testing"
)

const sampleJSON = `{
  "obaServerURL": "https://example.com",
  "apiKey": "k",
  "dataSources": [{"staticGtfsFeedURL":"https://s/gtfs.zip","agencyMapping":{"KCM":"1"}}]
}`

func TestLoadFromRawJSONAppliesDefaults(t *testing.T) {
	cfg, err := Load(sampleJSON)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.SampleSize != 3 || cfg.RTFreshnessSeconds != 300 || cfg.TimeoutSeconds != 120 {
		t.Errorf("defaults not applied: %+v", cfg)
	}
	if cfg.LocationSpan != 0.01 || cfg.MaxConcurrency != 4 {
		t.Errorf("span/concurrency defaults wrong: %+v", cfg)
	}
	if cfg.DataSources[0].AgencyMapping["KCM"] != "1" {
		t.Errorf("agencyMapping not parsed")
	}
}

func TestLoadFromFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "c.json")
	if err := os.WriteFile(p, []byte(sampleJSON), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OBAServerURL != "https://example.com" {
		t.Errorf("got %q", cfg.OBAServerURL)
	}
}

func TestLoadAPIKeyFromEnv(t *testing.T) {
	t.Setenv("ONEBUSAWAY_API_KEY", "envkey")
	cfg, err := Load(`{"obaServerURL":"https://x","dataSources":[{"staticGtfsFeedURL":"u"}]}`)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.APIKey != "envkey" {
		t.Errorf("APIKey=%q want envkey", cfg.APIKey)
	}
}

func TestLoadErrors(t *testing.T) {
	if _, err := Load(`{bad json`); err == nil {
		t.Error("expected parse error")
	}
	if _, err := Load(`{"apiKey":"k","dataSources":[{}]}`); err == nil {
		t.Error("expected missing obaServerURL error")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./config/ -v`
Expected: FAIL — `undefined: Load`.

- [ ] **Step 3: Write the implementation**

`config/config.go`:
```go
package config

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

// DataSource is one operator's set of feeds plus its agency remap.
type DataSource struct {
	StaticGtfsFeedURL   string            `json:"staticGtfsFeedURL"`
	VehiclePositionsURL string            `json:"vehiclePositionsURL"`
	TripUpdatesURL      string            `json:"tripUpdatesURL"`
	ServiceAlertsURL    string            `json:"serviceAlertsURL"`
	AgencyMapping       map[string]string `json:"agencyMapping"`
}

// Config is the full validator configuration. Runtime-only fields (NoCache,
// Refresh) are set by the CLI, not normally present in the JSON.
type Config struct {
	OBAServerURL       string       `json:"obaServerURL"`
	APIKey             string       `json:"apiKey"`
	DataSources        []DataSource `json:"dataSources"`
	SampleSize         int          `json:"sampleSize"`
	RTFreshnessSeconds int          `json:"rtFreshnessSeconds"`
	LocationSpan       float64      `json:"locationSpan"`
	MaxConcurrency     int          `json:"maxConcurrency"`
	TimeoutSeconds     int          `json:"timeoutSeconds"`
	CacheDir           string       `json:"cacheDir"`
	NoCache            bool         `json:"-"`
	Refresh            bool         `json:"-"`
}

// Load reads config from a file path or a raw JSON string (auto-detected by a
// leading '{'). Applies defaults and validates required fields.
func Load(pathOrJSON string) (Config, error) {
	var raw []byte
	if strings.HasPrefix(strings.TrimSpace(pathOrJSON), "{") {
		raw = []byte(pathOrJSON)
	} else {
		b, err := os.ReadFile(pathOrJSON)
		if err != nil {
			return Config{}, fmt.Errorf("reading config file: %w", err)
		}
		raw = b
	}
	var cfg Config
	if err := json.Unmarshal(raw, &cfg); err != nil {
		return Config{}, fmt.Errorf("parsing config JSON: %w", err)
	}
	cfg.applyDefaults()
	if err := cfg.validate(); err != nil {
		return Config{}, err
	}
	return cfg, nil
}

func (c *Config) applyDefaults() {
	if c.APIKey == "" {
		c.APIKey = os.Getenv("ONEBUSAWAY_API_KEY")
	}
	if c.SampleSize == 0 {
		c.SampleSize = 3
	}
	if c.RTFreshnessSeconds == 0 {
		c.RTFreshnessSeconds = 300
	}
	if c.LocationSpan == 0 {
		c.LocationSpan = 0.01
	}
	if c.MaxConcurrency == 0 {
		c.MaxConcurrency = 4
	}
	if c.TimeoutSeconds == 0 {
		c.TimeoutSeconds = 120
	}
}

func (c Config) validate() error {
	if c.OBAServerURL == "" {
		return fmt.Errorf("obaServerURL is required")
	}
	if c.APIKey == "" {
		return fmt.Errorf("apiKey is required (set in config or ONEBUSAWAY_API_KEY)")
	}
	if len(c.DataSources) == 0 {
		return fmt.Errorf("at least one dataSource is required")
	}
	return nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./config/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add config/config.go config/config_test.go
git commit -m "feat: add config loading with defaults and env apiKey"
```

---

## Task 6: Feed cache

**Files:**
- Create: `feeds/cache.go`
- Test: `feeds/cache_test.go`

- [ ] **Step 1: Write the failing test**

`feeds/cache_test.go`:
```go
package feeds

import (
	"os"
	"testing"
	"time"
)

func TestCacheStoreAndLoad(t *testing.T) {
	c := NewCache(t.TempDir(), time.Hour)
	k := key("https://x/gtfs.zip")
	if err := c.store(k, []byte("BODY"), cacheMeta{URL: "u", ETag: "e", FetchedAt: time.Now()}); err != nil {
		t.Fatal(err)
	}
	body, meta, ok := c.load(k)
	if !ok || string(body) != "BODY" || meta.ETag != "e" {
		t.Fatalf("load got ok=%v body=%q meta=%+v", ok, body, meta)
	}
}

func TestCacheBodyWithoutMetaIsMiss(t *testing.T) {
	dir := t.TempDir()
	c := NewCache(dir, time.Hour)
	k := key("u")
	if err := os.WriteFile(c.bodyPath(k), []byte("orphan"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, _, ok := c.load(k); ok {
		t.Error("orphan body should be a miss")
	}
}

func TestMetaFreshOnlyWithoutValidators(t *testing.T) {
	withValidator := &cacheMeta{ETag: "e", FetchedAt: time.Now()}
	if withValidator.fresh(time.Hour) {
		t.Error("entry with ETag must not be TTL-fresh")
	}
	noValidator := &cacheMeta{FetchedAt: time.Now()}
	if !noValidator.fresh(time.Hour) {
		t.Error("recent no-validator entry should be fresh")
	}
	stale := &cacheMeta{FetchedAt: time.Now().Add(-2 * time.Hour)}
	if stale.fresh(time.Hour) {
		t.Error("old no-validator entry should be stale")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./feeds/ -run TestCache -v`
Expected: FAIL — `undefined: NewCache`.

- [ ] **Step 3: Write the implementation**

`feeds/cache.go`:
```go
package feeds

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type cacheMeta struct {
	URL          string    `json:"url"`
	ETag         string    `json:"etag"`
	LastModified string    `json:"lastModified"`
	FetchedAt    time.Time `json:"fetchedAt"`
}

// fresh reports whether a *validator-less* entry is still within ttl. Entries
// that carry an ETag/Last-Modified are always revalidated, never TTL-served.
func (m *cacheMeta) fresh(ttl time.Duration) bool {
	return m.ETag == "" && m.LastModified == "" && time.Since(m.FetchedAt) < ttl
}

// Cache is an on-disk conditional-GET cache for static feeds.
type Cache struct {
	dir   string
	ttl   time.Duration
	locks sync.Map // key -> *sync.Mutex
}

func NewCache(dir string, ttl time.Duration) *Cache {
	return &Cache{dir: dir, ttl: ttl}
}

func key(url string) string {
	sum := sha256.Sum256([]byte(url))
	return hex.EncodeToString(sum[:])
}

func (c *Cache) lockFor(k string) *sync.Mutex {
	m, _ := c.locks.LoadOrStore(k, &sync.Mutex{})
	return m.(*sync.Mutex)
}

func (c *Cache) bodyPath(k string) string { return filepath.Join(c.dir, k+".body") }
func (c *Cache) metaPath(k string) string { return filepath.Join(c.dir, k+".meta.json") }

// load returns the entry only if BOTH body and parseable meta exist; a body
// without valid meta (e.g. truncated write) is reported as a miss.
func (c *Cache) load(k string) ([]byte, *cacheMeta, bool) {
	body, err := os.ReadFile(c.bodyPath(k))
	if err != nil {
		return nil, nil, false
	}
	mb, err := os.ReadFile(c.metaPath(k))
	if err != nil {
		return nil, nil, false
	}
	var m cacheMeta
	if err := json.Unmarshal(mb, &m); err != nil {
		return nil, nil, false
	}
	return body, &m, true
}

// store writes body via temp-file + atomic rename, THEN writes meta, so a crash
// can never leave a valid meta pointing at a partial body.
func (c *Cache) store(k string, body []byte, m cacheMeta) error {
	if err := os.MkdirAll(c.dir, 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(c.dir, k+".tmp.*")
	if err != nil {
		return err
	}
	tmpName := tmp.Name()
	if _, err := tmp.Write(body); err != nil {
		tmp.Close()
		os.Remove(tmpName)
		return err
	}
	if err := tmp.Close(); err != nil {
		os.Remove(tmpName)
		return err
	}
	if err := os.Rename(tmpName, c.bodyPath(k)); err != nil {
		os.Remove(tmpName)
		return err
	}
	mb, err := json.Marshal(m)
	if err != nil {
		return err
	}
	return os.WriteFile(c.metaPath(k), mb, 0o644)
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./feeds/ -run TestCache -v` and `go test ./feeds/ -run TestMetaFresh -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add feeds/cache.go feeds/cache_test.go
git commit -m "feat: add on-disk conditional-GET feed cache"
```

---

## Task 7: Feed fetcher

**Files:**
- Create: `feeds/fetch.go`
- Test: `feeds/fetch_test.go`

- [ ] **Step 1: Write the failing test**

`feeds/fetch_test.go`:
```go
package feeds

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestFetchRealtimeAlwaysHitsNetwork(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		w.Write([]byte("rt"))
	}))
	defer srv.Close()
	f := NewFetcher(srv.Client(), NewCache(t.TempDir(), time.Hour), false, false)
	for i := 0; i < 2; i++ {
		b, err := f.FetchRealtime(context.Background(), srv.URL)
		if err != nil || string(b) != "rt" {
			t.Fatalf("got %q err %v", b, err)
		}
	}
	if hits != 2 {
		t.Errorf("realtime hits=%d want 2 (never cached)", hits)
	}
}

func TestFetchStaticUsesConditionalGET(t *testing.T) {
	var hits int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		atomic.AddInt32(&hits, 1)
		if r.Header.Get("If-None-Match") == "v1" {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", "v1")
		w.Write([]byte("ZIPDATA"))
	}))
	defer srv.Close()
	f := NewFetcher(srv.Client(), NewCache(t.TempDir(), time.Hour), false, false)

	b1, err := f.FetchStatic(context.Background(), srv.URL)
	if err != nil || string(b1) != "ZIPDATA" {
		t.Fatalf("first fetch %q err %v", b1, err)
	}
	// Second fetch: server replies 304, fetcher returns cached body.
	b2, err := f.FetchStatic(context.Background(), srv.URL)
	if err != nil || string(b2) != "ZIPDATA" {
		t.Fatalf("second fetch %q err %v", b2, err)
	}
	if hits != 2 {
		t.Errorf("static hits=%d want 2 (a 200 then a 304)", hits)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./feeds/ -run TestFetch -v`
Expected: FAIL — `undefined: NewFetcher`.

- [ ] **Step 3: Write the implementation**

`feeds/fetch.go`:
```go
package feeds

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"time"
)

// Fetcher downloads feeds. Static feeds go through the conditional-GET Cache;
// realtime feeds are always fetched fresh.
type Fetcher struct {
	http    *http.Client
	cache   *Cache
	noCache bool
	refresh bool
}

func NewFetcher(httpClient *http.Client, cache *Cache, noCache, refresh bool) *Fetcher {
	return &Fetcher{http: httpClient, cache: cache, noCache: noCache, refresh: refresh}
}

func (f *Fetcher) get(ctx context.Context, url string, hdr http.Header) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	for k, vs := range hdr {
		for _, v := range vs {
			req.Header.Add(k, v)
		}
	}
	return f.http.Do(req)
}

// FetchRealtime always performs a fresh GET (no caching).
func (f *Fetcher) FetchRealtime(ctx context.Context, url string) ([]byte, error) {
	resp, err := f.get(ctx, url, nil)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	return io.ReadAll(resp.Body)
}

// FetchStatic returns the static feed bytes, using the conditional-GET cache.
func (f *Fetcher) FetchStatic(ctx context.Context, url string) ([]byte, error) {
	if f.noCache || f.cache == nil {
		return f.FetchRealtime(ctx, url)
	}
	k := key(url)
	mu := f.cache.lockFor(k)
	mu.Lock()
	defer mu.Unlock()

	body, meta, hit := f.cache.load(k)
	if hit && !f.refresh && meta.fresh(f.cache.ttl) {
		return body, nil
	}

	hdr := http.Header{}
	if hit && !f.refresh {
		if meta.ETag != "" {
			hdr.Set("If-None-Match", meta.ETag)
		}
		if meta.LastModified != "" {
			hdr.Set("If-Modified-Since", meta.LastModified)
		}
	}
	resp, err := f.get(ctx, url, hdr)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified && hit {
		return body, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GET %s: status %d", url, resp.StatusCode)
	}
	newBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	// Cache writes are best-effort: a write failure must not fail the fetch,
	// because we already hold valid bytes. (No logger in this package.)
	_ = f.cache.store(k, newBody, cacheMeta{
		URL:          url,
		ETag:         resp.Header.Get("ETag"),
		LastModified: resp.Header.Get("Last-Modified"),
		FetchedAt:    time.Now(),
	})
	return newBody, nil
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./feeds/ -run TestFetch -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add feeds/fetch.go feeds/fetch_test.go
git commit -m "feat: add feed fetcher (cached static, fresh realtime)"
```

---

## Task 8: GTFS parse wrappers + agency indexes

**Files:**
- Create: `feeds/gtfs.go`
- Test: `feeds/gtfs_test.go`

This wraps go-gtfs and precomputes the trip→agency / route→agency maps the vehicle-sampling check needs. The test builds a tiny static GTFS zip in memory.

- [ ] **Step 1: Write the failing test**

`feeds/gtfs_test.go`:
```go
package feeds

import (
	"archive/zip"
	"bytes"
	"testing"
)

// buildZip writes a minimal GTFS zip with the given files.
func buildZip(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for name, content := range files {
		w, err := zw.Create(name)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(content)); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func TestParseStaticIndexes(t *testing.T) {
	zip := buildZip(t, map[string]string{
		"agency.txt":     "agency_id,agency_name,agency_url,agency_timezone\nKCM,Metro Transit,https://kcm,America/Los_Angeles\n",
		"routes.txt":     "route_id,agency_id,route_short_name,route_long_name,route_type\nR1,KCM,1,One,3\n",
		"trips.txt":      "route_id,service_id,trip_id\nR1,S1,T1\n",
		"stops.txt":      "stop_id,stop_name,stop_lat,stop_lon\nST1,Stop 1,47.6,-122.3\n",
		"calendar.txt":   "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\nS1,1,1,1,1,1,0,0,20240101,20251231\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\nT1,08:00:00,08:00:00,ST1,1\n",
	})
	p, err := ParseStatic(zip)
	if err != nil {
		t.Fatal(err)
	}
	if len(p.AgencyIDs) != 1 || p.AgencyIDs[0] != "KCM" {
		t.Errorf("AgencyIDs=%v", p.AgencyIDs)
	}
	if p.AgencyNames["KCM"] != "Metro Transit" {
		t.Errorf("name=%q", p.AgencyNames["KCM"])
	}
	if a, ok := p.AgencyForTrip("T1"); !ok || a != "KCM" {
		t.Errorf("AgencyForTrip=%q,%v", a, ok)
	}
	if a, ok := p.AgencyForRoute("R1"); !ok || a != "KCM" {
		t.Errorf("AgencyForRoute=%q,%v", a, ok)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./feeds/ -run TestParseStatic -v`
Expected: FAIL — `undefined: ParseStatic`.

- [ ] **Step 3: Write the implementation**

`feeds/gtfs.go`:
```go
package feeds

import (
	"sort"
	"time"

	gtfs "github.com/OneBusAway/go-gtfs"
)

// ParsedStatic wraps a parsed static GTFS feed with lookup indexes used by the
// validator checks.
type ParsedStatic struct {
	Static      *gtfs.Static
	AgencyIDs   []string          // sorted, unique agency ids
	AgencyNames map[string]string // agency id -> name
	tripAgency  map[string]string // raw trip id -> agency id
	routeAgency map[string]string // raw route id -> agency id
}

// ParseStatic parses a GTFS zip (bytes) and builds the agency indexes.
func ParseStatic(b []byte) (*ParsedStatic, error) {
	s, err := gtfs.ParseStatic(b, gtfs.ParseStaticOptions{})
	if err != nil {
		return nil, err
	}
	p := &ParsedStatic{
		Static:      s,
		AgencyNames: map[string]string{},
		tripAgency:  map[string]string{},
		routeAgency: map[string]string{},
	}
	seen := map[string]bool{}
	for i := range s.Agencies {
		a := &s.Agencies[i]
		p.AgencyNames[a.Id] = a.Name
		if !seen[a.Id] {
			seen[a.Id] = true
			p.AgencyIDs = append(p.AgencyIDs, a.Id)
		}
	}
	sort.Strings(p.AgencyIDs)
	for i := range s.Routes {
		r := &s.Routes[i]
		if r.Agency != nil {
			p.routeAgency[r.Id] = r.Agency.Id
		}
	}
	for i := range s.Trips {
		tr := &s.Trips[i]
		if tr.Route != nil && tr.Route.Agency != nil {
			p.tripAgency[tr.ID] = tr.Route.Agency.Id
		}
	}
	return p, nil
}

// AgencyForTrip returns the GTFS agency id owning a raw trip id.
func (p *ParsedStatic) AgencyForTrip(tripID string) (string, bool) {
	a, ok := p.tripAgency[tripID]
	return a, ok
}

// AgencyForRoute returns the GTFS agency id owning a raw route id.
func (p *ParsedStatic) AgencyForRoute(routeID string) (string, bool) {
	a, ok := p.routeAgency[routeID]
	return a, ok
}

// ParseRealtime parses a GTFS-realtime feed, interpreting timestamps as UTC.
func ParseRealtime(b []byte) (*gtfs.Realtime, error) {
	return gtfs.ParseRealtime(b, &gtfs.ParseRealtimeOptions{Timezone: time.UTC})
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./feeds/ -run TestParseStatic -v`
Expected: PASS. (If go-gtfs requires additional mandatory files, the error message will name them — add minimal versions to `buildZip`.)

- [ ] **Step 5: Commit**

```bash
git add feeds/gtfs.go feeds/gtfs_test.go
git commit -m "feat: add GTFS static/realtime parse wrappers with agency indexes"
```

---

## Task 9: Validation context, interfaces, and utilities

**Files:**
- Create: `validator/context.go`
- Create: `validator/util.go`
- Test: `validator/util_test.go`

- [ ] **Step 1: Write the failing test**

`validator/util_test.go`:
```go
package validator

import (
	"errors"
	"testing"
)

func TestRedact(t *testing.T) {
	err := errors.New("GET https://x?key=SECRET failed")
	got := redact(err, "SECRET")
	if got != "GET https://x?key=*** failed" {
		t.Errorf("redact=%q", got)
	}
	if redact(nil, "SECRET") != "" {
		t.Error("nil error should redact to empty string")
	}
}

func TestSampleByID(t *testing.T) {
	ids := []string{"c", "a", "b", "d"}
	got := sampleByID(ids, 2, func(s string) string { return s })
	if len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Errorf("sampleByID=%v want [a b] (sorted, first 2)", got)
	}
	// n larger than slice returns all.
	if all := sampleByID(ids, 99, func(s string) string { return s }); len(all) != 4 {
		t.Errorf("len=%d want 4", len(all))
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./validator/ -run "TestRedact|TestSampleByID" -v`
Expected: FAIL — `undefined: redact`.

- [ ] **Step 3: Write the implementations**

`validator/util.go`:
```go
package validator

import (
	"sort"
	"strings"
)

// redact removes the apiKey from an error string so secrets never reach output.
func redact(err error, apiKey string) string {
	if err == nil {
		return ""
	}
	s := err.Error()
	if apiKey != "" {
		s = strings.ReplaceAll(s, apiKey, "***")
	}
	return s
}

// sampleByID deterministically selects up to n items: it sorts by keyFn(item)
// and returns the first n, so repeated runs sample the same entities.
func sampleByID[T any](items []T, n int, keyFn func(T) string) []T {
	cp := make([]T, len(items))
	copy(cp, items)
	sort.SliceStable(cp, func(i, j int) bool { return keyFn(cp[i]) < keyFn(cp[j]) })
	if n < len(cp) {
		cp = cp[:n]
	}
	return cp
}
```

`validator/context.go`:
```go
package validator

import (
	"context"
	"sync"

	gtfs "github.com/OneBusAway/go-gtfs"
	onebusaway "github.com/OneBusAway/go-sdk"

	"github.com/onebusaway/oba-validator/config"
	"github.com/onebusaway/oba-validator/feeds"
)

// SourceContext holds one data source's prepared feeds and config.
type SourceContext struct {
	Index            int
	Label            string
	Config           config.DataSource
	Static           *feeds.ParsedStatic
	VehiclePositions *gtfs.Realtime
	TripUpdates      *gtfs.Realtime
	ServiceAlerts    *gtfs.Realtime

	mu         sync.Mutex
	PrepErrors map[string]error // feed name -> preparation error
}

// prepErr safely records a preparation error during concurrent fetching.
func (s *SourceContext) prepErr(feed string, err error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.PrepErrors[feed] = err
}

// MapAgency translates a GTFS agency id to its OBA agency id via the data
// source's agencyMapping. Returns (gtfsID, false) when unmapped (identity).
func (s *SourceContext) MapAgency(gtfsAgencyID string) (obaID string, mapped bool) {
	if v, ok := s.Config.AgencyMapping[gtfsAgencyID]; ok {
		return v, true
	}
	return gtfsAgencyID, false
}

// ValidationContext is the shared state for a validation run.
type ValidationContext struct {
	Config      config.Config
	Client      *onebusaway.Client
	Agencies    *onebusaway.AgenciesWithCoverageListResponse
	AgenciesErr error
	Sources     []*SourceContext
}

// ServerCheck runs once against the whole server.
type ServerCheck interface {
	Name() string
	Run(ctx context.Context, vc *ValidationContext) []Result
}

// DataSourceCheck runs once per data source.
type DataSourceCheck interface {
	Name() string
	Run(ctx context.Context, vc *ValidationContext, src *SourceContext) []Result
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./validator/ -run "TestRedact|TestSampleByID" -v`
Expected: PASS. Also run `go build ./...` (context.go must compile).

- [ ] **Step 5: Commit**

```bash
git add validator/context.go validator/util.go validator/util_test.go
git commit -m "feat: add ValidationContext, check interfaces, and utilities"
```

---

## Task 10: Basic-endpoints check

**Files:**
- Create: `validator/check_endpoints.go`
- Test: `validator/check_endpoints_test.go`

A port of `docker/bin/validate.sh`: a dependency chain where each step feeds the next; a failure marks the rest `Skip`. Note the arrivals step verifies via the per-arrival `StopID` (there is no entry-level stopId).

- [ ] **Step 1: Write the failing test**

`validator/check_endpoints_test.go`:
```go
package validator

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	onebusaway "github.com/OneBusAway/go-sdk"
	"github.com/OneBusAway/go-sdk/option"
)

// newTestClient returns an SDK client pointed at a handler. Because each test
// drives one endpoint chain, the handler dispatches on the request path.
func newTestClient(t *testing.T, h http.HandlerFunc) *onebusaway.Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return onebusaway.NewClient(option.WithAPIKey("test"), option.WithBaseURL(srv.URL))
}

func TestEndpointsCheckHappyPath(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.Contains(p, "current-time"):
			w.Write([]byte(`{"data":{"entry":{"time":1716000000000,"readableTime":"now"}}}`))
		case strings.Contains(p, "agencies-with-coverage"):
			w.Write([]byte(`{"data":{"list":[{"agencyId":"1"}],"references":{"agencies":[{"id":"1","name":"Metro"}]}}}`))
		case strings.Contains(p, "routes-for-agency"):
			w.Write([]byte(`{"data":{"list":[{"id":"1_R1","agencyId":"1"}]}}`))
		case strings.Contains(p, "stops-for-route"):
			w.Write([]byte(`{"data":{"entry":{"routeId":"1_R1","stopIds":["1_S1"]}}}`))
		case strings.Contains(p, "stops-for-location"):
			w.Write([]byte(`{"data":{"outOfRange":false,"list":[{"id":"1_S1"}]}}`))
		case strings.Contains(p, "arrivals-and-departures-for-stop"):
			w.Write([]byte(`{"data":{"entry":{"arrivalsAndDepartures":[{"stopId":"1_S1","tripId":"1_T1","vehicleId":"1_V1","routeId":"1_R1"}]}}}`))
		case strings.Contains(p, "/stop/"):
			w.Write([]byte(`{"data":{"entry":{"id":"1_S1","lat":47.6,"lon":-122.3,"name":"Stop"}}}`))
		default:
			t.Errorf("unexpected path %s", p)
		}
	})
	vc := &ValidationContext{Config: cfgForTest("test"), Client: client}

	results := endpointsCheck{}.Run(context.Background(), vc)
	for _, r := range results {
		if r.Status == Fail || r.Status == Skip {
			t.Errorf("%s: status %v msg %q", r.Check, r.Status, r.Message)
		}
	}
	if len(results) != 7 {
		t.Errorf("got %d results want 7", len(results))
	}
}

func TestEndpointsCheckCurrentTimeFailureSkipsRest(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	})
	vc := &ValidationContext{Config: cfgForTest("test"), Client: client}
	results := endpointsCheck{}.Run(context.Background(), vc)
	if results[0].Status != Fail {
		t.Errorf("first status %v want Fail", results[0].Status)
	}
	for _, r := range results[1:] {
		if r.Status != Skip {
			t.Errorf("%s status %v want Skip", r.Check, r.Status)
		}
	}
}
```

Add a shared test helper `cfgForTest` (used across check tests) in `validator/check_endpoints_test.go`:
```go
func cfgForTest(apiKey string) config.Config {
	return config.Config{APIKey: apiKey, SampleSize: 3, LocationSpan: 0.01, RTFreshnessSeconds: 300}
}
```
…with the import `"github.com/onebusaway/oba-validator/config"` added to the test file.

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./validator/ -run TestEndpointsCheck -v`
Expected: FAIL — `undefined: endpointsCheck`.

- [ ] **Step 3: Write the implementation**

`validator/check_endpoints.go`:
```go
package validator

import (
	"context"
	"fmt"
	"time"

	onebusaway "github.com/OneBusAway/go-sdk"
)

type endpointsCheck struct{}

func (endpointsCheck) Name() string { return "basic-endpoints" }

func (endpointsCheck) Run(ctx context.Context, vc *ValidationContext) []Result {
	key := vc.Config.APIKey
	var out []Result
	add := func(step string, st Status, msg string, det map[string]any) {
		out = append(out, Result{Check: "basic-endpoints/" + step, Status: st, Message: msg, Details: det})
	}
	remaining := []string{
		"agencies-with-coverage", "routes-for-agency", "stops-for-route",
		"stop", "stops-for-location", "arrivals-and-departures-for-stop",
	}
	skipRest := func(reason string) {
		for _, s := range remaining {
			add(s, Skip, "skipped: "+reason, nil)
		}
	}
	pop := func() { remaining = remaining[1:] }

	// 1. current-time
	ct, err := vc.Client.CurrentTime.Get(ctx)
	if err != nil {
		add("current-time", Fail, "current-time failed: "+redact(err, key), nil)
		skipRest("current-time failed")
		return out
	}
	skew := time.Now().UnixMilli() - ct.Data.Entry.Time
	if skew < 0 {
		skew = -skew
	}
	if skew > time.Hour.Milliseconds() {
		add("current-time", Warn, fmt.Sprintf("clock skew %dms", skew), map[string]any{"serverTimeMs": ct.Data.Entry.Time})
	} else {
		add("current-time", Pass, "current-time OK", nil)
	}

	// 2. agencies-with-coverage
	if vc.Agencies == nil || vc.AgenciesErr != nil {
		add("agencies-with-coverage", Fail, "agencies-with-coverage failed: "+redact(vc.AgenciesErr, key), nil)
		pop()
		skipRest("agencies-with-coverage failed")
		return out
	}
	if len(vc.Agencies.Data.List) == 0 {
		add("agencies-with-coverage", Fail, "no agencies returned", nil)
		pop()
		skipRest("no agencies")
		return out
	}
	agencyID := vc.Agencies.Data.List[0].AgencyID
	add("agencies-with-coverage", Pass, fmt.Sprintf("%d agencies", len(vc.Agencies.Data.List)), map[string]any{"agencyId": agencyID})
	pop()

	// 3. routes-for-agency
	routes, err := vc.Client.RoutesForAgency.List(ctx, agencyID)
	if err != nil || len(routes.Data.List) == 0 {
		add("routes-for-agency", Fail, "routes-for-agency empty/failed: "+redact(err, key), map[string]any{"agencyId": agencyID})
		pop()
		skipRest("routes-for-agency failed")
		return out
	}
	routeID := routes.Data.List[0].ID
	add("routes-for-agency", Pass, fmt.Sprintf("%d routes", len(routes.Data.List)), map[string]any{"routeId": routeID})
	pop()

	// 4. stops-for-route
	sfr, err := vc.Client.StopsForRoute.List(ctx, routeID, onebusaway.StopsForRouteListParams{})
	if err != nil || len(sfr.Data.Entry.StopIDs) == 0 {
		add("stops-for-route", Fail, "stops-for-route empty/failed: "+redact(err, key), map[string]any{"routeId": routeID})
		pop()
		skipRest("stops-for-route failed")
		return out
	}
	stopID := sfr.Data.Entry.StopIDs[0]
	add("stops-for-route", Pass, fmt.Sprintf("%d stops", len(sfr.Data.Entry.StopIDs)), map[string]any{"stopId": stopID})
	pop()

	// 5. stop
	st, err := vc.Client.Stop.Get(ctx, stopID)
	if err != nil || st.Data.Entry.ID != stopID {
		add("stop", Fail, "stop lookup failed/mismatch: "+redact(err, key), map[string]any{"stopId": stopID})
		pop()
		skipRest("stop failed")
		return out
	}
	lat, lon := st.Data.Entry.Lat, st.Data.Entry.Lon
	add("stop", Pass, "stop OK", map[string]any{"lat": lat, "lon": lon})
	pop()

	// 6. stops-for-location
	loc, err := vc.Client.StopsForLocation.List(ctx, onebusaway.StopsForLocationListParams{
		Lat: onebusaway.Float(lat),
		Lon: onebusaway.Float(lon),
	})
	if err != nil || loc.Data.OutOfRange || len(loc.Data.List) == 0 {
		add("stops-for-location", Fail, "stops-for-location empty/out-of-range/failed: "+redact(err, key), nil)
		pop()
		skipRest("stops-for-location failed")
		return out
	}
	add("stops-for-location", Pass, fmt.Sprintf("%d stops near", len(loc.Data.List)), nil)
	pop()

	// 7. arrivals-and-departures-for-stop
	ad, err := vc.Client.ArrivalAndDeparture.List(ctx, stopID, onebusaway.ArrivalAndDepartureListParams{})
	if err != nil {
		add("arrivals-and-departures-for-stop", Fail, "arrivals failed: "+redact(err, key), map[string]any{"stopId": stopID})
		return out
	}
	n := len(ad.Data.Entry.ArrivalsAndDepartures)
	if n == 0 {
		add("arrivals-and-departures-for-stop", Warn, "endpoint OK but no arrivals at this time", map[string]any{"stopId": stopID})
	} else {
		add("arrivals-and-departures-for-stop", Pass, fmt.Sprintf("%d arrivals/departures", n), map[string]any{"stopId": stopID})
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./validator/ -run TestEndpointsCheck -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add validator/check_endpoints.go validator/check_endpoints_test.go
git commit -m "feat: add basic-endpoints check (validate.sh port)"
```

---

## Task 11: Agency-union check

**Files:**
- Create: `validator/check_agencies.go`
- Test: `validator/check_agencies_test.go`

- [ ] **Step 1: Write the failing test**

`validator/check_agencies_test.go`:
```go
package validator

import (
	"context"
	"testing"

	onebusaway "github.com/OneBusAway/go-sdk"

	"github.com/onebusaway/oba-validator/config"
	"github.com/onebusaway/oba-validator/feeds"
)

// agenciesResp builds a fake agencies-with-coverage response.
func agenciesResp(ids map[string]string) *onebusaway.AgenciesWithCoverageListResponse {
	r := &onebusaway.AgenciesWithCoverageListResponse{}
	for id, name := range ids {
		r.Data.List = append(r.Data.List, onebusaway.AgenciesWithCoverageListResponseDataList{AgencyID: id})
		r.Data.References.Agencies = append(r.Data.References.Agencies, onebusaway.ReferencesAgency{ID: id, Name: name})
	}
	return r
}

// staticWith builds a ParsedStatic carrying just the agency indexes the check reads.
func staticWith(ids, names map[string]string) *feeds.ParsedStatic {
	p := &feeds.ParsedStatic{AgencyNames: map[string]string{}}
	for id := range ids {
		p.AgencyIDs = append(p.AgencyIDs, id)
		p.AgencyNames[id] = names[id]
	}
	return p
}

func sourceWith(mapping map[string]string, static *feeds.ParsedStatic) *SourceContext {
	return &SourceContext{Label: "ds0", Config: config.DataSource{AgencyMapping: mapping}, Static: static, PrepErrors: map[string]error{}}
}

func TestAgencyUnionMappedMatch(t *testing.T) {
	vc := &ValidationContext{
		Agencies: agenciesResp(map[string]string{"1": "Metro Transit"}),
		Sources:  []*SourceContext{sourceWith(map[string]string{"KCM": "1"}, staticWith(map[string]string{"KCM": ""}, map[string]string{"KCM": "Metro Transit"}))},
	}
	results := agencyUnionCheck{}.Run(context.Background(), vc)
	for _, r := range results {
		if r.Status != Pass {
			t.Errorf("expected Pass, got %v: %s", r.Status, r.Message)
		}
	}
}

func TestAgencyUnionMappedMissingFails(t *testing.T) {
	vc := &ValidationContext{
		Agencies: agenciesResp(map[string]string{"99": "Other"}),
		Sources:  []*SourceContext{sourceWith(map[string]string{"KCM": "1"}, staticWith(map[string]string{"KCM": ""}, map[string]string{"KCM": "Metro Transit"}))},
	}
	results := agencyUnionCheck{}.Run(context.Background(), vc)
	foundFail := false
	for _, r := range results {
		if r.Status == Fail {
			foundFail = true
		}
	}
	if !foundFail {
		t.Error("expected a Fail for mapped-but-missing agency")
	}
}

func TestAgencyUnionUnmappedMissingWarns(t *testing.T) {
	vc := &ValidationContext{
		Agencies: agenciesResp(map[string]string{"1": "Metro"}),
		Sources:  []*SourceContext{sourceWith(nil, staticWith(map[string]string{"KCM": ""}, map[string]string{"KCM": "Metro Transit"}))},
	}
	results := agencyUnionCheck{}.Run(context.Background(), vc)
	for _, r := range results {
		if r.Status == Fail {
			t.Errorf("unmapped-missing should Warn not Fail: %s", r.Message)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./validator/ -run TestAgencyUnion -v`
Expected: FAIL — `undefined: agencyUnionCheck`.

- [ ] **Step 3: Write the implementation**

`validator/check_agencies.go`:
```go
package validator

import (
	"context"
	"fmt"
)

type agencyUnionCheck struct{}

func (agencyUnionCheck) Name() string { return "agency-union" }

func (agencyUnionCheck) Run(ctx context.Context, vc *ValidationContext) []Result {
	const name = "agency-union"
	if vc.AgenciesErr != nil || vc.Agencies == nil {
		return []Result{{Check: name, Status: Fail, Message: "agencies-with-coverage unavailable: " + redact(vc.AgenciesErr, vc.Config.APIKey)}}
	}

	apiSet := map[string]bool{}
	for _, a := range vc.Agencies.Data.List {
		apiSet[a.AgencyID] = true
	}
	apiNamesByID := map[string]string{}
	for _, ra := range vc.Agencies.Data.References.Agencies {
		apiNamesByID[ra.ID] = ra.Name
	}

	type expected struct {
		obaID, gtfsID, name string
		mapped              bool
	}
	var exp []expected
	for _, src := range vc.Sources {
		if src.Static == nil {
			continue
		}
		for _, gid := range src.Static.AgencyIDs {
			oba, mapped := src.MapAgency(gid)
			exp = append(exp, expected{obaID: oba, gtfsID: gid, name: src.Static.AgencyNames[gid], mapped: mapped})
		}
	}

	var out []Result
	expectedSet := map[string]bool{}
	for _, e := range exp {
		expectedSet[e.obaID] = true
		if apiSet[e.obaID] {
			out = append(out, Result{Check: name, Status: Pass,
				Message: fmt.Sprintf("agency %q present in API as %q", e.gtfsID, e.obaID)})
			continue
		}
		det := map[string]any{"gtfsAgencyId": e.gtfsID, "expectedObaId": e.obaID}
		if hint := agencyHint(e.name, apiNamesByID, expectedSet); hint != "" {
			det["hint"] = hint
		}
		if e.mapped {
			out = append(out, Result{Check: name, Status: Fail,
				Message: fmt.Sprintf("mapped agency %q→%q not served by API", e.gtfsID, e.obaID), Details: det})
		} else {
			out = append(out, Result{Check: name, Status: Warn,
				Message: fmt.Sprintf("assumed identity mapping %q not served by API; add an agencyMapping entry if it is remapped", e.gtfsID), Details: det})
		}
	}

	for id := range apiSet {
		if !expectedSet[id] {
			out = append(out, Result{Check: name, Status: Warn,
				Message: fmt.Sprintf("API serves agency %q not present in any configured GTFS feed", id),
				Details: map[string]any{"apiAgencyId": id}})
		}
	}
	return out
}

// agencyHint suggests a mapping when an unmatched API agency shares the GTFS
// agency's name. Purely advisory; never affects pass/fail.
func agencyHint(gtfsName string, apiNamesByID map[string]string, expectedSet map[string]bool) string {
	if gtfsName == "" {
		return ""
	}
	for apiID, apiName := range apiNamesByID {
		if apiName == gtfsName && !expectedSet[apiID] {
			return fmt.Sprintf("API agency %q is named %q — did you mean to map it?", apiID, apiName)
		}
	}
	return ""
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./validator/ -run TestAgencyUnion -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add validator/check_agencies.go validator/check_agencies_test.go
git commit -m "feat: add agency-union check"
```

---

## Task 12: GTFS-sanity check

**Files:**
- Create: `validator/check_gtfs_sanity.go`
- Test: `validator/check_gtfs_sanity_test.go`

- [ ] **Step 1: Write the failing test**

`validator/check_gtfs_sanity_test.go`:
```go
package validator

import (
	"context"
	"testing"

	gtfs "github.com/OneBusAway/go-gtfs"

	"github.com/onebusaway/oba-validator/feeds"
)

func TestGtfsSanityPassAndFail(t *testing.T) {
	good := &SourceContext{Label: "ds0", PrepErrors: map[string]error{}, Static: &feeds.ParsedStatic{Static: &gtfs.Static{
		Agencies: []gtfs.Agency{{Id: "1"}},
		Routes:   []gtfs.Route{{Id: "R"}},
		Stops:    []gtfs.Stop{{Id: "S"}},
		Trips:    []gtfs.ScheduledTrip{{ID: "T"}},
	}}}
	for _, r := range (gtfsSanityCheck{}).Run(context.Background(), &ValidationContext{}, good) {
		if r.Status != Pass {
			t.Errorf("good feed: %v %s", r.Status, r.Message)
		}
	}

	empty := &SourceContext{Label: "ds1", PrepErrors: map[string]error{}, Static: &feeds.ParsedStatic{Static: &gtfs.Static{}}}
	res := gtfsSanityCheck{}.Run(context.Background(), &ValidationContext{}, empty)
	if res[0].Status != Fail {
		t.Errorf("empty feed should Fail, got %v", res[0].Status)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./validator/ -run TestGtfsSanity -v`
Expected: FAIL — `undefined: gtfsSanityCheck`.

- [ ] **Step 3: Write the implementation**

`validator/check_gtfs_sanity.go`:
```go
package validator

import (
	"context"
	"fmt"
)

type gtfsSanityCheck struct{}

func (gtfsSanityCheck) Name() string { return "gtfs-sanity" }

func (gtfsSanityCheck) Run(ctx context.Context, vc *ValidationContext, src *SourceContext) []Result {
	const name = "gtfs-sanity"
	if err := src.PrepErrors["staticGtfs"]; err != nil {
		return []Result{{Check: name, Source: src.Label, Status: Fail, Message: "static GTFS unavailable: " + redact(err, vc.Config.APIKey)}}
	}
	if src.Static == nil || src.Static.Static == nil {
		return []Result{{Check: name, Source: src.Label, Status: Fail, Message: "static GTFS not parsed"}}
	}
	s := src.Static.Static
	var missing []string
	if len(s.Agencies) == 0 {
		missing = append(missing, "agencies")
	}
	if len(s.Routes) == 0 {
		missing = append(missing, "routes")
	}
	if len(s.Stops) == 0 {
		missing = append(missing, "stops")
	}
	if len(s.Trips) == 0 {
		missing = append(missing, "trips")
	}
	if len(missing) > 0 {
		return []Result{{Check: name, Source: src.Label, Status: Fail,
			Message: fmt.Sprintf("static GTFS missing: %v", missing), Details: map[string]any{"missing": missing}}}
	}
	return []Result{{Check: name, Source: src.Label, Status: Pass,
		Message: fmt.Sprintf("%d agencies, %d routes, %d stops, %d trips", len(s.Agencies), len(s.Routes), len(s.Stops), len(s.Trips))}}
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./validator/ -run TestGtfsSanity -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add validator/check_gtfs_sanity.go validator/check_gtfs_sanity_test.go
git commit -m "feat: add GTFS-sanity check"
```

---

## Task 13: RT-freshness check

**Files:**
- Create: `validator/check_freshness.go`
- Test: `validator/check_freshness_test.go`

- [ ] **Step 1: Write the failing test**

`validator/check_freshness_test.go`:
```go
package validator

import (
	"context"
	"testing"
	"time"

	gtfs "github.com/OneBusAway/go-gtfs"
)

func TestFreshnessFreshStaleAndMissing(t *testing.T) {
	vc := &ValidationContext{Config: cfgForTest("k")}
	src := &SourceContext{
		Label:            "ds0",
		PrepErrors:       map[string]error{},
		VehiclePositions: &gtfs.Realtime{CreatedAt: time.Now()},
		TripUpdates:      &gtfs.Realtime{CreatedAt: time.Now().Add(-30 * time.Minute)},
		ServiceAlerts:    &gtfs.Realtime{}, // zero CreatedAt -> missing
	}
	byCheck := map[string]Status{}
	for _, r := range (freshnessCheck{}).Run(context.Background(), vc, src) {
		byCheck[r.Check] = r.Status
	}
	if byCheck["rt-freshness/vehiclePositions"] != Pass {
		t.Errorf("vehiclePositions want Pass got %v", byCheck["rt-freshness/vehiclePositions"])
	}
	if byCheck["rt-freshness/tripUpdates"] != Fail {
		t.Errorf("tripUpdates (stale) want Fail got %v", byCheck["rt-freshness/tripUpdates"])
	}
	if byCheck["rt-freshness/serviceAlerts"] != Warn {
		t.Errorf("serviceAlerts (missing) want Warn got %v", byCheck["rt-freshness/serviceAlerts"])
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./validator/ -run TestFreshness -v`
Expected: FAIL — `undefined: freshnessCheck`.

- [ ] **Step 3: Write the implementation**

`validator/check_freshness.go`:
```go
package validator

import (
	"context"
	"fmt"
	"time"

	gtfs "github.com/OneBusAway/go-gtfs"
)

type freshnessCheck struct{}

func (freshnessCheck) Name() string { return "rt-freshness" }

func (freshnessCheck) Run(ctx context.Context, vc *ValidationContext, src *SourceContext) []Result {
	maxAge := time.Duration(vc.Config.RTFreshnessSeconds) * time.Second
	feeds := []struct {
		name string
		rt   *gtfs.Realtime
		key  string // PrepErrors key
	}{
		{"vehiclePositions", src.VehiclePositions, "vehiclePositions"},
		{"tripUpdates", src.TripUpdates, "tripUpdates"},
		{"serviceAlerts", src.ServiceAlerts, "serviceAlerts"},
	}
	var out []Result
	for _, f := range feeds {
		check := "rt-freshness/" + f.name
		if err := src.PrepErrors[f.key]; err != nil {
			out = append(out, Result{Check: check, Source: src.Label, Status: Fail, Message: f.name + " unavailable: " + redact(err, vc.Config.APIKey)})
			continue
		}
		if f.rt == nil || f.rt.CreatedAt.IsZero() {
			out = append(out, Result{Check: check, Source: src.Label, Status: Warn, Message: f.name + " has no feed timestamp"})
			continue
		}
		age := time.Since(f.rt.CreatedAt)
		switch {
		case age > maxAge:
			out = append(out, Result{Check: check, Source: src.Label, Status: Fail,
				Message: fmt.Sprintf("%s stale by %s", f.name, age.Round(time.Second)), Details: map[string]any{"ageSeconds": int(age.Seconds())}})
		case age < -maxAge:
			out = append(out, Result{Check: check, Source: src.Label, Status: Warn,
				Message: fmt.Sprintf("%s timestamp is in the future (clock/timezone?)", f.name)})
		default:
			out = append(out, Result{Check: check, Source: src.Label, Status: Pass,
				Message: fmt.Sprintf("%s fresh (%s old)", f.name, age.Round(time.Second))})
		}
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./validator/ -run TestFreshness -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add validator/check_freshness.go validator/check_freshness_test.go
git commit -m "feat: add RT-freshness check"
```

---

## Task 14: Vehicle-positions sampling check

**Files:**
- Create: `validator/check_vehicles.go`
- Test: `validator/check_vehicles_test.go`

This is the largest check: agency resolution (trip→route→agency join), then three sub-checks (vehicles-for-agency, trip-for-vehicle, trips-for-location) per sampled vehicle, with graded severity per the spec.

- [ ] **Step 1: Write the failing test**

`validator/check_vehicles_test.go`:
```go
package validator

import (
	"context"
	"net/http"
	"strings"
	"testing"

	gtfs "github.com/OneBusAway/go-gtfs"

	"github.com/onebusaway/oba-validator/config"
	"github.com/onebusaway/oba-validator/feeds"
)

func f32(v float32) *float32 { return &v }
func strp(s string) *string  { return &s }

// staticForVehicle builds a ParsedStatic where trip T1 (route R1) belongs to KCM.
func staticForVehicle() *feeds.ParsedStatic {
	s := &gtfs.Static{
		Agencies: []gtfs.Agency{{Id: "KCM", Name: "Metro"}},
		Routes:   []gtfs.Route{{Id: "R1"}},
		Trips:    []gtfs.ScheduledTrip{{ID: "T1"}},
		Stops:    []gtfs.Stop{{Id: "ST1"}},
	}
	s.Routes[0].Agency = &s.Agencies[0]
	s.Trips[0].Route = &s.Routes[0]
	p, _ := feeds.ParseStaticFromStruct(s) // helper added below
	return p
}

func TestVehicleSamplingHappyPath(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.Contains(p, "vehicles-for-agency"):
			w.Write([]byte(`{"data":{"list":[{"vehicleId":"1_V1","tripId":"1_T1"}]}}`))
		case strings.Contains(p, "trip-for-vehicle"):
			w.Write([]byte(`{"data":{"entry":{"tripId":"1_T1"}}}`))
		case strings.Contains(p, "trips-for-location"):
			w.Write([]byte(`{"data":{"list":[{"tripId":"1_T1"}]}}`))
		default:
			t.Errorf("unexpected path %s", p)
		}
	})
	src := &SourceContext{
		Label:      "ds0",
		Config:     config.DataSource{AgencyMapping: map[string]string{"KCM": "1"}},
		PrepErrors: map[string]error{},
		Static:     staticForVehicle(),
		VehiclePositions: &gtfs.Realtime{Vehicles: []gtfs.Vehicle{{
			ID:       &gtfs.VehicleID{ID: "V1"},
			Trip:     &gtfs.Trip{ID: gtfs.TripID{ID: "T1", RouteID: "R1"}},
			Position: &gtfs.Position{Latitude: f32(47.6), Longitude: f32(-122.3)},
		}}},
	}
	vc := &ValidationContext{Config: cfgForTest("test"), Client: client}
	results := vehicleSamplingCheck{}.Run(context.Background(), vc, src)
	for _, r := range results {
		if r.Status == Fail {
			t.Errorf("%s Fail: %s", r.Check, r.Message)
		}
	}
	if len(results) == 0 {
		t.Fatal("expected sub-results")
	}
}

func TestVehicleSamplingUnresolvableAgencyWarns(t *testing.T) {
	src := &SourceContext{
		Label:      "ds0",
		Config:     config.DataSource{},
		PrepErrors: map[string]error{},
		Static:     staticForVehicle(),
		VehiclePositions: &gtfs.Realtime{Vehicles: []gtfs.Vehicle{{
			ID:   &gtfs.VehicleID{ID: "V9"},
			Trip: &gtfs.Trip{ID: gtfs.TripID{ID: "UNKNOWN", RouteID: "ALSO_UNKNOWN"}},
		}}},
	}
	vc := &ValidationContext{Config: cfgForTest("test")}
	results := vehicleSamplingCheck{}.Run(context.Background(), vc, src)
	if results[0].Status != Warn {
		t.Errorf("unresolvable agency want Warn got %v", results[0].Status)
	}
}
```

Also add a small test-only constructor to `feeds/gtfs.go` so tests can wrap a hand-built `*gtfs.Static` without zipping:
```go
// ParseStaticFromStruct builds a ParsedStatic from an already-constructed
// *gtfs.Static. Useful in tests; production code uses ParseStatic.
func ParseStaticFromStruct(s *gtfs.Static) (*ParsedStatic, error) {
	p := &ParsedStatic{Static: s, AgencyNames: map[string]string{}, tripAgency: map[string]string{}, routeAgency: map[string]string{}}
	seen := map[string]bool{}
	for i := range s.Agencies {
		a := &s.Agencies[i]
		p.AgencyNames[a.Id] = a.Name
		if !seen[a.Id] {
			seen[a.Id] = true
			p.AgencyIDs = append(p.AgencyIDs, a.Id)
		}
	}
	sort.Strings(p.AgencyIDs)
	for i := range s.Routes {
		r := &s.Routes[i]
		if r.Agency != nil {
			p.routeAgency[r.Id] = r.Agency.Id
		}
	}
	for i := range s.Trips {
		tr := &s.Trips[i]
		if tr.Route != nil && tr.Route.Agency != nil {
			p.tripAgency[tr.ID] = tr.Route.Agency.Id
		}
	}
	return p, nil
}
```
Refactor note: `ParseStatic` should delegate to `ParseStaticFromStruct` to avoid duplication — change `ParseStatic` to:
```go
func ParseStatic(b []byte) (*ParsedStatic, error) {
	s, err := gtfs.ParseStatic(b, gtfs.ParseStaticOptions{})
	if err != nil {
		return nil, err
	}
	return ParseStaticFromStruct(s)
}
```
Commit this refactor with Task 14 (it's test-driven by the new check test).

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./validator/ -run TestVehicleSampling -v`
Expected: FAIL — `undefined: vehicleSamplingCheck` (and `feeds.ParseStaticFromStruct`).

- [ ] **Step 3: Write the implementation**

First apply the `feeds/gtfs.go` refactor above (add `ParseStaticFromStruct`, delegate `ParseStatic`). Then:

`validator/check_vehicles.go`:
```go
package validator

import (
	"context"
	"fmt"

	gtfs "github.com/OneBusAway/go-gtfs"
	onebusaway "github.com/OneBusAway/go-sdk"
)

type vehicleSamplingCheck struct{}

func (vehicleSamplingCheck) Name() string { return "vehicle-positions-sampling" }

func (vehicleSamplingCheck) Run(ctx context.Context, vc *ValidationContext, src *SourceContext) []Result {
	const name = "vehicle-positions-sampling"
	key := vc.Config.APIKey
	if err := src.PrepErrors["vehiclePositions"]; err != nil {
		return []Result{{Check: name, Source: src.Label, Status: Fail, Message: "vehicle positions feed unavailable: " + redact(err, key)}}
	}
	if src.VehiclePositions == nil || len(src.VehiclePositions.Vehicles) == 0 {
		return []Result{{Check: name, Source: src.Label, Status: Warn, Message: "no vehicles in feed to sample"}}
	}

	// Prefer vehicles that have both a trip and a position, then sample
	// deterministically by raw vehicle id.
	var candidates []gtfs.Vehicle
	for _, v := range src.VehiclePositions.Vehicles {
		if v.Trip != nil && v.Position != nil {
			candidates = append(candidates, v)
		}
	}
	if len(candidates) == 0 {
		candidates = src.VehiclePositions.Vehicles
	}
	sample := sampleByID(candidates, vc.Config.SampleSize, func(v gtfs.Vehicle) string {
		if v.ID != nil {
			return v.ID.ID
		}
		return ""
	})

	var out []Result
	for _, v := range sample {
		rawVeh := ""
		label := ""
		if v.ID != nil {
			rawVeh, label = v.ID.ID, v.ID.Label
		}
		agency, ok := resolveVehicleAgency(src, v)
		if !ok {
			out = append(out, Result{Check: name, Source: src.Label, Status: Warn,
				Message: fmt.Sprintf("could not resolve agency for vehicle %q (trip/route not in static GTFS)", rawVeh),
				Details: map[string]any{"vehicleId": rawVeh}})
			continue
		}

		// (a) vehicles-for-agency
		vfa, err := vc.Client.VehiclesForAgency.List(ctx, agency, onebusaway.VehiclesForAgencyListParams{})
		switch {
		case err != nil:
			out = append(out, Result{Check: name + "/vehicles-for-agency", Source: src.Label, Status: Fail,
				Message: "vehicles-for-agency failed: " + redact(err, key), Details: map[string]any{"agencyId": agency}})
		case len(vfa.Data.List) == 0:
			out = append(out, Result{Check: name + "/vehicles-for-agency", Source: src.Label, Status: Fail,
				Message: fmt.Sprintf("vehicles-for-agency %q empty while feed has vehicles", agency)})
		default:
			matched := false
			for _, item := range vfa.Data.List {
				if IDMatch(item.VehicleID, rawVeh, agency) || (label != "" && IDMatch(item.VehicleID, label, agency)) {
					matched = true
					break
				}
			}
			if matched {
				out = append(out, Result{Check: name + "/vehicles-for-agency", Source: src.Label, Status: Pass,
					Message: fmt.Sprintf("vehicle %q present", rawVeh)})
			} else {
				out = append(out, Result{Check: name + "/vehicles-for-agency", Source: src.Label, Status: Warn,
					Message: fmt.Sprintf("vehicle %q not found among %d vehicles (possible id-convention mismatch)", rawVeh, len(vfa.Data.List)),
					Details: map[string]any{"vehicleId": rawVeh, "agencyId": agency}})
			}
		}

		// (b) trip-for-vehicle
		obaVeh := PrefixedID(agency, rawVeh)
		tfv, err := vc.Client.TripForVehicle.Get(ctx, obaVeh, onebusaway.TripForVehicleGetParams{})
		rawTrip := v.Trip.ID.ID
		switch {
		case err != nil:
			out = append(out, Result{Check: name + "/trip-for-vehicle", Source: src.Label, Status: Warn,
				Message: "trip-for-vehicle returned no current trip: " + redact(err, key), Details: map[string]any{"vehicleId": obaVeh}})
		case IDMatch(tfv.Data.Entry.TripID, rawTrip, agency):
			out = append(out, Result{Check: name + "/trip-for-vehicle", Source: src.Label, Status: Pass,
				Message: fmt.Sprintf("vehicle %q on expected trip %q", rawVeh, rawTrip)})
		default:
			out = append(out, Result{Check: name + "/trip-for-vehicle", Source: src.Label, Status: Fail,
				Message: fmt.Sprintf("trip-for-vehicle returned %q, feed says %q", tfv.Data.Entry.TripID, rawTrip),
				Details: map[string]any{"apiTripId": tfv.Data.Entry.TripID, "feedTripId": rawTrip}})
		}

		// (c) trips-for-location
		if v.Position == nil || v.Position.Latitude == nil || v.Position.Longitude == nil {
			out = append(out, Result{Check: name + "/trips-for-location", Source: src.Label, Status: Warn,
				Message: fmt.Sprintf("vehicle %q has no position to query", rawVeh)})
			continue
		}
		tfl, err := vc.Client.TripsForLocation.List(ctx, onebusaway.TripsForLocationListParams{
			Lat:     onebusaway.Float(float64(*v.Position.Latitude)),
			Lon:     onebusaway.Float(float64(*v.Position.Longitude)),
			LatSpan: onebusaway.Float(vc.Config.LocationSpan),
			LonSpan: onebusaway.Float(vc.Config.LocationSpan),
		})
		if err != nil {
			out = append(out, Result{Check: name + "/trips-for-location", Source: src.Label, Status: Warn,
				Message: "trips-for-location failed: " + redact(err, key)})
			continue
		}
		found := false
		for _, item := range tfl.Data.List {
			if IDMatch(item.TripID, rawTrip, agency) {
				found = true
				break
			}
		}
		if found {
			out = append(out, Result{Check: name + "/trips-for-location", Source: src.Label, Status: Pass,
				Message: fmt.Sprintf("trip %q present near vehicle", rawTrip)})
		} else {
			out = append(out, Result{Check: name + "/trips-for-location", Source: src.Label, Status: Warn,
				Message: fmt.Sprintf("trip %q not in trips-for-location box (vehicle may have moved)", rawTrip)})
		}
	}
	return out
}

// resolveVehicleAgency finds the OBA agency id for a feed vehicle via the static
// GTFS trip→route→agency linkage, then applies the agencyMapping.
func resolveVehicleAgency(src *SourceContext, v gtfs.Vehicle) (string, bool) {
	if v.Trip == nil || src.Static == nil {
		return "", false
	}
	if gid, ok := src.Static.AgencyForTrip(v.Trip.ID.ID); ok {
		oba, _ := src.MapAgency(gid)
		return oba, true
	}
	if v.Trip.ID.RouteID != "" {
		if gid, ok := src.Static.AgencyForRoute(v.Trip.ID.RouteID); ok {
			oba, _ := src.MapAgency(gid)
			return oba, true
		}
	}
	return "", false
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./validator/ -run TestVehicleSampling -v` and `go test ./feeds/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add validator/check_vehicles.go validator/check_vehicles_test.go feeds/gtfs.go
git commit -m "feat: add vehicle-positions sampling check"
```

---

## Task 15: Trip-update sampling check

**Files:**
- Create: `validator/check_tripupdates.go`
- Test: `validator/check_tripupdates_test.go`

- [ ] **Step 1: Write the failing test**

`validator/check_tripupdates_test.go`:
```go
package validator

import (
	"context"
	"net/http"
	"strings"
	"testing"

	gtfs "github.com/OneBusAway/go-gtfs"

	"github.com/onebusaway/oba-validator/config"
)

func TestTripUpdateSamplingFound(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "arrivals-and-departures-for-stop") {
			w.Write([]byte(`{"data":{"entry":{"arrivalsAndDepartures":[{"stopId":"1_ST1","tripId":"1_T1","vehicleId":"1_V1","routeId":"1_R1"}]}}}`))
			return
		}
		t.Errorf("unexpected path %s", r.URL.Path)
	})
	src := &SourceContext{
		Label:      "ds0",
		Config:     config.DataSource{AgencyMapping: map[string]string{"KCM": "1"}},
		PrepErrors: map[string]error{},
		Static:     staticForVehicle(), // trip T1 -> agency KCM
		TripUpdates: &gtfs.Realtime{Trips: []gtfs.Trip{{
			ID:              gtfs.TripID{ID: "T1", RouteID: "R1"},
			StopTimeUpdates: []gtfs.StopTimeUpdate{{StopID: strp("ST1"), Arrival: &gtfs.StopTimeEvent{}}},
		}}},
	}
	vc := &ValidationContext{Config: cfgForTest("test"), Client: client}
	results := tripUpdateSamplingCheck{}.Run(context.Background(), vc, src)
	if len(results) == 0 || results[0].Status != Pass {
		t.Errorf("want Pass, got %+v", results)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./validator/ -run TestTripUpdateSampling -v`
Expected: FAIL — `undefined: tripUpdateSamplingCheck`.

- [ ] **Step 3: Write the implementation**

`validator/check_tripupdates.go`:
```go
package validator

import (
	"context"
	"fmt"

	gtfs "github.com/OneBusAway/go-gtfs"
	onebusaway "github.com/OneBusAway/go-sdk"
)

type tripUpdateSamplingCheck struct{}

func (tripUpdateSamplingCheck) Name() string { return "trip-update-sampling" }

func (tripUpdateSamplingCheck) Run(ctx context.Context, vc *ValidationContext, src *SourceContext) []Result {
	const name = "trip-update-sampling"
	key := vc.Config.APIKey
	if err := src.PrepErrors["tripUpdates"]; err != nil {
		return []Result{{Check: name, Source: src.Label, Status: Fail, Message: "trip updates feed unavailable: " + redact(err, key)}}
	}
	if src.TripUpdates == nil || len(src.TripUpdates.Trips) == 0 {
		return []Result{{Check: name, Source: src.Label, Status: Warn, Message: "no trip updates in feed to sample"}}
	}

	// Keep only trips that have a usable stop-time-update with a prediction.
	var usable []gtfs.Trip
	for _, tr := range src.TripUpdates.Trips {
		for _, stu := range tr.StopTimeUpdates {
			if stu.StopID != nil && (stu.Arrival != nil || stu.Departure != nil) {
				usable = append(usable, tr)
				break
			}
		}
	}
	if len(usable) == 0 {
		return []Result{{Check: name, Source: src.Label, Status: Warn, Message: "no trip update has a predicted stop-time"}}
	}
	sample := sampleByID(usable, vc.Config.SampleSize, func(t gtfs.Trip) string { return t.ID.ID })

	var out []Result
	for _, tr := range sample {
		// Resolve agency for prefixing the stop id, via the trip's route.
		agency := ""
		if gid, ok := src.Static.AgencyForTrip(tr.ID.ID); ok {
			agency, _ = src.MapAgency(gid)
		} else if tr.ID.RouteID != "" {
			if gid, ok := src.Static.AgencyForRoute(tr.ID.RouteID); ok {
				agency, _ = src.MapAgency(gid)
			}
		}

		// Pick the first stop-time-update with a prediction.
		var rawStop string
		for _, stu := range tr.StopTimeUpdates {
			if stu.StopID != nil && (stu.Arrival != nil || stu.Departure != nil) {
				rawStop = *stu.StopID
				break
			}
		}
		obaStop := PrefixedID(agency, rawStop)

		ad, err := vc.Client.ArrivalAndDeparture.List(ctx, obaStop, onebusaway.ArrivalAndDepartureListParams{})
		if err != nil {
			out = append(out, Result{Check: name, Source: src.Label, Status: Fail,
				Message: fmt.Sprintf("arrivals-and-departures-for-stop %q failed: %s", obaStop, redact(err, key))})
			continue
		}
		found := false
		for _, ad := range ad.Data.Entry.ArrivalsAndDepartures {
			if IDMatch(ad.TripID, tr.ID.ID, agency) {
				found = true
				break
			}
		}
		if found {
			out = append(out, Result{Check: name, Source: src.Label, Status: Pass,
				Message: fmt.Sprintf("predicted trip %q present at stop %q", tr.ID.ID, rawStop)})
		} else {
			out = append(out, Result{Check: name, Source: src.Label, Status: Fail,
				Message: fmt.Sprintf("trip %q predicted in feed but absent from arrivals at stop %q", tr.ID.ID, rawStop),
				Details: map[string]any{"feedTripId": tr.ID.ID, "stopId": obaStop}})
		}
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./validator/ -run TestTripUpdateSampling -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add validator/check_tripupdates.go validator/check_tripupdates_test.go
git commit -m "feat: add trip-update sampling check"
```

---

## Task 16: Service-alert cross-reference check

**Files:**
- Create: `validator/check_alerts.go`
- Test: `validator/check_alerts_test.go`

Prefers alerts with an informed `stop_id`; resolves trip-scoped alerts to a stop via the trip's stop-time-updates if available. Confirms the alert id appears in the per-arrival `SituationIDs`. Non-match ⇒ `Warn` (OBA may re-id situations).

- [ ] **Step 1: Write the failing test**

`validator/check_alerts_test.go`:
```go
package validator

import (
	"context"
	"net/http"
	"strings"
	"testing"

	gtfs "github.com/OneBusAway/go-gtfs"

	"github.com/onebusaway/oba-validator/config"
)

func TestServiceAlertFoundInSituationIDs(t *testing.T) {
	client := newTestClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "arrivals-and-departures-for-stop") {
			w.Write([]byte(`{"data":{"entry":{"arrivalsAndDepartures":[{"stopId":"1_ST1","tripId":"1_T1","situationIds":["1_ALERT1"]}]}}}`))
			return
		}
		t.Errorf("unexpected path %s", r.URL.Path)
	})
	src := &SourceContext{
		Label:      "ds0",
		Config:     config.DataSource{AgencyMapping: map[string]string{"KCM": "1"}},
		PrepErrors: map[string]error{},
		Static:     staticForVehicle(),
		ServiceAlerts: &gtfs.Realtime{Alerts: []gtfs.Alert{{
			ID:               "ALERT1",
			InformedEntities: []gtfs.AlertInformedEntity{{StopID: strp("ST1")}},
		}}},
	}
	vc := &ValidationContext{Config: cfgForTest("test"), Client: client}
	results := serviceAlertCheck{}.Run(context.Background(), vc, src)
	if len(results) == 0 || results[0].Status != Pass {
		t.Errorf("want Pass, got %+v", results)
	}
}

func TestServiceAlertNoSamplableWarns(t *testing.T) {
	src := &SourceContext{
		Label:         "ds0",
		Config:        config.DataSource{},
		PrepErrors:    map[string]error{},
		Static:        staticForVehicle(),
		ServiceAlerts: &gtfs.Realtime{Alerts: []gtfs.Alert{{ID: "A", InformedEntities: []gtfs.AlertInformedEntity{{AgencyID: strp("KCM")}}}}},
	}
	vc := &ValidationContext{Config: cfgForTest("test")}
	results := serviceAlertCheck{}.Run(context.Background(), vc, src)
	if results[0].Status != Warn {
		t.Errorf("agency-only alert not stop-referenceable: want Warn got %v", results[0].Status)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./validator/ -run TestServiceAlert -v`
Expected: FAIL — `undefined: serviceAlertCheck`.

- [ ] **Step 3: Write the implementation**

`validator/check_alerts.go`:
```go
package validator

import (
	"context"
	"fmt"

	gtfs "github.com/OneBusAway/go-gtfs"
	onebusaway "github.com/OneBusAway/go-sdk"
)

type serviceAlertCheck struct{}

func (serviceAlertCheck) Name() string { return "service-alert-crossref" }

func (serviceAlertCheck) Run(ctx context.Context, vc *ValidationContext, src *SourceContext) []Result {
	const name = "service-alert-crossref"
	key := vc.Config.APIKey
	if err := src.PrepErrors["serviceAlerts"]; err != nil {
		return []Result{{Check: name, Source: src.Label, Status: Fail, Message: "service alerts feed unavailable: " + redact(err, key)}}
	}
	if src.ServiceAlerts == nil || len(src.ServiceAlerts.Alerts) == 0 {
		return []Result{{Check: name, Source: src.Label, Status: Warn, Message: "no alerts in feed to sample"}}
	}

	// Keep alerts that have an informed stop we can query directly.
	type sampleAlert struct {
		alert   gtfs.Alert
		rawStop string
	}
	var usable []sampleAlert
	for _, a := range src.ServiceAlerts.Alerts {
		for _, ie := range a.InformedEntities {
			if ie.StopID != nil && *ie.StopID != "" {
				usable = append(usable, sampleAlert{alert: a, rawStop: *ie.StopID})
				break
			}
		}
	}
	if len(usable) == 0 {
		return []Result{{Check: name, Source: src.Label, Status: Warn, Message: "no alert references a stop id we can cross-check"}}
	}
	sample := sampleByID(usable, vc.Config.SampleSize, func(s sampleAlert) string { return s.alert.ID })

	// Resolve a single OBA agency for prefixing (first GTFS agency).
	agency := ""
	if len(src.Static.AgencyIDs) > 0 {
		agency, _ = src.MapAgency(src.Static.AgencyIDs[0])
	}

	var out []Result
	for _, s := range sample {
		obaStop := PrefixedID(agency, s.rawStop)
		ad, err := vc.Client.ArrivalAndDeparture.List(ctx, obaStop, onebusaway.ArrivalAndDepartureListParams{})
		if err != nil {
			out = append(out, Result{Check: name, Source: src.Label, Status: Fail,
				Message: fmt.Sprintf("arrivals-and-departures-for-stop %q failed: %s", obaStop, redact(err, key))})
			continue
		}
		anySituation := false
		matched := false
		for _, ad := range ad.Data.Entry.ArrivalsAndDepartures {
			for _, sid := range ad.SituationIDs {
				anySituation = true
				if IDMatch(sid, s.alert.ID, agency) {
					matched = true
				}
			}
		}
		switch {
		case matched:
			out = append(out, Result{Check: name, Source: src.Label, Status: Pass,
				Message: fmt.Sprintf("alert %q surfaced at stop %q", s.alert.ID, s.rawStop)})
		case !anySituation:
			out = append(out, Result{Check: name, Source: src.Label, Status: Fail,
				Message: fmt.Sprintf("stop %q has no situations though feed alert %q affects it", s.rawStop, s.alert.ID),
				Details: map[string]any{"stopId": obaStop, "feedAlertId": s.alert.ID}})
		default:
			out = append(out, Result{Check: name, Source: src.Label, Status: Warn,
				Message: fmt.Sprintf("stop %q has situations but none matched feed alert %q (OBA may re-id situations)", s.rawStop, s.alert.ID),
				Details: map[string]any{"stopId": obaStop, "feedAlertId": s.alert.ID}})
		}
	}
	return out
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./validator/ -run TestServiceAlert -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add validator/check_alerts.go validator/check_alerts_test.go
git commit -m "feat: add service-alert cross-reference check"
```

---

## Task 17: Orchestrator (prepare + Run)

**Files:**
- Create: `validator/validator.go`
- Test: `validator/validator_test.go`

- [ ] **Step 1: Write the failing test**

This test drives the full pipeline against an httptest OBA server plus an httptest feed server, using the in-memory GTFS zip builder pattern. It asserts `Run` returns results spanning server-level and per-source checks and that a fully-healthy mock yields no `Fail`.

`validator/validator_test.go`:
```go
package validator

import (
	"archive/zip"
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/onebusaway/oba-validator/config"
)

func miniGTFSZip(t *testing.T) []byte {
	t.Helper()
	files := map[string]string{
		"agency.txt":     "agency_id,agency_name,agency_url,agency_timezone\nKCM,Metro,https://k,America/Los_Angeles\n",
		"routes.txt":     "route_id,agency_id,route_short_name,route_long_name,route_type\nR1,KCM,1,One,3\n",
		"trips.txt":      "route_id,service_id,trip_id\nR1,S1,T1\n",
		"stops.txt":      "stop_id,stop_name,stop_lat,stop_lon\nST1,Stop,47.6,-122.3\n",
		"calendar.txt":   "service_id,monday,tuesday,wednesday,thursday,friday,saturday,sunday,start_date,end_date\nS1,1,1,1,1,1,0,0,20240101,20251231\n",
		"stop_times.txt": "trip_id,arrival_time,departure_time,stop_id,stop_sequence\nT1,08:00:00,08:00:00,ST1,1\n",
	}
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for n, c := range files {
		w, _ := zw.Create(n)
		w.Write([]byte(c))
	}
	zw.Close()
	return buf.Bytes()
}

func TestRunEndToEndNoFail(t *testing.T) {
	zipBytes := miniGTFSZip(t)
	// Feed server: static GTFS zip; realtime feeds return empty (valid) bodies.
	feedSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.Contains(r.URL.Path, "gtfs") {
			w.Write(zipBytes)
			return
		}
		w.Write([]byte{}) // empty realtime payload parses to an empty feed
	}))
	defer feedSrv.Close()

	obaSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p := r.URL.Path
		switch {
		case strings.Contains(p, "current-time"):
			w.Write([]byte(`{"data":{"entry":{"time":1716000000000}}}`))
		case strings.Contains(p, "agencies-with-coverage"):
			w.Write([]byte(`{"data":{"list":[{"agencyId":"1"}],"references":{"agencies":[{"id":"1","name":"Metro"}]}}}`))
		case strings.Contains(p, "routes-for-agency"):
			w.Write([]byte(`{"data":{"list":[{"id":"1_R1","agencyId":"1"}]}}`))
		case strings.Contains(p, "stops-for-route"):
			w.Write([]byte(`{"data":{"entry":{"routeId":"1_R1","stopIds":["1_ST1"]}}}`))
		case strings.Contains(p, "stops-for-location"):
			w.Write([]byte(`{"data":{"outOfRange":false,"list":[{"id":"1_ST1"}]}}`))
		case strings.Contains(p, "arrivals-and-departures-for-stop"):
			w.Write([]byte(`{"data":{"entry":{"arrivalsAndDepartures":[]}}}`))
		case strings.Contains(p, "/stop/"):
			w.Write([]byte(`{"data":{"entry":{"id":"1_ST1","lat":47.6,"lon":-122.3,"name":"Stop"}}}`))
		default:
			w.Write([]byte(`{"data":{"list":[]}}`))
		}
	}))
	defer obaSrv.Close()

	cfg := config.Config{
		OBAServerURL: obaSrv.URL, APIKey: "test",
		SampleSize: 3, RTFreshnessSeconds: 300, LocationSpan: 0.01, MaxConcurrency: 4, TimeoutSeconds: 30,
		NoCache: true,
		DataSources: []config.DataSource{{
			StaticGtfsFeedURL: feedSrv.URL + "/gtfs.zip",
			AgencyMapping:     map[string]string{"KCM": "1"},
		}},
	}
	rep, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	if len(rep.Results) == 0 {
		t.Fatal("no results")
	}
	for _, r := range rep.Results {
		if r.Status == Fail {
			t.Errorf("unexpected Fail %s: %s", r.Check, r.Message)
		}
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./validator/ -run TestRunEndToEnd -v`
Expected: FAIL — `undefined: Run`.

- [ ] **Step 3: Write the implementation**

`validator/validator.go`:
```go
package validator

import (
	"context"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"time"

	onebusaway "github.com/OneBusAway/go-sdk"
	"github.com/OneBusAway/go-sdk/option"

	"github.com/onebusaway/oba-validator/config"
	"github.com/onebusaway/oba-validator/feeds"
)

// Run prepares the shared context and executes all checks, returning a Report.
func Run(ctx context.Context, cfg config.Config) (Report, error) {
	vc, err := prepare(ctx, cfg)
	if err != nil {
		return Report{}, err
	}
	var rep Report
	for _, c := range serverChecks() {
		rep.Results = append(rep.Results, c.Run(ctx, vc)...)
	}
	for _, src := range vc.Sources {
		for _, c := range dataSourceChecks() {
			rep.Results = append(rep.Results, c.Run(ctx, vc, src)...)
		}
	}
	return rep, nil
}

func serverChecks() []ServerCheck {
	return []ServerCheck{endpointsCheck{}, agencyUnionCheck{}}
}

func dataSourceChecks() []DataSourceCheck {
	return []DataSourceCheck{
		gtfsSanityCheck{},
		freshnessCheck{},
		vehicleSamplingCheck{},
		tripUpdateSamplingCheck{},
		serviceAlertCheck{},
	}
}

func prepare(ctx context.Context, cfg config.Config) (*ValidationContext, error) {
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	httpClient := &http.Client{Timeout: timeout}

	client := onebusaway.NewClient(
		option.WithAPIKey(cfg.APIKey),
		option.WithBaseURL(cfg.OBAServerURL),
		option.WithMaxRetries(2),
		option.WithRequestTimeout(timeout),
		option.WithHTTPClient(httpClient),
	)

	cacheDir := cfg.CacheDir
	if cacheDir == "" {
		base, err := os.UserCacheDir()
		if err != nil {
			base = os.TempDir()
		}
		cacheDir = filepath.Join(base, "oba-validator")
	}
	fetcher := feeds.NewFetcher(httpClient, feeds.NewCache(cacheDir, time.Hour), cfg.NoCache, cfg.Refresh)

	vc := &ValidationContext{Config: cfg, Client: client}
	vc.Agencies, vc.AgenciesErr = client.AgenciesWithCoverage.List(ctx)

	vc.Sources = make([]*SourceContext, len(cfg.DataSources))
	sem := make(chan struct{}, cfg.MaxConcurrency)
	var wg sync.WaitGroup

	for i := range cfg.DataSources {
		ds := cfg.DataSources[i]
		src := &SourceContext{Index: i, Label: fmt.Sprintf("dataSource[%d]", i), Config: ds, PrepErrors: map[string]error{}}
		vc.Sources[i] = src

		// Static GTFS (cached).
		if ds.StaticGtfsFeedURL != "" {
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				b, err := fetcher.FetchStatic(ctx, ds.StaticGtfsFeedURL)
				if err == nil {
					var p *feeds.ParsedStatic
					p, err = feeds.ParseStatic(b)
					if err == nil {
						src.Static = p
					}
				}
				if err != nil {
					src.prepErr("staticGtfs", err)
				}
			}()
		}

		// Realtime feeds (always fresh).
		rt := func(feedName, url string, assign func(r *realtimeResult)) {
			if url == "" {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				sem <- struct{}{}
				defer func() { <-sem }()
				b, err := fetcher.FetchRealtime(ctx, url)
				if err == nil {
					var parsed = &realtimeResult{}
					parsed.rt, err = feeds.ParseRealtime(b)
					if err == nil {
						assign(parsed)
					}
				}
				if err != nil {
					src.prepErr(feedName, err)
				}
			}()
		}
		rt("vehiclePositions", ds.VehiclePositionsURL, func(r *realtimeResult) { src.VehiclePositions = r.rt })
		rt("tripUpdates", ds.TripUpdatesURL, func(r *realtimeResult) { src.TripUpdates = r.rt })
		rt("serviceAlerts", ds.ServiceAlertsURL, func(r *realtimeResult) { src.ServiceAlerts = r.rt })
	}
	wg.Wait()
	return vc, nil
}
```

Add the tiny helper type referenced above to `validator/context.go` (keeps `prepare` readable):
```go
import gtfs "github.com/OneBusAway/go-gtfs" // already imported in context.go

// realtimeResult carries a parsed realtime feed out of a fetch goroutine.
type realtimeResult struct{ rt *gtfs.Realtime }
```
- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./validator/ -run TestRunEndToEnd -v` then `go test ./... -v`
Expected: PASS across all packages.

- [ ] **Step 5: Commit**

```bash
git add validator/validator.go validator/context.go
git commit -m "feat: add validator orchestrator and preparation phase"
```

---

## Task 18: Reporters

**Files:**
- Create: `report/report.go`
- Test: `report/report_test.go`

- [ ] **Step 1: Write the failing test**

`report/report_test.go`:
```go
package report

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	"github.com/onebusaway/oba-validator/validator"
)

func sampleReport() validator.Report {
	return validator.Report{Results: []validator.Result{
		{Check: "basic-endpoints/current-time", Status: validator.Pass, Message: "OK"},
		{Check: "vehicle-positions-sampling", Source: "dataSource[0]", Status: validator.Fail, Message: "missing"},
	}}
}

func TestWriteJSON(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteJSON(&buf, sampleReport()); err != nil {
		t.Fatal(err)
	}
	var back struct {
		Results []struct {
			Check  string `json:"check"`
			Status string `json:"status"`
		} `json:"results"`
	}
	if err := json.Unmarshal(buf.Bytes(), &back); err != nil {
		t.Fatal(err)
	}
	if back.Results[1].Status != "FAIL" {
		t.Errorf("status=%q want FAIL", back.Results[1].Status)
	}
}

func TestWriteText(t *testing.T) {
	var buf bytes.Buffer
	if err := WriteText(&buf, sampleReport()); err != nil {
		t.Fatal(err)
	}
	out := buf.String()
	if !strings.Contains(out, "✓") || !strings.Contains(out, "✗") {
		t.Errorf("missing glyphs:\n%s", out)
	}
	if !strings.Contains(out, "FAIL") {
		t.Errorf("missing summary:\n%s", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./report/ -v`
Expected: FAIL — `undefined: WriteJSON`.

- [ ] **Step 3: Write the implementation**

`report/report.go`:
```go
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
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./report/ -v`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add report/report.go report/report_test.go
git commit -m "feat: add text and JSON reporters"
```

---

## Task 19: CLI

**Files:**
- Create: `cmd/oba-validator/main.go`
- Test: `cmd/oba-validator/main_test.go`

- [ ] **Step 1: Write the failing test**

`cmd/oba-validator/main_test.go`:
```go
package main

import (
	"bytes"
	"testing"

	"github.com/onebusaway/oba-validator/config"
)

func TestApplyFlagOverrides(t *testing.T) {
	cfg := config.Config{SampleSize: 3, NoCache: false}
	applyOverrides(&cfg, overrides{sampleSize: 5, noCache: true, freshness: 60})
	if cfg.SampleSize != 5 || !cfg.NoCache || cfg.RTFreshnessSeconds != 60 {
		t.Errorf("overrides not applied: %+v", cfg)
	}
}

func TestUsageWhenNoArgs(t *testing.T) {
	var stderr bytes.Buffer
	code := run([]string{"oba-validator"}, &bytes.Buffer{}, &stderr)
	if code != 2 {
		t.Errorf("exit=%d want 2", code)
	}
	if stderr.Len() == 0 {
		t.Error("expected usage on stderr")
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `go test ./cmd/oba-validator/ -v`
Expected: FAIL — `undefined: applyOverrides` / `undefined: run`.

- [ ] **Step 3: Write the implementation**

`cmd/oba-validator/main.go`:
```go
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"

	"github.com/onebusaway/oba-validator/config"
	"github.com/onebusaway/oba-validator/report"
	"github.com/onebusaway/oba-validator/validator"
)

type overrides struct {
	jsonOut    bool
	sampleSize int
	freshness  int
	timeout    int
	cacheDir   string
	noCache    bool
	refresh    bool
}

func applyOverrides(cfg *config.Config, o overrides) {
	if o.sampleSize > 0 {
		cfg.SampleSize = o.sampleSize
	}
	if o.freshness > 0 {
		cfg.RTFreshnessSeconds = o.freshness
	}
	if o.timeout > 0 {
		cfg.TimeoutSeconds = o.timeout
	}
	if o.cacheDir != "" {
		cfg.CacheDir = o.cacheDir
	}
	if o.noCache {
		cfg.NoCache = true
	}
	if o.refresh {
		cfg.Refresh = true
	}
}

func run(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("oba-validator", flag.ContinueOnError)
	fs.SetOutput(stderr)
	var o overrides
	fs.BoolVar(&o.jsonOut, "json", false, "emit JSON instead of text")
	fs.IntVar(&o.sampleSize, "sample-size", 0, "vehicles/trip-updates to sample per source")
	fs.IntVar(&o.freshness, "freshness", 0, "max realtime feed age in seconds")
	fs.IntVar(&o.timeout, "timeout", 0, "per-request timeout in seconds")
	fs.StringVar(&o.cacheDir, "cache-dir", "", "static GTFS cache directory")
	fs.BoolVar(&o.noCache, "no-cache", false, "bypass the static GTFS cache")
	fs.BoolVar(&o.refresh, "refresh", false, "force re-download of static GTFS")
	fs.Usage = func() {
		fmt.Fprintf(stderr, "usage: oba-validator [flags] <config.json | raw-json>\n\n")
		fs.PrintDefaults()
	}
	if err := fs.Parse(args[1:]); err != nil {
		return 2
	}
	if fs.NArg() != 1 {
		fs.Usage()
		return 2
	}

	cfg, err := config.Load(fs.Arg(0))
	if err != nil {
		fmt.Fprintln(stderr, "config error:", err)
		return 2
	}
	applyOverrides(&cfg, o)

	rep, err := validator.Run(context.Background(), cfg)
	if err != nil {
		fmt.Fprintln(stderr, "run error:", err)
		return 2
	}

	if o.jsonOut {
		_ = report.WriteJSON(stdout, rep)
	} else {
		_ = report.WriteText(stdout, rep)
	}
	return rep.ExitCode()
}

func main() {
	os.Exit(run(os.Args, os.Stdout, os.Stderr))
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `go test ./cmd/oba-validator/ -v` then `go build ./...`
Expected: PASS, build succeeds.

- [ ] **Step 5: Commit**

```bash
git add cmd/oba-validator/main.go cmd/oba-validator/main_test.go
git commit -m "feat: add CLI entry point"
```

---

## Task 20: Live integration test (env-gated)

**Files:**
- Create: `validator/integration_test.go`

- [ ] **Step 1: Write the gated test**

`validator/integration_test.go`:
```go
package validator

import (
	"context"
	"os"
	"testing"

	"github.com/onebusaway/oba-validator/config"
)

// Runs the real King County Metro config against the live Puget Sound server.
// Gated by OBA_VALIDATOR_LIVE=1 so it never runs in CI.
func TestLiveKingCountyMetro(t *testing.T) {
	if os.Getenv("OBA_VALIDATOR_LIVE") != "1" {
		t.Skip("set OBA_VALIDATOR_LIVE=1 to run the live integration test")
	}
	raw := `{
      "obaServerURL": "https://api.pugetsound.onebusaway.org",
      "apiKey": "org.onebusaway.iphone",
      "dataSources": [{
        "agencyMapping": {"KCM": "1"},
        "staticGtfsFeedURL": "https://metro.kingcounty.gov/GTFS/google_transit.zip",
        "vehiclePositionsURL": "https://s3.amazonaws.com/kcm-alerts-realtime-prod/vehiclepositions.pb",
        "tripUpdatesURL": "https://s3.amazonaws.com/kcm-alerts-realtime-prod/tripupdates.pb",
        "serviceAlertsURL": "https://s3.amazonaws.com/kcm-alerts-realtime-prod/alerts.pb"
      }]
    }`
	cfg, err := config.Load(raw)
	if err != nil {
		t.Fatal(err)
	}
	rep, err := Run(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	for _, r := range rep.Results {
		t.Logf("%s [%s] %s — %s", r.Status, r.Source, r.Check, r.Message)
	}
	// The agencyMapping key "KCM" may need adjustment once agency.txt is
	// inspected; this test is primarily for manual, observational runs.
}
```

- [ ] **Step 2: Verify it skips by default**

Run: `go test ./validator/ -run TestLiveKingCountyMetro -v`
Expected: `--- SKIP` (env var not set).

- [ ] **Step 3: (Manual) run it live**

Run: `OBA_VALIDATOR_LIVE=1 go test ./validator/ -run TestLiveKingCountyMetro -v`
Expected: logs every check result against the live server. Inspect output; if the agency-union check fails on `KCM`, inspect the real `agency.txt` and correct the `agencyMapping` key.

- [ ] **Step 4: Commit**

```bash
git add validator/integration_test.go
git commit -m "test: add env-gated live integration test"
```

---

## Task 21: Full build, vet, and final commit

- [ ] **Step 1: Run the whole suite**

Run: `go test ./... && go vet ./...`
Expected: all PASS, vet clean.

- [ ] **Step 2: Build the binary and smoke-test usage**

Run: `go build -o /tmp/oba-validator ./cmd/oba-validator && /tmp/oba-validator`
Expected: usage text on stderr, exit code 2.

- [ ] **Step 3: Update README**

Replace `README.md` body with concise usage:
```markdown
# OBA Validator

Validates that a OneBusAway server is functioning properly by cross-referencing
its REST API against the authoritative static GTFS and GTFS-realtime feeds.

## Usage

    oba-validator [flags] <config.json | raw-json-string>

Flags: `--json`, `--sample-size`, `--freshness`, `--timeout`, `--cache-dir`,
`--no-cache`, `--refresh`. Exit code: 0 = no failures, 1 = at least one failure,
2 = config/usage error.

See `docs/superpowers/specs/2026-05-24-oba-validator-design.md` for the full
design.
```

- [ ] **Step 4: Commit**

```bash
git add README.md
git commit -m "docs: document CLI usage"
```

---

## Self-Review (completed during planning)

- **Spec coverage:** basic endpoints (T10), agency union with mapping/blank/identity (T11), GTFS sanity (T12), RT freshness incl. future-dated (T13), vehicle sampling with agency resolution + graded matching + span-based trips-for-location (T14), trip-update sampling (T15), service-alert cross-ref via SituationIDs (T16), caching with atomic write/TTL precedence (T6–T7), idnorm incl. blank agency (T4), config incl. env apiKey/defaults (T5), secret redaction (T9/used throughout), deterministic sampling (T9), concurrency cap + WithMaxRetries (T17), reporters + exit codes (T18, T3), CLI (T19), live integration (T20). All spec sections map to a task.
- **Placeholder scan:** no code placeholders — every step shows complete, compiling code and exact commands.
- **Type consistency:** check structs (`endpointsCheck`, `agencyUnionCheck`, `gtfsSanityCheck`, `freshnessCheck`, `vehicleSamplingCheck`, `tripUpdateSamplingCheck`, `serviceAlertCheck`) match their registration in `serverChecks()`/`dataSourceChecks()` (T17). `Result`/`Status`/`Report`, `IDMatch`/`PrefixedID`/`RawID`, `sampleByID`, `redact`, `feeds.ParsedStatic`/`ParseStatic`/`ParseStaticFromStruct`/`ParseRealtime`, `feeds.Fetcher`/`Cache`, `config.Config`/`DataSource` are used with consistent signatures across tasks.
