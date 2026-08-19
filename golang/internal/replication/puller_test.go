package replication

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	"github.com/rachlenko/parparchik/golang/internal/catalog"
	"github.com/rachlenko/parparchik/golang/internal/objectstore"
)

type fakeClient struct {
	manifest    []catalog.Entry
	manifestErr error

	objects  map[string][]byte // by Key
	failKeys map[string]error  // by Key

	fetchedKeys []string
}

func (c *fakeClient) FetchManifest(context.Context) ([]catalog.Entry, error) {
	return c.manifest, c.manifestErr
}

func (c *fakeClient) FetchObject(_ context.Context, entry catalog.Entry) ([]byte, error) {
	c.fetchedKeys = append(c.fetchedKeys, entry.Key)
	if err, ok := c.failKeys[entry.Key]; ok {
		return nil, err
	}
	return c.objects[entry.Key], nil
}

type fakeStore struct {
	mu      sync.Mutex
	objects map[string]map[string][]byte // bucket -> key -> content
	failPut map[string]bool              // by key
}

func newFakeStore() *fakeStore {
	return &fakeStore{objects: make(map[string]map[string][]byte)}
}

func (s *fakeStore) ListObjects(context.Context, string) ([]objectstore.Object, error) {
	return nil, nil
}
func (s *fakeStore) HeadObject(context.Context, string, string) (*objectstore.Object, error) {
	return nil, nil
}
func (s *fakeStore) GetObject(context.Context, string, string) ([]byte, error) { return nil, nil }

func (s *fakeStore) PutObject(_ context.Context, bucket, key string, content []byte, _ string) error {
	if s.failPut[key] {
		return errors.New("put failed")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.objects[bucket] == nil {
		s.objects[bucket] = make(map[string][]byte)
	}
	s.objects[bucket][key] = content
	return nil
}

func (s *fakeStore) DeleteObject(context.Context, string, string) error { return nil }
func (s *fakeStore) PublicURL(string, string) string                    { return "" }
func (s *fakeStore) PresignedURL(context.Context, string, string, time.Duration) (string, error) {
	return "", nil
}

func newTestCatalog() *catalog.Catalog {
	return catalog.New(func(bucket string) int {
		if bucket == "target-bucket" {
			return 0
		}
		return 1
	}, func(string) string { return "private" })
}

func TestPuller_Pull_FetchesAndStoresNewEntries(t *testing.T) {
	// Arrange
	client := &fakeClient{
		manifest: []catalog.Entry{{Key: "a.txt", Bucket: "remote-bucket", Size: 5, LastModified: "2026-08-01T00:00:00Z"}},
		objects:  map[string][]byte{"a.txt": []byte("hello")},
	}
	store := newFakeStore()
	cat := newTestCatalog()
	p := &Puller{Remote: client, LocalStore: store, LocalCatalog: cat, TargetBucket: "target-bucket"}

	// Act
	result, err := p.Pull(context.Background())

	// Assert
	if err != nil {
		t.Fatalf("Pull() error = %v", err)
	}
	if len(result.Pulled) != 1 || result.Pulled[0] != "a.txt" {
		t.Errorf("Pulled = %v, want [a.txt]", result.Pulled)
	}
	if got := store.objects["target-bucket"]["a.txt"]; string(got) != "hello" {
		t.Errorf("stored object = %q, want hello", got)
	}
	entry, ok := cat.Lookup("a.txt")
	if !ok || entry.Bucket != "target-bucket" {
		t.Errorf("catalog entry = %+v, ok=%v, want it registered under target-bucket", entry, ok)
	}
}

func TestPuller_Pull_SkipsAlreadyUpToDateEntry(t *testing.T) {
	// Arrange
	cat := newTestCatalog()
	cat.Register("a.txt", "target-bucket", 5, "2026-08-01T00:00:00Z")
	client := &fakeClient{
		manifest: []catalog.Entry{{Key: "a.txt", Bucket: "remote-bucket", Size: 5, LastModified: "2026-08-01T00:00:00Z"}},
	}
	store := newFakeStore()
	p := &Puller{Remote: client, LocalStore: store, LocalCatalog: cat, TargetBucket: "target-bucket"}

	// Act
	result, err := p.Pull(context.Background())

	// Assert
	if err != nil {
		t.Fatalf("Pull() error = %v", err)
	}
	if len(result.Skipped) != 1 || result.Skipped[0] != "a.txt" {
		t.Errorf("Skipped = %v, want [a.txt]", result.Skipped)
	}
	if len(client.fetchedKeys) != 0 {
		t.Errorf("FetchObject was called for %v, want it never called for an up-to-date entry", client.fetchedKeys)
	}
}

func TestPuller_Pull_RefetchesWhenRemoteIsNewer(t *testing.T) {
	// Arrange
	cat := newTestCatalog()
	cat.Register("a.txt", "target-bucket", 5, "2026-01-01T00:00:00Z")
	client := &fakeClient{
		manifest: []catalog.Entry{{Key: "a.txt", Bucket: "remote-bucket", Size: 7, LastModified: "2026-08-01T00:00:00Z"}},
		objects:  map[string][]byte{"a.txt": []byte("updated")},
	}
	store := newFakeStore()
	p := &Puller{Remote: client, LocalStore: store, LocalCatalog: cat, TargetBucket: "target-bucket"}

	// Act
	result, err := p.Pull(context.Background())

	// Assert
	if err != nil {
		t.Fatalf("Pull() error = %v", err)
	}
	if len(result.Pulled) != 1 || result.Pulled[0] != "a.txt" {
		t.Errorf("Pulled = %v, want [a.txt] (remote LastModified is newer)", result.Pulled)
	}
	if got := store.objects["target-bucket"]["a.txt"]; string(got) != "updated" {
		t.Errorf("stored object = %q, want updated", got)
	}
}

func TestPuller_Pull_DoesNotSkipWhenLocalEntryIsUnderADifferentBucket(t *testing.T) {
	// Arrange: the local entry exists, but under a different bucket than
	// TargetBucket (e.g. a prior manual upload) — alreadyUpToDate must not
	// treat that as "already pulled".
	cat := newTestCatalog()
	cat.Register("a.txt", "other-bucket", 5, "2026-08-01T00:00:00Z")
	client := &fakeClient{
		manifest: []catalog.Entry{{Key: "a.txt", Bucket: "remote-bucket", Size: 5, LastModified: "2026-08-01T00:00:00Z"}},
		objects:  map[string][]byte{"a.txt": []byte("hello")},
	}
	store := newFakeStore()
	p := &Puller{Remote: client, LocalStore: store, LocalCatalog: cat, TargetBucket: "target-bucket"}

	// Act
	result, _ := p.Pull(context.Background())

	// Assert
	if len(result.Pulled) != 1 || result.Pulled[0] != "a.txt" {
		t.Errorf("Pulled = %v, want [a.txt]", result.Pulled)
	}
}

func TestPuller_Pull_RefetchesWhenRemoteLastModifiedIsEmpty(t *testing.T) {
	// Arrange: regression test — a remote entry that can't report its own
	// LastModified (e.g. objectstore.formatTime returning "" for a nil S3
	// timestamp) must never look "up to date" just because any non-empty
	// local timestamp string-compares as >= "".
	cat := newTestCatalog()
	cat.Register("a.txt", "target-bucket", 5, "2026-08-01T00:00:00Z")
	client := &fakeClient{
		manifest: []catalog.Entry{{Key: "a.txt", Bucket: "remote-bucket", Size: 7, LastModified: ""}},
		objects:  map[string][]byte{"a.txt": []byte("updated")},
	}
	store := newFakeStore()
	p := &Puller{Remote: client, LocalStore: store, LocalCatalog: cat, TargetBucket: "target-bucket"}

	// Act
	result, err := p.Pull(context.Background())

	// Assert
	if err != nil {
		t.Fatalf("Pull() error = %v", err)
	}
	if len(result.Pulled) != 1 || result.Pulled[0] != "a.txt" {
		t.Errorf("Pulled = %v, want [a.txt] (empty remote LastModified must never be treated as up to date)", result.Pulled)
	}
}

func TestPuller_Pull_RegisterAppliesExistingCatalogPriorityNotANewConflictModel(t *testing.T) {
	// Arrange: "other-bucket" outranks "target-bucket" in this catalog's
	// priority function (lower number = higher priority, per
	// catalog.Register's own convention), and already owns the key —
	// Register's existing priority check (not any new logic in this
	// package) must keep it that way even after this pull stores bytes
	// into TargetBucket.
	cat := catalog.New(func(bucket string) int {
		if bucket == "other-bucket" {
			return 0
		}
		return 1
	}, func(string) string { return "private" })
	cat.Register("a.txt", "other-bucket", 999, "2020-01-01T00:00:00Z")
	client := &fakeClient{
		manifest: []catalog.Entry{{Key: "a.txt", Bucket: "remote-bucket", Size: 5, LastModified: "2026-08-01T00:00:00Z"}},
		objects:  map[string][]byte{"a.txt": []byte("hello")},
	}
	store := newFakeStore()
	p := &Puller{Remote: client, LocalStore: store, LocalCatalog: cat, TargetBucket: "target-bucket"}

	// Act
	if _, err := p.Pull(context.Background()); err != nil {
		t.Fatalf("Pull() error = %v", err)
	}

	// Assert: bytes were still written to TargetBucket's storage...
	if _, ok := store.objects["target-bucket"]["a.txt"]; !ok {
		t.Error("object was not stored into TargetBucket")
	}
	// ...but the catalog still credits the higher-priority bucket.
	entry, ok := cat.Lookup("a.txt")
	if !ok || entry.Bucket != "other-bucket" {
		t.Errorf("catalog entry = %+v, ok=%v, want it still owned by other-bucket", entry, ok)
	}
}

func TestPuller_Pull_ManifestFetchErrorAbortsTheWholeRun(t *testing.T) {
	// Arrange
	client := &fakeClient{manifestErr: errors.New("network error")}
	p := &Puller{Remote: client, LocalStore: newFakeStore(), LocalCatalog: newTestCatalog(), TargetBucket: "target-bucket"}

	// Act
	_, err := p.Pull(context.Background())

	// Assert
	if err == nil {
		t.Fatal("Pull() error = nil, want an error when the manifest fetch itself fails")
	}
}

func TestPuller_Pull_ObjectFetchFailureDoesNotBlockTheRestOfTheBatch(t *testing.T) {
	// Arrange
	client := &fakeClient{
		manifest: []catalog.Entry{
			{Key: "bad.txt", Bucket: "remote-bucket", Size: 5, LastModified: "2026-08-01T00:00:00Z"},
			{Key: "good.txt", Bucket: "remote-bucket", Size: 5, LastModified: "2026-08-01T00:00:00Z"},
		},
		objects:  map[string][]byte{"good.txt": []byte("hello")},
		failKeys: map[string]error{"bad.txt": errors.New("fetch failed")},
	}
	store := newFakeStore()
	cat := newTestCatalog()
	p := &Puller{Remote: client, LocalStore: store, LocalCatalog: cat, TargetBucket: "target-bucket"}

	// Act
	result, err := p.Pull(context.Background())

	// Assert
	if err != nil {
		t.Fatalf("Pull() error = %v, want nil (per-entry failures don't abort the run)", err)
	}
	if len(result.Failed) != 1 || result.Failed[0] != "bad.txt" {
		t.Errorf("Failed = %v, want [bad.txt]", result.Failed)
	}
	if len(result.Errors) != 1 {
		t.Errorf("len(Errors) = %d, want 1", len(result.Errors))
	}
	if len(result.Pulled) != 1 || result.Pulled[0] != "good.txt" {
		t.Errorf("Pulled = %v, want [good.txt]", result.Pulled)
	}
}

func TestPuller_Pull_StoreFailureIsRecordedAndDoesNotRegister(t *testing.T) {
	// Arrange
	client := &fakeClient{
		manifest: []catalog.Entry{{Key: "a.txt", Bucket: "remote-bucket", Size: 5, LastModified: "2026-08-01T00:00:00Z"}},
		objects:  map[string][]byte{"a.txt": []byte("hello")},
	}
	store := newFakeStore()
	store.failPut = map[string]bool{"a.txt": true}
	cat := newTestCatalog()
	p := &Puller{Remote: client, LocalStore: store, LocalCatalog: cat, TargetBucket: "target-bucket"}

	// Act
	result, err := p.Pull(context.Background())

	// Assert
	if err != nil {
		t.Fatalf("Pull() error = %v, want nil", err)
	}
	if len(result.Failed) != 1 || result.Failed[0] != "a.txt" {
		t.Errorf("Failed = %v, want [a.txt]", result.Failed)
	}
	if _, ok := cat.Lookup("a.txt"); ok {
		t.Error("catalog has a.txt after a failed store, want it absent")
	}
}

func TestPuller_Pull_EmptyManifestIsANoOp(t *testing.T) {
	// Arrange
	client := &fakeClient{}
	p := &Puller{Remote: client, LocalStore: newFakeStore(), LocalCatalog: newTestCatalog(), TargetBucket: "target-bucket"}

	// Act
	result, err := p.Pull(context.Background())

	// Assert
	if err != nil {
		t.Fatalf("Pull() error = %v", err)
	}
	if len(result.Pulled)+len(result.Skipped)+len(result.Failed) != 0 {
		t.Errorf("result = %+v, want all-empty for an empty manifest", result)
	}
}
