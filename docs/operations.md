---
icon: lucide/settings
---

# Operations

## Prerequisites

- Docker for the local MinIO stack.
- MinIO client `mc` for e2e and mock tests.
- Go 1.25+ for native builds of the Go implementation (`golang/`).

## Go implementation

### Build

```bash
make go-build
# or directly:
cd golang && go build ./...
```

The binary is written to `golang/parparchik` (gitignored; rebuild as needed).

### Run locally with Docker

```bash
make go-run-docker
curl http://localhost:8080/status
```

`golang/docker-compose.yml` starts MinIO, creates `public-bucket` and
`private-bucket`, and runs parparchik with
`PARPARCHIK_REGISTRY_MANIFEST_KEY=.parparchik/files.json`.

### Tests

```bash
make go-test          # go vet + gofmt -l + go test -race -cover ./...
make go-test-e2e       # start the Docker stack and run test/e2e_test.sh against it
```

See [`golang/README.md`](https://github.com/rachlenko/parparchik/blob/main/golang/README.md) for the full package layout,
configuration reference, and the guide for adding new repository formats
(Maven, npm, PyPI, Docker, Helm, NuGet, Debian, RPM, Terraform, ML models).

---

## Step-by-step bucket setup

### Option A — AWS S3

1. Create two S3 buckets:

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

4. Create an IAM user or role with read/write access to both buckets:

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

5. Configure environment variables:

    ```bash
    export PARPARCHIK_PUBLIC_BUCKET=my-public-bucket
    export PARPARCHIK_PRIVATE_BUCKET=my-private-bucket
    export AWS_REGION=us-east-1
    export AWS_ACCESS_KEY_ID=AKIA...
    export AWS_SECRET_ACCESS_KEY=...
    ```

6. Start and verify:

    ```bash
    make run-native
    curl http://localhost:8080/status
    ```

### Option B — MinIO (local development)

1. Start MinIO and parparchik with Docker Compose:

    ```bash
    make go-run-docker
    ```

    This creates `public-bucket` (public read) and `private-bucket` (private)
    in MinIO and wires parparchik to use them.

2. Open the MinIO console at `http://localhost:9001` (user: `minioadmin`,
   password: `minioadmin`) to inspect buckets.

3. Verify and test:

    ```bash
    curl http://localhost:8080/status
    mc alias set local http://localhost:9000 minioadmin minioadmin
    mc cp testfile.txt local/public-bucket/testfile.txt
    curl http://localhost:8080/update?filename=testfile.txt
    curl -L http://localhost:8080/public-bucket/testfile.txt
    ```

## Configuration

| Variable | Default | Description |
| --- | --- | --- |
| `PARPARCHIK_PUBLIC_BUCKET` | | Public S3 bucket name. |
| `PARPARCHIK_PRIVATE_BUCKET` | | Private S3 bucket name. |
| `PARPARCHIK_BUCKETS` | | Multi-bucket config: `name:manifest:public,...` |
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

## Multi-bucket configuration

The Go implementation supports **any number of S3 buckets**. Use the
`PARPARCHIK_BUCKETS` environment variable to define them.

### `PARPARCHIK_BUCKETS` format

```
PARPARCHIK_BUCKETS=<name>:<manifest_key>:<access>,<name>:<manifest_key>:<access>,...
```

| Field | Required | Default | Description |
| --- | --- | --- | --- |
| `name` | **yes** | — | S3 bucket name |
| `manifest_key` | no | `.parparchik/files.json` | Object key for the JSON manifest |
| `access` | no | *private* | Set to `public` for public buckets; omit for private |

**Priority rule:** when the same file key exists in multiple buckets, the
**first bucket** in the list wins.

### Example: 3 buckets

```bash
export PARPARCHIK_BUCKETS="assets-public:.parparchik/files.json:public,docs-internal:.parparchik/files.json,backups-archive:.parparchik/files.json"
```

This configures:

| Bucket | Access | Route prefix | URL redirect |
| --- | --- | --- | --- |
| `assets-public` | public | `/assets-public/<key>` | Direct S3 URL |
| `docs-internal` | private | `/docs-internal/<key>` | Presigned URL |
| `backups-archive` | private | `/backups-archive/<key>` | Presigned URL |

### Example: 5 buckets with custom manifests

```bash
export PARPARCHIK_BUCKETS="cdn-images:manifests/images.json:public,cdn-videos:manifests/videos.json:public,user-uploads:manifests/uploads.json,reports:manifests/reports.json,audit-logs:manifests/audit.json"
```

### Go implementation — Docker Compose

```yaml
services:
  parparchik:
    build:
      context: ./golang
    ports:
      - "8080:8080"
    environment:
      PARPARCHIK_BUCKETS: "assets:.parparchik/files.json:public,docs-internal:.parparchik/files.json,backups:.parparchik/files.json"
      S3_ENDPOINT: minio:9000
      S3_EXTERNAL_ENDPOINT: "localhost:9000"
      AWS_REGION: us-east-1
      AWS_ACCESS_KEY_ID: minioadmin
      AWS_SECRET_ACCESS_KEY: minioadmin
      # See golang/README.md for PARPARCHIK_API_KEYS, rate limit, and sync
      # interval settings not present in the older editions.
```

!!! note
    When the manifest key is omitted (e.g. `assets:public`), it defaults to
    `.parparchik/files.json`. When the access field is omitted (e.g. `docs-internal`),
    the bucket is **private**.

### Kubernetes ConfigMap

```yaml
apiVersion: v1
kind: ConfigMap
metadata:
  name: parparchik-config
data:
  PARPARCHIK_BUCKETS: "prod-assets:.parparchik/files.json:public,staging-assets:.parparchik/files.json:public,internal-docs:.parparchik/files.json,audit-logs:audit/manifest.json"
  AWS_REGION: "eu-west-1"
  S3_ENDPOINT: "s3.eu-west-1.amazonaws.com"
```

### AWS IAM policy for N buckets

When using multiple buckets, the IAM policy must include all bucket ARNs:

```json
{
  "Version": "2012-10-17",
  "Statement": [{
    "Effect": "Allow",
    "Action": [
      "s3:GetObject", "s3:PutObject", "s3:DeleteObject",
      "s3:ListBucket", "s3:HeadObject"
    ],
    "Resource": [
      "arn:aws:s3:::assets-public", "arn:aws:s3:::assets-public/*",
      "arn:aws:s3:::docs-internal", "arn:aws:s3:::docs-internal/*",
      "arn:aws:s3:::backups-archive", "arn:aws:s3:::backups-archive/*"
    ]
  }]
}
```

### MinIO — creating multiple buckets

```bash
mc alias set local http://localhost:9000 minioadmin minioadmin
mc mb local/assets-public local/docs-internal local/backups-archive
mc anonymous set download local/assets-public
```

### Verifying multi-bucket setup

```bash
# Check that all buckets are registered
curl -s http://localhost:8080/status | jq '.buckets'

# Upload a file to each bucket and verify routing
echo "hello" | mc pipe local/assets-public/test.txt
curl -s http://localhost:8080/update?filename=test.txt | jq '.file.route'
# → "/assets-public/test.txt"

curl -sI http://localhost:8080/assets-public/test.txt
# → 302 redirect to public S3 URL
```

### Backward compatibility

The legacy two-bucket variables still work:

```bash
export PARPARCHIK_PUBLIC_BUCKET=my-public
export PARPARCHIK_PRIVATE_BUCKET=my-private
```

This is equivalent to:

```bash
export PARPARCHIK_BUCKETS="my-public:.parparchik/files.json:public,my-private:.parparchik/files.json"
```

!!! warning
    If `PARPARCHIK_BUCKETS` is set, the legacy `PARPARCHIK_PUBLIC_BUCKET` and
    `PARPARCHIK_PRIVATE_BUCKET` variables are **ignored**.

---

## Kubernetes and Argo CD

Use `argocd_deployment.conf.example` as a starter. It contains an Argo CD
`Application`, namespace, config map, deployment, service, service account,
Prometheus annotations, and probes:

- readiness: `/redines` (alias `/readiness`)
- liveness: `/healthcheck`

Production clusters should prefer IAM roles for service accounts or Pod Identity
instead of static access keys.

