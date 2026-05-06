---
icon: lucide/box
---

# parparchik

<p align="center">
  <img src="assets/logo.png" alt="parparchik logo" width="200">
</p>

`parparchik` is a C++20 service that routes versioned files from two S3 buckets:
public and private. Public files are redirected to public S3 URLs. Private files
are redirected to short-lived presigned URLs.

## Features

- Dynamic `/public/<key>` and `/private/<key>` file routes.
- In-memory file registry loaded from JSON manifests stored in both buckets.
- Startup backfill from S3 when manifests do not exist yet.
- Public bucket precedence when the same key exists in public and private.
- Runtime repair when manifests are stale or disagree with real bucket contents.
- `/status`, `/redines`, `/readiness`, `/helthcheck`, and `/healthcheck` probes.
- Prometheus metrics for public/private file counts and recent uploads.
- Prometheus, Alertmanager, Grafana, Docker Compose, and Argo CD examples.

## Architecture

```mermaid
flowchart LR
  client[Client] --> app[parparchik HTTP service]
  app --> public[(Public S3 bucket)]
  app --> private[(Private S3 bucket)]
  public --> publicManifest[.parparchik/files.json]
  private --> privateManifest[.parparchik/files.json]
  app --> metrics[/metrics]
```

## Runtime flow

1. Startup reads `PARPARCHIK_REGISTRY_MANIFEST_KEY` from both buckets.
2. If neither manifest exists, parparchik scans public and private buckets,
   builds the in-memory registry, writes manifests back, and becomes ready.
3. If manifests exist, parparchik loads them and verifies each record against
   actual S3 object existence.
4. If the same key exists in both buckets, public wins and private manifest
   records are removed for that key.
5. On request miss or stale route, parparchik checks public first, then private,
   serves the found route, updates memory, and writes both manifests.

## Manifest format

```json
{
  "version": 1,
  "bucket_type": "private",
  "files": [
    {
      "key": "1mb_v0.0.1_file.tgz",
      "bucket": "private-bucket",
      "bucket_type": "private",
      "route": "/private/1mb_v0.0.1_file.tgz",
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
| `/public/<key>` | Redirect to public S3 URL. |
| `/private/<key>` | Redirect to presigned S3 URL, or public URL if public wins. |
| `/redines`, `/readiness` | Readiness probe. |
| `/helthcheck`, `/healthcheck` | Liveness probe. |

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
