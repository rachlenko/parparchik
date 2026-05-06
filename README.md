# parparchik

<p align="center">
  <img src="docs/assets/logo.png" alt="parparchik logo" width="200">
</p>

[![Documentation](https://img.shields.io/badge/docs-rachlenko.github.io%2Fparparchik-blue)](https://rachlenko.github.io/parparchik/)

S3 file routing web service. Exposes files from public and private S3 buckets
behind dynamic HTTP routes. When a file moves between buckets, its route
updates automatically — clients always hit the same logical endpoint.

## Architecture

```
┌─────────────┐       ┌──────────────────┐       ┌──────────────┐
│   Client    │──────▶│   parparchik     │──────▶│  S3 / MinIO  │
│  (curl/app) │◀──302─│   :8080          │       │  buckets     │
└─────────────┘       └──────────────────┘       └──────────────┘
                          │                         ▲
                          └──── read/write JSON ────┘
                               `.parparchik/files.json`
```

**File in public bucket** → route is `/public/<key>` → redirect to public S3 URL.

**File in private bucket** → route is `/private/<key>` → redirect to presigned URL.

The in-memory registry is loaded from JSON manifests stored in both buckets.
When a requested key is missing or stale, the service checks the real S3 objects,
serves the found file, and repairs the public/private manifests. If the same key
exists in both buckets, the public bucket wins.

## Quick start (Docker)

Requires: Docker, MinIO client (`mc`).

```bash
# Start MinIO + parparchik
make run-docker

# Run the full e2e test suite (18 assertions)
make test-all MC=/path/to/mc

# Run mock S3 manifest/Prometheus counter scenario
make test-mock-metrics MC=/path/to/mc

# Check service health
make status

# Build the static documentation site
make docs-site

# Stop everything
make docker-down
```

MinIO console is at http://localhost:9001 (user: `minioadmin`, password: `minioadmin`).

## REST API

| Method | Endpoint                   | Description                                        |
|--------|----------------------------|----------------------------------------------------|
| GET    | `/status`                  | Service health, bucket names, file count           |
| GET    | `/redines`                 | Kubernetes readiness probe                         |
| GET    | `/helthcheck`              | Kubernetes liveness probe                          |
| GET    | `/list`                    | All registered files with bucket type and route    |
| GET    | `/update?filename=<name>`  | Sync and return current location of a file         |
| GET    | `/metrics`                 | Prometheus metrics for file volumes and uploads    |
| GET    | `/public/<key>`            | 302 redirect to public S3 URL                      |
| GET    | `/private/<key>`           | 302 redirect to presigned S3 URL (1h expiry)       |

## Argo CD deployment

Use `argocd_deployment.conf.example` as a GitOps deployment starter. It includes
an Argo CD `Application`, Kubernetes workload resources, Prometheus scrape
annotations, and probes wired to `/redines` and `/helthcheck`.

## Prometheus metrics

`/metrics` renders the current in-memory registry state and exposes:

- `parparchik_volume_files{volume="public"}` — current public volume file count.
- `parparchik_volume_files{volume="private"}` — current private volume file count.
- `parparchik_uploads_per_week` — known file versions modified during the last 7 days.
- `parparchik_uploads_per_month` — known file versions modified during the last 31 days.

### Example flow

```bash
# 1. Upload a file to the public bucket
mc cp photo.jpg myminio/public-bucket/photo.jpg

# 2. Query the service
curl http://localhost:8080/update?filename=photo.jpg
# → {"file": {"route": "/public/photo.jpg", "bucket_type": "public", ...}}

# 3. Download via the route
curl -L http://localhost:8080/public/photo.jpg -o photo.jpg

# 4. Move the file to private (externally)
mc mv myminio/public-bucket/photo.jpg myminio/private-bucket/photo.jpg

# 5. The route updates on next access
curl http://localhost:8080/list
# → {"files": [{"route": "/private/photo.jpg", "bucket_type": "private", ...}]}

# 6. Old route returns 404, new route works
curl -I http://localhost:8080/public/photo.jpg   # 404
curl -L http://localhost:8080/private/photo.jpg   # 302 → presigned URL → download
```

## Building the C++ binary

### Prerequisites

- CMake 3.25+
- C++20 compiler (GCC 12+, Clang 15+, Apple Clang 15+)
- git, curl (for initial sync only)

All other dependencies (AWS SDK, httplib, nlohmann-json, prometheus-cpp,
sccache) are managed by the `../vcpkgproxy` sibling repository.

### vcpkgproxy — offline package proxy

The `../vcpkgproxy` repo acts as a local mirror for all upstream dependencies.
After a one-time sync, builds work fully offline.

```
vcpkgproxy/
├── vcpkg/            git clone of github.com/microsoft/vcpkg (registry)
├── downloads/        source tarballs (github.com/awslabs/*, mozilla/sccache, etc.)
├── binary-cache/     pre-built vcpkg packages (*.zip)
├── installed/        unpacked headers + libs ready for linking
├── bin/sccache       cached binary from github.com/mozilla/sccache/releases
├── triplets/         custom triplet overlays (e.g. macOS header fixes)
├── ports/            custom/patched vcpkg ports
└── scripts/
    ├── env.sh        shared paths and versions
    ├── sync.sh       download everything from upstream (ONLINE)
    └── setup.sh      install from local cache (OFFLINE)
```

**Two-phase workflow:**

```bash
# Phase 1: Sync (requires network, run once)
make sync

# Phase 2: Build (fully offline)
make build-all
```

`make sync` downloads into vcpkgproxy:
- vcpkg registry from `github.com/microsoft/vcpkg`
- sccache binary from `github.com/mozilla/sccache/releases`
- All source tarballs for dependencies declared in `vcpkg.json`
- Compiles and caches binary packages locally

`make build-all` uses only local files:
- Restores 23 pre-built packages from `vcpkgproxy/binary-cache/`
- Installs headers and libs to `vcpkgproxy/installed/`
- Configures CMake with the vcpkg toolchain
- Builds `parparchik` with sccache

In short, `vcpkgproxy` gives this project two caches:

- **Download cache**: `VCPKG_DOWNLOADS=../vcpkgproxy/downloads` keeps upstream
  source archives local, so repeated dependency installs do not fetch the
  internet again.
- **Binary package cache**: `VCPKG_DEFAULT_BINARY_CACHE=../vcpkgproxy/binary-cache`
  keeps compiled vcpkg packages as archives, so clean builds can restore
  dependencies instead of rebuilding AWS SDK, Prometheus, and other libraries.
- **Compiler cache**: `sccache` is placed first in `PATH` and configured as the
  C/C++ compiler launcher, so repeated project compiles reuse cached object
  files when compiler flags and inputs match.

The Makefile wires this automatically:

```make
export VCPKG_ROOT := ../vcpkgproxy/vcpkg
export VCPKG_DOWNLOADS := ../vcpkgproxy/downloads
export VCPKG_DEFAULT_BINARY_CACHE := ../vcpkgproxy/binary-cache
cmake -DCMAKE_TOOLCHAIN_FILE=../vcpkgproxy/vcpkg/scripts/buildsystems/vcpkg.cmake \
      -DCMAKE_CXX_COMPILER_LAUNCHER=sccache
```

To set up a new machine, clone or restore `../vcpkgproxy`, run `make sync` once
when network is available, then use `make build-all` or `make build` for cached
offline rebuilds.

### Configuring vcpkg

Dependencies are declared in `vcpkg.json`:

```json
{
  "dependencies": [
    "cpp-httplib",
    "nlohmann-json",
    "prometheus-cpp",
    {"name": "aws-sdk-cpp", "features": ["s3"]}
  ]
}
```

To add a dependency:

1. Add it to the `dependencies` array in `vcpkg.json`.
2. Run `make sync` to download and cache the new package.
3. Add the corresponding `find_package()` and `target_link_libraries()` in `CMakeLists.txt`.

The `builtin-baseline` field in `vcpkg.json` pins the version set. Update it with:

```bash
cd ../vcpkgproxy/vcpkg
git log --oneline -1  # use this commit hash as the baseline
```

Custom or patched ports go in `../vcpkgproxy/ports/<port-name>/` and are
automatically picked up as overlay ports.

### Build

```bash
# Full pipeline: vcpkg setup (offline) → configure → build
make build-all

# Or step by step:
make vcpkg-setup    # install packages from local cache
make configure      # CMake configure (release)
make build          # compile

# Debug build
make configure-debug
make build
```

The binary is at `build/parparchik`.

### Run natively

```bash
cp .env.example .env   # edit with your bucket names and credentials
make run-native
```

## Configuration

All configuration is via environment variables:

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `PARPARCHIK_PUBLIC_BUCKET` | yes | | Name of the public S3 bucket |
| `PARPARCHIK_PRIVATE_BUCKET` | yes | | Name of the private S3 bucket |
| `PARPARCHIK_REGISTRY_MANIFEST_KEY` | no | `.parparchik/files.json` | Manifest object key stored in both buckets |
| `AWS_REGION` | no | `us-east-1` | AWS region |
| `S3_ENDPOINT` | no | | Custom S3 endpoint for MinIO/S3-compatible storage |
| `S3_EXTERNAL_ENDPOINT` | no | | Externally reachable S3 endpoint for generated URLs |
| `AWS_ACCESS_KEY_ID` | no | | AWS credentials |
| `AWS_SECRET_ACCESS_KEY` | no | | AWS credentials |
| `PARPARCHIK_HOST` | no | `0.0.0.0` | Listen address |
| `PARPARCHIK_PORT` | no | `8080` | Listen port |

When running in Docker with MinIO, `S3_ENDPOINT` points to the internal Docker
hostname (`minio:9000`) and `S3_EXTERNAL_ENDPOINT` to the host-reachable address
(`localhost:9000`) so presigned URLs work from outside the container network.

### S3 JSON manifest registry

At startup, parparchik reads `PARPARCHIK_REGISTRY_MANIFEST_KEY` from the public
and private buckets into memory. If neither manifest exists, it scans both
buckets, builds the registry, writes manifests back to both buckets, and only
then marks readiness as healthy.

Manifest format:

```json
{
  "version": 1,
  "bucket_type": "public",
  "files": [
    {
      "key": "example.tgz",
      "bucket": "public-bucket",
      "bucket_type": "public",
      "route": "/public/example.tgz",
      "size": 1048576,
      "last_modified": "2026-05-05T10:00:00Z"
    }
  ]
}
```

Conflict rules:

- If the same key exists in both real buckets, the public object is registered.
- If manifest records disagree, parparchik verifies actual S3 object existence.
- If a requested key is missing from memory, parparchik searches public first,
  then private, returns the found file route, and persists repaired manifests.
- If a manifest record points to a missing object, the stale record is removed.

### Kubernetes probes

- `/helthcheck` returns HTTP 200 when the process is alive. `/healthcheck` is
  available as a spelling-safe alias.
- `/redines` returns HTTP 200 only after S3 JSON manifest load and initial S3 sync have
  completed. Before that it returns HTTP 503. `/readiness` is available as a
  spelling-safe alias.

## Project structure

```
parparchik/
├── CMakeLists.txt          C++ build definition
├── CMakePresets.json        CMake presets (vcpkg + sccache)
├── vcpkg.json               Dependency manifest
├── Makefile                 Build/run/test commands
├── zensical.toml            Zensical static site configuration
├── argocd_deployment.conf.example Argo CD + Kubernetes deployment example
├── docker-compose.yml       MinIO + parparchik stack
├── Dockerfile               Production C++ image
├── Dockerfile.test          Lightweight Python image for testing
├── server.py                Python reference server
├── .env.example             Environment variable template
├── include/parparchik/
│   ├── config.h             Configuration from env vars
│   ├── s3_client.h          AWS S3 SDK wrapper
│   ├── file_registry.h      Thread-safe file → route registry
│   └── server.h             HTTP server
├── src/
│   ├── main.cc              Entry point
│   ├── config.cc
│   ├── s3_client.cc
│   ├── file_registry.cc
│   └── server.cc
├── docs/                    Zensical source documentation and generated site
├── procedures/              Maintenance procedures
├── skills/                  Project-specific workflow notes
└── test/
    └── e2e_test.sh          End-to-end test (18 assertions)

../vcpkgproxy/               Sibling repo — offline package proxy
├── scripts/
│   ├── env.sh                Shared paths and versions
│   ├── sync.sh               Download everything from upstream (ONLINE)
│   └── setup.sh              Install from local cache (OFFLINE)
├── vcpkg/                    git clone of microsoft/vcpkg (gitignored)
├── downloads/                Source tarballs (awslabs/*, mozilla/sccache, etc.)
├── binary-cache/             Pre-built vcpkg packages (*.zip)
├── installed/                Unpacked headers + libs (gitignored)
├── bin/                      Cached binaries (sccache)
├── triplets/                 Custom triplet overlays (e.g. macOS header fixes)
└── ports/                    Custom/patched vcpkg ports
```

## Makefile targets

```
make help               Show all targets

make sync               Download all deps into vcpkgproxy (online)
make vcpkg-setup        Install packages from local cache (offline)

make configure          Configure CMake (release)
make configure-debug    Configure CMake (debug)
make build              Build the C++ binary
make build-all          Full pipeline: vcpkg → configure → build
make clean              Remove build artifacts

make docker-up          Start MinIO + parparchik
make docker-down        Stop containers
make docker-logs        Tail container logs
make docker-restart     Rebuild and restart

make run-native         Run binary locally
make run-docker         Start full Docker stack

make test               Run e2e tests (containers must be up)
make test-all           Start containers + run e2e tests
make test-mock-metrics  Start containers + run mock metrics/S3 JSON manifest scenario

make status             Check service health
make list               List registered files
make metrics            Print Prometheus metrics

make docs-check         Validate Zensical documentation build
make docs-site          Build static documentation into docs/
make docs-serve         Serve docs locally at localhost:8000
make docs-procedure     Show documentation/skills update procedure
```
