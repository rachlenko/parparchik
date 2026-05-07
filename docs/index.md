---
icon: lucide/box
---

# parparchik

<p align="center">
  <img src="assets/logo.png" alt="parparchik logo" width="200">
</p>

`parparchik` is a C++20 service that routes versioned files from multiple configurable S3 buckets. Each bucket can be public or private, with its own manifest key. Public files are redirected to public S3 URLs. Private files are redirected to short-lived presigned URLs.

## Features

- Dynamic `/<bucket>/<key>` file routes for each configured bucket.
- In-memory file registry loaded from JSON manifests stored in each bucket.
- Startup backfill from S3 when manifests do not exist yet.
- Bucket priority precedence when the same key exists in multiple buckets.
- Runtime repair when manifests are stale or disagree with real bucket contents.
- `/status`, `/redines`, `/readiness`, `/helthcheck`, and `/healthcheck` probes.
- Prometheus metrics for file counts per bucket, duplicate detection, and recent uploads.
- Prometheus alert rule for duplicate files across S3 buckets.
- Prometheus, Alertmanager, Grafana, Docker Compose, and Argo CD examples.

## Architecture

```mermaid
flowchart LR
  client[Client] --> app[parparchik HTTP service]
  app --> bucket1[(Bucket 1)]
  app --> bucket2[(Bucket 2)]
  app --> bucketN[(Bucket N)]
  bucket1 --> manifest1[Manifest]
  bucket2 --> manifest2[Manifest]
  bucketN --> manifestN[Manifest]
  app --> metrics[/metrics]
```

## Runtime flow

1. Startup reads manifests from all configured buckets using their respective keys.
2. If any manifest is missing, parparchik scans all buckets, builds the in-memory registry, writes manifests back, and becomes ready.
3. If manifests exist, parparchik loads them and verifies each record against actual S3 object existence.
4. If the same key exists in multiple buckets, the highest priority bucket (first in config) wins.
5. On request miss or stale route, parparchik checks buckets in priority order, serves the found route, updates memory, and writes all manifests.

## Manifest format

```json
{
  "version": 1,
  "bucket": "private-bucket",
  "files": [
    {
      "key": "1mb_v0.0.1_file.tgz",
      "bucket": "private-bucket",
      "route": "/private-bucket/1mb_v0.0.1_file.tgz",
      "size": 1048576,
      "last_modified": "2026-05-05T10:00:00Z"
    }
  ]
}
```

## API

| Endpoint | Description |
| --- | --- |
| `/status` | Configuration, readiness, and file count. |
| `/list` | Current in-memory registry entries. |
| `/update?filename=<key>` | Resolve a key and repair manifests on miss/stale state. |
| `POST /relocate?filename=<key>` | Verify file location, relocate registry entry between buckets. Private wins on duplicate. |
| `/metrics` | Prometheus metrics. |
| `/<bucket>/<key>` | Redirect to S3 URL (public or presigned based on bucket config). |
| `/redines`, `/readiness` | Readiness probe. |
| `/helthcheck`, `/healthcheck` | Liveness probe. |

## Monitoring

`/metrics` exposes Prometheus gauges:

- `parparchik_volume_files{volume="<bucket>"}` — file count per bucket.
- `parparchik_duplicate_files` — file keys present in more than one bucket.
- `parparchik_uploads_per_week` / `parparchik_uploads_per_month` — recent upload activity.

`parparchik.rules.yml.example` defines a `ParparchikDuplicateFiles` alert that
fires when duplicates persist for 5 minutes. See [Monitoring](monitoring.md) for
full config examples.

## Common commands

```bash
make build-all
make run-docker
make test-all
make test-mock-metrics
make docs-site
```

See `docs/operations.md` for build, run, test, vcpkg cache, and Kubernetes
instructions.
