# Documentation, Skills, Makefile, and Site Procedure

Use this procedure whenever project behavior, observability, or operations
change.

## Inputs

- Source code changes in `src/` or `include/` (C++ edition).
- Source code changes in `nginx-lua/lua/` (Nginx + Lua edition).
- Runtime configuration changes in `.env.example`, `docker-compose.yml`, or `nginx-lua/docker-compose.yml`.
- Observability changes in metrics, Prometheus, Alertmanager, or Grafana files.
- Build/test workflow changes in `Makefile` or `nginx-lua/Makefile`.

## Steps

1. Update `README.md` for user-facing quick start changes.
2. Update `nginx-lua/README.md` for Nginx + Lua specific changes.
3. Update `docs/` for detailed Zensical documentation.
4. Update `skills/parparchik-project.md` with agent/operator workflow guidance.
5. Update `Makefile` or `nginx-lua/Makefile` targets if new workflows need one-command execution.
6. Run `make docs-check`.
7. Run `make docs-site` to generate `docs/`.
8. Review `git status --short` and confirm only relevant files changed.

## Documentation dependency flow

<div class="mermaid">
flowchart TD
  change["Code or config change"] --> which{"Which edition?"}
  which -->|C++| update_readme["Update README.md"]
  which -->|Nginx + Lua| update_both["Update README.md +<br/>nginx-lua/README.md"]
  which -->|Both| update_all["Update all READMEs"]

  update_readme --> update_docs["Update docs/ sources"]
  update_both --> update_docs
  update_all --> update_docs

  update_docs --> update_skills["Update skills/"]
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

`docs/` is the generated static output. Rebuild it from the source docs and
`zensical.toml`; do not edit generated HTML by hand.

<script src="https://cdn.jsdelivr.net/npm/mermaid@10/dist/mermaid.min.js"></script>
<script>
  mermaid.initialize({ startOnLoad: true, theme: 'default' });
</script>
