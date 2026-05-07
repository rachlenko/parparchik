# parparchik — Nginx + Lua Edition

<p align="center">
  <img src="../docs/assets/logo.png" alt="parparchik logo" width="200">
</p>

Alternative implementation of the parparchik S3 file routing service using
**OpenResty** (Nginx with LuaJIT). Drop-in replacement for the C++ and Python
implementations — same REST API, same environment variables, same MinIO stack.

## Architecture

```plantuml
!include https://raw.githubusercontent.com/plantuml-stdlib/C4-PlantUML/master/C4_Container.puml

System_Boundary(docker, "Docker Network") {
    Person(client, "Client", "curl / app")
    Container(openresty, "OpenResty :8080", "Nginx + LuaJIT")
    System_Ext(minio, "MinIO / S3 :9000")
    
    Rel(client, openresty, "Requests")
    Rel(openresty, minio, "SigV4 signed")
    Rel(openresty, client, "302 redirect")
}

System_Boundary(openresty_int, "OpenResty Internals") {
    Container(nginx_conf, "nginx.conf", "Config", "Routing")
    Container(handlers, "handlers.lua", "Lua")
    ContainerDb(registry, "registry.lua", "ngx.shared.DICT")
    Container(s3_client, "s3.lua", "resty.http")
    Container(aws_sig, "aws_sig.lua", "SigV4 FFI")
    Container(metrics_mod, "metrics.lua", "Prometheus")
    Container(config_mod, "config.lua", "Env vars")
    
    Rel(nginx_conf, handlers, "Uses")
    Rel(handlers, registry, "Uses")
    Rel(handlers, s3_client, "Uses")
    Rel(s3_client, aws_sig, "Uses")
    Rel(handlers, metrics_mod, "Uses")
    Rel(handlers, config_mod, "Uses")
}
```

## How it works

<div class="mermaid">
sequenceDiagram
  participant C as Client
  participant N as OpenResty
  participant R as Shared Dict Registry
  participant S as MinIO / S3

  Note over N: Startup init_worker
  N->>S: GET manifests from all buckets
  S-->>N: JSON manifest data
  N->>R: Load entries into shared dict
  N->>S: Reconcile (HEAD each object)
  N->>S: PUT updated manifests
  Note over N: ready = true

  C->>N: GET /public/photo.jpg
  N->>R: lookup_by_route("/public/photo.jpg")
  R-->>N: {key: "photo.jpg", bucket: "public-bucket"}
  N->>S: HEAD public-bucket/photo.jpg (verify)
  S-->>N: 200 OK
  N-->>C: 302 → http://minio:9000/public-bucket/photo.jpg
</div>

## Module architecture

<div class="mermaid">
graph TB
  subgraph Lua Modules
    config[config.lua<br/><i>env var parser</i>]
    aws[aws_sig.lua<br/><i>SigV4 signing via<br/>OpenSSL FFI</i>]
    s3[s3.lua<br/><i>S3 HTTP client<br/>resty.http + SigV4</i>]
    reg[registry.lua<br/><i>file registry<br/>ngx.shared.DICT</i>]
    met[metrics.lua<br/><i>Prometheus text<br/>format renderer</i>]
    hdl[handlers.lua<br/><i>HTTP request<br/>handlers</i>]
  end

  hdl --> config
  hdl --> s3
  hdl --> reg
  hdl --> met
  s3 --> aws
  s3 --> config

  subgraph Nginx
    nginx[nginx.conf] --> hdl
  end

  subgraph Shared State
    dict[(ngx.shared.DICT<br/>file_registry 10 MB)]
  end

  reg --> dict
</div>

## File routing logic

<div class="mermaid">
flowchart TD
  req[GET /<prefix>/<key>] --> lookup{Route in<br/>registry?}
  lookup -->|Yes| verify{Object exists<br/>in S3?}
  verify -->|Yes| redirect[302 Redirect<br/>to S3 URL]
  verify -->|No| resolve[Search all buckets<br/>in priority order]
  lookup -->|No| resolve
  resolve --> found{Found in<br/>any bucket?}
  found -->|Yes| check_route{Resolved route<br/>= requested route?}
  check_route -->|Yes| register[Register + persist<br/>manifests] --> redirect
  check_route -->|No| notfound[404 Not Found]
  found -->|No| notfound

  redirect --> public_check{Bucket is<br/>public?}
  public_check -->|Yes| pub_url[Public URL<br/>http://endpoint/bucket/key]
  public_check -->|No| pre_url[Presigned URL<br/>SigV4 query-string signed]
</div>

## Quick start

```bash
# Start MinIO + parparchik
make up

# Run the full e2e test suite (24 assertions)
make test-all

# Check service health
make status

# Stop everything
make down
```

MinIO console: http://localhost:9001 (user: `minioadmin`, password: `minioadmin`).

## REST API

| Method | Endpoint                    | Description                                          |
|--------|-----------------------------|------------------------------------------------------|
| GET    | `/status`                   | Service health, bucket names, file count             |
| GET    | `/redines`, `/readiness`    | Kubernetes readiness probe                           |
| GET    | `/helthcheck`, `/healthcheck` | Kubernetes liveness probe                          |
| GET    | `/list`                     | All registered files with bucket type and route      |
| GET    | `/update?filename=<name>`   | Sync and return current location of a file           |
| POST   | `/relocate?filename=<name>` | Verify file, relocate between buckets                |
| GET    | `/metrics`                  | Prometheus metrics                                   |
| GET    | `/public/<key>`             | 302 redirect to public S3 URL                        |
| GET    | `/private/<key>`            | 302 redirect to presigned S3 URL                     |

## Configuration

Same environment variables as the C++ and Python implementations:

| Variable | Default | Description |
| --- | --- | --- |
| `PARPARCHIK_PUBLIC_BUCKET` | | Public S3 bucket name |
| `PARPARCHIK_PRIVATE_BUCKET` | | Private S3 bucket name |
| `PARPARCHIK_BUCKETS` | | Multi-bucket config: `name:manifest:public,...` |
| `S3_ENDPOINT` | | Internal S3 endpoint (e.g. `minio:9000`) |
| `S3_EXTERNAL_ENDPOINT` | | External S3 endpoint for redirect URLs |
| `AWS_REGION` | `us-east-1` | AWS region |
| `AWS_ACCESS_KEY_ID` | | AWS/MinIO access key |
| `AWS_SECRET_ACCESS_KEY` | | AWS/MinIO secret key |

## Project structure

```
nginx-lua/
├── Dockerfile              OpenResty Alpine container
├── docker-compose.yml      MinIO + parparchik stack
├── Makefile                Build/run/test commands
├── nginx.conf              OpenResty route configuration
├── lua/
│   ├── config.lua          Environment variable parser
│   ├── aws_sig.lua         AWS SigV4 via OpenSSL FFI
│   ├── s3.lua              S3 client (resty.http)
│   ├── registry.lua        File registry (ngx.shared.DICT)
│   ├── metrics.lua         Prometheus text-format metrics
│   └── handlers.lua        HTTP request handlers
└── test/
    └── e2e_test.sh         End-to-end test (24 assertions)
```

## Technology stack

| Component | Technology | Purpose |
|-----------|-----------|---------|
| Web server | OpenResty 1.27 | Nginx + LuaJIT runtime |
| HTTP client | lua-resty-http | Non-blocking S3 requests |
| Crypto | OpenSSL via FFI | AWS SigV4 HMAC-SHA256 |
| State | ngx.shared.DICT | Cross-worker file registry |
| Metrics | Pure Lua | Prometheus text format |
| Container | Alpine Linux | Minimal Docker image |

## Comparison with C++ implementation

| Feature | C++ | Nginx + Lua |
|---------|-----|-------------|
| Binary size | ~20 MB + libs | ~60 MB (OpenResty) |
| Build time | Minutes (vcpkg + CMake) | Seconds (Docker layer cache) |
| Dependencies | AWS SDK, httplib, json, prometheus-cpp | lua-resty-http only |
| S3 signing | AWS SDK | Pure Lua SigV4 via FFI |
| State | `std::unordered_map` + mutex | `ngx.shared.DICT` |
| Concurrency | Thread pool | Nginx event loop + coroutines |
| Routes | `/bucket-name/key` | `/public/key`, `/private/key` |

## Makefile targets

```
make up          Start MinIO + parparchik containers
make down        Stop and remove all containers
make restart     Rebuild and restart everything
make test        Run e2e tests (containers must be running)
make test-all    Start containers + run e2e tests
make status      Check service status
make list        List all registered files
make metrics     Print Prometheus metrics
make logs        Tail container logs
make help        Show all targets
```

<script src="https://cdn.jsdelivr.net/npm/mermaid@10/dist/mermaid.min.js"></script>
<script>
  mermaid.initialize({ startOnLoad: true, theme: 'default' });
</script>
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
