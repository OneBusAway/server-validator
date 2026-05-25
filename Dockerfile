# Stage 1: build the Go binary
FROM golang:1-alpine AS builder

WORKDIR /build

# Cache module downloads across builds
COPY go.mod go.sum ./
RUN go mod download

# Build a static binary (no cgo) for the runtime image
COPY . .
RUN CGO_ENABLED=0 GOOS=linux go build -o oba-validator ./cmd/oba-validator

# Stage 2: minimal runtime
FROM alpine:3

# HTTPS to the OBA API and the GTFS / GTFS-realtime feeds needs CA certificates.
RUN apk add --no-cache ca-certificates

WORKDIR /app
COPY --from=builder /build/oba-validator /app/oba-validator
COPY entrypoint.sh /app/entrypoint.sh
RUN chmod +x /app/entrypoint.sh

# entrypoint.sh base64-decodes its argument into the config JSON before invoking
# the validator. This is required because a Render one-off job's startCommand is
# split on whitespace and passed as argv (no shell), so the JSON — which has
# spaces and special characters — must be base64-encoded by the caller:
# `/app/entrypoint.sh <base64-config>`. (Render uses the startCommand's first
# token as the executable, hence naming entrypoint.sh explicitly.) See "Deploying
# to Render" in the README. No API key is baked in — keys are per-server and
# travel in the config.
ENTRYPOINT ["/app/entrypoint.sh"]
CMD []
