# parparchik Project Skill

Use this project skill when modifying `parparchik`, its documentation,
monitoring configuration, or operational workflows.

## Project summary

`parparchik` is a C++20 S3 file routing service using `cpp-httplib`, AWS SDK for
C++ S3, `nlohmann-json`, and `prometheus-cpp` via vcpkg.

## Important files

- `src/server.cc` — HTTP routes, startup manifest load, reconciliation, and miss repair.
- `src/file_registry.cc` — S3 JSON manifest-backed in-memory file registry.
- `src/s3_client.cc` — AWS SDK C++ S3 operations.
- `src/metrics.cc` — Prometheus gauges and text rendering.
- `Makefile` — canonical build, run, test, and docs commands.
- `test/mock_s3_manifest_metrics_test.sh` — mock private/public metrics and
  manifest verification scenario.
- `argocd_deployment.conf.example` — Argo CD/Kubernetes deployment starter.
- `docs/` and `zensical.toml` — Zensical source site and generated static site output.
- `docs/assets/logo.png` — project logo used in README and website.

## Standard workflow

1. Inspect current files with `rg` and focused `sed` reads.
2. Keep C++ changes minimal and consistent with existing style.
3. Update `README.md`, `docs/`, `skills/`, and monitoring examples when behavior changes.
4. Run focused validation first, then broader validation when practical.
5. For docs-only changes, run `make docs-check`.
6. For site updates, run `make docs-site`.

## Build and validation commands

```bash
make configure
make build
make test
make test-mock-metrics
make docs-check
make docs-site
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

- Persistence uses `PARPARCHIK_REGISTRY_MANIFEST_KEY`, default `.parparchik/files.json`.
- The same manifest key is stored in public and private buckets.
- Startup reads both manifests into memory; if both are absent, it scans both
  buckets, writes manifests, and then marks readiness healthy.
- Manifest output shape is `{version, bucket_type, files}` where `files` stores
  `key`, `bucket`, `bucket_type`, `route`, `size`, and `last_modified`.
- Public wins for duplicate keys in manifests or in real buckets (default behavior).
- `POST /relocate` reverses this: private wins when a file exists in both buckets.
  It moves the registry entry between manifests and persists both.
- On miss or stale route, the service checks public first and private second,
  serves the resolved object, updates memory, refreshes metrics, and persists
  both manifests.

## Kubernetes probe contract

- `/helthcheck` is the requested liveness endpoint; `/healthcheck` is an alias.
- `/redines` is the requested readiness endpoint; `/readiness` is an alias.
- Readiness returns HTTP 503 until startup load/backfill/reconcile completes.

## Monitoring contract

Keep these metric names stable unless documentation and dashboards are updated
at the same time:

- `parparchik_volume_files{volume="public"}`
- `parparchik_volume_files{volume="private"}`
- `parparchik_uploads_per_week`
- `parparchik_uploads_per_month`

## REST API contract

| Method | Endpoint | Description |
| --- | --- | --- |
| GET | `/status` | Service health, bucket names, file count |
| GET | `/redines`, `/readiness` | Readiness probe (503 until startup completes) |
| GET | `/helthcheck`, `/healthcheck` | Liveness probe |
| GET | `/list` | All registered files with bucket type and route |
| GET | `/update?filename=<key>` | Locate file, repair manifests on miss (public wins) |
| POST | `/relocate?filename=<key>` | Verify file, relocate between buckets (private wins on duplicate) |
| GET | `/metrics` | Prometheus metrics |
| GET | `/public/<key>` | 302 redirect to public S3 URL |
| GET | `/private/<key>` | 302 redirect to presigned S3 URL |

## Documentation coverage contract

When behavior changes, update all affected documentation surfaces:

- `README.md` for quick start, public features, config, bucket setup guide, and Makefile command list.
- `docs/index.md` for architecture, API, runtime flow, and feature overview.
- `docs/operations.md` for build, run, config, bucket setup guide, S3 manifests,
  Kubernetes, Argo CD, and tests.
- `docs/monitoring.md` for Prometheus, Alertmanager, Grafana, and metric test evidence.
- `docs/` by running `make docs-site` after source docs change.

## External links

- Website: https://rachlenko.github.io/parparchik/
- Repository: https://github.com/rachlenko/parparchik
