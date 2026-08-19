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
	"time"

	"github.com/rachlenko/parparchik/golang/internal/catalog"
	"github.com/rachlenko/parparchik/golang/internal/config"
	"github.com/rachlenko/parparchik/golang/internal/objectstore"
	"github.com/rachlenko/parparchik/golang/internal/proxycache"
)

// Resolver ties configuration, the in-memory catalog, and object storage
// together to answer "where does this route/filename actually live right
// now", reconciling the catalog against storage as needed.
type Resolver struct {
	cfg     *config.Config
	catalog *catalog.Catalog
	store   objectstore.Store
	fetcher proxycache.Fetcher

	mu             sync.Mutex
	duplicateCount int
}

// Option configures optional Resolver behavior (see WithFetcher).
type Option func(*Resolver)

// WithFetcher overrides the Fetcher used to resolve config.KindProxy
// repositories. Defaults to proxycache.NewHTTPFetcher(); tests should
// supply a fake to avoid real network calls.
func WithFetcher(f proxycache.Fetcher) Option {
	return func(r *Resolver) { r.fetcher = f }
}

// New builds a Resolver.
func New(cfg *config.Config, cat *catalog.Catalog, store objectstore.Store, opts ...Option) *Resolver {
	r := &Resolver{cfg: cfg, catalog: cat, store: store}
	for _, opt := range opts {
		opt(r)
	}
	if r.fetcher == nil {
		r.fetcher = proxycache.NewHTTPFetcher()
	}
	return r
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
		if !bucket.HasStorage() {
			continue
		}
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
		if !bucket.HasStorage() {
			continue
		}
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
		if !bucket.HasStorage() {
			continue
		}
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
		if !bucket.HasStorage() {
			continue
		}
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
		key := strings.TrimPrefix(route, prefix)

		switch bucket.Kind {
		case config.KindProxy:
			return r.resolveProxyRoute(ctx, bucket, key)
		case config.KindVirtual:
			return r.resolveVirtualRoute(ctx, bucket, key)
		}

		resolved, err := r.ResolveMissingFile(ctx, key)
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
		if !bucket.HasStorage() || bucket.Public != public {
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

// resolveProxyRoute implements the fetch-through-and-cache behavior for a
// config.KindProxy bucket: a cache hit that hasn't exceeded bucket.CacheTTL
// (the key already exists in the proxy's own S3 cache bucket, and hasn't
// gone stale) is served directly; a cache miss — or an expired entry —
// triggers an upstream fetch via r.fetcher, and a successful fetch is
// cached and registered before being served. Returns (nil, nil) when the
// key genuinely doesn't exist upstream either — not an error, just a 404.
//
// Concurrent misses for the same not-yet-cached key each independently hit
// the upstream and re-cache (no request coalescing/singleflight) — fine for
// occasional traffic, but a thundering-herd risk against the upstream under
// concurrent load for a popular new key. Not addressed here since this
// isn't mounted into a live router yet; revisit if/when it is.
func (r *Resolver) resolveProxyRoute(ctx context.Context, bucket config.Bucket, key string) (*catalog.Entry, error) {
	obj, err := r.store.HeadObject(ctx, bucket.Name, key)
	if err != nil {
		return nil, fmt.Errorf("resolver: proxy cache check %q in %q: %w", key, bucket.Name, err)
	}
	if obj != nil && !proxyCacheExpired(bucket, obj) {
		return r.registerResolvedObject(ctx, bucket.Name, key, obj.Size, obj.LastModified, false)
	}

	body, contentType, ok, err := r.fetcher.Fetch(ctx, bucket.UpstreamURL, key)
	if err != nil {
		return nil, fmt.Errorf("resolver: proxy fetch %q from %q: %w", key, bucket.UpstreamURL, err)
	}
	if !ok {
		return nil, nil
	}

	if err := r.store.PutObject(ctx, bucket.Name, key, body, contentType); err != nil {
		return nil, fmt.Errorf("resolver: proxy cache store %q in %q: %w", key, bucket.Name, err)
	}
	return r.registerResolvedObject(ctx, bucket.Name, key, int64(len(body)), "", true)
}

// proxyCacheExpired reports whether a cached proxy object is older than
// bucket.CacheTTL. CacheTTL == 0 means "cache forever" (never expires) —
// the original proxy-repository behavior, preserved as the default. The
// object's storage LastModified timestamp doubles as its "cached at" time,
// since resolveProxyRoute is the only thing that ever writes into a proxy
// bucket: S3/MinIO stamps LastModified on PUT, so no separate cache
// timestamp needs to be tracked in the catalog.
func proxyCacheExpired(bucket config.Bucket, obj *objectstore.Object) bool {
	if bucket.CacheTTL <= 0 {
		return false
	}
	cachedAt, err := time.Parse(time.RFC3339, obj.LastModified)
	if err != nil {
		// Can't determine age from an unparseable timestamp — refresh
		// rather than risk serving indefinitely-stale content.
		return true
	}
	return time.Since(cachedAt) > bucket.CacheTTL
}

// resolveVirtualRoute resolves a route matched to a config.KindVirtual
// bucket by trying each of its Members in order, returning the first one
// that resolves. A member name that doesn't match any configured bucket is
// skipped rather than treated as an error — see parseVirtualRepos's doc
// comment on why a dangling reference isn't inherently invalid config.
func (r *Resolver) resolveVirtualRoute(ctx context.Context, virtual config.Bucket, key string) (*catalog.Entry, error) {
	for _, memberName := range virtual.Members {
		member, ok := r.cfg.BucketByName(memberName)
		if !ok {
			// config.Load's validateVirtualRepoMembers rejects this at
			// startup, so reaching here means Config was constructed
			// directly rather than via Load — still worth surfacing
			// rather than silently degrading to an unexplained 404.
			slog.Warn("resolver: virtual repo references unknown member, skipping", "virtual_repo", virtual.Name, "member", memberName)
			continue
		}

		var (
			entry *catalog.Entry
			err   error
		)
		switch member.Kind {
		case config.KindProxy:
			entry, err = r.resolveProxyRoute(ctx, member, key)
		case config.KindVirtual:
			// A virtual repo listing another virtual repo as a member is
			// almost certainly a config mistake (and risks a resolution
			// cycle) — skip rather than recurse. Also rejected at startup
			// by validateVirtualRepoMembers when Config comes from Load.
			slog.Warn("resolver: virtual repo references another virtual repo as a member, skipping (nesting unsupported)", "virtual_repo", virtual.Name, "member", memberName)
			continue
		default:
			entry, err = r.resolveHostedMemberRoute(ctx, member, key)
		}
		if err != nil {
			return nil, fmt.Errorf("resolver: virtual repo %q: member %q: %w", virtual.Name, memberName, err)
		}
		if entry != nil {
			return entry, nil
		}
	}
	return nil, nil
}

// resolveHostedMemberRoute checks whether a specific hosted-bucket member
// of a virtual repo currently owns key, with a single HEAD against that
// member only.
//
// This deliberately does NOT reuse ResolveMissingFile: an earlier version
// did, but ResolveMissingFile scans *every* configured bucket (not just
// this member) and unconditionally persists manifests for the whole
// fleet — a go-reviewer pass flagged that as O(members × total buckets)
// HEAD calls plus a full-fleet manifest-persist sweep per virtual-repo
// request, all to answer a question ("does this one specific member have
// this key?") that a single targeted HEAD already answers. Uses the same
// registerResolvedObject priority-check helper resolveProxyRoute uses, so
// a higher-priority bucket elsewhere still can't be shadowed by this
// member — consistent with the "route/priority mismatch = 404" rule
// documented on registerResolvedObject.
func (r *Resolver) resolveHostedMemberRoute(ctx context.Context, member config.Bucket, key string) (*catalog.Entry, error) {
	obj, err := r.store.HeadObject(ctx, member.Name, key)
	if err != nil {
		return nil, fmt.Errorf("resolver: virtual member %q: check %q: %w", member.Name, key, err)
	}
	if obj == nil {
		return nil, nil
	}
	return r.registerResolvedObject(ctx, member.Name, key, obj.Size, obj.LastModified, true)
}

// registerResolvedObject applies catalog.Register's priority check to an
// object a caller has already confirmed (via HEAD or fetch) lives in
// bucketName, and only returns it if this exact bucket still won the key.
// Used by both resolveProxyRoute (a proxy cache hit/fetch) and
// resolveHostedMemberRoute (a virtual repo's hosted-member check).
//
// This uses Register, not Set, deliberately: an earlier version of
// resolveProxyRoute used Set (unconditional overwrite) here, which let a
// proxy repository's cache silently hijack a key already legitimately
// owned by a higher-priority hosted bucket — deleting that hosted bucket's
// route index entry in the process, since the catalog's key namespace is
// shared flat across every bucket regardless of kind. Register's priority
// check prevents that; if a higher-priority bucket already owns key, the
// confirmed object stays on disk in bucketName (harmless) but is not
// surfaced through this route — the same "route/priority mismatch = 404,
// never serve the wrong bucket's content" rule ResolveMissingFile already
// applies elsewhere in this file.
func (r *Resolver) registerResolvedObject(ctx context.Context, bucketName, key string, size int64, lastModified string, persist bool) (*catalog.Entry, error) {
	r.catalog.Register(key, bucketName, size, lastModified)
	if persist {
		if err := r.PersistManifests(ctx); err != nil {
			slog.Error("resolver: persist manifests failed", "error", err)
		}
	}

	entry, ok := r.catalog.Lookup(key)
	if !ok || entry.Bucket != bucketName {
		return nil, nil
	}
	return &entry, nil
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
		if !bucket.HasStorage() {
			continue
		}
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
