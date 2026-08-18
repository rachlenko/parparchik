# parparchik — Go Edition

A ground-up Go rewrite of the parparchik S3 file-routing/registry service —
functionally compatible with the C++, Python, and OpenResty+Lua
implementations in this repository (same REST API, same environment
variables, same MinIO/S3 stack), but built on `aws-sdk-go-v2` instead of a
hand-rolled AWS SigV4 signer, with a mutex-guarded in-memory catalog instead
of a shared-dict registry, and with the auth/rate-limiting/input-validation
the original never had.

See `docs/plans/` in the repo root for the module-by-module implementation
plan and the roadmap for adding Artifactory-style repository formats (Maven,
npm, PyPI, Docker, Helm, NuGet, Debian, RPM, Terraform, ML models, ...).

## Why a rewrite, not a port

A code review and a security review of `nginx-lua/` (see the review agents'
findings, summarized in the plan doc) turned up several issues in the Lua
implementation that this rewrite deliberately does **not** replicate:

- **No authentication at all.** Any unauthenticated client could enumerate
  every registered file via `GET /list` and download "private" bucket
  objects via server-generated presigned URLs. This version adds an
  optional API-key middleware (`PARPARCHIK_API_KEYS`) — off by default for
  local/dev parity with the original, but logs a loud startup warning when
  unset.
- **S3 client wiring bug.** `handlers.lua` called the S3 client constructor
  with four positional arguments where `s3.lua` expected a single config
  table — every field silently resolved to empty/default, so all traffic
  actually went to public AWS with empty credentials, never to the
  configured `S3_ENDPOINT`. Using `aws-sdk-go-v2`'s typed config eliminates
  this entire class of bug.
- **`/public/<key>` and `/private/<key>` were dead routes** whenever a
  bucket wasn't literally named `public`/`private` (this project's own
  `docker-compose.yml` uses `public-bucket`/`private-bucket`, so these
  routes 404'd in the project's own reference deployment). This version
  resolves them by the bucket's configured `public` flag, not by string
  comparison against the bucket name.
- **No input validation on the `filename` query parameter** used directly
  as an S3 object key. This version rejects empty keys, leading `/`, `..`
  path segments, control characters, and overlong keys before ever handing
  client input to the storage layer.
- **`GET /list` had write side effects** (a full bucket listing plus a
  manifest `PUT` to every bucket, on every single request) and no rate
  limiting. This version does that reconciliation once at startup and on a
  configurable background interval (`PARPARCHIK_SYNC_INTERVAL`); `GET
  /list` is now a pure read. A per-client-IP rate limiter
  (`PARPARCHIK_RATE_LIMIT_PER_SECOND` / `PARPARCHIK_RATE_LIMIT_BURST`)
  covers every other route.
- **`handle_relocate` picked the *last* matching bucket** in config order,
  contradicting the priority convention used everywhere else in the same
  codebase. This version picks the highest-priority (first-configured)
  bucket consistently.
- **Hand-rolled SigV4 had real correctness bugs** (double-encoded
  pagination continuation tokens, canonical-path/wire-path mismatches for
  special characters) that would break on any bucket over 1000 objects or
  any key with unusual characters. `aws-sdk-go-v2` handles this correctly.

## Package layout

```
golang/
├── cmd/parparchik/        entrypoint: wiring, graceful shutdown, background sync loop
├── internal/
│   ├── config/             environment variable configuration
│   ├── catalog/            in-memory file registry (concurrency-safe, mutex-guarded)
│   ├── objectstore/        Store interface + aws-sdk-go-v2-backed S3 implementation
│   ├── format/             pluggable repository-format interface (extension seam — see below)
│   │   └── generic/        today's only format: flat bucket/key routing
│   ├── resolver/           route resolution, reconciliation, relocate — the core business logic
│   ├── httpapi/            HTTP handlers, routing, auth + rate-limit middleware, key validation
│   └── metricsapi/         Prometheus metrics (client_golang)
```

## Extending to new repository formats

`internal/format.Format` is the seam for adding Artifactory-style
repository formats without touching `catalog`, `objectstore`, or the base
`httpapi` routes:

```go
type Format interface {
    Name() string
    Route(bucket, key string) string
    ParseRoute(route string) (bucket, key string, ok bool)
}
```

A new format (Maven, npm, PyPI, Docker/OCI, Helm, NuGet, Debian, RPM,
Terraform, ML models, ...) is a new package implementing this interface
plus whatever format-specific index documents and HTTP sub-router it needs
(e.g. Maven's `maven-metadata.xml`, npm's package/tarball responses,
Debian's `Packages`/`Release` files, RPM's `repodata`), built on top of the
existing `catalog.Catalog` and `objectstore.Store` — not by replacing them.
See `docs/plans/` for the per-format task breakdown; only `generic` is
implemented today.

## Configuration

Same environment variables as the other implementations, plus new ones for
the security hardening this version adds:

| Variable | Default | Description |
| --- | --- | --- |
| `PARPARCHIK_PUBLIC_BUCKET` / `PARPARCHIK_PRIVATE_BUCKET` | | Legacy 2-bucket config |
| `PARPARCHIK_BUCKETS` | | Multi-bucket config: `name:manifest:public,...` |
| `PARPARCHIK_REGISTRY_MANIFEST_KEY` | `.parparchik/files.json` | Manifest key (legacy mode) |
| `PARPARCHIK_HOST` | `0.0.0.0` | Listen host |
| `PARPARCHIK_PORT` | `8080` | Listen port |
| `S3_ENDPOINT` | | Internal S3 endpoint (`host:port` or full `scheme://host:port`) |
| `S3_EXTERNAL_ENDPOINT` | | External S3 endpoint for redirect URLs |
| `AWS_REGION` | `us-east-1` | AWS region |
| `AWS_ACCESS_KEY_ID` / `AWS_SECRET_ACCESS_KEY` | | AWS/MinIO credentials |
| `PARPARCHIK_API_KEYS` | *(empty = open)* | Comma-separated accepted API keys; **set this for anything not fully trusted/local** |
| `PARPARCHIK_RATE_LIMIT_PER_SECOND` | `5` | Per-client-IP request rate |
| `PARPARCHIK_RATE_LIMIT_BURST` | `10` | Per-client-IP burst allowance |
| `PARPARCHIK_SYNC_INTERVAL` | `5m` | Background catalog/storage reconciliation interval |

## Development

```bash
go build ./...
go vet ./...
gofmt -l .
go test -race -cover ./...
```
