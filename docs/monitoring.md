---
icon: lucide/activity
---

# Monitoring

`/metrics` exposes Prometheus text format from the current in-memory registry.
The registry is loaded from S3 JSON manifests and repaired on startup or request
misses.

## Metrics

| Metric | Type | Description |
| --- | --- | --- |
| `parparchik_volume_files{volume="public"}` | gauge | Current public bucket registry count. |
| `parparchik_volume_files{volume="private"}` | gauge | Current private bucket registry count. |
| `parparchik_uploads_per_week` | gauge | Files modified during the last 7 days. |
| `parparchik_uploads_per_month` | gauge | Files modified during the last 31 days. |

## Example configs

- `prometheus.conf.example` scrapes `parparchik:8080/metrics`.
- `alertmanager.conf.example` provides a local webhook-style receiver example.
- `grafana-parparchik-desktop.example` is an importable Grafana dashboard JSON
  with prepared panels and alert lines for public/private file counts and recent
  uploads.

## Mock metrics test

```bash
make test-mock-metrics
```

The test prints:

- `/metrics` after private upload.
- Public/private manifest JSON after private upload.
- `/metrics` after moving the object to public.
- Public/private manifest JSON after public wins.

Expected transition:

| Step | Private files | Public files |
| --- | ---: | ---: |
| After private upload | `1` | `0` |
| After move to public | `0` | `1` |
