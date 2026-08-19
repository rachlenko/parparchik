package snapshot

import (
	"reflect"
	"testing"

	"github.com/rachlenko/parparchik/golang/internal/catalog"
)

func priorityFunc(bucket string) int {
	if bucket == "public-bucket" {
		return 0
	}
	return 1
}

func bucketTypeFunc(bucket string) string {
	if bucket == "public-bucket" {
		return "public"
	}
	return "private"
}

func newTestCatalog() *catalog.Catalog {
	return catalog.New(priorityFunc, bucketTypeFunc)
}

func TestCapture_DerivesBucketsFromCatalogEntries(t *testing.T) {
	// Arrange
	cat := newTestCatalog()
	cat.Register("a.txt", "public-bucket", 10, "2026-08-01T00:00:00Z")
	cat.Register("b.txt", "private-bucket", 20, "2026-08-01T00:00:00Z")

	// Act
	doc := Capture(cat)

	// Assert
	if len(doc.Buckets) != 2 {
		t.Fatalf("len(doc.Buckets) = %d, want 2", len(doc.Buckets))
	}
	names := map[string]bool{doc.Buckets[0].Bucket: true, doc.Buckets[1].Bucket: true}
	if !names["public-bucket"] || !names["private-bucket"] {
		t.Errorf("captured buckets = %v, want public-bucket and private-bucket", names)
	}
	if doc.Version != documentVersion {
		t.Errorf("Version = %d, want %d", doc.Version, documentVersion)
	}
	if doc.CapturedAt.IsZero() {
		t.Error("CapturedAt is zero, want it set")
	}
}

func TestCapture_EmptyCatalogProducesEmptyDocument(t *testing.T) {
	// Act
	doc := Capture(newTestCatalog())

	// Assert
	if len(doc.Buckets) != 0 {
		t.Errorf("len(doc.Buckets) = %d, want 0", len(doc.Buckets))
	}
}

func TestRestore_ProducesTheSameCatalogStateAsALiveSync(t *testing.T) {
	// Arrange: populate an original catalog the way a live sync would
	// (via Register, which is exactly what resolver.SyncRegistry calls
	// per discovered object).
	original := newTestCatalog()
	original.Register("a.txt", "public-bucket", 10, "2026-08-01T00:00:00Z")
	original.Register("b.txt", "private-bucket", 20, "2026-08-02T00:00:00Z")
	original.Register("c.txt", "private-bucket", 30, "2026-08-03T00:00:00Z")

	doc := Capture(original)

	// Act: round-trip through JSON, the way a real backup/restore would,
	// then restore into a fresh catalog.
	body, err := doc.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	decoded, err := Unmarshal(body)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	restored := newTestCatalog()
	if err := Restore(restored, decoded); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	// Assert
	want := original.ListAll()
	got := restored.ListAll()
	if !reflect.DeepEqual(got, want) {
		t.Errorf("restored.ListAll() = %+v, want %+v (identical to the original live-populated catalog)", got, want)
	}
}

func TestRestore_ReplacesExistingCatalogContents(t *testing.T) {
	// Arrange
	source := newTestCatalog()
	source.Register("a.txt", "public-bucket", 10, "2026-08-01T00:00:00Z")
	doc := Capture(source)

	target := newTestCatalog()
	target.Register("stale-entry.txt", "public-bucket", 999, "2020-01-01T00:00:00Z")

	// Act
	if err := Restore(target, doc); err != nil {
		t.Fatalf("Restore() error = %v", err)
	}

	// Assert
	if _, ok := target.Lookup("stale-entry.txt"); ok {
		t.Error("target still has stale-entry.txt after Restore, want it replaced entirely")
	}
	if _, ok := target.Lookup("a.txt"); !ok {
		t.Error("target does not have a.txt after Restore, want it present")
	}
}

func TestUnmarshal_RejectsUnsupportedVersion(t *testing.T) {
	// Arrange
	body := []byte(`{"version": 999, "buckets": []}`)

	// Act
	_, err := Unmarshal(body)

	// Assert
	if err == nil {
		t.Fatal("Unmarshal() error = nil, want an error for an unsupported version")
	}
}

func TestUnmarshal_RejectsMalformedJSON(t *testing.T) {
	// Act
	_, err := Unmarshal([]byte(`not json`))

	// Assert
	if err == nil {
		t.Fatal("Unmarshal() error = nil, want an error for malformed JSON")
	}
}

func TestDocument_MarshalUnmarshalRoundTrip(t *testing.T) {
	// Arrange
	cat := newTestCatalog()
	cat.Register("a.txt", "public-bucket", 10, "2026-08-01T00:00:00Z")
	doc := Capture(cat)

	// Act
	body, err := doc.Marshal()
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	got, err := Unmarshal(body)
	if err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}

	// Assert: CapturedAt is compared with time.Time.Equal, not
	// reflect.DeepEqual — a JSON round-trip strips the monotonic clock
	// reading time.Now() attaches, so DeepEqual would spuriously fail
	// even though the two values represent the identical instant.
	if got.Version != doc.Version {
		t.Errorf("Version = %d, want %d", got.Version, doc.Version)
	}
	if !got.CapturedAt.Equal(doc.CapturedAt) {
		t.Errorf("CapturedAt = %v, want %v", got.CapturedAt, doc.CapturedAt)
	}
	if !reflect.DeepEqual(got.Buckets, doc.Buckets) {
		t.Errorf("Buckets = %+v, want %+v", got.Buckets, doc.Buckets)
	}
}
