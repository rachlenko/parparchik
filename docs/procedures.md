---
icon: lucide/list-checks
---

# Procedures

## Documentation update procedure

1. Update source docs in `docs/` and examples in the repository root.
2. Keep `README.md` focused on quick start, architecture, config, and commands.
3. Keep operational details in `docs/operations.md`.
4. Keep monitoring details in `docs/monitoring.md`.
5. Keep Go implementation details in `golang/README.md`.
6. Update `skills/parparchik-project.md` when workflows or contracts change.
7. Run `make docs-check`, then `make docs-site` to refresh `docs/`.

## Release readiness checklist

### Go implementation

- `make go-build` succeeds.
- `make go-test` (`go vet` + `gofmt -l` + `go test -race -cover ./...`) passes.
- `make docs-check` succeeds.
- `make go-test-e2e` passes when Docker and MinIO are available.

### Common

- `prometheus.conf.example`, `alertmanager.conf.example`, and
  `grafana-parparchik-desktop.example` match exported metric names.
- README, docs, `golang/README.md`, and generated `docs/` describe the S3 JSON manifest registry.

## Coverage checklist

- Architecture and runtime flow: `README.md`, `docs/index.md`.
- Build, run, config, Kubernetes, Argo CD: `docs/operations.md`.
- Go implementation package layout and extension guide: `golang/README.md`.
- Prometheus, Alertmanager, Grafana, and mock metrics test: `docs/monitoring.md`.
- Maintenance workflow and skill updates: this page and `skills/`.
