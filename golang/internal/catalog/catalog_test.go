package catalog

import (
	"encoding/json"
	"testing"
)

// testBuckets mirrors a 2-bucket config: "public-bucket" has priority 0
// (highest), "private-bucket" priority 1.
var testBuckets = map[string]struct {
	priority int
	public   bool
}{
	"public-bucket":  {priority: 0, public: true},
	"private-bucket": {priority: 1, public: false},
}

func testPriority(bucket string) int {
	if b, ok := testBuckets[bucket]; ok {
		return b.priority
	}
	return len(testBuckets)
}

func testBucketType(bucket string) string {
	if b, ok := testBuckets[bucket]; ok && b.public {
		return "public"
	}
	return "private"
}

func newTestCatalog() *Catalog {
	return New(testPriority, testBucketType)
}

func TestRegister_NewEntry(t *testing.T) {
	// Arrange
	c := newTestCatalog()

	// Act
	c.Register("photo.jpg", "public-bucket", 100, "2026-01-01T00:00:00Z")

	// Assert
	entry, ok := c.Lookup("photo.jpg")
	if !ok {
		t.Fatal("Lookup(photo.jpg) ok = false, want true")
	}
	want := Entry{
		Key: "photo.jpg", Bucket: "public-bucket", BucketType: "public",
		Route: "/public-bucket/photo.jpg", Size: 100, LastModified: "2026-01-01T00:00:00Z",
	}
	if entry != want {
		t.Errorf("Lookup(photo.jpg) = %+v, want %+v", entry, want)
	}
	if _, ok := c.LookupByRoute("/public-bucket/photo.jpg"); !ok {
		t.Error("LookupByRoute did not index the new entry's route")
	}
}

func TestRegister_LowerPriorityDoesNotOverwrite(t *testing.T) {
	// Arrange: public-bucket (priority 0) registers first.
	c := newTestCatalog()
	c.Register("shared.txt", "public-bucket", 10, "t1")

	// Act: private-bucket (priority 1, lower priority) tries to register the same key.
	c.Register("shared.txt", "private-bucket", 20, "t2")

	// Assert: the higher-priority bucket's entry wins.
	entry, _ := c.Lookup("shared.txt")
	if entry.Bucket != "public-bucket" || entry.Size != 10 {
		t.Errorf("Lookup(shared.txt) = %+v, want it to still belong to public-bucket", entry)
	}
}

func TestRegister_HigherPriorityOverwrites(t *testing.T) {
	// Arrange: private-bucket (priority 1) registers first.
	c := newTestCatalog()
	c.Register("shared.txt", "private-bucket", 10, "t1")

	// Act: public-bucket (priority 0, higher priority) registers the same key.
	c.Register("shared.txt", "public-bucket", 20, "t2")

	// Assert: the higher-priority bucket's entry wins, and the old route is gone.
	entry, _ := c.Lookup("shared.txt")
	if entry.Bucket != "public-bucket" || entry.Size != 20 {
		t.Errorf("Lookup(shared.txt) = %+v, want it to now belong to public-bucket", entry)
	}
	if _, ok := c.LookupByRoute("/private-bucket/shared.txt"); ok {
		t.Error("stale route /private-bucket/shared.txt should have been removed")
	}
}

func TestSet_MovesEntryAcrossBucketsRegardlessOfPriority(t *testing.T) {
	// Arrange: public-bucket (priority 0) already owns the key.
	c := newTestCatalog()
	c.Register("f.bin", "public-bucket", 5, "t1")

	// Act: Set reassigns it to private-bucket (priority 1) unconditionally —
	// unlike Register, it must not be blocked by public-bucket's higher
	// priority, since the caller has already confirmed via direct storage
	// lookup that private-bucket is where the file lives now.
	c.Set("f.bin", "private-bucket", 9, "t2")

	// Assert
	entry, _ := c.Lookup("f.bin")
	want := Entry{
		Key: "f.bin", Bucket: "private-bucket", BucketType: "private",
		Route: "/private-bucket/f.bin", Size: 9, LastModified: "t2",
	}
	if entry != want {
		t.Errorf("Lookup(f.bin) = %+v, want %+v", entry, want)
	}
	if _, ok := c.LookupByRoute("/public-bucket/f.bin"); ok {
		t.Error("old route should have been removed after Set")
	}
	if _, ok := c.LookupByRoute("/private-bucket/f.bin"); !ok {
		t.Error("new route should be indexed after Set")
	}
}

func TestSet_NewEntry(t *testing.T) {
	// Arrange
	c := newTestCatalog()

	// Act
	c.Set("new.bin", "private-bucket", 3, "t1")

	// Assert
	entry, ok := c.Lookup("new.bin")
	if !ok || entry.Bucket != "private-bucket" {
		t.Errorf("Lookup(new.bin) = %+v, ok=%v, want it registered under private-bucket", entry, ok)
	}
}

func TestRemove(t *testing.T) {
	// Arrange
	c := newTestCatalog()
	c.Register("gone.txt", "public-bucket", 1, "t1")

	// Act
	c.Remove("gone.txt")

	// Assert
	if _, ok := c.Lookup("gone.txt"); ok {
		t.Error("Lookup(gone.txt) found an entry after Remove")
	}
	if _, ok := c.LookupByRoute("/public-bucket/gone.txt"); ok {
		t.Error("LookupByRoute found a route after Remove")
	}
	if c.Count() != 0 {
		t.Errorf("Count() = %d, want 0", c.Count())
	}
}

func TestListAll_SortedByKey(t *testing.T) {
	// Arrange
	c := newTestCatalog()
	c.Register("zebra", "public-bucket", 1, "")
	c.Register("apple", "public-bucket", 1, "")
	c.Register("mango", "public-bucket", 1, "")

	// Act
	entries := c.ListAll()

	// Assert
	want := []string{"apple", "mango", "zebra"}
	if len(entries) != len(want) {
		t.Fatalf("len(entries) = %d, want %d", len(entries), len(want))
	}
	for i, k := range want {
		if entries[i].Key != k {
			t.Errorf("entries[%d].Key = %q, want %q", i, entries[i].Key, k)
		}
	}
}

func TestListByBucket(t *testing.T) {
	// Arrange
	c := newTestCatalog()
	c.Register("a", "public-bucket", 1, "")
	c.Register("b", "private-bucket", 1, "")

	// Act
	entries := c.ListByBucket("private-bucket")

	// Assert
	if len(entries) != 1 || entries[0].Key != "b" {
		t.Errorf("ListByBucket(private-bucket) = %+v, want just entry \"b\"", entries)
	}
}

func TestManifestForBucket(t *testing.T) {
	// Arrange
	c := newTestCatalog()
	c.Register("a", "public-bucket", 1, "")

	// Act
	m := c.ManifestForBucket("public-bucket")

	// Assert
	if m.Version != 1 || m.Bucket != "public-bucket" || len(m.Files) != 1 {
		t.Errorf("ManifestForBucket = %+v, want version=1 bucket=public-bucket with 1 file", m)
	}
}

func TestLoadManifests_HigherPriorityBucketWinsOnConflict(t *testing.T) {
	// Arrange: both manifests list the same key; public-bucket has priority.
	c := newTestCatalog()
	publicManifest, _ := json.Marshal(Manifest{Files: []Entry{{Key: "shared", Size: 100}}})
	privateManifest, _ := json.Marshal(Manifest{Files: []Entry{{Key: "shared", Size: 999}}})

	// Act
	c.LoadManifests([]BucketManifest{
		{Bucket: "public-bucket", Content: publicManifest},
		{Bucket: "private-bucket", Content: privateManifest},
	})

	// Assert
	entry, ok := c.Lookup("shared")
	if !ok {
		t.Fatal("Lookup(shared) ok = false")
	}
	if entry.Bucket != "public-bucket" || entry.Size != 100 {
		t.Errorf("Lookup(shared) = %+v, want it to belong to public-bucket with size 100", entry)
	}
}

func TestLoadManifests_BareArrayFormat(t *testing.T) {
	// Arrange: manifest content is a bare JSON array, not a {"files": [...]} envelope.
	c := newTestCatalog()
	content, _ := json.Marshal([]Entry{{Key: "a"}, {Key: "b"}})

	// Act
	c.LoadManifests([]BucketManifest{{Bucket: "public-bucket", Content: content}})

	// Assert
	if c.Count() != 2 {
		t.Errorf("Count() = %d, want 2", c.Count())
	}
}

func TestLoadManifests_InvalidContentIsIgnored(t *testing.T) {
	// Arrange
	c := newTestCatalog()
	c.Register("survivor", "public-bucket", 1, "")

	// Act: LoadManifests always clears first, so this should end with zero
	// entries — not an error — for malformed content.
	c.LoadManifests([]BucketManifest{{Bucket: "public-bucket", Content: []byte("not json")}})

	// Assert
	if c.Count() != 0 {
		t.Errorf("Count() = %d, want 0 after loading invalid manifest content", c.Count())
	}
}

func TestLoadManifests_EntryBucketOverridesManifestBucket(t *testing.T) {
	// Arrange: an entry embeds its own bucket field, which should win over
	// the manifest's own bucket (mirrors items relocated after the manifest
	// was last written but before it was refreshed).
	c := newTestCatalog()
	content, _ := json.Marshal(Manifest{Files: []Entry{{Key: "moved", Bucket: "private-bucket"}}})

	// Act
	c.LoadManifests([]BucketManifest{{Bucket: "public-bucket", Content: content}})

	// Assert
	entry, _ := c.Lookup("moved")
	if entry.Bucket != "private-bucket" {
		t.Errorf("Lookup(moved).Bucket = %q, want private-bucket", entry.Bucket)
	}
}
