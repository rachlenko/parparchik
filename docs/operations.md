---
icon: lucide/settings
---

# Operations

## Prerequisites

- Docker for the local MinIO stack.
- MinIO client `mc` for e2e and mock tests.
- CMake 3.25+ and a C++20 compiler for native builds.
- `../vcpkgproxy` sibling repository for cached dependencies.

## Build

```bash
make sync          # online: fill vcpkgproxy downloads and binary cache
make build-all     # offline path: vcpkg setup, CMake configure, compile
```

The binary is written to `build/parparchik`.

## vcpkg and build cache

Dependencies are declared in `vcpkg.json`. The project uses AWS SDK C++ with the
S3 feature only, plus `cpp-httplib`, `nlohmann-json`, and `prometheus-cpp`.

`../vcpkgproxy` acts as a caching proxy:

- `downloads/` stores source archives.
- `binary-cache/` stores built vcpkg packages.
- `installed/` stores restored headers and libraries.
- `bin/sccache` and CMake compiler launcher settings cache compiler outputs.

Use `make sync` when dependencies or baselines change. Use `make build-all` for
normal repeatable builds; it restores from cache and avoids unnecessary network
work.

## Run locally with Docker

```bash
make run-docker
curl http://localhost:8080/status
```

Docker Compose starts MinIO, creates `public-bucket` and `private-bucket`, and
runs parparchik with `PARPARCHIK_REGISTRY_MANIFEST_KEY=.parparchik/files.json`.

## Native run

```bash
cp .env.example .env
make configure
make build
make run-native
```

Edit `.env` with bucket names, S3 endpoint, and credentials.

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `PARPARCHIK_PUBLIC_BUCKET` | | Public S3 bucket name. |
| `PARPARCHIK_PRIVATE_BUCKET` | | Private S3 bucket name. |
| `PARPARCHIK_REGISTRY_MANIFEST_KEY` | `.parparchik/files.json` | Manifest object key in both buckets. |
| `AWS_REGION` | `us-east-1` | AWS region. |
| `S3_ENDPOINT` | | Internal S3-compatible endpoint, e.g. `minio:9000`. |
| `S3_EXTERNAL_ENDPOINT` | | Host-reachable S3 endpoint for generated URLs. |
| `AWS_ACCESS_KEY_ID` | | AWS or MinIO access key. |
| `AWS_SECRET_ACCESS_KEY` | | AWS or MinIO secret key. |
| `PARPARCHIK_HOST` | `0.0.0.0` | Listen host. |
| `PARPARCHIK_PORT` | `8080` | Listen port. |

## Manifest registry behavior

- Startup reads the JSON manifest from both buckets.
- If both manifests are absent, startup scans both buckets and writes manifests.
- Manifests can be a root array of file entries or an object with a `files`
  array; parparchik writes the object format.
- Public records override private records for duplicate keys.
- Stale manifest records are verified against real S3 objects and repaired.
- A missing request key triggers public-then-private S3 lookup, response serving,
  in-memory update, metric refresh, and manifest persistence.

## Tests

```bash
make test-all
make test-mock-metrics
```

`make test-mock-metrics` creates `1mb_v0.0.1_file.tgz`, uploads it to the
private bucket, prints `/metrics`, prints both JSON manifests, then moves the
object to public and verifies public wins while private returns to zero entries.

## Kubernetes and Argo CD

Use `argocd_deployment.conf.example` as a starter. It contains an Argo CD
`Application`, namespace, config map, deployment, service, service account,
Prometheus annotations, and probes:

- readiness: `/redines` (alias `/readiness`)
- liveness: `/helthcheck` (alias `/healthcheck`)

Production clusters should prefer IAM roles for service accounts or Pod Identity
instead of static access keys.
