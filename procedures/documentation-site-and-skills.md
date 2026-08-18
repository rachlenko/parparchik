# Documentation, Skills, Makefile, and Site Procedure

Use this procedure whenever project behavior, observability, or operations
change.

## Inputs

- Source code changes in `golang/internal/` or `golang/cmd/` (Go implementation).
- Runtime configuration changes in `.env.example`, `golang/docker-compose.yml`.
- Observability changes in metrics, Prometheus, Alertmanager, or Grafana files.
- Build/test workflow changes in `Makefile` (`go-*` targets) or `golang/`.

## Steps

1. Update `README.md` for user-facing quick start changes.
2. Update `golang/README.md` for Go implementation specific changes.
3. Update `docs/` for detailed Zensical documentation.
4. Update `skills/parparchik-project.md` with agent/operator workflow guidance.
5. Update `docs/plans/` if the roadmap or module/format task breakdown changed.
6. Update `Makefile` targets if new workflows need one-command execution.
7. Run `make docs-check`.
8. Run `make docs-site` to generate `site/`.
9. Review `git status --short` and confirm only relevant files changed.

## Documentation dependency flow

<div class="mermaid">
flowchart TD
  change["Code or config change"] --> update_readme["Update README.md +<br/>golang/README.md"]
  update_readme --> update_docs["Update docs/ sources"]
  update_docs --> update_plans["Update docs/plans/ if scope changed"]
  update_plans --> update_skills["Update skills/"]
  update_skills --> docs_check["make docs-check"]
  docs_check --> docs_site["make docs-site"]
  docs_site --> review["git status --short"]
</div>

## Zensical commands

```bash
make docs-check
make docs-site
make docs-serve
```

`docs/*.md` are the Zensical **source** files; `site/` is the generated
static output built from them via `zensical.toml`. Edit `docs/`, then
regenerate `site/` with `make docs-site` — do not hand-edit `site/`.

<script src="https://cdn.jsdelivr.net/npm/mermaid@10/dist/mermaid.min.js"></script>
<script>
  mermaid.initialize({ startOnLoad: true, theme: 'default' });
</script>
