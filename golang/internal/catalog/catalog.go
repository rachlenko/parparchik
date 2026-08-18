// Package catalog holds the in-memory file registry: which S3 key lives in
// which bucket, and which public route serves it. It is the Go equivalent
// of the OpenResty ngx.shared.DICT-backed registry, but concurrency-safe via
// a single RWMutex instead of ad hoc shared-dict access.
package catalog

import (
	"encoding/json"
	"sort"
	"sync"
)

// Entry describes one registered file.
type Entry struct {
	Key          string `json:"key"`
	Bucket       string `json:"bucket"`
	BucketType   string `json:"bucket_type"`
	Route        string `json:"route"`
	Size         int64  `json:"size"`
	LastModified string `json:"last_modified"`
}

// Manifest is the JSON document persisted to each bucket describing the
// files that currently live there.
type Manifest struct {
	Version int     `json:"version"`
	Bucket  string  `json:"bucket"`
	Files   []Entry `json:"files"`
}

// BucketManifest pairs a bucket name with the raw manifest content read
// back from storage (possibly empty, if the bucket has never had one
// written yet).
type BucketManifest struct {
	Bucket  string
	Content []byte
}

// PriorityFunc ranks buckets for conflict resolution: lower return value
// wins when the same key is registered from two different buckets.
type PriorityFunc func(bucket string) int

// BucketTypeFunc classifies a bucket as "public" or "private".
type BucketTypeFunc func(bucket string) string

// Catalog is the concurrency-safe in-memory file registry.
type Catalog struct {
	mu         sync.RWMutex
	byKey      map[string]Entry
	byRoute    map[string]string // route -> key
	priority   PriorityFunc
	bucketType BucketTypeFunc
}

// New creates an empty Catalog. priority and bucketType are supplied by the
// caller (backed by config.Config) rather than baked into this package, so
// catalog stays free of any dependency on how buckets are configured.
func New(priority PriorityFunc, bucketType BucketTypeFunc) *Catalog {
	return &Catalog{
		byKey:      make(map[string]Entry),
		byRoute:    make(map[string]string),
		priority:   priority,
		bucketType: bucketType,
	}
}

func routeFor(bucket, key string) string {
	return "/" + bucket + "/" + key
}

// Register adds or updates a file entry. If key is already registered under
// a higher-priority bucket, the existing entry wins and this call is a
// no-op — mirroring the Lua registry's "lower index = higher priority"
// rule.
func (c *Catalog) Register(key, bucket string, size int64, lastModified string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if existing, ok := c.byKey[key]; ok {
		if c.priority(existing.Bucket) < c.priority(bucket) {
			return
		}
		delete(c.byRoute, existing.Route)
	}

	entry := Entry{
		Key:          key,
		Bucket:       bucket,
		BucketType:   c.bucketType(bucket),
		Route:        routeFor(bucket, key),
		Size:         size,
		LastModified: lastModified,
	}
	c.byKey[key] = entry
	c.byRoute[entry.Route] = key
}

// Set unconditionally registers key under bucket, overwriting any existing
// entry (and its route index) regardless of bucket priority.
//
// Use Set when the caller has already established, via a direct storage
// check (HEAD), exactly where a specific key lives right now — as
// resolver.ResolveMissingFile and resolver.Relocate do, searching buckets
// in priority order and stopping at the first hit, so priority has already
// been applied at the search level. Register's additional priority check
// exists for the different case of reconciling a bulk listing where a key
// might legitimately appear in more than one bucket at once (SyncRegistry);
// applying that same check again in Set's callers would let a stale
// higher-priority entry block a confirmed reconciliation update forever.
func (c *Catalog) Set(key, bucket string, size int64, lastModified string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if existing, ok := c.byKey[key]; ok {
		delete(c.byRoute, existing.Route)
	}

	entry := Entry{
		Key:          key,
		Bucket:       bucket,
		BucketType:   c.bucketType(bucket),
		Route:        routeFor(bucket, key),
		Size:         size,
		LastModified: lastModified,
	}
	c.byKey[key] = entry
	c.byRoute[entry.Route] = key
}

// Remove deletes a file entry and its route index, if present.
func (c *Catalog) Remove(key string) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if entry, ok := c.byKey[key]; ok {
		delete(c.byRoute, entry.Route)
	}
	delete(c.byKey, key)
}

// Lookup returns the entry registered under key.
func (c *Catalog) Lookup(key string) (Entry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	entry, ok := c.byKey[key]
	return entry, ok
}

// LookupByRoute returns the entry whose public route matches route.
func (c *Catalog) LookupByRoute(route string) (Entry, bool) {
	c.mu.RLock()
	defer c.mu.RUnlock()
	key, ok := c.byRoute[route]
	if !ok {
		return Entry{}, false
	}
	return c.byKey[key], true
}

// ListAll returns every registered entry, sorted by key for deterministic
// output.
func (c *Catalog) ListAll() []Entry {
	c.mu.RLock()
	defer c.mu.RUnlock()

	result := make([]Entry, 0, len(c.byKey))
	for _, entry := range c.byKey {
		result = append(result, entry)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Key < result[j].Key })
	return result
}

// ListByBucket returns every entry currently attributed to bucket, sorted by
// key.
func (c *Catalog) ListByBucket(bucket string) []Entry {
	all := c.ListAll()
	result := make([]Entry, 0, len(all))
	for _, entry := range all {
		if entry.Bucket == bucket {
			result = append(result, entry)
		}
	}
	return result
}

// ManifestForBucket builds the manifest document to persist for bucket.
func (c *Catalog) ManifestForBucket(bucket string) Manifest {
	return Manifest{Version: 1, Bucket: bucket, Files: c.ListByBucket(bucket)}
}

// Clear removes every registered entry.
func (c *Catalog) Clear() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.byKey = make(map[string]Entry)
	c.byRoute = make(map[string]string)
}

// Count returns the number of registered files.
func (c *Catalog) Count() int {
	c.mu.RLock()
	defer c.mu.RUnlock()
	return len(c.byKey)
}

// LoadManifests replaces the catalog contents with the given per-bucket
// manifests. Application order doesn't matter: Register applies its own
// priority check on every call, so whichever bucket ranks highest ends up
// owning any key that collides across manifests, regardless of which
// manifest was processed first.
func (c *Catalog) LoadManifests(manifests []BucketManifest) {
	c.Clear()

	for _, m := range manifests {
		if len(m.Content) == 0 {
			continue
		}

		files, ok := decodeManifestFiles(m.Content)
		if !ok {
			continue
		}
		for _, item := range files {
			if item.Key == "" {
				continue
			}
			bucket := item.Bucket
			if bucket == "" {
				bucket = m.Bucket
			}
			c.Register(item.Key, bucket, item.Size, item.LastModified)
		}
	}
}

// decodeManifestFiles accepts either a bare JSON array of entries or a
// {"files": [...]} envelope, matching what the Lua registry historically
// wrote and read.
func decodeManifestFiles(content []byte) ([]Entry, bool) {
	var envelope struct {
		Files []Entry `json:"files"`
	}
	if err := json.Unmarshal(content, &envelope); err == nil && envelope.Files != nil {
		return envelope.Files, true
	}

	var bare []Entry
	if err := json.Unmarshal(content, &bare); err == nil {
		return bare, true
	}
	return nil, false
}
