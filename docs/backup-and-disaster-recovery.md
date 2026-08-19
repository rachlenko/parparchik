# Backup & disaster recovery

## The short version

Back up the S3/MinIO buckets. That's it — there is no separate
parparchik-specific state to back up, because there is no separate
parparchik-specific *durable* state: everything the service needs to
rebuild its own operational state (the in-memory catalog) lives inside
the buckets already, as each bucket's `.parparchik/files.json` manifest
(`PARPARCHIK_REGISTRY_MANIFEST_KEY`), written by
`internal/resolver.PersistManifests`.

`scripts/backup.sh` / `scripts/restore.sh` do exactly this: mirror bucket
contents to/from a local (or otherwise durable) directory via the MinIO
client. Restoring is: restore the buckets, start parparchik, let
`Bootstrap` rebuild the catalog from the restored manifests plus a live
sync pass — the same startup path an ordinary restart already takes.

## Why the in-memory catalog needs no separate backup

`internal/catalog.Catalog` is explicitly documented (see
`internal/resolver/resolver.go`'s package doc comment) as a rebuildable
cache, not a source of truth: `Bootstrap` reconstructs it from every
configured bucket's manifest, then a live storage listing reconciles
anything the manifest missed. Losing the catalog (a crash, a restart, a
fresh replica) is not a data-loss event — it's a few seconds of
`Bootstrap` work.

`internal/snapshot` (this task) makes that rebuild faster, not more
correct: `snapshot.Capture`/`snapshot.Restore` let a new process load a
prior instance's catalog state immediately instead of waiting on
`Bootstrap`'s S3 round-trips, with `Bootstrap`'s normal manifest+sync pass
still running afterward to catch up on anything that changed since the
snapshot was taken. It is a startup-time optimization — a cache
warm-start — not a disaster-recovery mechanism, and using it (or not)
makes no difference to what needs backing up.

## Auditing every task this session for state that ISN'T just "the manifest"

The plan's own Task 35 asks this to be verified explicitly, since several
tasks this session (24, 28, 29, 30, 33) each introduce some kind of new
in-memory or derived state. Going through each:

| Source | New state? | Covered by backing up the buckets? |
|---|---|---|
| **Proxy repository caches** (Task 24, `config.KindProxy`) | A cached object lives in its own real S3 bucket, same as any hosted bucket. | **Yes** — it's just another bucket in the backup list. Not *required* for correctness even if skipped: a cache miss just re-fetches from `UpstreamURL` — but skipping it means a restore starts with a cold cache, a performance cost, not a data-loss one. |
| **`internal/cleanup`** (Task 29) | No new state — it only deletes catalog entries + storage objects under configured retention rules. | N/A — nothing to back up; deleting is inherently non-durable by design. |
| **`internal/replication`** (Task 30, pull-only) | No new *durable* state on the pulling instance beyond the objects it already wrote into its own `TargetBucket` (already covered above) and the catalog entries `Register` creates (already covered — rebuildable). | **Yes**, same as any other bucket; also independently re-derivable by re-running `Puller.Pull` against the same source instance, since it's pull-only and idempotent. |
| **`internal/policy.AuditLog`** (Task 28) | **Real gap.** `AuditLog` is purely in-process memory (`sync.Mutex`-guarded slice) — see `internal/policy/audit.go`'s own doc comment: "It does not persist across process restarts." | **No.** A policy engine's audit trail — the record of every allow/quarantine/deny decision, including waiver overrides — is lost on any restart, crash, or replica rotation, today. This is not a bug (the package isn't wired into a live request path yet, so nothing is actually being decided/lost in production), but it's the one genuine finding from this audit: **whoever wires `internal/policy.Engine` into a real request path must also give `AuditLog` real persistence** (write entries to S3, a database, structured logs shipped somewhere durable — any of these) before that audit trail is something an operator can actually rely on for compliance/incident-review purposes. Tracked as follow-up work, not fixed in this session (no live caller exists yet to persist from). |
| **`internal/webhook.Registry`** (Task 33) | In-process memory (subscriber list). | **N/A as data-loss** — this is configuration (which URLs to notify), not state derived from artifacts; the plan for populating a `Registry` was always "load from config at startup," the same as `AuthConfig`'s API key list already works. Losing it on restart just means re-reading config, not losing anything unrecoverable. |
| **`internal/authz`** (Task 32) | Same shape as `webhook.Registry` — `Authorizer`/`ServiceAccounts` are built from config-shaped input, not derived from artifacts. | **N/A**, same reasoning as `webhook.Registry`. |
| **Task 31 (HA/clustering)** | Design doc only this session — no code, no new state introduced. | N/A until implemented; the HA design doc's own Option B (externalizing the catalog to Redis/etc.) would introduce genuinely new infrastructure with its own backup story, to be addressed when/if that's actually built. |

**Conclusion: the "back up the buckets" story from before this session's
Tasks 24–34 still holds**, with one documented, deliberately-deferred
exception (`internal/policy.AuditLog`'s persistence, relevant only once
Task 28 is wired into a live path) and one clarification (proxy caches are
covered but not load-bearing for correctness).

## Running a backup

```bash
BACKUP_BUCKETS="public-bucket private-bucket npm-cache" \
BACKUP_DEST=/backups/parparchik/$(date +%F) \
  ./scripts/backup.sh
```

List every bucket your `PARPARCHIK_BUCKETS` / `PARPARCHIK_PROXY_REPOS`
config actually gives storage to (a virtual repository has none of its
own — see `config.Bucket.HasStorage` — and needs no entry here). See the
script's own header comment for why bucket discovery isn't automated: it
would mean re-implementing `internal/config`'s parsing in bash, a second,
driftable copy of logic that already lives in Go.

## Running a restore

```bash
BACKUP_BUCKETS="public-bucket private-bucket npm-cache" \
BACKUP_SRC=/backups/parparchik/2026-08-19 \
  ./scripts/restore.sh
```

Then start parparchik against the restored buckets normally —
`Bootstrap` rebuilds the catalog from the restored manifests plus a live
sync pass, exactly as it would on any other startup.
