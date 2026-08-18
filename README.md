# parparchik

<p align="center">
  <img src="docs/assets/logo.png" alt="parparchik logo" width="200">
</p>

[![Documentation](https://img.shields.io/badge/docs-rachlenko.github.io%2Fparparchik-blue)](https://rachlenko.github.io/parparchik/)

S3 file routing web service. Exposes files from configured S3 buckets
behind dynamic HTTP routes. When a file moves between buckets, its route
updates automatically — clients always hit the same logical endpoint.

## Architecture

```ini
┌─────────────┐       ┌──────────────────┐       ┌──────────────┐
│   Client    │──────▶│   parparchik     │──────▶│  S3 / MinIO  │
│  (curl/app) │◀──302─│   :8080          │       │  buckets     │
└─────────────┘       └──────────────────┘       └──────────────┘
                          │                         ▲
                          └──── read/write JSON ────┘
                               `.parparchik/files.json`
```

**File in public bucket** → route is `/<bucket>/<key>` → redirect to public S3 URL.

**File in private bucket** → route is `/<bucket>/<key>` → redirect to presigned URL.

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
| GET    | `/healthcheck`             | Kubernetes liveness probe                          |
| GET    | `/list`                    | All registered files with bucket type and route    |
| GET    | `/update?filename=<name>`  | Sync and return current location of a file         |
| POST   | `/relocate?filename=<name>`| Verify file, relocate between buckets, return ok/fail |
| GET    | `/metrics`                 | Prometheus metrics for file volumes and uploads    |
| GET    | `/<bucket>/<key>`          | 302 redirect to S3 URL (public or presigned)       |

## Argo CD deployment

Use `argocd_deployment.conf.example` as a GitOps deployment starter. It includes
an Argo CD `Application`, Kubernetes workload resources, Prometheus scrape
annotations, and probes wired to `/redines` and `/healthcheck`.

## Prometheus metrics

`/metrics` renders the current in-memory registry state and exposes:

- `parparchik_volume_files{volume="<bucket>"}` — current file count for each configured bucket.
- `parparchik_duplicate_files` — number of file keys that exist in more than one S3 bucket.
- `parparchik_uploads_per_week` — known file versions modified during the last 7 days.
- `parparchik_uploads_per_month` — known file versions modified during the last 31 days.

### Duplicate file alert

`parparchik.rules.yml.example` provides a Prometheus alert rule
`ParparchikDuplicateFiles` that fires when `parparchik_duplicate_files > 0` for
5 minutes. `alertmanager.conf.example` routes this alert to a dedicated
`parparchik-duplicates` receiver with a 12-hour repeat interval.

### Example flow

```bash
# 1. Upload a file to the public bucket
mc cp photo.jpg myminio/public-bucket/photo.jpg

# 2. Query the service
curl http://localhost:8080/update?filename=photo.jpg
# → {"file": {"route": "/public-bucket/photo.jpg", "bucket": "public-bucket", ...}}

# 3. Download via the route
curl -L http://localhost:8080/public-bucket/photo.jpg -o photo.jpg

# 4. Move the file to private (externally)
mc mv myminio/public-bucket/photo.jpg myminio/private-bucket/photo.jpg

# 5. The route updates on next access
curl http://localhost:8080/list
# → {"files": [{"route": "/private-bucket/photo.jpg", "bucket": "private-bucket", ...}]}

# 6. Old route returns 404, new route works
curl -I http://localhost:8080/public-bucket/photo.jpg    # 404
curl -L http://localhost:8080/private-bucket/photo.jpg   # 302 → presigned URL → download
```

## Go implementation (recommended)

`golang/` is the primary, recommended implementation — an idiomatic Go
rewrite built on `aws-sdk-go-v2`, with the extensible
`internal/format.Format` architecture that lets this project grow into a
general multi-format artifact repository (Maven, npm, PyPI, Docker, Helm,
NuGet, Debian, RPM, Terraform, ML models — see
[`docs/plans/`](docs/plans/) for the roadmap).

```bash
cd golang
go build ./...
go test -race -cover ./...

# Or via the root Makefile:
make go-build
make go-test
make go-run-docker   # full MinIO + parparchik stack
```

See [`golang/README.md`](golang/README.md) for configuration, package
layout, and the extension guide for adding new repository formats.

A Python reference server (`server.py`) remains available for comparison
via `make run-docker` / `make test-all`.

## Configuration

### Step-by-step bucket setup

#### Option A — AWS S3

1. Create two S3 buckets in the AWS console or CLI:

```bash
aws s3 mb s3://my-public-bucket --region us-east-1
aws s3 mb s3://my-private-bucket --region us-east-1
```

2. Make the public bucket publicly readable:

```bash
aws s3api put-bucket-policy --bucket my-public-bucket --policy '{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Principal": "*",
    "Action": "s3:GetObject",
    "Resource": "arn:aws:s3:::my-public-bucket/*"
  }]
}'
```

3. Keep the private bucket with default access (private). Parparchik generates
   presigned URLs for private files automatically.

4. Create an IAM user or role with read/write access to both buckets. The
minimum policy is:

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Action": ["s3:GetObject", "s3:PutObject", "s3:DeleteObject",
               "s3:ListBucket", "s3:HeadObject"],
    "Resource": [
      "arn:aws:s3:::my-public-bucket", "arn:aws:s3:::my-public-bucket/*",
      "arn:aws:s3:::my-private-bucket", "arn:aws:s3:::my-private-bucket/*"
    ]
  }]
}
```

5. Configure parparchik:

```bash
export PARPARCHIK_BUCKETS=my-public-bucket:.parparchik/files.json:public,my-private-bucket:.parparchik/files.json
export AWS_REGION=us-east-1
export AWS_ACCESS_KEY_ID=AKIA...
export AWS_SECRET_ACCESS_KEY=...
```

6. Start the service:

```bash
make run-native
```

7. Verify both buckets are connected:

```bash
curl http://localhost:8080/status
```

#### Option B — MinIO (local development)

1. Start MinIO and parparchik with Docker Compose:

```bash
make run-docker
```

This automatically creates `public-bucket` and `private-bucket` in MinIO
and configures parparchik to use them.

2. Open the MinIO console at http://localhost:9001 (user: `minioadmin`,
   password: `minioadmin`) to inspect buckets.

3. Verify the service:

```bash
curl http://localhost:8080/status
```

4. Upload a test file and confirm routing:

```bash
mc alias set local http://localhost:9000 minioadmin minioadmin
mc cp testfile.txt local/public-bucket/testfile.txt
curl http://localhost:8080/update?filename=testfile.txt
curl -L http://localhost:8080/public-bucket/testfile.txt
```

### Environment variables

All configuration is via environment variables:

| Variable | Required | Default | Description |
| --- | --- | --- | --- |
| `PARPARCHIK_BUCKETS` | yes* | | Comma-separated bucket list: `name:manifest_key:public` (`:public` suffix marks public buckets, manifest key defaults to `.parparchik/files.json`) |
| `PARPARCHIK_PUBLIC_BUCKET` | yes* | | Legacy: name of the public S3 bucket |
| `PARPARCHIK_PRIVATE_BUCKET` | yes* | | Legacy: name of the private S3 bucket |
| `AWS_REGION` | no | `us-east-1` | AWS region |
| `S3_ENDPOINT` | no | | Custom S3 endpoint for MinIO/S3-compatible storage |
| `S3_EXTERNAL_ENDPOINT` | no | | Externally reachable S3 endpoint for generated URLs |
| `AWS_ACCESS_KEY_ID` | no | | AWS credentials |
| `AWS_SECRET_ACCESS_KEY` | no | | AWS credentials |
| `PARPARCHIK_HOST` | no | `0.0.0.0` | Listen address |
| `PARPARCHIK_PORT` | no | `8080` | Listen port |

*Set either `PARPARCHIK_BUCKETS` or both `PARPARCHIK_PUBLIC_BUCKET` and
`PARPARCHIK_PRIVATE_BUCKET`. The legacy variables are supported for backward
compatibility and create two buckets with the default manifest key.

When running in Docker with MinIO, `S3_ENDPOINT` points to the internal Docker
hostname (`minio:9000`) and `S3_EXTERNAL_ENDPOINT` to the host-reachable address
(`localhost:9000`) so presigned URLs work from outside the container network.

### S3 JSON manifest registry

At startup, parparchik reads each bucket's manifest using its configured key.
If any manifest is missing, it scans all buckets, builds the registry, writes
manifests back, and only then marks readiness as healthy.

Manifest format:

```json
{
  "version": 1,
  "bucket": "my-public-bucket",
  "files": [
    {
      "key": "example.tgz",
      "bucket": "my-public-bucket",
      "route": "/my-public-bucket/example.tgz",
      "size": 1048576,
      "last_modified": "2026-05-05T10:00:00Z"
    }
  ]
}
```

Conflict rules:

- If the same key exists in multiple buckets, the highest-priority bucket (first in config) wins.
- If manifest records disagree, parparchik verifies actual S3 object existence.
- If a requested key is missing from memory, parparchik searches buckets in priority order,
  returns the found file route, and persists repaired manifests.
- If a manifest record points to a missing object, the stale record is removed.

### Kubernetes probes

- `/healthcheck` returns HTTP 200 when the process is alive.
- `/redines` returns HTTP 200 only after S3 JSON manifest load and initial S3 sync have
   completed. Before that it returns HTTP 503. `/readiness` is available as a
   spelling-safe alias.

## Project structure

```ini
parparchik/
├── Makefile                 Build/run/test commands (Go + Python reference server)
├── zensical.toml             Zensical static site configuration
├── argocd_deployment.conf.example Argo CD + Kubernetes deployment example
├── docker-compose.yml       MinIO + Python reference server stack
├── Dockerfile.test          Lightweight Python image for testing
├── server.py                Python reference server
├── .env.example             Environment variable template
├── golang/                  Go implementation (recommended) — see golang/README.md
│   ├── cmd/parparchik/       Entrypoint
│   ├── internal/             config, catalog, objectstore, format, resolver, httpapi, metricsapi
│   ├── Dockerfile
│   └── docker-compose.yml    MinIO + parparchik (Go) stack
├── docs/                    Zensical source documentation, generated site, and docs/plans/ (roadmap)
├── procedures/              Maintenance procedures
├── skills/                  Project-specific workflow notes
└── test/
    └── e2e_test.sh          End-to-end test (18 assertions)
```

## Makefile targets

```md
make help               Show all targets

make go-build           Build the Go binary (golang/)
make go-test            Run the Go test suite (race + coverage)
make go-docker-up       Start MinIO + the Go parparchik service
make go-docker-down     Stop the Go implementation's Docker stack
make go-docker-logs     Tail the Go implementation's container logs
make go-run-docker      Start the Go implementation's full Docker stack
make go-test-e2e        Start the Go stack and run e2e tests against it

make docker-up          Start MinIO + the Python reference server
make docker-down        Stop containers
make docker-logs        Tail container logs
make docker-restart     Rebuild and restart

make run-docker         Start the Python reference server's full stack

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
