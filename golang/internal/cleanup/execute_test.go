package cleanup

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/rachlenko/parparchik/golang/internal/catalog"
	"github.com/rachlenko/parparchik/golang/internal/objectstore"
)

type fakeStore struct {
	deleted   []string // "bucket/key"
	failKeys  map[string]bool
	deleteErr error
}

func (s *fakeStore) ListObjects(context.Context, string) ([]objectstore.Object, error) {
	return nil, nil
}
func (s *fakeStore) HeadObject(context.Context, string, string) (*objectstore.Object, error) {
	return nil, nil
}
func (s *fakeStore) GetObject(context.Context, string, string) ([]byte, error)       { return nil, nil }
func (s *fakeStore) PutObject(context.Context, string, string, []byte, string) error { return nil }
func (s *fakeStore) PublicURL(bucket, key string) string                             { return "" }
func (s *fakeStore) PresignedURL(context.Context, string, string, time.Duration) (string, error) {
	return "", nil
}

func (s *fakeStore) DeleteObject(_ context.Context, bucket, key string) error {
	if s.failKeys[key] {
		return s.deleteErr
	}
	s.deleted = append(s.deleted, bucket+"/"+key)
	return nil
}

func newTestCatalog() *catalog.Catalog {
	return catalog.New(func(string) int { return 0 }, func(string) string { return "private" })
}

func TestExecute_DeletesFromStoreAndRemovesFromCatalog(t *testing.T) {
	// Arrange
	store := &fakeStore{}
	cat := newTestCatalog()
	cat.Register("k1", "bucket-a", 10, "2026-01-01T00:00:00Z")
	victims := []catalog.Entry{{Key: "k1", Bucket: "bucket-a"}}

	// Act
	err := Execute(context.Background(), store, cat, victims)

	// Assert
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(store.deleted) != 1 || store.deleted[0] != "bucket-a/k1" {
		t.Errorf("store.deleted = %v, want [bucket-a/k1]", store.deleted)
	}
	if _, ok := cat.Lookup("k1"); ok {
		t.Error("catalog still has k1 after Execute, want it removed")
	}
}

func TestExecute_FailedDeleteIsNotRemovedFromCatalogAndErrorIsReturned(t *testing.T) {
	// Arrange
	wantErr := errors.New("network error")
	store := &fakeStore{failKeys: map[string]bool{"k1": true}, deleteErr: wantErr}
	cat := newTestCatalog()
	cat.Register("k1", "bucket-a", 10, "2026-01-01T00:00:00Z")
	victims := []catalog.Entry{{Key: "k1", Bucket: "bucket-a"}}

	// Act
	err := Execute(context.Background(), store, cat, victims)

	// Assert
	if err == nil {
		t.Fatal("Execute() error = nil, want an error for the failed delete")
	}
	if !errors.Is(err, wantErr) {
		t.Errorf("Execute() error = %v, want it to wrap %v", err, wantErr)
	}
	if _, ok := cat.Lookup("k1"); !ok {
		t.Error("catalog no longer has k1 after a failed delete, want it kept")
	}
}

func TestExecute_OneFailureDoesNotBlockTheRestOfTheBatch(t *testing.T) {
	// Arrange
	store := &fakeStore{failKeys: map[string]bool{"bad": true}, deleteErr: errors.New("boom")}
	cat := newTestCatalog()
	cat.Register("good", "bucket-a", 10, "2026-01-01T00:00:00Z")
	cat.Register("bad", "bucket-a", 10, "2026-01-01T00:00:00Z")
	victims := []catalog.Entry{{Key: "bad", Bucket: "bucket-a"}, {Key: "good", Bucket: "bucket-a"}}

	// Act
	err := Execute(context.Background(), store, cat, victims)

	// Assert
	if err == nil {
		t.Fatal("Execute() error = nil, want an error covering the one failure")
	}
	if _, ok := cat.Lookup("good"); ok {
		t.Error("catalog still has \"good\" after a successful delete, want it removed")
	}
	if _, ok := cat.Lookup("bad"); !ok {
		t.Error("catalog no longer has \"bad\" after a failed delete, want it kept")
	}
}

func TestExecute_EmptyVictimsIsANoOp(t *testing.T) {
	// Arrange
	store := &fakeStore{}
	cat := newTestCatalog()

	// Act
	err := Execute(context.Background(), store, cat, nil)

	// Assert
	if err != nil {
		t.Errorf("Execute() error = %v, want nil", err)
	}
}
