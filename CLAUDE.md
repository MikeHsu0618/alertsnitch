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
go test ./internal/storage/loki -run TestFunctionName -v
go test ./internal/storage/loki -run TestName/subtest_name -v   # specific subtest
```

The SQL backends (`internal/storage/sqlstore/{mysql,postgres}.go`) only have meaningful coverage when a real
database is reachable. CI spins up MySQL 8.4 and Postgres 15 service containers, bootstraps them
with all four SQL files, and sets `ALERTSNITCH_BACKEND` + `ALERTSNITCH_BACKEND_ENDPOINT` (see
`.github/workflows/ci.yml`). To replicate locally, run a DB, apply the files in
`database/<engine>/` in order (`0.0.1-bootstrap.sql`, `0.1.0-fingerprint.sql`, `0.2.0-labelkv.sql`,
`0.3.0-alertgroup-kv.sql`), then
`make integration` (or `go test -tags integration ./internal/storage/`) with those env vars set.

## Architecture

Request flow: **AlertManager → `POST /webhook` → `webhook.Parse` → `Storer.Save(ctx, group, extraLabels)`**.

The storage layer is deliberately decoupled from HTTP and metrics concerns. The dependency
direction is one-way: `internal` (leaf: domain model + interfaces) ← `internal/storage/*` (backends)
← `internal/storage` (registry) ← `main`. The server depends on `internal` + `internal/storage`.

- `internal/internal.go` — the central contracts (this is the leaf package everything imports).
  `Storer` is the minimal backend interface: `Save(ctx, *AlertGroup, extraLabels) error` and
  `Close(ctx) error`. `HealthChecker` (`CheckLiveness`/`CheckReadiness(ctx) Health`) is **optional** — the server
  type-asserts it; a backend without it is treated as always ready. `AlertGroup`/`Alert` is the
  parsed webhook payload.
- `internal/storage/storage.go` — the backend **registry**. `Connect(Config)` looks up a `Factory`
  by name. **To add a backend: implement `internal.Storer` in a subpackage and `Register("name", …)`
  (or add it to the `registry` map).** No other package changes — this is the extensibility seam.
  `Config` aggregates the typed per-backend configs (`sqlstore.Config`, `loki.Config`); only the one
  matching `Backend` is consulted (no stringly-typed `map[string]string`).
- `internal/storage/sqlstore/` — MySQL + Postgres. They share connect / transaction / model-check /
  health / close via the embedded `base`; only the dialect-specific INSERTs differ (`?` vs `$N`,
  `LastInsertId` vs `RETURNING`). `SupportedModel` ("0.3.0") is checked in `CheckReadiness` (liveness only pings).
  Labels/annotations and the AlertGroup `receiver`/`externalURL`/`groupKey` are **deduplicated** into
  lookup tables (`LabelKV`/`AnnotationKV` keyed by an MD5 `KvHash`, plus `AlertGroup*` tables); `kv.go`
  holds the per-dialect `getOrCreate` helpers (MySQL `INSERT IGNORE` + `SELECT`, Postgres
  `INSERT … ON CONFLICT DO UPDATE … RETURNING`) and the child rows store only the FK id.
- `internal/storage/loki/` — the Loki backend, split by concern: `config` (typed config + validation
  + TLS), `encoding` (wire types + `FlattenAlertGroup`), `labels` (low-cardinality allow-list +
  label-name validation), `stream` (stream construction), `timestamps` (per-stream de-collision),
  `metadata` (structured-metadata field selection), `transport` (gzip push + health ping), `batch`
  (async processor), `wal` (crash-durable write-ahead log), `client` (the `Client` type). See below.
- `internal/storage/null/` — no-op backend for debugging the webhook path.
- `internal/server/server.go` — gorilla/mux router (`/webhook`, `/-/ready`, `/-/health`, `/metrics`).
  `SupportedWebhookVersion` ("4") is enforced (else 400). The handler extracts query params
  (`/webhook?source=alertmanager`) via `queryLabels` and passes them as `extraLabels` — the storage
  layer never touches HTTP. The probe handlers own the `DatabaseUp` gauge; `/-/health` is liveness (ping only), `/-/ready` is readiness (ping + model).
- `main.go` — `parseArgs` (each flag mirrors an `ALERTSNITCH_*` env var via `pkg/env`) → `buildConfig`
  (typed; invalid values like a bad batch-flush duration error at startup) → `storage.Connect` →
  serve. Graceful shutdown drains the server **and** `driver.Close(ctx)` within one 30s deadline.

### Loki backend specifics

- **Stream labels**: only labels in a **low-cardinality** allow-list (`defaultAllowedLabels` in
  `labels.go`, overridable via `ALERTSNITCH_LOKI_ALLOWED_LABELS`) plus the query-param `extraLabels`
  become stream labels; everything else stays in the JSON log line. High-cardinality labels (`pod`,
  `instance`, `node`, `container`) are deliberately **not** default stream labels — promoting them
  explodes Loki's active-stream count. Label *names* are validated (`isValidLabelName`) at the single
  chokepoint `buildStreamLabels`, so a stray `?app-id=x` query param can't make Loki reject the whole
  push. One stream per `alert_status`.
- **Timestamps**: the Loki entry timestamp is the **webhook receive time**, stamped once in
  `Save` and carried through the batch queue and the WAL (`walRecord.ReceivedAt`) so a replay
  reproduces the identical entry. It is **not** the alert's `StartsAt`: Loki is ingest-ordered, so an
  alert that has been firing for a while is rejected outright (`reject_old_samples`, or the per-stream
  "entry too far behind" window) or lands outside `retention_period` — an alert firing longer than
  retention would be written straight into an expired window. Nothing is lost: `startsAt`/`endsAt`
  travel in the JSON log line. `ensureMonotonic` (`timestamps.go`) sorts each stream's entries
  ascending and nudges colliding timestamps forward 1ns so Loki doesn't silently drop the alerts of a
  group (which all share one receive time); applied on the sync path and after `mergeStreams`
  (cross-group collisions).
- **Structured metadata** (`ALERTSNITCH_LOKI_STRUCTURED_METADATA`, opt-in): attaches a curated set of
  high-value fields (`fingerprint` + the high-card labels kept out of stream labels) as Loki 3.x
  structured metadata (the optional 3rd tuple element in `row`), for fast filtering without stream
  cardinality cost. Requires a Loki TSDB schema v13+.
- **Batch mode** (`ALERTSNITCH_LOKI_BATCH_ENABLED`): `accumulate` drains the queue into batches and a
  separate `flusher` goroutine ships them with retries — so retry backoff never blocks accumulation.
  `Close(ctx)` drains buffered alerts within the deadline.
- **WAL** (`ALERTSNITCH_LOKI_WAL_ENABLED` + `_DIR`, opt-in, requires batch mode): `wal.go` durably
  appends each enqueued alert (length-prefixed JSON + fsync) before it enters the pipeline and acks it
  only when its batch reaches a terminal outcome; a contiguous-ack checkpoint + compaction bound the
  log. On startup, records past the checkpoint are replayed. Crash-durable, **at-least-once** (a crash
  between push and checkpoint replays already-delivered alerts — tolerated because timestamp
  de-collision + Loki dedup absorb duplicates; the persisted `ReceivedAt` is what makes a replayed
  entry byte-identical, and therefore droppable, rather than a second copy).
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
