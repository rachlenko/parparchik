---
icon: lucide/server
---

# Nginx + Lua Implementation

Alternative implementation of parparchik using **OpenResty** (Nginx with LuaJIT).
Drop-in replacement with the same REST API, environment variables, and MinIO stack.

## Architecture

```plantuml
!include https://raw.githubusercontent.com/plantuml-stdlib/C4-PlantUML/master/C4_Container.puml

Person(client, "Client", "curl / browser")

System_Boundary(openresty, "OpenResty Container :8080") {
    Container(nginx, "nginx.conf", "Config", "Location routing")
    Container(handlers, "handlers.lua", "Lua", "Request handlers")
    
    System_Boundary(core, "Core Modules") {
        Container(config, "config.lua", "Lua", "Env parser")
        Container(s3, "s3.lua", "Lua", "HTTP client")
        Container(aws, "aws_sig.lua", "Lua", "SigV4 FFI")
        Container(registry, "registry.lua", "Lua", "Shared dict manager")
        Container(metrics_mod, "metrics.lua", "Lua", "Prometheus metrics")
    }
    ContainerDb(dict, "ngx.shared.DICT", "Memory", "file_registry")
}

System_Ext(pub, "public-bucket", "MinIO / S3 :9000")
System_Ext(priv, "private-bucket", "MinIO / S3 :9000")

Rel(client, nginx, "Makes requests")
Rel(nginx, handlers, "Delegates")
Rel(handlers, config, "Uses")
Rel(handlers, s3, "Uses")
Rel(handlers, registry, "Uses")
Rel(handlers, metrics_mod, "Uses")
Rel(s3, aws, "Uses")
Rel(registry, dict, "Stores state")

Rel(s3, pub, "SigV4 signed")
Rel(s3, priv, "SigV4 signed")
Rel(nginx, client, "302 redirect")
```

## Request lifecycle

<div class="mermaid">
sequenceDiagram
  participant C as Client
  participant N as Nginx
  participant H as handlers.lua
  participant R as registry.lua
  participant S as s3.lua
  participant M as MinIO

  C->>N: GET /public/photo.jpg
  N->>H: content_by_lua_block
  H->>R: lookup_by_route

  alt Route found in registry
    R-->>H: entry with key, bucket, route
    H->>S: object_exists
    S->>M: HEAD /bucket/key
    M-->>S: 200 OK
    H->>S: public_url
    H-->>C: 302 Location
  else Route not found
    R-->>H: nil
    H->>S: head_object in each bucket
    alt Found
      H->>R: register_file
      H->>S: persist manifests
      H-->>C: 302 Location
    else Not found anywhere
      H-->>C: 404 Not Found
    end
  end
</div>

## Startup sequence

<div class="mermaid">
sequenceDiagram
  participant W as Worker 0
  participant D as ngx.shared.DICT
  participant S as MinIO / S3

  Note over W: init_worker_by_lua
  W->>W: config.load
  W->>S: try_get_object for each bucket manifest

  alt All manifests present
    W->>D: Load entries from manifest JSON
  else Any manifest missing
    W->>S: list_objects for all buckets
    W->>D: Register all discovered files
  end

  loop For each registered file
    W->>S: HEAD object to reconcile
    alt Missing from S3
      W->>D: Remove stale entry
    end
  end

  W->>S: PUT updated manifests
  W->>D: set __ready__ = 1
  Note over W,D: Service ready
</div>

## File routing decision tree

<div class="mermaid">
flowchart TD
  A["GET /prefix/key"] --> B{"lookup_by_route"}
  B -->|Hit| C{"object_exists in bucket?"}
  C -->|Yes| D["Return entry"]
  C -->|No| E["resolve_missing_file"]

  B -->|Miss| F["Extract key from URL"]
  F --> E

  E --> G{"HEAD in each bucket"}
  G -->|Found| H{"Resolved route = requested route?"}
  H -->|Yes| I["Register + persist"] --> D
  H -->|No| J["404 Not Found"]
  G -->|Not found| K["Remove stale entry"] --> J

  D --> L{"bucket_type?"}
  L -->|public| M["302 public URL"]
  L -->|private| N["302 presigned URL"]
</div>

## Module reference

| Module | Purpose | Key functions |
|--------|---------|---------------|
| `config.lua` | Parse environment variables | `load()` |
| `aws_sig.lua` | AWS SigV4 signing | `sign_request()`, `presigned_url()` |
| `s3.lua` | S3 HTTP operations | `list_objects()`, `head_object()`, `get_object()`, `put_object()` |
| `registry.lua` | File registry | `register_file()`, `lookup()`, `lookup_by_route()`, `list_all()` |
| `metrics.lua` | Prometheus metrics | `render()` |
| `handlers.lua` | HTTP handlers + init | `init()`, `handle_status()`, `handle_list()`, `handle_download()` |

## Technology stack

| Layer | Technology | Purpose |
|-------|-----------|---------|
| Web server | OpenResty 1.27 (Alpine) | Nginx + LuaJIT runtime |
| HTTP client | lua-resty-http 0.17 | Non-blocking cosocket HTTP |
| Crypto | OpenSSL via LuaJIT FFI | HMAC-SHA256 for SigV4 |
| JSON | lua-cjson (bundled) | Request/response encoding |
| State | ngx.shared.DICT (10 MB) | Cross-worker file registry |
| Container | Docker + Compose | MinIO + OpenResty stack |

## Quick start

```bash
cd nginx-lua
make test-all    # Start stack + run 24-assertion e2e test
make status      # Check health
make down        # Stop everything
```

See `nginx-lua/README.md` for the full project README with additional diagrams.

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
