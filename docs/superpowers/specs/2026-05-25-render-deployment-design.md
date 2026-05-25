# Render Deployment — Design

**Date:** 2026-05-25
**Status:** Approved (chat); invocation model corrected after PR review against
obacloud's production Render integration.

## Purpose

Add the artifacts needed to deploy `oba-validator` to [Render](https://render.com)
as a **Docker-runtime cron job** whose schedule is set so far in the future that
it effectively never fires on its own. Real validations are launched on demand as
**one-off jobs** against that service, with the entire config — including the
per-server `apiKey` — passed (base64-encoded) in the job's start command.

This mirrors the deployment approach of the sibling `gtfs-merge-service` repo
(multi-stage Dockerfile) and obacloud's existing Render one-off-job pattern
(`/app/entrypoint.sh <base64>`), adapted to this pure-Go tool.

## Key constraint: keys are per-server

OneBusAway `apiKey` values differ from one OBA server to the next. Therefore **no
key is baked into the image, the Blueprint, or a service-level env var.** The
whole config (with `apiKey`) is supplied — base64-encoded — at invocation time.
The image and `render.yaml` are key-agnostic.

## Invocation model

Render cron jobs and one-off jobs both run the service's container. We exploit
two facts:

1. A cron job needs a schedule. We use `0 2 29 2 *` (Feb 29 02:00 UTC), so the
   automatic run happens at most once every ~4 years — i.e. "virtually never."
2. A **one-off job** runs against an existing service with a caller-supplied
   `startCommand`, inheriting the latest build. This is how real validations are
   triggered.

**How Render runs the start command (verified against obacloud's production
integration, `app/jobs/api_key_service_job.rb` and `apply_merge_transform_rule_set_job.rb`):**
Render **splits the start command on whitespace and passes it as argv — there is
no shell** (quotes are not stripped), and the **first token is the executable**.
The image `ENTRYPOINT` is therefore *overridden* by a one-off job's start command,
not appended to. So the start command must name an executable first, and any
argument with spaces or shell-special characters must be encoded into a single
token.

The validator's config is JSON — full of spaces and special characters — so it
cannot be passed inline. We mirror obacloud's established pattern: **base64-encode
the config** and ship a small `entrypoint.sh` that decodes it. The one-off start
command is:

```
/app/entrypoint.sh <base64-of-compact-config-json>
```

`entrypoint.sh` base64-decodes its argument and execs `oba-validator` with the
resulting JSON as the sole positional argument (`config.Load` accepts a raw JSON
string). As a convenience, an argument that already starts with `{` is treated as
raw JSON and passed through unchanged — base64 never starts with `{`, so the two
cases are unambiguous and local `docker run` stays ergonomic. The `apiKey` rides
inside the encoded config; nothing key-related is in the image.

## Artifacts

All new, except edits to `README.md` and `Makefile`.

### `Dockerfile` (multi-stage, pure Go)

- **Builder:** `golang:1-alpine`. Copy `go.mod`/`go.sum`, `go mod download`, copy
  source, then `CGO_ENABLED=0 GOOS=linux go build -o oba-validator ./cmd/oba-validator`.
- **Runtime:** `alpine:3` + `ca-certificates` (required for HTTPS to the OBA API
  and the GTFS / GTFS-realtime feed URLs). Alpine over distroless so `/bin/sh` and
  busybox `base64` are available for the entrypoint and for debugging one-off runs.
- Copies in `entrypoint.sh` and sets `ENTRYPOINT ["/app/entrypoint.sh"]`,
  `CMD []`. The `ENTRYPOINT` governs scheduled and local runs; a one-off job
  overrides it but names `entrypoint.sh` as its first token by convention.

### `entrypoint.sh`

A POSIX `sh` script that base64-decodes its first argument into the config JSON
and `exec`s `/app/oba-validator` with it (forwarding any trailing flags before the
config). An argument starting with `{` is passed through as raw JSON. `set -eu`
makes a bad/empty argument fail loudly (no silent fallthrough). Required because
Render passes the start command as whitespace-split argv with no shell.

### `render.yaml` (Blueprint)

```yaml
services:
  - type: cron
    name: oba-validator
    runtime: docker
    dockerfilePath: ./Dockerfile
    schedule: "0 2 29 2 *"   # Feb 29 02:00 UTC — effectively never
    plan: starter            # cron jobs are not free-tier; adjust as needed
```

No `envVars` (per the per-server-key constraint). No `dockerCommand`: the rare
scheduled fire runs `entrypoint.sh` with no args, which prints usage and exits 2 —
harmless, at most once every ~4 years.

### `.dockerignore`

Exclude build/dev artifacts that don't belong in the build context:
`bin/`, `.git/`, `docs/`, `.github/`, `*.md`, `example_configs/`. (Configs are
not baked in; the base64-encoded config is the input path.)

### `Makefile`

Add a `docker-build` target: `docker build -t oba-validator .`.

### `README.md`

Add a **Deploying to Render** section covering: local image build, deploying via
the Blueprint, base64-encoding the config, the `POST /v1/services/<id>/jobs` curl
with `"startCommand": "/app/entrypoint.sh <base64>"`, the equivalent Ruby
(`Base64.strict_encode64`) for obacloud, and the exit-code meaning.

### Dropped from the merge-service pattern

- **`env.example`** — not created. There is no env var to set (the key lives in
  the config JSON).

## Filesystem / cache

The static-GTFS cache defaults to `os.UserCacheDir()/oba-validator`, falling back
to `os.TempDir()` (`/tmp`) when `$HOME` is unset; `MkdirAll` creates it. In the
container this just works and is ephemeral per run. **No persistent disk is
required** — a fresh cache each run is correct for an on-demand validator.

## Exit codes (already implemented; documented here)

`0` = no failures · `1` = ≥1 failure · `2` = config/usage error. These surface as
the Render job's exit status, so a failed validation shows as a failed run.

## Out of scope

- Baking configs into the image, persistent disk/caching across runs, scheduled
  recurring validation of a fixed server, and any name-based key/agency
  inference.

## Verification

All exercised against the live KCM/Puget Sound config (public sample key
`org.onebusaway.iphone`):

- `make docker-build` builds clean.
- **Render path:** `docker run --rm oba-validator "$(printf '%s' '<compact-json>' | base64)"`
  produces a full report — exercising base64 decode + the validator, and
  confirming CA certs / outbound HTTPS work. Exit code propagates through the
  `exec` (validation failure → 1).
- **Render-override simulation:** the same with `--entrypoint /app/entrypoint.sh`
  (Render names `entrypoint.sh` as the executable) produces the same report.
- **Local ergonomics:** `docker run --rm oba-validator '<compact-json>'` (raw,
  `{`-prefixed) is passed through and produces a report.
- **Flags:** `... <base64> --json` emits JSON.
- **Failure modes:** no argument → exit 2 (usage); a non-base64, non-`{`
  argument → loud `base64` decode error and non-zero exit (no silent
  fallthrough).
