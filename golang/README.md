# parparchik — Go Edition

The primary, recommended implementation of parparchik — an S3
file-routing/registry service, functionally compatible with the retired
C++ and OpenResty+Lua implementations this replaced (same REST API, same
environment variables, same MinIO/S3 stack; a Python reference server
remains for comparison), built on `aws-sdk-go-v2` instead of a hand-rolled
AWS SigV4 signer, with a mutex-guarded in-memory catalog instead of a
shared-dict registry, and with the auth/rate-limiting/input-validation the
original never had.

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
│   ├── config/             environment variable configuration (buckets, proxy repos, auth, rate limit)
│   ├── catalog/            in-memory file registry (concurrency-safe, mutex-guarded)
│   ├── objectstore/        Store interface + aws-sdk-go-v2-backed S3 implementation
│   ├── proxycache/         Fetcher interface + HTTP implementation for proxy-repository fetch-through
│   ├── format/             pluggable repository-format interface (extension seam — see below)
│   │   ├── generic/        flat bucket/key routing (mounted, in active use)
│   │   ├── maven/          Maven2 coordinate parsing + Route/ParseRoute (addressing only — not mounted)
│   │   ├── npm/             npm package/tarball key parsing + Route/ParseRoute (addressing only — not mounted)
│   │   ├── pypi/            PEP 503 name normalization + distribution filename parsing (addressing only — not mounted)
│   │   ├── helm/             Helm chart filename parsing + Route/ParseRoute (addressing only — not mounted)
│   │   ├── nuget/            NuGet package filename parsing + Route/ParseRoute (addressing only — not mounted)
│   │   ├── debian/           .deb filename + APT pool-path parsing + Route/ParseRoute (addressing only — not mounted)
│   │   ├── rpm/              RPM filename parsing + Route/ParseRoute (addressing only — not mounted)
│   │   ├── terraform/        Terraform Registry Protocol path parsing (primitives only, no format.Format — see package doc)
│   │   └── docker/          OCI Distribution manifest/blob path parsing (primitives only, no format.Format — see package doc)
│   ├── scan/               vulnerability scanning: Scanner interface, OSV.dev-backed implementation, Policy (not yet wired to an ingest hook)
│   ├── license/            license detection: npm/Python/Maven metadata adapters, SPDX normalization (not yet wired to catalog entries or a policy check)
│   ├── sbom/               CycloneDX SBOM generation + ingestion, combining catalog/license/scan data (not yet wired to an HTTP endpoint or catalog storage)
│   ├── policy/             allow/quarantine/deny policy engine over scan+license+age, with waivers and an audit log (not yet wired to a request path — see below)
│   ├── cleanup/            retention rules (max age, max total size, max version count) over catalog entries: Plan (dry-run) + Execute (real delete via objectstore.Store.DeleteObject); not yet wired to a scheduled GC goroutine
│   ├── replication/        pull-only cross-instance sync over the existing HTTP API (GET /list + GET /{bucket}/{key}); not yet wired to a scheduled goroutine, no push direction
│   ├── authz/              per-repository, per-action RBAC primitives (Authorizer, Role, Grant, ServiceAccounts bridge for the existing API-key auth); not yet wired into httpapi's live auth middleware, no OIDC/SSO
│   ├── webhook/            event notifications (Registry + Dispatcher) with retry/backoff and per-attempt delivery status; not yet wired to any live event source
│   ├── reporting/          cross-repo search + CSV/JSON reports (policy violations, license breakdown, storage usage) over catalog/license/scan/policy Records; not wired to an HTTP endpoint — no enriched Record data source exists yet
│   ├── resolver/           route resolution, reconciliation, relocate, proxy fetch-through + TTL, virtual repo aggregation — the core business logic
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

`generic` is the only format actually mounted and serving traffic today.
`maven`, `npm`, `pypi`, `helm`, `nuget`, and `debian`/`rpm` implement real,
tested path/key parsing and the `Format` interface, but aren't mounted as
HTTP sub-routers yet, and don't generate their protocol's metadata
documents — see `docs/plans/` for the per-format remaining-work breakdown.
`docker` and `terraform` intentionally do *not* implement `Format`: OCI's
`/v2/<name>/manifests|blobs/<ref>` namespace and the Terraform Registry
Protocol's `/v1/providers/...`/`/v1/modules/...` paths are both global
namespaces, not bucket-prefixed the way `Route`/`ParseRoute` assumes, so
each only exposes path-parsing primitives for a future dedicated
sub-router.

## Configuration

Same environment variables as the other implementations, plus new ones for
the security hardening this version adds:

| Variable | Default | Description |
| --- | --- | --- |
| `PARPARCHIK_PUBLIC_BUCKET` / `PARPARCHIK_PRIVATE_BUCKET` | | Legacy 2-bucket config |
| `PARPARCHIK_BUCKETS` | | Multi-bucket config: `name:manifest:public,...` |
| `PARPARCHIK_PROXY_REPOS` | | Proxy (fetch-through cache) repos: `name\|upstream_url\|public\|ttl,...` — see Proxy repositories below |
| `PARPARCHIK_VIRTUAL_REPOS` | | Virtual (aggregating) repos: `name\|member1+member2+...,...` — see Virtual repositories below |
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

## Proxy repositories

A proxy repository is a fetch-through cache in front of an upstream URL —
the single most-used feature of Nexus/Artifactory-class tools in practice.
Configure one via `PARPARCHIK_PROXY_REPOS`:

```bash
export PARPARCHIK_PROXY_REPOS="npm-cache|https://registry.npmjs.org|public|1h"
```

Each token is `name|upstream_url|public|ttl` (pipe-separated — upstream
URLs already contain colons from their scheme; `public` and `ttl` are both
optional). Each proxy repo gets its own S3 bucket (named after the repo) as
its cache. Requesting `GET /npm-cache/<key>`:

1. Checks the cache bucket for `<key>` — a hit younger than `ttl` (a Go
   duration string, e.g. `1h`, `30m`; omitted or `0` means cache forever,
   the original behavior) is served immediately, no upstream call.
2. On a miss (or an expired entry), fetches `<upstream_url>/<key>`
   (`internal/proxycache.Fetcher` — a plain HTTP GET; a real npm/PyPI/Maven
   Central/Docker Hub upstream needs a protocol-aware fetcher, not
   implemented here yet), caches a successful response into the bucket,
   registers it in the catalog, and serves it. A genuine upstream 404 is a
   404, not an error.

## Virtual repositories

A virtual repository aggregates other repositories (hosted or proxy) under
one route namespace, resolved in a configured priority order. Configure one
via `PARPARCHIK_VIRTUAL_REPOS`:

```bash
export PARPARCHIK_VIRTUAL_REPOS="all|public-bucket+npm-cache+private-bucket"
```

Each token is `name|member1+member2+...` (`+`-joined member bucket names).
Requesting `GET /all/<key>` tries each member in order and serves the first
one that resolves `<key>` — a hosted member via a single targeted `HEAD`,
a proxy member via its usual fetch-through-and-cache path. A virtual
repository has no storage of its own (`Bucket.HasStorage()` is false), so
it's excluded from every operation that touches real S3 storage (sync,
bootstrap, relocate, manifest persistence). A member name that isn't a
configured hosted/proxy bucket, or that is itself another virtual
repository (nesting isn't supported), fails startup.

## Vulnerability scanning

`internal/scan` provides a `Scanner` interface, an `OSVScanner`
implementation (queries [osv.dev](https://osv.dev)'s free API — no
Sonatype/JFrog license needed), and a `Policy.Evaluate` allow/deny decision
from a severity threshold. It is a standalone, tested package — **not
wired into any request path yet**. parparchik has no upload endpoint
(objects arrive in S3 externally), so there's no single synchronous
"ingest" moment to gate; wiring this into an actual quarantine hook needs
composing `scan.Scanner` with a format package's key parser first (to know
a key's ecosystem/name/version) — see `docs/plans/` Task 25/28.

## Policy engine

`internal/policy` composes `scan.Result`, `license.Result`, and artifact
publish-age into a three-tier `Decision` (Allow/Quarantine/Deny) via a
`Ruleset`, with an explicit, audited `Waiver` override for a specific
package+version (or every version of a package) and an in-memory
`AuditLog` recording every decision made. It is a standalone, tested
package — **not wired into any request path yet**: there's still no
upload endpoint, and the proxy fetch-through path (Task 24) doesn't yet
know an arbitrary key's ecosystem/name/version since no format package is
mounted as an HTTP sub-router. Note that `Ruleset{}`'s zero value is
*not* permissive for vulnerabilities — `MaxSeverity`/`QuarantineSeverity`
both default to `scan.SeverityUnknown`, so an unconfigured `Ruleset`
denies any artifact with a known finding above that; a real deployment
must set `MaxSeverity` explicitly.

## Cleanup & retention

`internal/cleanup` prunes old catalog entries under a `Rule` of up to three
independent thresholds — max age, max total size (evicts oldest-first
until under budget), and max version count within a caller-supplied
package group — mainly relevant to bounding a proxy repository cache's
(Task 24) footprint over time. `Rule.Plan` is a pure dry-run: it decides
what would be deleted without touching anything; `cleanup.Execute` then
actually calls the new `objectstore.Store.DeleteObject` and removes each
successfully-deleted entry from the catalog, continuing past individual
failures rather than aborting the whole batch. Not yet wired into a
scheduled goroutine in `cmd/parparchik`, and there's no per-bucket config
surface for retention thresholds yet either — both are real product
decisions (default retention policy, config schema) deferred until there's
a concrete deployment to size them against.

## Replication

`internal/replication` pulls a remote parparchik instance's catalog and
objects into this instance's own storage and catalog, entirely over the
existing HTTP API — no new wire protocol, no new server-side endpoint.
`HTTPClient.FetchManifest` reads the remote's `GET /list`; `FetchObject`
follows `GET /{bucket}/{key}`'s redirect itself (rather than letting
`http.Client` auto-follow it) specifically so the `X-API-Key` used to
authenticate against the *remote parparchik instance* is never forwarded
to the redirect target — a different host (S3/MinIO/a CDN) that header has
no business reaching. `Puller.Pull` skips re-fetching a key it already has
an equal-or-newer copy of locally, then reuses `catalog.Register`'s
existing priority semantics unchanged for conflict resolution — no new
conflict model. Push (the reverse direction) and a scheduled replication
goroutine in `cmd/parparchik` are not implemented; the latter needs a real
peer-list/interval config surface this project doesn't have yet.

## Access control (RBAC primitives)

`internal/authz` models per-repository, per-action authorization —
`Authorizer.Allowed(principal, action, repo)`, deny-by-default — on top of,
not instead of, `internal/httpapi.AuthConfig`'s existing flat shared-API-key
gate. `ServiceAccounts` bridges the two: it maps an API key `AuthConfig`
already validated to the `Principal` (identity + role names) it acts as,
and `SuperuserRole()` is the documented way an existing flat-API-key
deployment reproduces today's all-or-nothing access exactly under the new
model, by mapping every key to a `Principal` holding only that role. Not
yet wired into httpapi's live request path, and OIDC/SSO integration is
deferred entirely — it needs a concrete identity provider this project
doesn't have; `Principal` is deliberately agnostic to how it was
authenticated, so adding one later only ever needs to produce a
`Principal`, not touch this package.

## Webhooks

`internal/webhook` fires event notifications (upload, relocate, a policy
quarantine/deny decision, a completed vulnerability scan) to configurable
subscriber URLs. `Registry` holds subscribers filtered by event type;
`Dispatcher.Fire` delivers concurrently to every matching subscriber,
retrying a failed delivery with doubling backoff (default 3 attempts),
optionally HMAC-SHA256-signing the payload (`X-Webhook-Signature`) when a
subscriber has a configured secret, and returning every attempt's
`DeliveryStatus` — not just the final one. Not yet wired into any live
event source (no config surface exists yet for an operator to actually
register a subscriber). **Security note:** a subscriber URL is trusted
operator configuration, not user input — this package does not validate or
allowlist it, so anything that later lets an untrusted party register a
subscriber URL must add its own SSRF defenses before doing so; see the
package doc comment.

## Reporting & cross-repo search

`internal/reporting` joins `catalog.Entry` with optional license/scan/policy
data into a `Record`, then offers `Search` (by name substring, exact
version, license SPDX ID, vulnerability status/severity) and three report
types — `PolicyViolationsReport`, `LicenseBreakdown`, and
`StorageUsagePerRepository` (the only one computable from real,
unenriched catalog data alone) — each exportable as JSON or CSV.
Deliberately API-only, no web UI, per the plan; even the API side has no
real source of enriched `[]Record` data yet, since nothing in this
codebase attaches license/scan/policy results to a catalog entry anywhere
today. A `Record.PolicyVerdict == nil` ("never evaluated") is treated as
absent data, not an implicit allow.

## Development

```bash
go build ./...
go vet ./...
gofmt -l .
go test -race -cover ./...
```
