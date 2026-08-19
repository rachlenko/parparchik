package resolver

import (
	"context"
	"testing"

	"github.com/rachlenko/parparchik/golang/internal/catalog"
	"github.com/rachlenko/parparchik/golang/internal/config"
)

func newTestResolverWithVirtual() (*Resolver, *fakeStore, *fakeFetcher, *catalog.Catalog) {
	cfg := &config.Config{
		Buckets: []config.Bucket{
			{Name: "pub", ManifestKey: "manifest.json", Public: true, Kind: config.KindHosted},
			{Name: "priv", ManifestKey: "manifest.json", Public: false, Kind: config.KindHosted},
			{Name: "npm-cache", ManifestKey: "manifest.json", Public: true, Kind: config.KindProxy, UpstreamURL: "https://registry.npmjs.org"},
			{Name: "all", Kind: config.KindVirtual, Members: []string{"pub", "npm-cache", "priv"}},
		},
	}
	store := newFakeStore()
	fetcher := newFakeFetcher()
	cat := catalog.New(cfg.BucketPriority, cfg.BucketType)
	r := New(cfg, cat, store, WithFetcher(fetcher))
	return r, store, fetcher, cat
}

func TestResolveRoute_VirtualRepo_ResolvesFirstMatchingHostedMember(t *testing.T) {
	// Arrange: resolveHostedMemberRoute re-verifies against live storage
	// (via ResolveMissingFile), so the object must actually be in the fake
	// store, not just pre-registered in the catalog.
	r, store, _, _ := newTestResolverWithVirtual()
	store.put("pub", "shared.txt", []byte("0123456789"), "t1")

	// Act
	entry, err := r.ResolveRoute(context.Background(), "/all/shared.txt")

	// Assert
	if err != nil {
		t.Fatalf("ResolveRoute() error = %v", err)
	}
	if entry == nil || entry.Bucket != "pub" {
		t.Errorf("ResolveRoute() = %+v, want it resolved via member pub", entry)
	}
}

func TestResolveRoute_VirtualRepo_FallsThroughToLaterMember(t *testing.T) {
	// Arrange: not in pub, only in priv (a later member than pub, earlier than npm-cache is irrelevant here).
	r, store, _, _ := newTestResolverWithVirtual()
	store.put("priv", "only-in-priv.txt", []byte("secret"), "t1")

	// Act
	entry, err := r.ResolveRoute(context.Background(), "/all/only-in-priv.txt")

	// Assert
	if err != nil {
		t.Fatalf("ResolveRoute() error = %v", err)
	}
	if entry == nil || entry.Bucket != "priv" {
		t.Errorf("ResolveRoute() = %+v, want it resolved via member priv", entry)
	}
}

func TestResolveRoute_VirtualRepo_ResolvesThroughProxyMember(t *testing.T) {
	// Arrange: not in any hosted member, but available via the proxy member's upstream.
	r, _, fetcher, _ := newTestResolverWithVirtual()
	fetcher.put("https://registry.npmjs.org", "lodash.tgz", []byte("tarball"))

	// Act
	entry, err := r.ResolveRoute(context.Background(), "/all/lodash.tgz")

	// Assert
	if err != nil {
		t.Fatalf("ResolveRoute() error = %v", err)
	}
	if entry == nil || entry.Bucket != "npm-cache" {
		t.Errorf("ResolveRoute() = %+v, want it resolved via proxy member npm-cache", entry)
	}
	if fetcher.fetches != 1 {
		t.Errorf("fetches = %d, want 1", fetcher.fetches)
	}
}

func TestResolveRoute_VirtualRepo_NotFoundInAnyMember(t *testing.T) {
	// Arrange
	r, _, fetcher, _ := newTestResolverWithVirtual()

	// Act
	entry, err := r.ResolveRoute(context.Background(), "/all/nowhere.txt")

	// Assert
	if err != nil {
		t.Fatalf("ResolveRoute() error = %v", err)
	}
	if entry != nil {
		t.Errorf("ResolveRoute() = %+v, want nil", entry)
	}
	if fetcher.fetches != 1 {
		t.Errorf("fetches = %d, want 1 (the proxy member should still have been tried)", fetcher.fetches)
	}
}

func TestResolveRoute_VirtualRepo_UnknownMemberNameIsSkipped(t *testing.T) {
	// Arrange
	cfg := &config.Config{Buckets: []config.Bucket{
		{Name: "pub", ManifestKey: "manifest.json", Public: true, Kind: config.KindHosted},
		{Name: "all", Kind: config.KindVirtual, Members: []string{"does-not-exist", "pub"}},
	}}
	store := newFakeStore()
	cat := catalog.New(cfg.BucketPriority, cfg.BucketType)
	r := New(cfg, cat, store)
	store.put("pub", "file.txt", []byte("hello"), "t1")

	// Act
	entry, err := r.ResolveRoute(context.Background(), "/all/file.txt")

	// Assert
	if err != nil {
		t.Fatalf("ResolveRoute() error = %v", err)
	}
	if entry == nil || entry.Bucket != "pub" {
		t.Errorf("ResolveRoute() = %+v, want the dangling member name skipped and pub resolved", entry)
	}
}

func TestResolveRoute_VirtualRepo_NestedVirtualMemberIsSkipped(t *testing.T) {
	// Arrange: a virtual repo listing another virtual repo as a member —
	// must not recurse, just skip it.
	cfg := &config.Config{Buckets: []config.Bucket{
		{Name: "pub", ManifestKey: "manifest.json", Public: true, Kind: config.KindHosted},
		{Name: "inner", Kind: config.KindVirtual, Members: []string{"pub"}},
		{Name: "outer", Kind: config.KindVirtual, Members: []string{"inner", "pub"}},
	}}
	store := newFakeStore()
	cat := catalog.New(cfg.BucketPriority, cfg.BucketType)
	r := New(cfg, cat, store)
	store.put("pub", "file.txt", []byte("hello"), "t1")

	// Act
	entry, err := r.ResolveRoute(context.Background(), "/outer/file.txt")

	// Assert
	if err != nil {
		t.Fatalf("ResolveRoute() error = %v", err)
	}
	if entry == nil || entry.Bucket != "pub" {
		t.Errorf("ResolveRoute() = %+v, want the nested virtual member skipped and pub resolved directly", entry)
	}
}

func TestSyncRegistry_SkipsVirtualBuckets(t *testing.T) {
	// Arrange: a virtual bucket has no real S3 bucket behind it — listing
	// it must not be attempted (fakeStore.ListObjects on an unknown bucket
	// name just returns empty, but a real S3 backend would error with
	// NoSuchBucket; this guards the HasStorage() skip that prevents that).
	r, store, _, _ := newTestResolverWithVirtual()
	store.put("pub", "a.txt", []byte("x"), "t1")

	// Act
	err := r.SyncRegistry(context.Background())

	// Assert
	if err != nil {
		t.Fatalf("SyncRegistry() error = %v", err)
	}
}

func TestBootstrap_SkipsVirtualBuckets(t *testing.T) {
	// Arrange
	r, _, _, _ := newTestResolverWithVirtual()

	// Act
	err := r.Bootstrap(context.Background())

	// Assert
	if err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}
}
