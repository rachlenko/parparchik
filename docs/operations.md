---
icon: lucide/settings
---

# Operations

## Prerequisites

- Docker for the local MinIO stack.
- MinIO client `mc` for e2e and mock tests.
- CMake 3.25+ and a C++20 compiler for native builds (C++ edition only).
- `../vcpkgproxy` sibling repository for cached dependencies (C++ edition only).

## C++ implementation

### Build

```bash
make sync          # online: fill vcpkgproxy downloads and binary cache
make build-all     # offline path: vcpkg setup, CMake configure, compile
```

The binary is written to `build/parparchik`.

### vcpkg and build cache

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

### Run locally with Docker

```bash
make run-docker
curl http://localhost:8080/status
```

Docker Compose starts MinIO, creates `public-bucket` and `private-bucket`, and
runs parparchik with `PARPARCHIK_REGISTRY_MANIFEST_KEY=.parparchik/files.json`.

### Native run

```bash
cp .env.example .env
make configure
make build
make run-native
```

Edit `.env` with bucket names, S3 endpoint, and credentials.

### Tests

```bash
make test-all
make test-mock-metrics
```

`make test-mock-metrics` creates `1mb_v0.0.1_file.tgz`, uploads it to the
private bucket, prints `/metrics`, prints both JSON manifests, then moves the
object to public and verifies public wins while private returns to zero entries.

---

## Nginx + Lua implementation

### Architecture overview

```plantuml
!include https://raw.githubusercontent.com/plantuml-stdlib/C4-PlantUML/master/C4_Container.puml

System_Boundary(docker, "Docker") {
    Person(client, "Client")
    Container(openresty, "OpenResty :8080", "Nginx", "Web Server")
    System_Ext(minio, "MinIO :9000", "S3 Storage")
    
    Rel(client, openresty, "Requests")
    Rel(openresty, minio, "SigV4")
    Rel(openresty, client, "302 Redirect")
}

System_Boundary(modules, "Modules") {
    Container(nginx, "nginx.conf", "Config")
    Container(handlers, "handlers.lua", "Lua")
    Container(registry, "registry.lua", "Lua")
    Container(s3, "s3.lua", "Lua")
    Container(aws_sig, "aws_sig.lua", "Lua")
    Container(metrics_mod, "metrics.lua", "Lua")
    Container(config, "config.lua", "Lua")
    
    Rel(nginx, handlers, "Uses")
    Rel(handlers, registry, "Uses")
    Rel(handlers, s3, "Uses")
    Rel(s3, aws_sig, "Uses")
    Rel(handlers, metrics_mod, "Uses")
    Rel(handlers, config, "Uses")
}
```

### Build and run

```bash
cd nginx-lua
make up          # Start MinIO + OpenResty containers
make test        # Run e2e tests (24 assertions)
make test-all    # Combined: start + test
make down        # Stop and remove containers
```

No compilation required — Lua scripts are copied directly into the container.

### Module responsibilities

| Module | Dependencies | Purpose |
|--------|-------------|---------|
| `config.lua` | none | Parse `PARPARCHIK_*`, `S3_*`, `AWS_*` env vars |
| `aws_sig.lua` | OpenSSL FFI | HMAC-SHA256, SigV4 request signing, presigned URLs |
| `s3.lua` | resty.http, aws_sig | ListObjects, HeadObject, GetObject, PutObject |
| `registry.lua` | ngx.shared.DICT | File to route mapping, manifest load/persist |
| `metrics.lua` | none | Prometheus text-format gauge rendering |
| `handlers.lua` | all above | Init, sync, resolve, HTTP handlers |

See [Nginx + Lua](nginx-lua.md) for full module diagrams and routing logic.

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
    make run-docker     # C++ edition
    # or
    cd nginx-lua && make up   # Nginx + Lua edition
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

Both implementations (C++ and Nginx + Lua) support **any number of S3 buckets**.
Use the `PARPARCHIK_BUCKETS` environment variable to define them.

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

### C++ implementation — Docker Compose

```yaml
services:
  parparchik:
    build: .
    ports:
      - "8080:8080"
    environment:
      PARPARCHIK_BUCKETS: "assets:public,docs-internal,backups"
      S3_ENDPOINT: minio:9000
      AWS_REGION: us-east-1
      AWS_ACCESS_KEY_ID: minioadmin
      AWS_SECRET_ACCESS_KEY: minioadmin
```

!!! note
    When the manifest key is omitted (e.g. `assets:public`), it defaults to
    `.parparchik/files.json`. When the access field is omitted (e.g. `docs-internal`),
    the bucket is **private**.

### Nginx + Lua implementation — Docker Compose

```yaml
services:
  parparchik:
    build:
      context: ./nginx-lua
    ports:
      - "8080:8080"
    environment:
      PARPARCHIK_BUCKETS: "assets:.parparchik/files.json:public,docs-internal:.parparchik/files.json,backups:.parparchik/files.json"
      S3_ENDPOINT: minio:9000
      S3_EXTERNAL_ENDPOINT: "localhost:9000"
      AWS_REGION: us-east-1
      AWS_ACCESS_KEY_ID: minioadmin
      AWS_SECRET_ACCESS_KEY: minioadmin
```

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
- liveness: `/helthcheck` (alias `/healthcheck`)

Production clusters should prefer IAM roles for service accounts or Pod Identity
instead of static access keys.

<script src="https://cdn.jsdelivr.net/npm/plantuml-encoder@1.4.0/dist/plantuml-encoder.min.js"></script>
<script>
document.querySelectorAll('.language-plantuml, .language-text').forEach(function(el) {
    var text = el.textContent;
    if (text.includes('!include') || text.includes('System_Boundary') || text.includes('Person(')) {
        var encoded = plantumlEncoder.encode(text);
        var img = document.createElement('img');
        img.src = 'https://www.plantuml.com/plantuml/svg/' + encoded;
        el.parentNode.replaceChild(img, el);
    }
});
</script>
