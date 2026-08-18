# parparchik Project Skill

Use this project skill when modifying `parparchik`, its documentation,
monitoring configuration, or operational workflows.

## Project summary

`parparchik` is an S3 file routing service. The **Go implementation**
(`golang/`) is the primary, recommended implementation, built to grow into a
general multi-format artifact repository (Maven, npm, PyPI, Docker, Helm,
NuGet, Debian, RPM, Terraform, ML models — see `docs/plans/` for the
roadmap). A Python reference server (`server.py`) remains for comparison.

## Important files

### Go implementation (`golang/`)

- `cmd/parparchik/main.go` — entrypoint: config → S3 store → catalog →
  resolver → metrics → HTTP API wiring, graceful shutdown, background sync.
- `internal/config` — environment variable parsing (buckets, auth, rate
  limit, sync interval).
- `internal/catalog` — mutex-guarded in-memory file registry. `Register`
  applies bucket-priority conflict resolution (bulk multi-bucket sync);
  `Set` unconditionally overwrites (confirmed single-key HEAD-check
  results, e.g. `ResolveMissingFile`/`Relocate`) — do not use `Register` in
  those call sites, it reintroduces a stale-entry bug that was already
  caught and fixed once (see `internal/catalog/catalog.go` doc comments).
- `internal/objectstore` — `Store` interface + `aws-sdk-go-v2`-backed
  `S3Store`. Do not hand-roll AWS SigV4 signing — the Lua implementation
  this project replaced had real signature bugs from doing that.
- `internal/format` — `Format` interface, the extension seam for new
  repository formats. Only `generic` (flat bucket/key routing) is
  implemented; add new formats as their own package, not as stubs ahead of
  need (YAGNI).
- `internal/resolver` — route resolution, missing-file resolution, registry
  sync/reconciliation, relocate. The core business logic.
- `internal/httpapi` — HTTP handlers/routing (Go 1.22+ `ServeMux`
  method+wildcard patterns), API-key auth middleware, per-IP rate limiter,
  `validateKey` path-traversal/control-character input validation.
- `internal/metricsapi` — Prometheus metrics via `client_golang`.
- `Dockerfile`, `docker-compose.yml` — Go implementation's own container
  build and local MinIO stack.
- `README.md` — configuration reference, package layout, "why a rewrite
  not a port" (bugs found in the retired Lua implementation and how the Go
  version avoids them), extension guide for new formats.

### Shared

- `argocd_deployment.conf.example` — Argo CD/Kubernetes deployment starter.
- `docs/` and `zensical.toml` — Zensical source site and generated static site output.
- `docs/plans/` — implementation plans, including the Go rewrite plan and
  the module/format task breakdown.
- `docs/assets/logo.png` — project logo used in README and website.
- `Makefile` — root build/run/test commands; `go-*` targets delegate into
  `golang/`, plain targets (`docker-up`, `test-all`, ...) operate the
  Python reference server.

## Standard workflow

1. Inspect current files with `rg` and focused reads.
2. Keep Go changes idiomatic: small interfaces, `fmt.Errorf("...: %w", err)`
   wrapping, table-driven tests, `gofmt`-clean.
3. Update `README.md`, `golang/README.md`, `docs/`, `skills/`, and
   monitoring examples when behavior changes.
4. Run focused validation first (`cd golang && go build ./... && go vet
   ./... && gofmt -l .`), then broader validation (`go test -race -cover
   ./...`) when practical.
5. For docs-only changes, run `make docs-check`.
6. For site updates, run `make docs-site`.

## Build and validation commands

```bash
make go-build
make go-test        # go vet + gofmt -l + go test -race -cover ./...
make go-test-e2e     # start the Docker stack, run test/e2e_test.sh against it
make docs-check
make docs-site
```

Or directly in `golang/`:

```bash
go build ./...
go vet ./...
gofmt -l .
go test -race -cover ./...
```

## S3 JSON manifest registry contract

- Persistence uses configurable manifest keys per bucket (default `.parparchik/files.json`).
- Each bucket stores its own manifest with the configured key.
- Startup (`resolver.Bootstrap`) reads manifests from all buckets into the
  catalog, then runs one `SyncRegistry` reconcile pass against actual
  bucket contents before marking readiness healthy. A background ticker
  (`PARPARCHIK_SYNC_INTERVAL`, default 5m) repeats the reconcile
  periodically. `GET /list` is a pure catalog read with no side effects —
  it does **not** trigger a sync (unlike the retired Lua implementation).
- Manifest output shape is `{version, bucket, files}` where `files` stores `key`, `bucket`, `route`, `size`, and `last_modified`.
- Bucket priority determines which bucket wins for duplicate keys (first in
  config list has highest priority) — both for `SyncRegistry`'s bulk
  reconciliation (`catalog.Register`) and for `Relocate`'s target-bucket
  selection (`catalog.Set`, deliberately using the *first* matching
  bucket, not the last — see `resolver.Relocate`'s doc comment for why).
- `/public/<key>` and `/private/<key>` resolve by each bucket's configured
  `public` flag (`resolver.resolveMissingFileByType`), not by string-matching
  a literal bucket name — the retired Lua implementation compared route
  strings, which only worked if a bucket happened to be named literally
  `public`/`private`.

## Security posture

- `PARPARCHIK_API_KEYS` (comma-separated) gates every route except
  `/healthcheck`/`/readiness` behind an API key, checked via
  `crypto/subtle.ConstantTimeCompare`. Left empty by default (open, for
  local dev parity) — `cmd/parparchik` logs a startup warning when unset.
  **Never deploy without setting this outside a trusted local network.**
- `PARPARCHIK_RATE_LIMIT_PER_SECOND`/`_BURST` configure a per-client-IP
  token-bucket limiter (`golang.org/x/time/rate`). Known limitation: keys
  on `r.RemoteAddr`, which collapses to one bucket behind a reverse
  proxy/ingress — see `internal/httpapi/middleware.go`'s doc comment
  before relying on this as the sole mitigation in that topology.
- `httpapi.validateKey` rejects empty keys, leading `/`, `..` path
  segments, control characters, and overlong keys on every handler that
  takes a key from client input (`/update`, `/relocate`, download routes).

## Kubernetes probe contract

- `/healthcheck` is the liveness endpoint — always returns 200, never
  gated by readiness, auth, or rate limiting.
- `/redines` is the (intentionally kept, legacy-typo-compatible) readiness
  endpoint; `/readiness` is the correctly-spelled alias. Both return HTTP
  503 with `ready: false` until `Bootstrap` completes; neither is gated by
  auth or rate limiting either.

## Monitoring contract

Keep these metric names stable unless documentation and dashboards are updated
at the same time:

- `parparchik_volume_files{volume="<bucket>"}` — current file count for each configured bucket.
- `parparchik_duplicate_files` — file keys present in more than one S3 bucket.
  Alert rule `ParparchikDuplicateFiles` fires when this gauge stays above 0 for 5 minutes.
- `parparchik_uploads_per_week`
- `parparchik_uploads_per_month`

## REST API contract

| Method | Endpoint | Description |
| --- | --- | --- |
| GET | `/status` | Service health, bucket names, file count |
| GET | `/redines`, `/readiness` | Readiness probe (503 until startup completes) |
| GET | `/healthcheck` | Liveness probe |
| GET | `/list` | All registered files with bucket and route (pure read) |
| GET | `/update?filename=<key>` | Locate file, repair manifests on miss (priority order) |
| POST | `/relocate?filename=<key>` | Verify file, relocate between buckets |
| GET | `/metrics` | Prometheus metrics |
| GET | `/<bucket>/<key>` | 302 redirect to S3 URL |
| GET | `/public/<key>`, `/private/<key>` | 302 redirect to S3 URL, resolved by bucket type |

## Documentation coverage contract

When behavior changes, update all affected documentation surfaces:

- `README.md` for quick start, public features, config, bucket setup guide, and Makefile command list.
- `golang/README.md` for Go implementation architecture, package layout, and quick start.
- `docs/index.md` for architecture, API, runtime flow, and feature overview.
- `docs/operations.md` for build, run, config, bucket setup guide, S3 manifests,
   Kubernetes, Argo CD, and tests.
- `docs/monitoring.md` for Prometheus, Alertmanager, Grafana, and metric test evidence.
- `docs/plans/` when the roadmap or module/format task breakdown changes.
- `docs/` by running `make docs-site` after source docs change.

## External links

- Website: https://rachlenko.github.io/parparchik/
- Repository: https://github.com/rachlenko/parparchik
