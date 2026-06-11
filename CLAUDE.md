# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

AlertSnitch is a Go service that receives Prometheus AlertManager webhooks and persists every
alert to a backend (MySQL, Postgres, or Loki) for offline querying and analysis. It is a fork of
[gitlab.com/yakshaving.art/alertsnitch](https://gitlab.com/yakshaving.art/alertsnitch) (upstream
last updated 2020); the main addition in this fork is the **Loki backend** plus modernized
tooling. The longer-term goal (see `TODO-AI-RCA-ROADMAP.md`) is to feed this alert history into
HolmesGPT for AI root-cause analysis surfaced through a Grafana plugin — that work is not yet implemented.

The module path is `github.com/mikehsu0618/alertsnitch` (matching the repo). The upstream attribution
in the README is historical only.

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
`database/<engine>/` in order (`0.0.1-bootstrap.sql` then `0.1.0-fingerprint.sql`), then
`make integration` (or `go test -tags integration ./internal/storage/`) with those env vars set.

## Architecture

Request flow: **AlertManager → `POST /webhook` → `webhook.Parse` → `Storer.Save(ctx, group, extraLabels)`**.

The storage layer is deliberately decoupled from HTTP and metrics concerns. The dependency
direction is one-way: `internal` (leaf: domain model + interfaces) ← `internal/storage/*` (backends)
← `internal/storage` (registry) ← `main`. The server depends on `internal` + `internal/storage`.

- `internal/internal.go` — the central contracts (this is the leaf package everything imports).
  `Storer` is the minimal backend interface: `Save(ctx, *AlertGroup, extraLabels) error` and
  `Close(ctx) error`. `HealthChecker` (`CheckHealth(ctx) Health`) is **optional** — the server
  type-asserts it; a backend without it is treated as always ready. `AlertGroup`/`Alert` is the
  parsed webhook payload.
- `internal/storage/storage.go` — the backend **registry**. `Connect(Config)` looks up a `Factory`
  by name. **To add a backend: implement `internal.Storer` in a subpackage and `Register("name", …)`
  (or add it to the `registry` map).** No other package changes — this is the extensibility seam.
  `Config` aggregates the typed per-backend configs (`sqlstore.Config`, `loki.Config`); only the one
  matching `Backend` is consulted (no stringly-typed `map[string]string`).
- `internal/storage/sqlstore/` — MySQL + Postgres. They share connect / transaction / model-check /
  health / close via the embedded `base`; only the dialect-specific INSERTs differ (`?` vs `$N`,
  `LastInsertId` vs `RETURNING`). `SupportedModel` ("0.1.0") is checked in `CheckHealth`.
- `internal/storage/loki/` — the Loki backend, split by concern: `config` (typed config + validation
  + TLS), `encoding` (wire types + `FlattenAlertGroup`), `stream` (label allow-list + stream
  construction), `transport` (gzip push + health ping), `batch` (async processor), `client` (the
  `Client` type). See below.
- `internal/storage/null/` — no-op backend for debugging the webhook path.
- `internal/server/server.go` — gorilla/mux router (`/webhook`, `/-/ready`, `/-/health`, `/metrics`).
  `SupportedWebhookVersion` ("4") is enforced (else 400). The handler extracts query params
  (`/webhook?source=alertmanager`) via `queryLabels` and passes them as `extraLabels` — the storage
  layer never touches HTTP. The probe handlers own the `DatabaseUp` gauge, set from `CheckHealth`.
- `main.go` — `parseArgs` (each flag mirrors an `ALERTSNITCH_*` env var via `pkg/env`) → `buildConfig`
  (typed; invalid values like a bad batch-flush duration error at startup) → `storage.Connect` →
  serve. Graceful shutdown drains the server **and** `driver.Close(ctx)` within one 30s deadline.

### Loki backend specifics

- **Stream labels**: only labels in an allow-list (`defaultAllowedLabels`, overridable via
  `ALERTSNITCH_LOKI_ALLOWED_LABELS`) plus the query-param `extraLabels` become stream labels;
  everything else stays in the JSON log line. One stream per `alert_status`.
- **Timestamps** use the alert's real `StartsAt`/`EndsAt`, not `time.Now()`.
- **Batch mode** (`ALERTSNITCH_LOKI_BATCH_ENABLED`): `accumulate` drains the queue into batches and a
  separate `flusher` goroutine ships them with retries — so retry backoff never blocks accumulation.
  `Close(ctx)` drains buffered alerts within the deadline.
- **Persistence metrics**: the backend records `saved_total`/`saving_failures_total` at the *real*
  point of durability (synchronously, or at batch-flush resolution). Queue-full drops count as
  failures. The server does **not** double-count these — it owns only received/invalid + the gauge.
  (This is the one deliberate, documented place storage touches `internal/metrics`.)

## Conventions specific to this repo

- Config is **flag-or-env**, never hardcoded. Add a flag in `main.go`'s `parseArgs` bound to an
  `env.GetEnv*` default, fold it into the typed config in `buildConfig`, and keep the README env-var
  table in sync.
- Tests use `testify`, table-driven. Loki tests use `httptest.Server` (`fakeLoki`) — no live Loki
  needed. Metric assertions use `prometheus/.../testutil` with unique label values per test to stay
  isolated (the counters are process-global). SQL backends are exercised by the integration build tag.
- golangci-lint runs 25+ linters including `gosec`, `gocyclo` (min 15), `gocognit` (min 20), and
  `noctx`. Keep functions small and pass `context.Context` to anything doing I/O.
