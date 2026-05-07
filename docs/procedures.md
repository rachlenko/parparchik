---
icon: lucide/list-checks
---

# Procedures

## Documentation update procedure

1. Update source docs in `docs/` and examples in the repository root.
2. Keep `README.md` focused on quick start, architecture, config, and commands.
3. Keep operational details in `docs/operations.md`.
4. Keep monitoring details in `docs/monitoring.md`.
5. Keep Nginx + Lua details in `docs/nginx-lua.md` and `nginx-lua/README.md`.
6. Update `skills/parparchik-project.md` when workflows or contracts change.
7. Run `make docs-check`, then `make docs-site` to refresh `docs/`.

## Release readiness checklist

### C++ edition

- `make build` succeeds.
- `make docs-check` succeeds.
- `make test` or `make test-all` passes when Docker and MinIO are available.
- `make test-mock-metrics` passes with Docker and `mc` available.

### Nginx + Lua edition

- `cd nginx-lua && make test-all` passes (24 assertions).
- All containers start cleanly with no error logs.
- `curl http://localhost:8080/status` returns `ready: true`.

### Common

- `prometheus.conf.example`, `alertmanager.conf.example`, and
  `grafana-parparchik-desktop.example` match exported metric names.
- README, docs, `nginx-lua/README.md`, and generated `docs/` describe the S3 JSON manifest registry.

## Coverage checklist

- Architecture and runtime flow: `README.md`, `docs/index.md`.
- Build, vcpkg cache, run, config, Kubernetes, Argo CD: `docs/operations.md`.
- Nginx + Lua architecture and modules: `docs/nginx-lua.md`, `nginx-lua/README.md`.
- Prometheus, Alertmanager, Grafana, and mock metrics test: `docs/monitoring.md`.
- Maintenance workflow and skill updates: this page and `skills/`.
