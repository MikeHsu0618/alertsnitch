# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

AlertSnitch is a Go service that receives Prometheus AlertManager webhooks and persists every
alert to a backend (MySQL, Postgres, or Loki) for offline querying and analysis. It is a fork of
[gitlab.com/yakshaving.art/alertsnitch](https://gitlab.com/yakshaving.art/alertsnitch) (upstream
last updated 2020); the main addition in this fork is the **Loki backend** plus modernized
tooling. The longer-term goal (see `TODO-AI-RCA-ROADMAP.md`) is to feed this alert history into
HolmesGPT for AI root-cause analysis surfaced through a Grafana plugin — that work is not yet implemented.

> Note: the Go module path is still `gitlab.com/yakshaving.art/alertsnitch` even though the repo
> lives at `github.com/mikehsu0618/alertsnitch`. All internal imports and `-ldflags` version paths
> use the gitlab path — keep using it when adding code; do not "fix" it piecemeal.

## Commands

```sh
make build          # build binary for current platform -> alertsnitch-$(GOOS)-$(GOARCH)
make run            # go run main.go -debug (uses .env if present)
make test           # go test -v -race -coverprofile=coverage.out ./... + prints total coverage
make coverage       # generate + open HTML coverage report
make lint           # golangci-lint (auto-installs if missing); lint-fix applies fixes
make check          # fmt + vet + lint + test (run before committing)
make watch          # hot reload via air (go install github.com/air-verse/air@latest)
```

Run a single test:

```sh
go test ./internal/db -run TestFunctionName -v
go test ./internal/db -run TestName/subtest_name -v   # specific subtest
go test ./internal/db -bench BenchmarkName -run '^$'   # benchmarks live in *_benchmark_test.go
```

The SQL backends (`internal/db/{mysql,postgres}.go`) only have meaningful coverage when a real
database is reachable. CI spins up MySQL 8.0 and Postgres 15 service containers, bootstraps them
with both SQL files, and sets `ALERTSNITCH_BACKEND` + `ALERTSNITCH_BACKEND_ENDPOINT` (see
`.github/workflows/ci.yml`). To replicate locally, run a DB, apply both files in
`database/<engine>/` in order (`0.0.1-bootstrap.sql` then `0.1.0-fingerprint.sql`), then export
the same env vars before `go test ./internal/db`.

## Architecture

Request flow: **AlertManager → `POST /webhook` → `webhook.Parse` → `Storer.Save`**.

- `main.go` — wires everything: parses flags (each flag mirrors an `ALERTSNITCH_*` env var via
  `pkg/env`), builds an `Options map[string]string` of Loki settings, calls `db.Connect`, starts
  the server, and handles SIGINT/SIGTERM graceful shutdown.
- `internal/internal.go` — the central contracts. `Storer` is the backend interface
  (`Save / Ping / CheckModel / Close`); `AlertGroup`/`Alert` is the parsed webhook payload;
  `FlattenAlertGroup` is one alert denormalized with its group context (this is what gets written
  to Loki as a JSON log line).
- `internal/db/db.go` — `Connect(backend, args)` is the factory that switches on backend name and
  returns a `Storer`. **To add a backend, implement `Storer` and add a case here.** `SupportedModel`
  ("0.1.0") is the schema version the SQL backends check via `CheckModel`.
- `internal/server/server.go` — gorilla/mux router. Routes: `/webhook` (POST), `/-/ready`,
  `/-/health`, `/metrics`. `SupportedWebhookVersion` ("4") is enforced — payloads of other versions
  are rejected 400. Every stage increments a Prometheus counter from `internal/metrics`.
- `internal/middleware/context.go` — `WithQueryParameters` stuffs the request's URL query params
  into the context; the Loki backend reads them back via `middleware.GetQueryParameters` to use as
  extra stream labels (e.g. `/webhook?source=alertmanager`).
- `internal/webhook/webhook.go` — parses + validates the raw JSON body into an `AlertGroup`.

### Loki backend (`internal/db/loki.go`, the most substantial file)

This is where most fork-specific complexity lives. Key concepts:

- **Stream labels**: alerts become Loki streams. Only labels in an allowlist
  (`allowedLabels`, defaulting to severity/namespace/pod/etc., overridable via
  `ALERTSNITCH_LOKI_ALLOWED_LABELS`) plus query-param labels are promoted to stream labels;
  everything else stays in the JSON log line. Alerts are split into one stream per `alert_status`.
- **Timestamps** use the alert's real `StartsAt`/`EndsAt`, not `time.Now()`, so history is accurate.
- **Batch mode** (`ALERTSNITCH_LOKI_BATCH_ENABLED`): `Save` enqueues onto `alertCh`; a background
  `processBatches` goroutine merges streams by label key, gzip-compresses, and pushes with retry +
  backoff. When the channel is full, `Save` drops the alert and returns an error. `Close` drains.
- Config is validated up front (`LokiConfig.Validate` and friends) covering URL scheme, timeout
  bounds, paired basic-auth, and paired client cert/key. TLS/mTLS, multi-tenancy
  (`X-Scope-OrgID`), and `HTTP(S)_PROXY` are all supported.

## Conventions specific to this repo

- Config is **flag-or-env**, never hardcoded. Add a flag in `main.go` bound to an `env.GetEnv*`
  default, and (for Loki) thread it through the `Options` map → `connectLoki`. Keep the README
  env-var table in sync.
- Tests use `testify`; table-driven style throughout `internal/db`. Loki tests use
  `httptest.Server` to fake the Loki API — no live Loki needed for `internal/db` unit tests.
- golangci-lint runs 25+ linters including `gosec`, `gocyclo` (min 15), `gocognit` (min 20), and
  `noctx`. Keep functions small and pass `context.Context` to anything doing HTTP.
