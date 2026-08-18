package resolver

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/rachlenko/parparchik/golang/internal/catalog"
	"github.com/rachlenko/parparchik/golang/internal/config"
)

// fakeFetcher is a hand-written proxycache.Fetcher for tests, so proxy
// resolution can be exercised without a real upstream.
type fakeFetcher struct {
	mu      sync.Mutex
	objects map[string][]byte // key: upstreamURL+"/"+key
	fetches int
	err     error
}

func newFakeFetcher() *fakeFetcher {
	return &fakeFetcher{objects: make(map[string][]byte)}
}

func (f *fakeFetcher) put(upstreamURL, key string, content []byte) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.objects[upstreamURL+"/"+key] = content
}

func (f *fakeFetcher) Fetch(_ context.Context, upstreamURL, key string) ([]byte, string, bool, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.fetches++
	if f.err != nil {
		return nil, "", false, f.err
	}
	body, ok := f.objects[upstreamURL+"/"+key]
	if !ok {
		return nil, "", false, nil
	}
	return body, "application/octet-stream", true, nil
}

func newTestResolverWithProxy() (*Resolver, *fakeStore, *fakeFetcher, *catalog.Catalog) {
	cfg := &config.Config{
		Buckets: []config.Bucket{
			{Name: "pub", ManifestKey: "manifest.json", Public: true, Kind: config.KindHosted},
			{Name: "npm-cache", ManifestKey: "manifest.json", Public: true, Kind: config.KindProxy, UpstreamURL: "https://registry.npmjs.org"},
		},
	}
	store := newFakeStore()
	fetcher := newFakeFetcher()
	cat := catalog.New(cfg.BucketPriority, cfg.BucketType)
	r := New(cfg, cat, store, WithFetcher(fetcher))
	return r, store, fetcher, cat
}

func TestResolveRoute_ProxyCacheHit(t *testing.T) {
	// Arrange: already cached from a previous fetch.
	r, store, fetcher, _ := newTestResolverWithProxy()
	store.put("npm-cache", "lodash.tgz", []byte("cached-tarball"), "t1")

	// Act
	entry, err := r.ResolveRoute(context.Background(), "/npm-cache/lodash.tgz")

	// Assert
	if err != nil {
		t.Fatalf("ResolveRoute() error = %v", err)
	}
	if entry == nil || entry.Bucket != "npm-cache" {
		t.Fatalf("ResolveRoute() = %+v, want cached entry in npm-cache", entry)
	}
	if fetcher.fetches != 0 {
		t.Errorf("fetches = %d, want 0 (should serve from cache without hitting upstream)", fetcher.fetches)
	}
}

func TestResolveRoute_ProxyCacheMissFetchesAndCaches(t *testing.T) {
	// Arrange
	r, store, fetcher, cat := newTestResolverWithProxy()
	fetcher.put("https://registry.npmjs.org", "lodash.tgz", []byte("tarball-data"))

	// Act
	entry, err := r.ResolveRoute(context.Background(), "/npm-cache/lodash.tgz")

	// Assert
	if err != nil {
		t.Fatalf("ResolveRoute() error = %v", err)
	}
	if entry == nil || entry.Bucket != "npm-cache" || entry.Size != int64(len("tarball-data")) {
		t.Fatalf("ResolveRoute() = %+v, want a newly cached entry", entry)
	}
	if fetcher.fetches != 1 {
		t.Errorf("fetches = %d, want 1", fetcher.fetches)
	}

	// The object must actually be stored, and the catalog updated, so a
	// subsequent request is a cache hit.
	stored, err := store.HeadObject(context.Background(), "npm-cache", "lodash.tgz")
	if err != nil || stored == nil {
		t.Fatalf("expected fetched object to be cached in storage, HeadObject = %+v, err = %v", stored, err)
	}
	if _, ok := cat.Lookup("lodash.tgz"); !ok {
		t.Error("expected fetched object to be registered in the catalog")
	}

	// Second request should now be a pure cache hit.
	if _, err := r.ResolveRoute(context.Background(), "/npm-cache/lodash.tgz"); err != nil {
		t.Fatalf("second ResolveRoute() error = %v", err)
	}
	if fetcher.fetches != 1 {
		t.Errorf("fetches after second request = %d, want still 1 (served from cache)", fetcher.fetches)
	}
}

func TestResolveRoute_ProxyUpstreamMiss(t *testing.T) {
	// Arrange: nothing cached, nothing upstream either.
	r, _, fetcher, _ := newTestResolverWithProxy()

	// Act
	entry, err := r.ResolveRoute(context.Background(), "/npm-cache/does-not-exist.tgz")

	// Assert
	if err != nil {
		t.Fatalf("ResolveRoute() error = %v, want nil for a genuine upstream 404", err)
	}
	if entry != nil {
		t.Errorf("ResolveRoute() = %+v, want nil", entry)
	}
	if fetcher.fetches != 1 {
		t.Errorf("fetches = %d, want 1", fetcher.fetches)
	}
}

func TestResolveRoute_ProxyUpstreamError(t *testing.T) {
	// Arrange
	r, _, fetcher, _ := newTestResolverWithProxy()
	fetcher.err = errors.New("upstream unreachable")

	// Act
	entry, err := r.ResolveRoute(context.Background(), "/npm-cache/lodash.tgz")

	// Assert
	if err == nil {
		t.Fatal("ResolveRoute() error = nil, want a propagated fetch error")
	}
	if entry != nil {
		t.Errorf("ResolveRoute() = %+v, want nil on error", entry)
	}
}

func TestResolveRoute_ProxyDoesNotHijackHigherPriorityHostedKey(t *testing.T) {
	// Arrange: "pub" (hosted, priority 0) already legitimately owns
	// "shared.txt". The proxy repo "npm-cache" (lower priority) also has
	// content for the same key upstream — a realistic collision once
	// real per-format key parsers feed unrelated ecosystems into the same
	// flat catalog key namespace.
	r, store, fetcher, cat := newTestResolverWithProxy()
	cat.Register("shared.txt", "pub", 5, "t0")
	fetcher.put("https://registry.npmjs.org", "shared.txt", []byte("upstream-content"))

	// Act
	entry, err := r.ResolveRoute(context.Background(), "/npm-cache/shared.txt")

	// Assert: the proxy route must not hijack the key — it stays 404 on
	// this route rather than silently serving (or claiming ownership of)
	// content that collides with a higher-priority hosted bucket's entry.
	if err != nil {
		t.Fatalf("ResolveRoute() error = %v", err)
	}
	if entry != nil {
		t.Errorf("ResolveRoute() = %+v, want nil (higher-priority bucket already owns this key)", entry)
	}

	// The hosted bucket's catalog entry and route index must be untouched.
	owner, ok := cat.Lookup("shared.txt")
	if !ok || owner.Bucket != "pub" {
		t.Errorf("Lookup(shared.txt) = %+v, ok=%v, want it still owned by pub", owner, ok)
	}
	if _, ok := cat.LookupByRoute("/pub/shared.txt"); !ok {
		t.Error("expected /pub/shared.txt route to remain indexed after the proxy fetch attempt")
	}

	// The upstream fetch still happened and the object is cached on disk
	// in the proxy's own bucket (harmless — just not surfaced via this
	// route), so a direct HeadObject against npm-cache finds it.
	cached, err := store.HeadObject(context.Background(), "npm-cache", "shared.txt")
	if err != nil || cached == nil {
		t.Errorf("expected the fetched object to still be cached in npm-cache, HeadObject = %+v, err = %v", cached, err)
	}
}
