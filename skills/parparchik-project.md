# parparchik Project Skill

Use this project skill when modifying `parparchik`, its documentation,
monitoring configuration, or operational workflows.

## Project summary

`parparchik` is an S3 file routing service available in two implementations:

1. **C++20** — production server using `cpp-httplib`, AWS SDK for C++ S3,
   `nlohmann-json`, and `prometheus-cpp` via vcpkg.
2. **Nginx + Lua** — alternative server using OpenResty (Nginx + LuaJIT)
   with `lua-resty-http` and pure Lua AWS SigV4 via OpenSSL FFI.

Both support multiple configurable S3 buckets, each with its own manifest key,
allowing flexible file routing with public or private access.

## Important files

### C++ implementation

- `src/server.cc` — HTTP routes, startup manifest load, reconciliation, and miss repair.
- `src/file_registry.cc` — S3 JSON manifest-backed in-memory file registry.
- `src/s3_client.cc` — AWS SDK C++ S3 operations.
- `src/metrics.cc` — Prometheus gauges and text rendering.
- `Makefile` — canonical build, run, test, and docs commands.
- `test/mock_s3_manifest_metrics_test.sh` — mock private/public metrics and
   manifest verification scenario.

### Nginx + Lua implementation

- `nginx-lua/lua/handlers.lua` — HTTP handlers, init, sync, resolve logic.
- `nginx-lua/lua/registry.lua` — File registry backed by `ngx.shared.DICT`.
- `nginx-lua/lua/s3.lua` — S3 client using `resty.http` with SigV4 signing.
- `nginx-lua/lua/aws_sig.lua` — Pure Lua AWS SigV4 via OpenSSL FFI.
- `nginx-lua/lua/metrics.lua` — Prometheus text-format rendering.
- `nginx-lua/lua/config.lua` — Environment variable parser.
- `nginx-lua/nginx.conf` — OpenResty route configuration.
- `nginx-lua/Makefile` — build, run, test commands for Lua edition.
- `nginx-lua/test/e2e_test.sh` — 24-assertion end-to-end test.

### Shared

- `argocd_deployment.conf.example` — Argo CD/Kubernetes deployment starter.
- `docs/` and `zensical.toml` — Zensical source site and generated static site output.
- `docs/assets/logo.png` — project logo used in README and website.

## Standard workflow

1. Inspect current files with `rg` and focused `sed` reads.
2. Keep C++ changes minimal and consistent with existing style.
3. Keep Lua changes consistent with OpenResty idioms (`local`, module tables, shared dict).
4. Update `README.md`, `nginx-lua/README.md`, `docs/`, `skills/`, and monitoring examples when behavior changes.
5. Run focused validation first, then broader validation when practical.
6. For docs-only changes, run `make docs-check`.
7. For site updates, run `make docs-site`.

## Build and validation commands

### C++ edition

```bash
make configure
make build
make test
make test-mock-metrics
make docs-check
make docs-site
```

### Nginx + Lua edition

```bash
cd nginx-lua
make up
make test
make test-all
make down
```

## vcpkgproxy and build cache

- Dependencies are resolved through the sibling `../vcpkgproxy` repository.
- `make sync` is the online step that fills `vcpkg/`, `downloads/`,
   `binary-cache/`, and cached helper binaries such as `sccache`.
- `make build-all` is the offline build path that restores binary packages into
   `installed/`, configures CMake with the vcpkg toolchain, and compiles with
   `sccache`.
- Keep Makefile variables `VCPKG_ROOT`, `VCPKG_DOWNLOADS`,
   `VCPKG_DEFAULT_BINARY_CACHE`, and compiler launcher settings documented when
   dependency handling changes.

## S3 JSON manifest registry contract

- Persistence uses configurable manifest keys per bucket (default `.parparchik/files.json`).
- Each bucket stores its own manifest with the configured key.
- Startup reads manifests from all buckets into memory; if any are absent, it scans buckets, writes manifests, and then marks readiness healthy.
- Manifest output shape is `{version, bucket, files}` where `files` stores `key`, `bucket`, `route`, `size`, and `last_modified`.
- Bucket priority determines which bucket wins for duplicate keys (first in config list has highest priority).
- `POST /relocate` reverses this: last bucket wins when a file exists in multiple buckets. It moves the registry entry between manifests and persists all.
- On miss or stale route, the service checks buckets in priority order, serves the resolved object, updates memory, refreshes metrics, and persists all manifests.

## Nginx + Lua specific contracts

### Shared state via ngx.shared.DICT

- All file entries stored as `file:<key>` → JSON in the `file_registry` shared dict.
- Route index stored as `route:<route>` → key for reverse lookup.
- Ready flag stored as `__ready__` → 1/0 (numeric, not boolean).
- `registry:clear()` only removes file/route entries, not meta keys like `__ready__`.
- Worker 0 runs the init timer; all workers share the same dict.

### AWS SigV4 via FFI

- HMAC-SHA256 uses `ffi.C.HMAC()` calling OpenSSL directly.
- SHA-256 uses `resty.sha256` (bundled with OpenResty).
- Presigned URLs use query-string signing with `UNSIGNED-PAYLOAD`.

### Route matching

- Routes use `/public/<key>` and `/private/<key>` prefixes (not bucket names).
- When a file moves between buckets, requesting the old route returns 404.
- `resolve_route()` checks that the resolved entry's route matches the requested route.

## Kubernetes probe contract

- `/healthcheck` is the requested liveness endpoint.
- `/redines` is the requested readiness endpoint; `/readiness` is an alias.
- Readiness returns HTTP 503 until startup load/backfill/reconcile completes.

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
| GET | `/list` | All registered files with bucket and route |
| GET | `/update?filename=<key>` | Locate file, repair manifests on miss (priority order) |
| POST | `/relocate?filename=<key>` | Verify file, relocate between buckets |
| GET | `/metrics` | Prometheus metrics |
| GET | `/<bucket>/<key>` | 302 redirect to S3 URL (C++ edition) |
| GET | `/public/<key>`, `/private/<key>` | 302 redirect to S3 URL (Nginx + Lua edition) |

## Documentation coverage contract

When behavior changes, update all affected documentation surfaces:

- `README.md` for quick start, public features, config, bucket setup guide, and Makefile command list.
- `nginx-lua/README.md` for Nginx + Lua architecture, modules, and quick start.
- `docs/index.md` for architecture, API, runtime flow, and feature overview.
- `docs/operations.md` for build, run, config, bucket setup guide, S3 manifests,
   Kubernetes, Argo CD, and tests.
- `docs/monitoring.md` for Prometheus, Alertmanager, Grafana, and metric test evidence.
- `docs/` by running `make docs-site` after source docs change.

## External links

- Website: https://rachlenko.github.io/parparchik/
- Repository: https://github.com/rachlenko/parparchik
