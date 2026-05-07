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
| `parparchik_volume_files{volume="<bucket>"}` | gauge | Current file count for each configured bucket. |
| `parparchik_duplicate_files` | gauge | Number of file keys that exist in more than one S3 bucket. |
| `parparchik_uploads_per_week` | gauge | Files modified during the last 7 days. |
| `parparchik_uploads_per_month` | gauge | Files modified during the last 31 days. |

## Alerts

`parparchik.rules.yml.example` defines Prometheus alert rules:

| Alert | Condition | Severity | Description |
| --- | --- | --- | --- |
| `ParparchikDuplicateFiles` | `parparchik_duplicate_files > 0` for 5m | warning | One or more file keys exist in multiple S3 buckets. The highest-priority copy is served, but duplicates waste storage and may cause confusion. |

`alertmanager.conf.example` routes `ParparchikDuplicateFiles` to a dedicated
`parparchik-duplicates` receiver with a 12-hour repeat interval to avoid noise.

## Example configs

- `prometheus.conf.example` scrapes `parparchik:8080/metrics`.
- `parparchik.rules.yml.example` defines Prometheus alert rules including duplicate file detection.
- `alertmanager.conf.example` provides a local webhook-style receiver example with a dedicated route for duplicate alerts.
- `grafana-parparchik-desktop.example` is an importable Grafana dashboard JSON
  with prepared panels and alert lines for file counts and recent uploads.

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
