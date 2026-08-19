// Package replication pulls a remote parparchik instance's catalog and
// objects into this instance's own storage and catalog, over the same
// HTTP API this service already exposes (GET /list, GET /{bucket}/{key})
// — no new wire protocol and no new server-side endpoint to build or run.
// Conflict handling (what happens when a pulled key already exists
// locally under a different, higher-priority bucket) reuses
// catalog.Catalog.Register's existing priority semantics unchanged; this
// package introduces no new conflict model, per the plan's own note.
//
// Implemented: Client/HTTPClient (talk to a remote instance) and Puller
// (fetch the remote manifest, skip entries already pulled and unchanged,
// fetch and store the rest).
//
// Not implemented (see docs/plans/ Task 30): push (a local instance
// advertising its own catalog to a remote one — the reverse direction),
// and a scheduled replication goroutine wired into cmd/parparchik. The
// latter needs a real peer-list/interval config surface and a policy for
// whether replication is one-way or bidirectional — the same kind of
// deferred product/config-schema decision Task 29's GC goroutine was left
// with, not a "no upload endpoint" scope cut like Tasks 25-28.
package replication

import (
	"context"
	"fmt"

	"github.com/rachlenko/parparchik/golang/internal/catalog"
	"github.com/rachlenko/parparchik/golang/internal/objectstore"
)

// Puller pulls one remote parparchik instance's catalog into local
// storage and a local catalog.
type Puller struct {
	Remote Client

	LocalStore   objectstore.Store
	LocalCatalog *catalog.Catalog

	// TargetBucket is the local bucket every pulled object's bytes are
	// written into, and the bucket Register is called with. A single
	// Puller always writes to one bucket; a deployment pulling from
	// several remotes or into several local buckets configures one Puller
	// per (remote, target bucket) pair.
	TargetBucket string
}

// Result summarizes one Pull call. Pulled/Skipped/Failed always partition
// the remote manifest's keys (a key appears in exactly one), so
// len(Pulled)+len(Skipped)+len(Failed) equals the remote entry count Pull
// was given.
type Result struct {
	Pulled  []string
	Skipped []string
	Failed  []string

	// Errors holds one error per Failed key, in the same order as Failed.
	Errors []error
}

// Pull fetches the remote instance's manifest and, for every entry not
// already present locally with an equal-or-newer LastModified, fetches
// its bytes and stores them into TargetBucket, then registers the key via
// catalog.Register — which applies its own existing priority check, so a
// key some other, higher-priority local bucket already owns keeps that
// owner regardless of what this pull just fetched into TargetBucket's
// storage.
//
// A single key's fetch or store failure does not abort the run — Pull
// attempts every remote entry and reports failures in Result.Failed /
// Result.Errors, mirroring cleanup.Execute's "maximize forward progress"
// stance for the same reason: one bad object in a large remote manifest
// shouldn't block replicating the rest of it.
func (p *Puller) Pull(ctx context.Context) (Result, error) {
	remoteEntries, err := p.Remote.FetchManifest(ctx)
	if err != nil {
		return Result{}, fmt.Errorf("replication: fetch remote manifest: %w", err)
	}

	var result Result
	for _, e := range remoteEntries {
		if p.alreadyUpToDate(e) {
			result.Skipped = append(result.Skipped, e.Key)
			continue
		}

		body, err := p.Remote.FetchObject(ctx, e)
		if err != nil {
			result.Failed = append(result.Failed, e.Key)
			result.Errors = append(result.Errors, fmt.Errorf("replication: fetch %q: %w", e.Key, err))
			continue
		}
		if err := p.LocalStore.PutObject(ctx, p.TargetBucket, e.Key, body, ""); err != nil {
			result.Failed = append(result.Failed, e.Key)
			result.Errors = append(result.Errors, fmt.Errorf("replication: store %q: %w", e.Key, err))
			continue
		}

		p.LocalCatalog.Register(e.Key, p.TargetBucket, int64(len(body)), e.LastModified)
		result.Pulled = append(result.Pulled, e.Key)
	}
	return result, nil
}

// alreadyUpToDate reports whether the local catalog already has remote's
// key, pulled into this same TargetBucket, at an equal-or-newer
// LastModified — purely a "don't bother re-fetching bytes we already
// have" optimization, not a conflict decision (Register still makes that
// call, on every Pull, for every key this returns false for).
// LastModified values are RFC3339 UTC strings (see objectstore.formatTime),
// which sort lexically in chronological order, so a plain string compare
// is sufficient — except an empty string is never trusted as a real
// timestamp, in either direction: an empty *local* LastModified simply
// sorts before any real remote timestamp and triggers a redundant but
// harmless re-fetch, but an empty *remote* LastModified is instead treated
// as "always refetch" rather than "always up to date" (a naive
// local >= remote compare would otherwise make any non-empty local
// timestamp look newer than an empty one, permanently skipping a key
// whose remote peer just can't report its own timestamp — e.g.
// objectstore.formatTime returns "" for a nil S3 LastModified, or a
// hand-edited manifest omits the field entirely).
func (p *Puller) alreadyUpToDate(remote catalog.Entry) bool {
	if remote.LastModified == "" {
		return false
	}
	local, ok := p.LocalCatalog.Lookup(remote.Key)
	if !ok || local.Bucket != p.TargetBucket {
		return false
	}
	return local.LastModified >= remote.LastModified
}
