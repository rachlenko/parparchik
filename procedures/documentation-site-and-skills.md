# Documentation, Skills, Makefile, and Site Procedure

Use this procedure whenever project behavior, observability, or operations
change.

## Inputs

- Source code changes in `src/` or `include/`.
- Runtime configuration changes in `.env.example` or `docker-compose.yml`.
- Observability changes in metrics, Prometheus, Alertmanager, or Grafana files.
- Build/test workflow changes in `Makefile`.

## Steps

1. Update `README.md` for user-facing quick start changes.
2. Update `docs/` for detailed Zensical documentation.
3. Update `skills/parparchik-project.md` with agent/operator workflow guidance.
4. Update `Makefile` targets if new workflows need one-command execution.
5. Run `make docs-check`.
6. Run `make docs-site` to generate `site/`.
7. Review `git status --short` and confirm only relevant files changed.

## Zensical commands

```bash
make docs-check
make docs-site
make docs-serve
```

`site/` is the generated static output. Rebuild it from `docs/` and
`zensical.toml`; do not edit generated HTML by hand.

