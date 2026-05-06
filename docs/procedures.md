---
icon: lucide/list-checks
---

# Procedures

## Documentation update procedure

1. Update source docs in `docs/` and examples in the repository root.
2. Keep `README.md` focused on quick start, architecture, config, and commands.
3. Keep operational details in `docs/operations.md`.
4. Keep monitoring details in `docs/monitoring.md`.
5. Update `skills/parparchik-project.md` when workflows or contracts change.
6. Run `make docs-check`, then `make docs-site` to refresh `site/`.

## Release readiness checklist

- `make build` succeeds.
- `make docs-check` succeeds.
- `make test` or `make test-all` passes when Docker and MinIO are available.
- `make test-mock-metrics` passes with Docker and `mc` available.
- `prometheus.conf.example`, `alertmanager.conf.example`, and
  `grafana-parparchik-desktop.example` match exported metric names.
- README, docs, and generated `site/` describe the S3 JSON manifest registry.

## Coverage checklist

- Architecture and runtime flow: `README.md`, `docs/index.md`.
- Build, vcpkg cache, run, config, Kubernetes, Argo CD: `docs/operations.md`.
- Prometheus, Alertmanager, Grafana, and mock metrics test: `docs/monitoring.md`.
- Maintenance workflow and skill updates: this page and `skills/`.
