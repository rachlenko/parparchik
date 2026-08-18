# Go Rewrite of parparchik (nginx-lua → golang/)

## Overview

parparchik already has C++, Python, and OpenResty+Lua implementations of
the same S3 file-routing/registry service. This plan adds a fourth,
`golang/`, on branch `go-rewrite`: a clean, idiomatic Go implementation that
is functionally compatible with the others (same REST API, same env vars,
same MinIO/S3 stack) but built to be *extended* into a general
Artifactory-style multi-format artifact repository — Maven, npm, PyPI,
Docker, Helm, NuGet, Debian, RPM, Terraform, ML models, and more — without
having to rewrite the core catalog/storage/routing layers again for each
new format.

The immediate driver was a code review and a security review of
`nginx-lua/lua/*.lua`, which found the Lua implementation has real bugs
(not just style issues) worth fixing rather than porting as-is — see
Technical Details below.

## Context

- Source of truth for current behavior: `nginx-lua/lua/{config,aws_sig,s3,registry,metrics,handlers}.lua`, `nginx-lua/nginx.conf`, `nginx-lua/README.md`.
- New implementation lives entirely under `golang/` at the repo root, parallel to `nginx-lua/`, `src/` (C++), and `server.py` (Python).
- Module path: `github.com/rachlenko/parparchik/golang`.
- Adopted findings from two review passes (code-reviewer and security-reviewer agents) run directly against `nginx-lua/` — see Technical Details for the specific bugs found and how this rewrite handles each one.
- Constraint from the requester: idiomatic Go design, and an architecture that can grow to support the repository formats listed at jfrog.com/artifactory (Maven/Java, npm/Node.js, PyPI/Python, Docker, Helm, NuGet/.NET, Debian, RPM, Terraform, ML Models, and more) without a rewrite.

## Development Approach

- Testing approach: regular (code + tests together per task, not strict red-green TDD) for the initial port in Tasks 1–8, since the target behavior was already fully known from the Lua source and its review findings before any Go code was written. Every *future* format-addition task (Tasks 10+) should follow TDD — write the format's route-parsing tests first, since those are pure functions with no external dependencies.
- Complete each task fully (including its tests) before moving to the next.
- Small interfaces, defined where they're consumed or as a deliberate shared abstraction seam (`objectstore.Store`, `format.Format`) — not one god-interface.
- No half-finished implementations: `internal/format` ships with exactly one working implementation (`generic`) and a clean extension interface; it does **not** ship empty stub packages for the other formats (that would be dead code sitting in the tree — YAGNI). Each future format is its own task, done when there's an actual consumer for it.
- Update this plan file if scope changes.

## Testing Strategy

- Unit tests required for every task, table-driven, Arrange/Act/Assert.
- `go test -race -cover ./...` must pass before moving to the next task.
- No mocking framework — small hand-written fakes (`fakeStore` per package) satisfying the real `objectstore.Store` interface, so tests exercise real interface contracts.
- `internal/objectstore`'s actual AWS SDK call sites are intentionally *not* unit-tested against a mocked transport in this pass (would need `aws-sdk-go-v2`'s test harness or a real MinIO container) — tracked as a gap in Task 11, not silently dropped.

## Progress Tracking

- Mark completed items with `[x]` immediately when done.
- Add newly discovered tasks with ➕ prefix.
- Document issues/blockers with ⚠️ prefix.

## Technical Details

### Review findings this rewrite does NOT replicate

From the code review (correctness) and security review of `nginx-lua/`:

1. **S3 client wiring bug** (`handlers.lua:26` vs `s3.lua:10`) — the client
   constructor was called with four positional args where it expected one
   config table; every field silently resolved to empty, so the service
   always talked to public AWS with empty credentials, never the
   configured `S3_ENDPOINT`. Go: `objectstore.NewS3Store` takes a single
   `*config.Config`, and uses `aws-sdk-go-v2`'s typed `aws.Config`/`s3.Options` —
   this class of bug is structurally impossible.
2. **No authentication anywhere** — any unauthenticated client could
   enumerate all files via `GET /list` and download "private" bucket
   objects via server-generated presigned URLs. Go: optional API-key
   middleware (`internal/httpapi.AuthConfig`, `PARPARCHIK_API_KEYS`),
   applied to every route except `/healthcheck`/`/readiness`. Off by
   default (open, matching the original) but logs a loud startup warning
   when unset.
3. **`/public/<key>` / `/private/<key>` were dead routes** unless a bucket
   was literally named `public`/`private` (this project's own
   `docker-compose.yml` uses `public-bucket`/`private-bucket`). Go:
   `resolver.resolveMissingFileByType` resolves by the bucket's `Public`
   flag, not by string-comparing the reconstructed route.
4. **No input validation on `filename`**, used directly as an S3 key —
   path traversal / control-character risk on any S3-compatible backend
   that maps keys to real filesystem paths. Go: `httpapi.validateKey`
   rejects empty keys, leading `/`, `..` segments, control bytes, and
   overlong keys at the HTTP boundary, before it ever reaches storage.
5. **`GET /list` had write side effects** (full multi-bucket listing +
   manifest `PUT` to every bucket, every single request) and no rate
   limiting. Go: reconciliation runs once at `Bootstrap` and on a
   configurable ticker (`PARPARCHIK_SYNC_INTERVAL`); `GET /list` is a pure
   catalog read. A per-IP token-bucket rate limiter
   (`golang.org/x/time/rate`) covers every route except the health probes.
6. **`handle_relocate` picked the last matching bucket** in config order,
   contradicting the priority convention `register_file` and
   `resolve_missing_file` both use elsewhere in the same file. Go:
   `resolver.Relocate` picks the highest-priority (first-configured)
   bucket, consistently.
7. **Hand-rolled SigV4 had real bugs** (double-encoded pagination
   continuation tokens breaking signatures on >1000-object buckets;
   canonical-path vs wire-path mismatches for keys with special
   characters). Go: `aws-sdk-go-v2` handles signing and canonicalization.
8. **Multi-worker init only ran on `nginx` worker 0** (`ngx.worker.id() ==
   0`), so every other worker returned 500 on any multi-core host. Go's
   single-process/goroutine model has no equivalent failure mode.
9. **`ngx.shared.DICT` read-modify-write races** across `file:<key>`,
   `route:<route>`, and `__all_keys__` as separate dict entries. Go:
   `internal/catalog.Catalog` is a single mutex-guarded struct — updates to
   an entry and its route index happen under one lock.

### `catalog.Register` vs `catalog.Set` — a deliberate split, not a duplicate API

`Register` applies bucket-priority conflict resolution (lower configured
index wins) and is used by `resolver.SyncRegistry`, which scans *all*
buckets in one pass and may see the same key appear in more than one
bucket during that scan — priority is what makes that outcome
deterministic.

`Set` unconditionally overwrites and is used by
`resolver.ResolveMissingFile`, `resolveMissingFileByType`, and `Relocate` —
each of which already searched buckets in priority order and stopped at
the first confirmed HEAD hit, so priority was already applied at the
search level. Using `Register` in those call sites was tried first and
caught a real bug in this branch's own test suite: a stale catalog entry
pointing at a higher-priority bucket (whose copy of the file had since
been deleted directly in storage, outside this service) permanently
blocked the correct lower-priority reconciliation update, because
`Register`'s priority check compared against the stale entry and silently
no-op'd. `Set` exists specifically for "the caller just confirmed via a
direct storage check where this lives right now" — see
`internal/catalog/catalog.go`'s doc comments and
`internal/resolver/resolver_test.go`'s
`TestResolveRoute_StaleCatalogEntryReResolvesElsewhere`.

### go-reviewer pass on the new code (post-implementation)

A `go-reviewer` agent audited the finished `golang/` tree independently and
confirmed the `Register`/`Set` split above is sound and correctly
regression-tested. It found one HIGH bug (the `PublicURL` scheme-doubling
issue folded into Task 2 above, now fixed) and several smaller items,
addressed as follows:

- **Sentinel error compared with `==`** instead of `errors.Is` in
  `httpapi.handleRelocate` — fixed; would have silently broken (404 →
  false 500) the moment `Relocate` started wrapping the error with `%w`,
  which every other error path in the package already does.
- **Inconsistent persist-failure handling** across resolver mutators
  (`Relocate` fails loud, others log-and-continue) — this was intentional
  but undocumented; now has an explicit policy comment on the `resolver`
  package doc: HEAD-confirmed reconciliation paths are best-effort because
  the in-memory catalog is already correct and the periodic sync will
  retry the write, while `Relocate` is a synchronous user action that
  should tell the caller if the durable write failed.
- **Rate limiter keys on `RemoteAddr`**, which collapses to one shared
  bucket behind a reverse proxy/ingress — documented as a known scope cut
  (needs a trusted-proxy-aware forwarded-header lookup to fix safely,
  which was out of scope for this pass) rather than silently left
  unexplained.
- **API key comparison via constant-time compare** — switched from a map
  lookup to `crypto/subtle.ConstantTimeCompare` per configured key, cheap
  defense-in-depth given the small number of expected keys.
- **`LoadManifests`' reverse-iteration** was dead complexity (`Register`'s
  own priority check already makes application order irrelevant) —
  simplified to a plain forward loop and the comment corrected.

### Extension architecture for future repository formats

`internal/format.Format` is the seam:

```go
type Format interface {
    Name() string
    Route(bucket, key string) string
    ParseRoute(route string) (bucket, key string, ok bool)
}
```

A new format is a new package implementing this interface plus whatever
format-specific index documents and HTTP sub-router it needs, built on top
of the existing `catalog.Catalog` (rename/generalize to a multi-format
index if a format needs richer metadata than `Entry` provides — evaluate
per format, don't pre-generalize speculatively) and `objectstore.Store`.
`cmd/parparchik` mounts each enabled format's sub-router alongside the
existing `httpapi` routes. See Tasks 10–20 below for the per-format
breakdown; each is deliberately its own task, started only when there's a
real need for that format, not pre-built as a stub.

## Implementation Steps

### Task 1: Project scaffolding & configuration module
- [x] `go.mod` (module `github.com/rachlenko/parparchik/golang`)
- [x] `internal/config`: env var parsing — `PARPARCHIK_BUCKETS` list and legacy `PARPARCHIK_PUBLIC_BUCKET`/`PARPARCHIK_PRIVATE_BUCKET` pair, plus new `PARPARCHIK_API_KEYS`, `PARPARCHIK_RATE_LIMIT_PER_SECOND`, `PARPARCHIK_RATE_LIMIT_BURST`, `PARPARCHIK_SYNC_INTERVAL`
- [x] write tests for config parsing (success + error cases)
- [x] run project tests - must pass before next task

### Task 2: Object storage abstraction
- [x] `internal/objectstore.Store` interface (List/Head/Get/Put/PublicURL/PresignedURL)
- [x] `S3Store` implementation on `aws-sdk-go-v2` (custom-endpoint MinIO support, path-style addressing, static or default credential chain)
- [x] `resolveEndpointURL` scheme handling (bare `host:port` defaults to `http://` with a warning; full `scheme://` URLs honored as-is)
- [x] write tests for `resolveEndpointURL` and `PublicURL` (including the scheme-qualified-external-endpoint case a go-reviewer pass caught as a HIGH bug — see below)
- [x] run project tests - must pass before next task
- ⚠️ Fixed during review: `PublicURL` unconditionally prepended `"http://"` even when `S3_EXTERNAL_ENDPOINT` already had a scheme (e.g. `https://cdn.example.com`), producing a malformed `"http://https://..."` redirect URL. Fixed by resolving the external endpoint's scheme once in `NewS3Store` via `resolveEndpointURL`, same as the internal endpoint.
- ⚠️ Still not covered: unit tests for `ListObjects`/`HeadObject`/`GetObject`/`PutObject` against a mocked/faked AWS transport (only pure helpers and `PublicURL` are tested; `objectstore` coverage is 34%, up from 6.5%) — tracked in Task 11.

### Task 3: In-memory catalog (file registry)
- [x] `internal/catalog.Catalog`: mutex-guarded map, `Entry`/`Manifest` types
- [x] `Register` (priority-based conflict resolution) and `Set` (unconditional overwrite) — see Technical Details for why both exist
- [x] `LoadManifests` (bare-array and `{"files":[...]}` envelope support, entry-embedded bucket override)
- [x] write tests for register/set/move/remove/list/manifest-loading (success + conflict + malformed-input cases)
- [x] run project tests - must pass before next task

### Task 4: Repository-format abstraction
- [x] `internal/format.Format` interface + `Registry`
- [x] `internal/format/generic`: today's flat bucket/key routing
- [x] write tests for `generic.Format` route building/parsing (including edge cases: missing key, no leading slash, nested keys)
- [x] run project tests - must pass before next task

### Task 5: Route resolver (core business logic)
- [x] `internal/resolver.Resolver`: `Bootstrap`, `PersistManifests`, `SyncRegistry`, `ResolveMissingFile`, `ResolveRoute`, `resolveMissingFileByType`, `Relocate`
- [x] `Bootstrap` performs an initial `SyncRegistry` reconcile pass (see finding #5 in Technical Details) so `GET /list` can be a pure read
- [x] write tests using a hand-written `fakeStore` (route resolution, stale-entry reconciliation, virtual `/public//private/` prefix-by-type resolution, relocate priority/duplicate handling, sync duplicate detection)
- [x] run project tests - must pass before next task

### Task 6: HTTP API layer
- [x] `internal/httpapi.API`: routes via Go 1.22+ `http.ServeMux` method+wildcard patterns, readiness gating
- [x] `validateKey` (path-traversal/control-character rejection)
- [x] `AuthConfig` API-key middleware + `ipRateLimiter` per-IP rate-limit middleware (with idle-entry eviction so it doesn't grow unbounded)
- [x] health/readiness probes bypass both auth and rate limiting
- [x] write tests: readiness gating, status bucket-by-type derivation, update/relocate/download handlers, auth middleware accept/reject, rate limiter burst/probe-bypass behavior
- [x] run project tests - must pass before next task

### Task 7: Metrics
- [x] `internal/metricsapi`: Prometheus gauges via `client_golang` (`parparchik_volume_files`, `parparchik_duplicate_files`, `parparchik_uploads_per_week`, `parparchik_uploads_per_month`)
- [x] write tests (gauge values, stale-label reset on bucket removal, upload-window boundaries)
- [x] run project tests - must pass before next task

### Task 8: Entrypoint & operational wiring
- [x] `cmd/parparchik/main.go`: config load → S3 store → catalog → resolver → metrics → API, with functional options (`WithAuth`, `WithRateLimit`)
- [x] background bootstrap goroutine (server accepts `/healthcheck`/`/readiness` traffic reporting not-ready while it runs)
- [x] periodic background sync goroutine (`PARPARCHIK_SYNC_INTERVAL`)
- [x] graceful shutdown on SIGINT/SIGTERM
- [x] run project tests - must pass before next task

### Task 9: Deployment parity
- [x] `golang/Dockerfile` (multi-stage, distroless nonroot final image)
- [x] `golang/docker-compose.yml` (MinIO + bucket bootstrap, mirroring `nginx-lua/docker-compose.yml`)
- [x] `golang/README.md` (config table, package layout, extension guide, "why a rewrite not a port" section)
- [ ] port `nginx-lua/test/e2e_test.sh`'s 24 assertions (or an equivalent Go-native e2e test) against `golang/docker-compose.yml`
- [ ] run e2e suite against a live `docker compose up`

### Task 11: Close testing gaps from Task 2
- [ ] add S3-call-site tests for `objectstore.S3Store` against either a real MinIO test container or `aws-sdk-go-v2`'s HTTP transport mocking (`smithy-go` middleware test doubles)
- [ ] target ≥80% coverage for `internal/objectstore` (currently ~6%, pure-helper-only)
- [ ] run project tests - must pass before next task

### Task 12: Verify acceptance criteria
- [ ] verify every requirement in Overview is implemented (`go build ./...`, `go vet ./...`, `gofmt -l .` clean, `go test -race -cover ./...` passing)
- [ ] verify all nine "does NOT replicate" findings in Technical Details have a corresponding test (several already do — audit for gaps)
- [ ] run full project test suite
- [ ] run project linter (`go vet`; consider adding `golangci-lint` — not yet in this repo)

### Task 13: Documentation
- [ ] add `golang/` as a fourth implementation option in the repo root `README.md`'s comparison table (alongside C++/Python/Lua)
- [ ] update `golang/README.md` if scope changed during implementation

---

The tasks below are the future-format roadmap the user asked the
architecture to support. Each is independent, sized as its own multi-day
effort, and intentionally **not started** — `internal/format` ships today
with zero stub packages for these (see Development Approach). Start
whichever is actually needed next; there's no required order among them
except that each should look at how `generic` mounts into `httpapi` first.

### Task 14: Maven repository format
- [ ] `internal/format/maven`: groupId/artifactId/version path layout, `Route`/`ParseRoute`
- [ ] `maven-metadata.xml` generation (versioning, snapshot timestamps) served from catalog state
- [ ] mount format's HTTP sub-router in `cmd/parparchik`
- [ ] write tests for path parsing and metadata generation
- [ ] run project tests - must pass before next task

### Task 15: npm repository format
- [ ] `internal/format/npm`: scoped/unscoped package routes, tarball URLs
- [ ] package metadata JSON responses (`GET /<pkg>`, `GET /<pkg>/-/<pkg>-<version>.tgz`)
- [ ] mount format's HTTP sub-router
- [ ] write tests
- [ ] run project tests - must pass before next task

### Task 16: PyPI repository format
- [ ] `internal/format/pypi`: simple index (PEP 503) and JSON API (PEP 691) responses
- [ ] mount format's HTTP sub-router
- [ ] write tests
- [ ] run project tests - must pass before next task

### Task 17: Docker (OCI Distribution) repository format
- [ ] `internal/format/docker`: OCI Distribution Spec v2 API (`/v2/...`), manifest/blob/tag handling
- [ ] mount format's HTTP sub-router
- [ ] write tests
- [ ] run project tests - must pass before next task

### Task 18: Helm repository format
- [ ] `internal/format/helm`: `index.yaml` generation, chart tarball routes
- [ ] mount format's HTTP sub-router
- [ ] write tests
- [ ] run project tests - must pass before next task

### Task 19: NuGet repository format
- [ ] `internal/format/nuget`: NuGet V3 API (service index, package base address, registration)
- [ ] mount format's HTTP sub-router
- [ ] write tests
- [ ] run project tests - must pass before next task

### Task 20: Debian (APT) repository format
- [ ] `internal/format/debian`: `Release`/`Packages`(.gz) index generation, pool layout
- [ ] mount format's HTTP sub-router
- [ ] write tests
- [ ] run project tests - must pass before next task

### Task 21: RPM (yum) repository format
- [ ] `internal/format/rpm`: `repodata` (repomd.xml, primary.xml.gz) generation
- [ ] mount format's HTTP sub-router
- [ ] write tests
- [ ] run project tests - must pass before next task

### Task 22: Terraform provider/module registry format
- [ ] `internal/format/terraform`: Terraform Registry Protocol (provider + module discovery/download endpoints)
- [ ] mount format's HTTP sub-router
- [ ] write tests
- [ ] run project tests - must pass before next task

### Task 23: ML model repository format
- [ ] `internal/format/mlmodel`: define a layout (e.g. MLflow-model-registry-compatible or a simple versioned-artifact scheme) — needs a design decision before implementation, not just a port
- [ ] mount format's HTTP sub-router
- [ ] write tests
- [ ] run project tests - must pass before next task

## Post-Completion

*Items requiring manual intervention — no checkboxes, informational only*

**Manual verification:**
- Load-test the rate limiter's per-IP map under real traffic patterns and tune `PARPARCHIK_RATE_LIMIT_PER_SECOND`/`_BURST` defaults.
- If deploying behind a reverse proxy/ingress/load balancer: the rate limiter currently keys on `r.RemoteAddr`, which will be the proxy's IP, collapsing all clients into one shared bucket (see Technical Details). Either rely on the proxy's own rate limiting in that topology, or extend `httpapi.clientIP` with a trusted-proxy-aware `X-Forwarded-For` lookup before depending on this as the sole mitigation.
- Independent security review of the API-key auth middleware before any non-local deployment (constant-time comparison is not currently used for key matching — Go map lookup is not constant-time; low risk for a bearer-token check over TLS, but worth a second look before treating this as production-hardened auth).
- Confirm TLS termination strategy for `S3_ENDPOINT` in any non-local deployment (this version supports `https://` endpoints; the default assumption for a bare `host:port` is still plaintext `http://`, matching current docker-compose/MinIO usage).

**External system updates:**
- CI/CD pipeline changes to build and publish `golang/`'s Docker image alongside the existing C++/Python/Lua images.
- Any deployment manifests (k8s, compose) that currently reference `nginx-lua` or another implementation, if this one is meant to replace rather than sit alongside them.
