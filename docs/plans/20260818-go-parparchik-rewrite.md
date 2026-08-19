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

Tasks 14–23 cover per-format support; Tasks 24–35 cover the Nexus/
Artifactory-class capabilities (proxy/virtual repositories, vulnerability
scanning, policy engine, HA, RBAC, and more) identified in the "Feature Gap
Analysis" section near the end of this document, added after reviewing a
Sonatype Nexus-vs-Artifactory feature comparison.

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
- [x] ran the built image end-to-end against real MinIO (`docker compose build` + `docker run`, then upload/`GET /update`/download/move/`POST /relocate`/verify-old-route-404/`GET /list`/`GET /metrics` via curl and the `minio/mc` image) — the first time this implementation was actually executed rather than only tested against fakes
- ⚠️ Found and fixed via this live run: `S3Store.PresignedURL` used the internal `S3_ENDPOINT`'s client to sign, so presigned URLs for private-bucket downloads pointed at `minio:9000` — unresolvable outside the Docker network — instead of `S3_EXTERNAL_ENDPOINT` (`localhost:9000`). This is exactly the shape `golang/docker-compose.yml` ships, so every private-bucket download was broken outside the container network. See the commit fixing `internal/objectstore/s3.go`. Confirmed fixed by following the corrected presigned URL to a real, byte-identical download.
- [ ] port `nginx-lua/test/e2e_test.sh`'s 24 assertions (or an equivalent Go-native e2e test) — **blocked in this sandbox**: `/usr/bin/mc` on this host resolves to Midnight Commander, not the MinIO client, so the existing script's `$MC` calls silently no-op instead of failing loudly (worth hardening the script itself to `command -v` and version-check `$MC` before trusting it). Worked around for manual verification by driving `minio/mc` via `docker run --rm --network ... minio/mc:latest`, but the script itself still needs porting/running in an environment with a real `mc` on `PATH`, or rewritten to use the Docker-image approach directly so it isn't host-`mc`-dependent.
- [ ] run the ported e2e suite as an automated (non-manual) check

### Task 11: Close testing gaps from Task 2
- [x] add S3-call-site tests for `objectstore.S3Store` — `internal/objectstore/s3_transport_test.go`, an `httptest.Server` standing in for S3/MinIO (not a real MinIO container): `ListObjects` (single page, pagination via continuation token, empty bucket, transport error), `HeadObject`/`GetObject` (found, not-found — HEAD's 404-with-no-body vs GET's XML `NoSuchKey` error body — and transport error), `PutObject` (content-type passthrough and default, error), plus a stateful Put→Head→Get round-trip against one fake bucket
- [x] target ≥80% coverage for `internal/objectstore` — reached 94.3% (up from ~6% pure-helper-only, then 44.3% after the presigned-URL fix below added its own tests)
- [x] run project tests - must pass before next task
- ⚠️ Found and fixed via this work (not from the transport tests directly, but from manually running the built image against real MinIO while validating Task 9's deployment parity — see that task): `S3Store.PresignedURL` signed against the internal `S3_ENDPOINT` client instead of `S3_EXTERNAL_ENDPOINT`, producing presigned URLs unresolvable outside the Docker network. Fixed with a dedicated presign-only client bound to the external endpoint.

### Task 12: Verify acceptance criteria
- [x] verify every requirement in Overview is implemented (`go build ./...`, `go vet ./...`, `gofmt -l .` clean, `go test -race -cover ./...` passing)
- [x] verify all nine "does NOT replicate" findings in Technical Details have a corresponding test — audited; found and closed two real gaps: `GET /list` had no test at all (added `TestHandleList_ReturnsCatalogContents` and `TestHandleList_IsAPureReadWithNoSideEffects`, the latter asserting the store's `ListObjects`/`PutObject` are never called, guarding finding #5/#7 in the Technical Details table), and the catalog's mutex-safety claim (finding #9, replacing `ngx.shared.DICT`'s decomposed-key races) was only ever exercised incidentally by other tests passing under `-race` — added `TestConcurrentAccess` exercising every mutator/reader concurrently. The remaining findings (#1, #2, #6, #8) are structural (impossible-by-construction with `aws-sdk-go-v2`/Go's process model) rather than behavior a test asserts, or already covered (virtual-prefix-by-type, relocate priority, auth/rate-limit).
- [x] run full project test suite
- [x] run project linter — added `golangci-lint` (`golang/.golangci.yml`, `make go-lint`); fixed all 15 findings it surfaced on first run (unchecked `errcheck` returns — mostly `defer resp.Body.Close()` and test-handler writes — plus one dead method, `fakeStore.remove`, deleted per no-dead-code convention)

### Task 13: Documentation
- [x] ~~add `golang/` as a fourth implementation option in the repo root `README.md`'s comparison table (alongside C++/Python/Lua)~~ — superseded: C++ and Nginx+Lua were deleted entirely (a later, larger request: "clean lua based and all related to c++ code and update the master with clean and solid go language solution"), so there's no comparison table left to add a fourth row to. Root `README.md` now has a `## Go implementation (recommended)` section presenting it as the primary implementation (Python remains as a reference server, mentioned there), which accomplishes this task's actual intent more directly than the originally-planned row addition would have.
- [x] update `golang/README.md` if scope changed during implementation — kept current incrementally through Tasks 24/14-17/25 (proxy repositories section, package layout, format packages, vulnerability scanning section, `PARPARCHIK_PROXY_REPOS` config entry)

---

The tasks below are the future-format roadmap the user asked the
architecture to support. Each is independent, sized as its own multi-day
effort. Start whichever is actually needed next; there's no required order
among them except that each should look at how `generic` mounts into
`httpapi` first.

**2026-08-18 update — addressing-scheme slice implemented for four of
these** (Maven, npm, PyPI, Docker): each format's path/key parser and, for
the three that fit the bucket-prefixed model, its `format.Format`
(`Route`/`ParseRoute`) implementation. None are mounted as HTTP
sub-routers yet, and none generate their protocol's metadata documents
(`maven-metadata.xml`, npm registry JSON, PEP 503/691 index pages) — that
remaining work is called out per-task below, unchecked.

### Task 14: Maven repository format
- [x] `internal/format/maven`: `ParseCoordinate` (groupId/artifactId/version/classifier/extension from a Maven2-layout path), `Route`/`ParseRoute`
- [ ] `maven-metadata.xml` generation (versioning, snapshot timestamps) served from catalog state
- [ ] mount format's HTTP sub-router in `cmd/parparchik`
- [x] write tests for path parsing (table-driven, valid + malformed cases)
- [x] run project tests - must pass before next task

### Task 15: npm repository format
- [x] `internal/format/npm`: `ParseKey` (scoped/unscoped package + tarball-version parsing), `Route`/`ParseRoute`
- [ ] package metadata JSON responses (`GET /<pkg>`, `GET /<pkg>/-/<pkg>-<version>.tgz`)
- [ ] mount format's HTTP sub-router
- [x] write tests
- [x] run project tests - must pass before next task

### Task 16: PyPI repository format
- [x] `internal/format/pypi`: `NormalizeName` (PEP 503), `ParseFilename` (sdist/wheel), `Route`/`ParseRoute`
- [ ] simple index (PEP 503) and JSON API (PEP 691) responses
- [ ] mount format's HTTP sub-router
- [x] write tests
- [x] run project tests - must pass before next task

### Task 17: Docker (OCI Distribution) repository format
- [x] `internal/format/docker`: `ParseManifestPath`, `ParseBlobPath`, `IsDigest` — path-parsing primitives only. Deliberately does **not** implement `format.Format`: OCI's global `/v2/<name>/manifests|blobs/<ref>` namespace (where `<name>` itself contains slashes) doesn't fit the bucket-prefixed `Route`/`ParseRoute` contract every other format here uses. See the package doc comment.
- [ ] OCI Distribution Spec v2 API (`/v2/...` handlers: manifest/blob GET/HEAD/PUT, tag listing)
- [ ] mount a dedicated `/v2/` HTTP sub-router (not `format.Format`-shaped)
- [x] write tests
- [x] run project tests - must pass before next task

### Task 18: Helm repository format
- [x] `internal/format/helm`: `ParseChartFilename` (`<name>-<version>.tgz`, version boundary = first "-"-segment starting with a digit, same heuristic as maven/npm), `Route`/`ParseRoute`
- [ ] `index.yaml` generation
- [ ] mount format's HTTP sub-router
- [x] write tests
- [x] run project tests - must pass before next task

### Task 19: NuGet repository format
- [x] `internal/format/nuget`: `ParsePackageFilename` (`<id>.<version>.nupkg`, split on "." at the first digit-leading segment) — documented known limitation: an id segment starting with a digit (e.g. "Company.2FA.Library") misparses, `Route`/`ParseRoute`
- [ ] NuGet V3 API (service index, package base address, registration)
- [ ] mount format's HTTP sub-router
- [x] write tests
- [x] run project tests - must pass before next task

### Task 20: Debian (APT) repository format
- [x] `internal/format/debian`: `ParsePackageFilename` (`<name>_<version>_<arch>.deb`, unambiguous — Debian's own convention already delimits on "_"), `PoolPath` (APT pool layout, "lib<x>" 4-char bucketing), `Route`/`ParseRoute`
- [ ] `Release`/`Packages`(.gz) index generation
- [ ] mount format's HTTP sub-router
- [x] write tests
- [x] run project tests - must pass before next task

### Task 21: RPM (yum) repository format
- [x] `internal/format/rpm`: `ParsePackageFilename` (`<name>-<version>-<release>.<arch>.rpm`), validated against a `knownArchitectures` allowlist — a go-reviewer pass on the first version (which took the last "." as the arch separator unconditionally) confirmed it would misparse a filename missing its architecture suffix, mistaking the release field's own embedded dot for the arch separator; the allowlist check closes that gap (residual limitation: a real-but-unlisted architecture, or a release tag colliding with a listed one, still misparses — a fixed allowlist, not a full RPM header parse), `Route`/`ParseRoute`
- [ ] `repodata` (repomd.xml, primary.xml.gz) generation
- [ ] mount format's HTTP sub-router
- [x] write tests
- [x] run project tests - must pass before next task

### Task 22: Terraform provider/module registry format
- [x] `internal/format/terraform`: `ParseProviderVersionsPath`, `ParseProviderDownloadPath`, `ParseModuleVersionsPath`, `ParseModuleDownloadPath` against the real Terraform Registry Protocol. Deliberately does **not** implement `format.Format` — same reasoning as `docker`: `/v1/providers/...` and `/v1/modules/...` are a global namespace, not bucket-prefixed.
- [ ] the protocol's actual JSON response bodies (version lists, download metadata with checksums/signing keys)
- [ ] mount a dedicated `/v1/` HTTP sub-router (not `format.Format`-shaped)
- [x] write tests
- [x] run project tests - must pass before next task

### Task 23: ML model repository format
- [ ] `internal/format/mlmodel`: define a layout (e.g. MLflow-model-registry-compatible or a simple versioned-artifact scheme) — needs a design decision before implementation, not just a port
- [ ] mount format's HTTP sub-router
- [ ] write tests
- [ ] run project tests - must pass before next task

---

## Feature Gap Analysis: Nexus vs Artifactory (2026-08-18)

Source: https://www.sonatype.com/compare/sonatype-nexus-versus-jfrog-artifactory
— a Sonatype-authored vendor comparison, so its head-to-head claims
("Nexus is 80% more accurate", "Artifactory has no depth of SCA") are
marketing and not treated as engineering requirements here. What's useful
is the *feature taxonomy* both products compete on — every capability
listed below is something a real Nexus/Artifactory deployment is expected
to have, regardless of which vendor does it better. Tasks 14–23 above only
cover repository *formats*; the tasks in this section cover the
capabilities that make a multi-format repository actually operate like
Nexus/Artifactory rather than just serve files in more shapes. None of
these are started; `internal/` has no packages for them yet, by the same
no-stub-code policy as Tasks 14–23.

### Priority note

Task 24 (repository types: hosted/proxy/virtual) should come **before**
most of Tasks 14–23, not after — it's orthogonal to format and multiplies
the value of every format package that exists or gets added: a `generic`
or `maven` format is far more useful as a caching *proxy* in front of the
real npm/Maven Central/PyPI than as a hosted-only store, which is the
single most-used feature of both Nexus and Artifactory in practice. Task
25 (vulnerability scanning / firewall) and Task 28 (policy engine) are the
next-highest-value pair, since they're the comparison page's entire focus
and the most differentiating capability of this product class. The rest
(31–35) are operational/enterprise maturity items — valuable, but only
once there's a real multi-tenant deployment to operate.

### Task 24: Repository types — hosted, proxy, virtual/group
- [x] extend `config.Bucket` with `Kind: hosted | proxy` (`config.RepoKind`, `config.KindHosted`/`config.KindProxy`) — kept as an addition to `Bucket` rather than a new `Repository` type, to avoid an invasive rename across `catalog`/`resolver`/`httpapi`, all of which already reference `bucket.Name` extensively
- [x] **proxy** repositories: fetch-through cache in front of a configured upstream URL, on-miss fetch via the new `internal/proxycache.Fetcher` (`HTTPFetcher` does a plain GET) and store into `objectstore` — configured via `PARPARCHIK_PROXY_REPOS` (`name|upstream_url|public` tokens)
- [x] TTL/expiry for cached proxy entries: `Bucket.CacheTTL`, parsed as an optional 4th `PARPARCHIK_PROXY_REPOS` token; `resolver.proxyCacheExpired` reuses the S3 object's own `LastModified` as the "cached at" time (accurate because `resolveProxyRoute` is the only writer into a proxy bucket) rather than tracking a separate cache timestamp. `CacheTTL == 0` (the default, omitted token) still means cache forever — no behavior change for existing configs.
- [x] **virtual/group** repositories: `config.KindVirtual` + `Bucket.Members`, configured via `PARPARCHIK_VIRTUAL_REPOS` (`name|member1+member2+...`). `resolver.resolveVirtualRoute` tries members in order, dispatching to `resolveProxyRoute` or the new `resolveHostedMemberRoute` by member kind. A virtual bucket has no storage of its own (`Bucket.HasStorage()`), so every place that iterates `Config.Buckets` against real S3 storage (`Bootstrap`, `PersistManifests`, `SyncRegistry`, `ResolveMissingFile`, `resolveMissingFileByType`, `Relocate`, and `httpapi.handleStatus`'s public/private-bucket derivation) now skips it — a go-reviewer pass grepped every such loop in the tree and confirmed none were missed.
- ⚠️ Fixed during review: (1) `resolveHostedMemberRoute` originally reused `ResolveMissingFile`, which scans *every* configured bucket (not just the member being checked) and unconditionally persists manifests fleet-wide — `O(members × total buckets)` HEAD calls plus a full manifest-persist sweep per virtual-repo request. Replaced with a single targeted `HeadObject` against just that member, reusing the same priority-checked `registerResolvedObject` helper (renamed from `registerProxyResult` since it's no longer proxy-specific) `resolveProxyRoute` already used. (2) A dangling virtual-repo member reference or a member that was itself another virtual repo (nesting) previously degraded silently to an unexplained 404 with no diagnostic anywhere; `config.Load` now calls `validateVirtualRepoMembers` and fails loudly at startup instead (consistent with how an invalid proxy-repo TTL token already fails `Load`), with a defensive `slog.Warn` kept in `resolveVirtualRoute` for the case where `Config` is constructed directly rather than via `Load`. Also closed a test gap: `proxyCacheExpired`'s "unparseable timestamp fails safe to expired" branch had no direct coverage.
- [x] `internal/resolver` gains the "not cached, but repo is a proxy — fetch upstream" path (`resolver.resolveProxyRoute`, wired into `ResolveRoute`'s existing bucket-prefix-match loop)
- [x] write tests (proxy cache hit, cache-miss-then-fetch-then-cache, upstream 404, upstream fetch error) — `internal/resolver/proxy_test.go`, `internal/proxycache/fetcher_test.go`
- [x] run project tests - must pass before next task
- ⚠️ Fixed during review: `resolveProxyRoute` originally used `catalog.Set` (unconditional overwrite) to register a cached/fetched object, which let a proxy repo's cache silently hijack a key already owned by a higher-priority hosted bucket — deleting that bucket's route index entry. Fixed to use `catalog.Register` (priority-checked) and only surface the entry if this exact bucket still won the key; a route/priority mismatch now returns 404 rather than the wrong bucket's content, matching `ResolveMissingFile`'s existing behavior. Regression test: `TestResolveRoute_ProxyDoesNotHijackHigherPriorityHostedKey`. Also fixed: unbounded response-body reads in `proxycache.HTTPFetcher` and `scan.OSVScanner` (now capped at 1 GiB / 8 MiB respectively).

### Task 25: Vulnerability scanning / repository firewall
- [x] `internal/scan`: pluggable `Scanner` interface + `OSVScanner` querying osv.dev's free API (no Sonatype OSS Index/Nexus IQ license available) for a package+version, returning findings (ID, summary, severity)
- [x] `Policy.Evaluate`: allow/deny decision from a `MaxSeverity` threshold against a scan `Result`
- [ ] quarantine-on-ingest hook — **not implemented**: parparchik has no upload endpoint (objects arrive in S3 externally), so there's no single synchronous "ingest" moment to gate. The natural hook (catalog discovery of a new key during `SyncRegistry`) needs a package-ecosystem+name+version first, which means composing `scan.Scanner` with a format package's key parser (Task 14-16) — deferred to Task 28 (policy engine), which is the natural place to also add the "hold in quarantine" state machine.
- [ ] `GET /status`-style endpoint exposing quarantined items and why (depends on the quarantine hook existing)
- [x] write tests with a fake `Scanner`-shaped HTTP server (clean package, vulnerable package with mixed severities, unexpected-status error path) — `internal/scan/osv_test.go`, `internal/scan/policy_test.go`
- [x] run project tests - must pass before next task

### Task 26: License compliance detection
- [x] `internal/license`: `ParseNPMPackageJSON` (modern string field + legacy object/array forms), `ParsePythonMetadata` (PKG-INFO/METADATA `License:` header, falling back to a `Classifier: License :: OSI Approved :: <name>` header), `ParseMavenPOM` (`<licenses><license><name>`) — one small adapter per ecosystem, not a universal parser. Helm/NuGet/Debian/RPM adapters not implemented (Helm charts have no license metadata field at all; the others need a real gating use case first)
- [x] normalize to SPDX identifiers where possible (`NormalizeSPDX`, a hand-maintained alias table for the common cases — deliberately leaves ambiguous strings like bare "BSD" or "Public Domain" unnormalized rather than guessing)
- [ ] expose via catalog entry metadata + an optional policy check (Task 28) blocking disallowed licenses — **not wired anywhere yet**, same "standalone tested primitives" scope as the Task 14-22 format parsers and Task 25's scan package; there's still no upload endpoint to feed real package metadata through these adapters
- [x] write tests per adapter (known-license, missing-license, malformed-metadata cases)
- [x] run project tests - must pass before next task
- ⚠️ Fixed during review: two HIGH-severity bugs in `NormalizeSPDX`'s alias table, the shared primitive every adapter feeds through. (1) `"unlicensed"` (npm's own `"license": "UNLICENSED"` convention for "no rights granted, proprietary, do not use") was mapped to SPDX `Unlicense` — the opposite meaning (a public-domain-equivalent, maximally permissive dedication). Removed; regression tests added (`NormalizeSPDX("UNLICENSED") == ""`, case-insensitively). (2) The table only mapped *deprecated bare* GPL/LGPL forms (`"gpl-3.0"` etc.) to their current `-only` SPDX identifiers, with no identity entries — so a package already declaring the exact, current, unambiguous identifier (`"GPL-3.0-only"`) failed to normalize. Added identity entries for every `-only`/`-or-later` variant. Also fixed on review: `ParsePythonMetadata` now explicitly keeps the *first* `Classifier: License ::` line on a dual-licensed package (previously the last silently won, undocumented) — consistent with `ParseMavenPOM`'s already-documented "first license entry wins" choice; added a namespaced-POM regression test (100% of real Maven Central POMs declare `xmlns="http://maven.apache.org/POM/4.0.0"`, previously untested).

### Task 27: SBOM generation & ingestion
- [x] `internal/sbom`: `Generate` builds a CycloneDX 1.5 JSON document (chose CycloneDX over SPDX — simpler to produce/round-trip for this project's component+license+vulnerability shape, and it's what OSV.dev and most registry tooling emit natively) from `[]sbom.Component`, combining `license.Result` (Task 26) and `[]scan.Finding` (Task 25) per component
- [x] `Ingest` parses an externally-generated CycloneDX document back into `[]Component` (name/version/purl/license — not vulnerabilities, which CycloneDX links to components only indirectly; no round-trip need for those yet); rejects a non-CycloneDX `bomFormat` rather than silently misparsing
- [ ] wiring to catalog/repository artifacts and a `GET /sbom?...` endpoint — **not implemented**, same "standalone tested primitives, not wired anywhere" scope as Task 25/26; still no upload endpoint in this service to hang either side of this on
- [x] write tests (generation shape, license normalized-vs-raw branches, findings→vulnerabilities mapping, ingestion round-trip, malformed/non-CycloneDX rejection, empty-components ingest, multi-license-array truncation)
- [x] run project tests - must pass before next task
- ⚠️ Fixed during review: no CRITICAL/HIGH findings; two MEDIUM data-trust/lossiness gaps documented (not code bugs — the behavior was already correct, just unstated). (1) `Ingest(Generate(x))` doesn't always reproduce `x.License.Raw` exactly — CycloneDX's `licenseChoice` is `id`-XOR-`name`, so `Generate` already discards `Raw` in favor of `SPDXID` once `License.Normalized()` is true; documented on `Ingest`. (2) `licenseFromCDX` trusts an externally-supplied CycloneDX `license.id` directly as `SPDXID` with no validation against a real SPDX list — flagged for Task 28's policy engine, which must not assume `Ingest`'s `SPDXID` carries the same validation guarantee `NormalizeSPDX`'s own output does. Also on review: removed the no-op `omitempty` on `cdxVulnSource.Name` (meaningless on a non-pointer struct field) with a comment explaining why, documented the silent multi-license-array truncation to `Licenses[0]`, and added the two missing test cases named above.

### Task 28: Policy engine
- [x] `internal/policy`: `Ruleset.Evaluate` composes scan (Task 25) + license (Task 26) + artifact publish-age into a three-tier `Decision` (Allow/Quarantine/Deny) — vulnerability severity thresholds (`MaxSeverity`/`QuarantineSeverity`), a normalized-SPDX-only denylist (`DeniedLicenses`, e.g. GPL-family), and "block packages published < N days ago" (`MinPackageAge` — a real malware-mitigation technique: most malicious packages are pulled from public registries within days). Each sub-rule's Decision+reason merges via "worst wins" (Deny > Quarantine > Allow), accumulating every reason (including Allow reasons) for full audit transparency
- [ ] wire into the upload path and the proxy fetch-through path (Task 24) as a live gate — **not implemented**; same "standalone tested primitive" scope as Task 25/26/27 (no upload endpoint), plus the proxy path specifically can't identify an artifact's ecosystem/name/version yet since no format package (Task 14-22) is mounted as an HTTP sub-router
- [x] waivers: `Waiver` (ecosystem+name, optional exact version, optional expiry) forces `Engine.Evaluate` to Allow regardless of the `Ruleset` verdict, appending (not replacing) the override reason
- [x] audit log of policy decisions: `AuditLog`, mutex-guarded, in-memory, append-only (no persistence — durable storage is future work once there's a real caller generating entries worth persisting)
- [x] write tests (allow, deny, quarantine per rule, worst-decision-wins across rules, waiver match/wildcard-version/expiry-boundary, waiver not applied to an already-allowed artifact, concurrent audit log writes)
- [x] run project tests - must pass before next task
- ⚠️ Fixed during review: one HIGH finding — `Engine.Evaluate` built each `AuditEntry.Reasons` from the same `Verdict.Reasons` slice it also returned to the caller; both `AuditLog.Record` and `Entries` only shallow-copied the `AuditEntry` struct, so a caller mutating its own returned `Verdict.Reasons` silently mutated the "append-only" audit log's stored entry too (reproduced as a real `go test -race` failure). Fixed by having `AuditLog.Record` and `Entries` each defensively copy the `Reasons` backing array; added a regression test. One MEDIUM: the `Ruleset` doc comment claimed "the zero value denies nothing... except via the age rule" — false, since `MaxSeverity`/`QuarantineSeverity` both default to `scan.SeverityUnknown`, so `Ruleset{}` denies any artifact with a known finding above that. Fixed the doc comment and added a regression test pinning this. Also on review: removed an unreachable "no rule had an opinion" fallback branch (the vulnerability rule always contributes a non-empty reason, so it could never fire) and added the "normalized license, not on the denylist" Allow-branch test that was missing coverage.

### Task 29: Cleanup & retention policies
- [x] `internal/cleanup`: `Rule` with three independent, opt-in thresholds — `MaxAge`, `MaxTotalSize` (evicts oldest-first by parsed `LastModified` until under budget), `MaxVersionCount` (evicts oldest-first within a caller-supplied `GroupKey` group, since this package has no ecosystem awareness of its own to derive package/version grouping — mirrors Task 25/28's "BYOC ecosystem grouping" deferral). An entry whose `LastModified` doesn't parse as RFC3339 is never itself evicted by any rule (the same "don't guess" stance as `license.NormalizeSPDX`), though its bytes/group-membership still count toward the other rules' budget accounting
- [x] added `objectstore.Store.DeleteObject` (+ `S3Store` implementation, + updated the two test fakes in `internal/resolver` and `internal/httpapi`) — real deletion capability the interface didn't have before, needed for `Execute` to do anything
- [ ] scheduled GC goroutine wired into `cmd/parparchik`, and a per-bucket retention config surface (config.Bucket fields, the way `PARPARCHIK_PROXY_REPOS` already configures `CacheTTL`) — **not implemented**; a real default-retention-policy and config-schema decision, deferred until there's a concrete deployment to size it against, not a "standalone primitive" scope note like Task 25-28's upload-endpoint gap
- [x] dry-run mode: `Rule.Plan` is pure (decides victims without touching storage/catalog); `Execute` is the separate, explicit "actually delete" step, calling `DeleteObject` per victim and removing only successful deletes from the catalog (continues past individual failures, returns every failure joined via `errors.Join`)
- [x] write tests (all three rules independently and combined/deduplicated, tie-break determinism at equal timestamps, unparseable-timestamp entries never evicted, `GroupKey` returning `""` exclusion, `Execute` success/failure/partial-batch/empty-batch, new `S3Store.DeleteObject` success/idempotent-404/transport-error against a real HTTP server)
- [x] run project tests - must pass before next task
- ⚠️ Fixed during review: no CRITICAL/HIGH findings (Approve). Three MEDIUM: (1) the oldest-first eviction comparators (both `MaxTotalSize` and `MaxVersionCount`) had no tie-break for entries sharing the same `LastModified` — RFC3339 is second-precision, so a real batch PUT within one second ties, and `sort.Slice`'s unspecified behavior for equal elements made eviction choice non-deterministic across Go versions/slice sizes; fixed by adding `Key` as a secondary sort key, with a regression test run 5× to catch non-determinism. (2) `GroupKey` returning `""` silently excludes an entry from `MaxVersionCount` with no doc note explaining it's deliberate (not a bug) — documented, with a regression test. (3) the new `S3Store.DeleteObject` had zero tests despite `s3_transport_test.go`'s own doc comment enumerating exactly which methods it covers (and `DeleteObject` wasn't in the list) — added success/idempotent-absent-key/transport-error tests matching the existing `PutObject` test pattern. One LOW (cross-referencing the `MaxTotalSize` field doc to `Plan`'s unparseable-entry caveat) also applied; a second LOW (an unsynchronized pre-existing test fake in `internal/httpapi`, not a regression from this change) was left as-is.

### Task 30: Replication
- [x] `internal/replication`: pull artifact/manifest sync between two parparchik instances, reusing the existing HTTP API instead of a new wire protocol (user chose "reuse existing HTTP API" over direct S3-to-S3 sync or a design doc first) — `HTTPClient.FetchManifest` reads the remote's `GET /list`; `FetchObject` deliberately does NOT let `http.Client` auto-follow `GET /{bucket}/{key}`'s redirect (which points at a *different host* — presigned S3/MinIO or a public CDN URL); it inspects `Location` itself and issues a completely fresh, header-free request to it, so the remote-auth `X-API-Key` can never leak to the redirect target the way Go's default cross-host redirect header handling (which only strips `Authorization`/`Cookie`/`WWW-Authenticate`) would otherwise allow
- [ ] push (the reverse direction) — **not implemented**, `Puller` is pull-only
- [x] conflict handling reuses `catalog`'s existing priority/`Set` semantics — no new conflict model needed: `Pull` calls `catalog.Register` per pulled key exactly as any other bulk-reconciliation caller would, so a key some other, higher-priority local bucket already owns keeps that owner even after this pull writes bytes into `TargetBucket`'s storage
- [x] write tests (manifest fetch, redirect-following, the API-key-not-forwarded-to-redirect-target security property, up-to-date skip, priority-conflict-not-overridden, partial-batch-failure-continues, transport/status-error branches)
- [x] run project tests - must pass before next task
- ⚠️ Fixed during review: no CRITICAL/HIGH findings (Approve) — the redirect/header-isolation design was independently verified against Go's stdlib redirect-header-copying source, not just trusted. One MEDIUM: `alreadyUpToDate`'s "skip re-fetch" optimization only reasoned about an empty/malformed *local* LastModified (harmlessly triggers an extra fetch); it missed the mirror case — an empty *remote* LastModified (e.g. `objectstore.formatTime` returns `""` for a nil S3 timestamp) made any non-empty local timestamp string-compare as "newer", permanently skipping re-fetch of a key whose remote peer just couldn't report its own timestamp. Fixed by treating an empty remote LastModified as "always refetch"; added a regression test. Also closed on review: added transport-error-branch tests for `FetchManifest`/`FetchObject`/`fetchRedirectTarget` (deterministic via a pre-canceled context or a refused-connection redirect target, not flaky network simulation) and a minor variable-naming inconsistency fix, raising coverage from 93.3% to 96.7%.

### Task 31: High availability & clustering — needs a design doc first
- [ ] **Architectural blocker, not a drop-in task**: `internal/catalog.Catalog` is an in-process map today. Running more than one instance behind a load balancer means two processes each have their own, divergent view of the registry — `Bootstrap`'s reconcile-from-storage step is the only thing keeping them roughly consistent, on the sync interval, not in real time.
- [ ] design doc: externalize catalog state to a shared backend (e.g. Redis, Postgres, or etcd) behind the *same* `Catalog` method signatures, so `resolver`/`httpapi` don't change — this is exactly what the `catalog` package's small, already-abstracted API is for
- [ ] evaluate whether `objectstore`-only reconciliation (current design) is actually sufficient for a first HA pass before building a distributed catalog — it might be, given manifests are already the durable source of truth
- [ ] write tests once a design is chosen
- [ ] run project tests - must pass before next task

### Task 32: Access control — RBAC and SSO
- [ ] `internal/authz`: replace the flat `PARPARCHIK_API_KEYS` list (Task 6) with per-repository and per-path permissions, roles/groups
- [ ] OIDC/SSO integration for interactive/UI use cases (no UI exists yet — this is prep for one)
- [ ] keep the existing `AuthConfig` API-key path as a "service account" tier alongside the new user/role model, don't remove it
- [ ] write tests (permission matrix cases, token validation, backward-compat with existing API-key-only configs)
- [ ] run project tests - must pass before next task

### Task 33: Webhooks & event notifications
- [ ] `internal/webhook`: fire on upload, relocate, policy quarantine/deny (Task 28), scan-complete (Task 25) events to configurable subscriber URLs
- [ ] retry with backoff, per-subscriber delivery status
- [ ] write tests (fake HTTP subscriber, retry-on-failure, malformed subscriber config)
- [ ] run project tests - must pass before next task

### Task 34: Reporting, dashboards & cross-repo search
- [ ] extend `httpapi` (or a new `internal/reporting`) with search across all repositories by name/version/license/vulnerability status
- [ ] exportable reports (CSV/JSON) summarizing policy violations, license breakdown, storage usage per repository
- [ ] no web UI is in scope here — this is API-only, matching this project's current backend-service shape; a UI would be a separate, explicitly-scoped effort
- [ ] write tests
- [ ] run project tests - must pass before next task

### Task 35: Backup & disaster recovery
- [ ] document and script a backup procedure: today's design already makes each bucket's manifest the durable source of truth (`resolver.PersistManifests`) and the in-memory catalog a rebuildable cache, so "backup" is largely "back up the object storage buckets themselves" — verify this holds once Tasks 24/30/31 add proxy caches and replication, which introduce state that isn't just "the manifest"
- [ ] snapshot/restore tooling for the catalog cache (mainly a startup-time optimization, since `Bootstrap` can already rebuild it from manifests + a sync pass)
- [ ] write tests (restore-from-manifest produces the same catalog state as a live sync)
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
