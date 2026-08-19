# Design: High availability & clustering (Task 31)

Status: draft, no code written against this yet. This document exists because
Task 31 in [`20260818-go-parparchik-rewrite.md`](./20260818-go-parparchik-rewrite.md)
is explicitly flagged as an architectural blocker, not a drop-in task — the
plan calls for a design doc before any implementation, and this is it.

## Problem statement

`internal/catalog.Catalog` is an in-process `map[string]Entry` guarded by a
single `sync.RWMutex` (`internal/catalog/catalog.go`). `internal/resolver`'s
own package doc comment states the actual operating assumption plainly:

> "the in-memory catalog (the actual source of truth for routing decisions)
> is already correct, and the next periodic sync ... will retry the write"
> — `internal/resolver/resolver.go:9-10`

That assumption is true for exactly one running process. Put two or more
`cmd/parparchik` replicas behind a load balancer and it stops being true:
each replica has its own catalog, its own view of which bucket "owns" a
given key, and the two only ever reconcile indirectly, through S3 manifests,
on `PARPARCHIK_SYNC_INTERVAL` (default 5m) or at `Bootstrap`. Concretely,
today, with N replicas:

- A client hitting replica A's `POST /relocate` gets a routing decision
  replica A computed from replica A's catalog. The same key, requested from
  replica B a moment later, can resolve differently until the next sync.
- `Relocate` and `ResolveMissingFile` both mutate the local catalog
  synchronously and call `PersistManifests` to write it back to S3 — but
  that write is a snapshot of *that replica's* current manifest state for
  the bucket(s) it touched, not a coordinated update. Two replicas
  relocating the same key at nearly the same moment can both believe they
  won, both write a manifest, and the last write to a given bucket's
  manifest key wins with no conflict detection.
- `internal/cleanup` and `internal/replication` (Tasks 29/30, both merged
  ahead of this doc) each assume they're the only writer touching the local
  catalog + storage pair they were given — neither one accounts for a peer
  replica concurrently doing the same thing.

None of this is a "someone made a mistake" finding — every one of those
components was built and reviewed under the explicit, and until now
accurate, assumption that a `Catalog` belongs to exactly one process.

## Requirements and non-goals

Before picking a mechanism, "HA" needs to be pinned down, because the three
things people usually mean by it pull in different directions:

1. **Availability** — an instance crashing or restarting shouldn't take
   routing down. This is mostly already true today at the load-balancer
   level (multiple replicas, k8s-style readiness probes already exist in
   `cmd/parparchik`) — what's missing is *correctness* across replicas, not
   uptime of any one of them.
2. **Consistency** — replicas agree on which bucket owns a key, at least
   eventually, ideally with a bounded staleness window much shorter than
   today's 5-minute default sync interval.
3. **Scale beyond one process's memory** — the catalog growing past what one
   process can hold in a map. Nothing in this codebase or its current usage
   suggests this is an actual near-term problem; a real deployment's
   artifact count would need to be enormous (tens of millions of entries)
   before an in-memory map genuinely becomes the bottleneck, and no one has
   asked for this. **Non-goal for this design** — revisit if a concrete
   catalog size ever motivates it.

This document is scoped to (1) and (2): multiple replicas of the same
`cmd/parparchik` service agreeing on routing state, without inventing a
horizontal-partitioning story nobody has asked for.

## Option A: objectstore-only reconciliation (no new infrastructure)

The plan doc itself asks whether the *current* design — each replica
independently re-derives its catalog from S3 via `Bootstrap`/`SyncRegistry`
— is "actually sufficient for a first HA pass... given manifests are
already the durable source of truth." Worth taking seriously before reaching
for new infrastructure.

**What it would take:** nothing new to build. Run N replicas of the
existing binary today; each already does this. The question is whether the
existing staleness window is acceptable.

**Where it holds up:**
- Read-heavy traffic (`GET /list`, `GET /{bucket}/{key}`, `GET /update` for
  an already-known key) is unaffected by which replica answers — every
  replica's `ListAll`/`Lookup` reflects *some* recent, self-consistent
  snapshot of storage, just not necessarily the same snapshot as its peers.
- S3 (and MinIO, in practice) is strongly read-after-write consistent for
  both PUTs and DELETEs, so a replica's own `Bootstrap`/`SyncRegistry` pass
  never itself sees a stale view of the bucket it just wrote to.

**Where it breaks down:**
- `POST /relocate` and `ResolveMissingFile` (a `GET /update` cache miss) are
  synchronous, client-visible mutations. Two replicas can race on the same
  key with no coordination beyond "last manifest write to S3 wins," and a
  client can observe a relocate as failed→retried→succeeded→reverted across
  requests landing on different replicas, purely as an artifact of which
  replica's manifest write landed last.
- The staleness window between a mutation on replica A and replica B
  observing it is bounded only by `PARPARCHIK_SYNC_INTERVAL` (5m default) —
  far too coarse for anything that wants read-your-writes behavior across a
  load balancer with no session affinity.
- `internal/cleanup.Execute` and a future `internal/replication` puller
  goroutine, if ever scheduled on more than one replica, would each
  independently decide what to delete/pull with no shared view of what a
  peer already did — not incorrect exactly (deleting an already-deleted key
  is a documented no-op; `internal/replication`'s `Register` call already
  tolerates a peer having a different opinion), but wasteful, and a real
  footgun if two replicas' cleanup rules ever disagree (e.g. mid-rollout of
  a config change) about what should exist.

**Verdict:** sufficient for a **read-mostly, single-writer** deployment
shape — e.g. multiple replicas behind a load balancer where all mutating
traffic (`/relocate`, uploads landing on a cache-miss `/update`) is either
rare, or funneled to one active replica at a time (active/passive, not
active/active). Not sufficient for a deployment that wants active/active
writes with tight consistency. Given this project has no evidence yet of
either failure mode actually biting anyone in production — there's no
concrete deployment to size this against — **Option A is the pragmatic
first step**: ship multi-replica read scaling with the existing mechanism,
document the write-path staleness/race explicitly (this section already is
that documentation), and revisit Option B only once a real requirement
(active/active writes, a bounded consistency SLA) shows up.

## Option B: externalize the catalog to a shared backend

If/when Option A's limitations become a real problem, the fix the plan doc
proposes is to move `Catalog`'s storage out of a process-local map into a
shared backend, **behind the same method surface**, so nothing outside
`internal/catalog` needs to change. This is achievable exactly as stated:
`resolver.Resolver` and `httpapi.API` both hold a concrete `*catalog.Catalog`
field (not an interface — confirmed by reading both constructors), so
Go's structural typing means swapping `Catalog`'s internal fields from a
`map`+`sync.RWMutex` to a backend client, while keeping the type name and
every exported method's signature identical, requires zero changes to
`resolver`, `httpapi`, `metricsapi`, `cmd/parparchik`, or any of Tasks
25-30's packages. The actual exported surface every current caller uses,
confirmed by grepping every call site outside `internal/catalog`:

```
New(priority, bucketType) *Catalog
Register(key, bucket string, size int64, lastModified string)
Set(key, bucket string, size int64, lastModified string)
Remove(key string)
Lookup(key string) (Entry, bool)
LookupByRoute(route string) (Entry, bool)
ListAll() []Entry
ManifestForBucket(bucket string) Manifest
LoadManifests(manifests []BucketManifest)
Count() int
```

### The actual hard part: atomic priority-checked Register

`Register`'s documented behavior (`catalog.go:71-96`) is a check-then-set:
look up any existing entry for `key`, compare priorities, only overwrite if
the new bucket outranks the old one. In a single process under one mutex,
that's trivially atomic. Across replicas talking to a shared backend, it
is a classic compare-and-swap problem — two replicas' `Register` calls
racing on the same key need the backend itself to serialize the
check-then-set, or a stale read lets a lower-priority write win.

Backend options, evaluated specifically against this requirement (not
generic "which database is nice" criteria):

| Backend | CAS primitive | Operational cost | Fit |
|---|---|---|---|
| **Redis** | `WATCH`/`MULTI` transactions, or a Lua script (atomic server-side) doing the priority check + `HSET` in one round trip | Low — already a common sidecar, no schema, sub-ms latency | **Best fit.** A single Lua script naturally expresses `Register`'s exact "read existing, compare priority, conditionally write" logic atomically, and `ListAll`/`ManifestForBucket` map cleanly onto `HGETALL`/`SCAN`. |
| **Postgres** (or any transactional SQL) | `SELECT ... FOR UPDATE` + `UPDATE`/`INSERT` in one transaction | Medium — real schema/migrations, but this project's team may already run Postgres elsewhere | Solid fit, more operational weight than Redis for what is fundamentally a KV problem; worth it only if the deployment already standardizes on Postgres for everything and wants one fewer moving part *type*, not fewer moving parts. |
| **etcd** | Native compare-and-swap (`Txn` with a version check) | Medium-high — a consensus store is a heavier dependency to run correctly (odd-sized cluster, its own HA story) than the problem here needs | Etcd's real strength is *strong consistency for small, critical config* (leader election, cluster membership) — a good fit if this project ever needs leader election too (e.g. "exactly one replica runs the cleanup/replication goroutine"), overkill if the catalog is the only thing being externalized. |

**Recommendation, if/when Option B is greenlit:** Redis. It's the closest
match to `Catalog`'s actual access pattern (point lookups + full scans, one
atomic check-and-set operation, no relational structure), lowest
operational overhead of the three, and every method in the table above maps
onto a small, direct set of Redis commands/one Lua script — no ORM, no
migrations, no schema versioning to design.

### What this does NOT solve by itself

Externalizing the catalog fixes the *routing decision* consistency problem.
It does not, by itself, address:
- **Coordinating background work** (the periodic sync ticker in
  `cmd/parparchik`, a future scheduled `internal/cleanup` GC run, a future
  scheduled `internal/replication` pull) so it runs once across the fleet
  rather than once per replica. That's a leader-election problem, separate
  from where catalog *state* lives — worth calling out explicitly since
  it's easy to assume "externalize the catalog" quietly solves this too; it
  doesn't. A cheap first answer (not evaluated in depth here) is a
  Kubernetes `CronJob`/leader-election sidecar running these on a schedule
  independent of the HTTP-serving replicas, rather than each replica's own
  ticker goroutine.
- **`internal/objectstore.S3Store`'s presign/public-URL logic** — already
  stateless per request, needs no changes for multi-replica operation.

## Phasing recommendation

1. **Now:** ship Option A (multiple replicas of the existing binary,
   documented as read-mostly/active-passive-for-writes) — this document's
   Option A section *is* that documentation. No code change needed beyond
   what Tasks 1-30 already built.
2. **When a concrete requirement appears** (an active/active write SLA, or
   observed inconsistency causing real user-facing problems): implement
   Option B with Redis, behind `catalog.Catalog`'s unchanged method
   surface, plus a separate, small leader-election mechanism for the
   background-job-coordination gap noted above.
3. Re-evaluate Postgres/etcd only if the deployment's actual constraints
   change this recommendation (e.g. a hard "no new infra dependency types"
   policy that already includes Postgres but not Redis).

## Open questions for whoever picks this up

These are genuine unknowns this document can't resolve without a concrete
deployment target:

- What write-path consistency SLA does the deployment actually need? (Read
  Option A's breakdown above — is the staleness window it describes
  actually a problem, or acceptable?)
- Is active/active writes a real requirement, or would active/passive (one
  designated "leader" replica handling `/relocate` and cache-miss
  `/update`, others read-only) meet the actual need at a fraction of the
  complexity?
- Does the team already operate Redis, Postgres, or etcd elsewhere? Reusing
  an existing operational dependency is a real factor the table above
  doesn't capture, since it changes "operational cost" per deployment.

This document intentionally stops at "here is the tradeoff space and a
recommendation," not "here is the implementation" — per Task 31's own
instruction and the scope agreed for this pass.
