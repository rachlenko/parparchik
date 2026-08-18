// Package resolver contains the route-resolution and registry-reconciliation
// logic that sits between the HTTP API and the catalog/objectstore layers —
// the Go equivalent of handlers.lua's resolve_route, resolve_missing_file,
// sync_registry, and handle_relocate helpers.
//
// Manifest-persist failure policy: every mutator here calls PersistManifests
// after changing the catalog. ResolveMissingFile, resolveMissingFileByType,
// and SyncRegistry treat a persist failure as best-effort — they log and
// return success, because the in-memory catalog (the actual source of truth
// for routing decisions) is already correct, and the next periodic sync
// (cmd/parparchik's background ticker) will retry the write. Relocate is the
// one exception: it is a synchronous, explicitly user-triggered mutation, so
// a persist failure there is returned as an error rather than swallowed —
// the caller asked for a durable state change and deserves to know if it
// didn't happen, rather than seeing an "ok" response for a change that may
// not survive a restart before the next sync.
package resolver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"sync"

	"github.com/rachlenko/parparchik/golang/internal/catalog"
	"github.com/rachlenko/parparchik/golang/internal/config"
	"github.com/rachlenko/parparchik/golang/internal/objectstore"
)

// Resolver ties configuration, the in-memory catalog, and object storage
// together to answer "where does this route/filename actually live right
// now", reconciling the catalog against storage as needed.
type Resolver struct {
	cfg     *config.Config
	catalog *catalog.Catalog
	store   objectstore.Store

	mu             sync.Mutex
	duplicateCount int
}

// New builds a Resolver.
func New(cfg *config.Config, cat *catalog.Catalog, store objectstore.Store) *Resolver {
	return &Resolver{cfg: cfg, catalog: cat, store: store}
}

// DuplicateCount returns how many file keys were last observed to exist in
// more than one configured bucket (updated by SyncRegistry).
func (r *Resolver) DuplicateCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.duplicateCount
}

func (r *Resolver) setDuplicateCount(n int) {
	r.mu.Lock()
	r.duplicateCount = n
	r.mu.Unlock()
}

// Bootstrap loads each configured bucket's manifest into the catalog, then
// reconciles that catalog against a fresh listing of actual bucket contents
// (SyncRegistry) so newly-added objects that predate any manifest write are
// picked up immediately. It is meant to run once at startup, before the
// server accepts traffic.
//
// The Lua original's README documented this reconcile-at-startup behavior
// but its init() never actually performed it — reconciliation only happened
// as a side effect of the first GET /list call. This Go version does what
// the docs always claimed, and keeps GET /list a pure read (see
// httpapi.handleList); see SyncRegistry's doc comment for the periodic
// re-sync that keeps the catalog fresh afterward.
func (r *Resolver) Bootstrap(ctx context.Context) error {
	manifests := make([]catalog.BucketManifest, 0, len(r.cfg.Buckets))
	for _, bucket := range r.cfg.Buckets {
		content, err := r.store.GetObject(ctx, bucket.Name, bucket.ManifestKey)
		if err != nil {
			return fmt.Errorf("resolver: bootstrap: fetch manifest for %q: %w", bucket.Name, err)
		}
		manifests = append(manifests, catalog.BucketManifest{Bucket: bucket.Name, Content: content})
	}
	r.catalog.LoadManifests(manifests)

	if err := r.SyncRegistry(ctx); err != nil {
		return fmt.Errorf("resolver: bootstrap: initial reconcile: %w", err)
	}
	return nil
}

// PersistManifests writes every bucket's current manifest back to storage.
// It attempts every bucket even if earlier ones fail, and returns a joined
// error describing every failure.
func (r *Resolver) PersistManifests(ctx context.Context) error {
	var errs []error
	for _, bucket := range r.cfg.Buckets {
		manifest := r.catalog.ManifestForBucket(bucket.Name)
		body, err := json.Marshal(manifest)
		if err != nil {
			errs = append(errs, fmt.Errorf("encode manifest for %q: %w", bucket.Name, err))
			continue
		}
		if err := r.store.PutObject(ctx, bucket.Name, bucket.ManifestKey, body, "application/json"); err != nil {
			errs = append(errs, fmt.Errorf("persist manifest for %q: %w", bucket.Name, err))
		}
	}
	return errors.Join(errs...)
}

// SyncRegistry reconciles the catalog against a fresh listing of every
// configured bucket, registers any files it finds, counts keys that exist
// in more than one bucket, and persists the resulting manifests. It is
// O(total objects across all buckets), so callers should run it from
// Bootstrap and a periodic background timer (see cmd/parparchik) rather
// than from a request handler — the Lua original ran the equivalent of this
// on every GET /list request, which made an idempotent read expensive and
// gave it S3-call-driven latency.
func (r *Resolver) SyncRegistry(ctx context.Context) error {
	seen := make(map[string]bool)
	dups := make(map[string]bool)

	for _, bucket := range r.cfg.Buckets {
		objects, err := r.store.ListObjects(ctx, bucket.Name)
		if err != nil {
			return fmt.Errorf("resolver: sync: list %q: %w", bucket.Name, err)
		}
		for _, obj := range objects {
			if obj.Key == bucket.ManifestKey {
				continue
			}
			if seen[obj.Key] {
				dups[obj.Key] = true
			} else {
				r.catalog.Register(obj.Key, bucket.Name, obj.Size, obj.LastModified)
			}
			seen[obj.Key] = true
		}
	}

	r.setDuplicateCount(len(dups))

	if err := r.PersistManifests(ctx); err != nil {
		slog.Error("resolver: sync: persist manifests failed", "error", err)
	}
	return nil
}

// ResolveMissingFile searches every configured bucket (in priority order)
// for key via HEAD requests, registers it under whichever bucket has it,
// and persists the updated manifests. If no bucket has it, the key is
// removed from the catalog. Returns (nil, nil) when the key genuinely does
// not exist anywhere; a non-nil error means a bucket lookup itself failed.
func (r *Resolver) ResolveMissingFile(ctx context.Context, key string) (*catalog.Entry, error) {
	for _, bucket := range r.cfg.Buckets {
		obj, err := r.store.HeadObject(ctx, bucket.Name, key)
		if err != nil {
			return nil, fmt.Errorf("resolver: resolve missing file %q in %q: %w", key, bucket.Name, err)
		}
		if obj != nil {
			r.catalog.Set(key, bucket.Name, obj.Size, obj.LastModified)
			if err := r.PersistManifests(ctx); err != nil {
				slog.Error("resolver: persist manifests failed", "error", err)
			}
			entry, _ := r.catalog.Lookup(key)
			return &entry, nil
		}
	}

	r.catalog.Remove(key)
	if err := r.PersistManifests(ctx); err != nil {
		slog.Error("resolver: persist manifests failed", "error", err)
	}
	return nil, nil
}

// ResolveRoute resolves an incoming request path to the catalog entry that
// should serve it, reconciling against storage when the catalog is stale or
// has no record of the route yet.
func (r *Resolver) ResolveRoute(ctx context.Context, route string) (*catalog.Entry, error) {
	if entry, ok := r.catalog.LookupByRoute(route); ok {
		if r.Exists(ctx, entry.Bucket, entry.Key) {
			return &entry, nil
		}
		resolved, err := r.ResolveMissingFile(ctx, entry.Key)
		if err != nil {
			return nil, err
		}
		if resolved != nil && resolved.Route == route {
			return resolved, nil
		}
		return nil, nil
	}

	if key, public, ok := parseVirtualPrefix(route); ok {
		return r.resolveMissingFileByType(ctx, key, public)
	}

	for _, bucket := range r.cfg.Buckets {
		prefix := "/" + bucket.Name + "/"
		if !strings.HasPrefix(route, prefix) {
			continue
		}
		resolved, err := r.ResolveMissingFile(ctx, strings.TrimPrefix(route, prefix))
		if err != nil {
			return nil, err
		}
		if resolved != nil && resolved.Route == route {
			return resolved, nil
		}
		return nil, nil
	}

	return nil, nil
}

// parseVirtualPrefix extracts the key from the /public/<key> or
// /private/<key> convenience prefixes. These resolve by bucket *type*
// (config.Bucket.Public), not by a literal bucket named "public"/"private" —
// the Lua original compared the resolved route string against the request
// route, which only worked by accident when a bucket happened to be named
// literally "public" or "private"; any other bucket name (as in this
// project's own docker-compose.yml, "public-bucket"/"private-bucket") made
// these routes permanently 404. See the nginx-lua review notes.
func parseVirtualPrefix(route string) (key string, public bool, ok bool) {
	if k, found := strings.CutPrefix(route, "/public/"); found && k != "" {
		return k, true, true
	}
	if k, found := strings.CutPrefix(route, "/private/"); found && k != "" {
		return k, false, true
	}
	return "", false, false
}

// resolveMissingFileByType searches only buckets matching the requested
// visibility (public or private), in priority order, registering and
// persisting on the first hit.
func (r *Resolver) resolveMissingFileByType(ctx context.Context, key string, public bool) (*catalog.Entry, error) {
	for _, bucket := range r.cfg.Buckets {
		if bucket.Public != public {
			continue
		}
		obj, err := r.store.HeadObject(ctx, bucket.Name, key)
		if err != nil {
			return nil, fmt.Errorf("resolver: resolve %q in %q: %w", key, bucket.Name, err)
		}
		if obj == nil {
			continue
		}
		r.catalog.Set(key, bucket.Name, obj.Size, obj.LastModified)
		if err := r.PersistManifests(ctx); err != nil {
			slog.Error("resolver: persist manifests failed", "error", err)
		}
		entry, _ := r.catalog.Lookup(key)
		return &entry, nil
	}
	return nil, nil
}

// Exists reports whether bucket/key currently exists in storage. A
// transport-level failure is logged and treated as "does not exist" for
// this convenience check — callers that need to distinguish the two should
// call the Store's HeadObject directly.
func (r *Resolver) Exists(ctx context.Context, bucket, key string) bool {
	obj, err := r.store.HeadObject(ctx, bucket, key)
	if err != nil {
		slog.Error("resolver: existence check failed", "bucket", bucket, "key", key, "error", err)
		return false
	}
	return obj != nil
}

// RelocateResult describes the outcome of a Relocate call.
type RelocateResult struct {
	Entry         catalog.Entry
	RelocatedFrom string // empty if the file did not move buckets
	RelocatedTo   string
	Duplicate     bool // true if the file was found in more than one bucket
}

// ErrFileNotFound is returned by Relocate when filename exists in none of
// the configured buckets.
var ErrFileNotFound = errors.New("resolver: file not found in any bucket")

// Relocate re-verifies filename against every configured bucket and
// consolidates the catalog entry onto the highest-priority (first
// configured) bucket that has it, matching the priority convention used by
// Register and ResolveMissingFile elsewhere in this package. (The Lua
// original picked whichever bucket it checked *last*, contradicting its own
// registry's priority rule — see the nginx-lua review notes; that looked
// like an unintentional bug, not a deliberate policy, so it is not
// replicated here.)
func (r *Resolver) Relocate(ctx context.Context, filename string) (*RelocateResult, error) {
	var targetBucket string
	var targetObj *objectstore.Object
	foundCount := 0

	for _, bucket := range r.cfg.Buckets {
		obj, err := r.store.HeadObject(ctx, bucket.Name, filename)
		if err != nil {
			return nil, fmt.Errorf("resolver: relocate: check %q in %q: %w", filename, bucket.Name, err)
		}
		if obj == nil {
			continue
		}
		foundCount++
		if targetBucket == "" {
			targetBucket = bucket.Name
			targetObj = obj
		}
	}

	if foundCount == 0 {
		r.catalog.Remove(filename)
		if err := r.PersistManifests(ctx); err != nil {
			slog.Error("resolver: persist manifests failed", "error", err)
		}
		return nil, ErrFileNotFound
	}

	previous, hadPrevious := r.catalog.Lookup(filename)
	r.catalog.Set(filename, targetBucket, targetObj.Size, targetObj.LastModified)

	if err := r.PersistManifests(ctx); err != nil {
		return nil, fmt.Errorf("resolver: relocate: persist manifests: %w", err)
	}

	entry, _ := r.catalog.Lookup(filename)
	result := &RelocateResult{Entry: entry, Duplicate: foundCount > 1}
	if hadPrevious && previous.Bucket != targetBucket {
		result.RelocatedFrom = previous.Bucket
		result.RelocatedTo = targetBucket
	}
	return result, nil
}
