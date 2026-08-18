package resolver

import (
	"context"
	"errors"
	"testing"

	"github.com/rachlenko/parparchik/golang/internal/catalog"
	"github.com/rachlenko/parparchik/golang/internal/config"
)

func testConfig() *config.Config {
	return &config.Config{
		Buckets: []config.Bucket{
			{Name: "pub", ManifestKey: "manifest.json", Public: true},
			{Name: "priv", ManifestKey: "manifest.json", Public: false},
		},
	}
}

func newTestResolver() (*Resolver, *fakeStore, *catalog.Catalog) {
	cfg := testConfig()
	store := newFakeStore()
	cat := catalog.New(cfg.BucketPriority, cfg.BucketType)
	return New(cfg, cat, store), store, cat
}

func TestResolveRoute_ExistingCatalogEntryStillInStorage(t *testing.T) {
	// Arrange
	r, store, cat := newTestResolver()
	store.put("pub", "a.txt", []byte("hi"), "t1")
	cat.Register("a.txt", "pub", 2, "t1")

	// Act
	entry, err := r.ResolveRoute(context.Background(), "/pub/a.txt")

	// Assert
	if err != nil {
		t.Fatalf("ResolveRoute() error = %v", err)
	}
	if entry == nil || entry.Key != "a.txt" {
		t.Errorf("ResolveRoute() = %+v, want entry for a.txt", entry)
	}
}

func TestResolveRoute_StaleCatalogEntryReResolvesElsewhere(t *testing.T) {
	// Arrange: catalog thinks a.txt is in "pub", but it now actually lives in "priv".
	r, store, cat := newTestResolver()
	cat.Register("a.txt", "pub", 2, "t1")
	store.put("priv", "a.txt", []byte("hi"), "t1")

	// Act
	entry, err := r.ResolveRoute(context.Background(), "/pub/a.txt")

	// Assert: the stale route no longer resolves to anything (the file
	// moved, so /pub/a.txt itself is gone even though a.txt still exists
	// under its new route).
	if err != nil {
		t.Fatalf("ResolveRoute() error = %v", err)
	}
	if entry != nil {
		t.Errorf("ResolveRoute() = %+v, want nil (route moved)", entry)
	}
	if got, _ := cat.Lookup("a.txt"); got.Bucket != "priv" {
		t.Errorf("catalog was not reconciled: Lookup(a.txt).Bucket = %q, want priv", got.Bucket)
	}
}

func TestResolveRoute_VirtualPublicPrefixResolvesByBucketType(t *testing.T) {
	// Arrange: bucket is literally named "pub", not "public" — this is the
	// exact scenario that was broken in the Lua original (see review notes).
	r, store, _ := newTestResolver()
	store.put("pub", "photo.jpg", []byte("data"), "t1")

	// Act
	entry, err := r.ResolveRoute(context.Background(), "/public/photo.jpg")

	// Assert
	if err != nil {
		t.Fatalf("ResolveRoute() error = %v", err)
	}
	if entry == nil || entry.Bucket != "pub" {
		t.Errorf("ResolveRoute(/public/photo.jpg) = %+v, want it resolved against the public-typed bucket", entry)
	}
}

func TestResolveRoute_VirtualPrivatePrefixDoesNotMatchPublicBucket(t *testing.T) {
	// Arrange: the object only exists in the public bucket.
	r, store, _ := newTestResolver()
	store.put("pub", "photo.jpg", []byte("data"), "t1")

	// Act
	entry, err := r.ResolveRoute(context.Background(), "/private/photo.jpg")

	// Assert
	if err != nil {
		t.Fatalf("ResolveRoute() error = %v", err)
	}
	if entry != nil {
		t.Errorf("ResolveRoute(/private/photo.jpg) = %+v, want nil (object is only in the public bucket)", entry)
	}
}

func TestResolveRoute_UnknownRoute(t *testing.T) {
	// Arrange
	r, _, _ := newTestResolver()

	// Act
	entry, err := r.ResolveRoute(context.Background(), "/nowhere/nothing")

	// Assert
	if err != nil {
		t.Fatalf("ResolveRoute() error = %v", err)
	}
	if entry != nil {
		t.Errorf("ResolveRoute() = %+v, want nil", entry)
	}
}

func TestRelocate_PicksHighestPriorityBucketOnDuplicate(t *testing.T) {
	// Arrange: file exists in both buckets; "pub" is configured first (higher priority).
	r, store, _ := newTestResolver()
	store.put("pub", "dup.txt", []byte("a"), "t1")
	store.put("priv", "dup.txt", []byte("bb"), "t2")

	// Act
	result, err := r.Relocate(context.Background(), "dup.txt")

	// Assert
	if err != nil {
		t.Fatalf("Relocate() error = %v", err)
	}
	if result.Entry.Bucket != "pub" {
		t.Errorf("Relocate() target bucket = %q, want pub (highest priority)", result.Entry.Bucket)
	}
	if !result.Duplicate {
		t.Error("Relocate() Duplicate = false, want true (file exists in both buckets)")
	}
}

func TestRelocate_MovesEntryWhenBucketChanged(t *testing.T) {
	// Arrange: catalog says the file is in "pub", but it now only exists in "priv".
	r, store, cat := newTestResolver()
	cat.Register("moved.txt", "pub", 1, "old")
	store.put("priv", "moved.txt", []byte("new"), "t2")

	// Act
	result, err := r.Relocate(context.Background(), "moved.txt")

	// Assert
	if err != nil {
		t.Fatalf("Relocate() error = %v", err)
	}
	if result.RelocatedFrom != "pub" || result.RelocatedTo != "priv" {
		t.Errorf("Relocate() from/to = %q/%q, want pub/priv", result.RelocatedFrom, result.RelocatedTo)
	}
}

func TestRelocate_NotFoundAnywhereRemovesFromCatalog(t *testing.T) {
	// Arrange
	r, _, cat := newTestResolver()
	cat.Register("ghost.txt", "pub", 1, "t1")

	// Act
	_, err := r.Relocate(context.Background(), "ghost.txt")

	// Assert
	if !errors.Is(err, ErrFileNotFound) {
		t.Fatalf("Relocate() error = %v, want ErrFileNotFound", err)
	}
	if _, ok := cat.Lookup("ghost.txt"); ok {
		t.Error("catalog still has an entry for a file that exists in no bucket")
	}
}

func TestSyncRegistry_DetectsDuplicatesAndRegistersFiles(t *testing.T) {
	// Arrange
	r, store, cat := newTestResolver()
	store.put("pub", "only-pub.txt", []byte("a"), "t1")
	store.put("pub", "both.txt", []byte("b"), "t2")
	store.put("priv", "both.txt", []byte("c"), "t3")

	// Act
	if err := r.SyncRegistry(context.Background()); err != nil {
		t.Fatalf("SyncRegistry() error = %v", err)
	}

	// Assert
	if cat.Count() != 2 {
		t.Errorf("Count() = %d, want 2", cat.Count())
	}
	if r.DuplicateCount() != 1 {
		t.Errorf("DuplicateCount() = %d, want 1", r.DuplicateCount())
	}
	entry, ok := cat.Lookup("both.txt")
	if !ok || entry.Bucket != "pub" {
		t.Errorf("Lookup(both.txt) = %+v, want it registered under pub (seen first, higher priority)", entry)
	}
}

func TestSyncRegistry_SkipsManifestKeyItself(t *testing.T) {
	// Arrange
	r, store, cat := newTestResolver()
	store.put("pub", "manifest.json", []byte(`{}`), "t1")

	// Act
	if err := r.SyncRegistry(context.Background()); err != nil {
		t.Fatalf("SyncRegistry() error = %v", err)
	}

	// Assert
	if cat.Count() != 0 {
		t.Errorf("Count() = %d, want 0 (manifest key itself should never be registered as a file)", cat.Count())
	}
}

func TestBootstrap_LoadsManifestsThenReconciles(t *testing.T) {
	// Arrange: manifest says "old.txt" lives in pub, but storage now also has
	// a second file "new.txt" that predates any manifest write.
	r, store, cat := newTestResolver()
	manifest := `{"version":1,"bucket":"pub","files":[{"key":"old.txt","bucket":"pub","size":1}]}`
	store.put("pub", "manifest.json", []byte(manifest), "t0")
	store.put("pub", "old.txt", []byte("x"), "t1")
	store.put("pub", "new.txt", []byte("y"), "t2")

	// Act
	if err := r.Bootstrap(context.Background()); err != nil {
		t.Fatalf("Bootstrap() error = %v", err)
	}

	// Assert
	if _, ok := cat.Lookup("old.txt"); !ok {
		t.Error("Bootstrap did not load the manifest's existing entry")
	}
	if _, ok := cat.Lookup("new.txt"); !ok {
		t.Error("Bootstrap did not reconcile a file present in storage but absent from the manifest")
	}
}
