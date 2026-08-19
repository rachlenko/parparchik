// Package snapshot captures and restores an internal/catalog.Catalog's
// entire state as a single serializable Document — mainly a startup-time
// optimization, not a durability mechanism.
//
// resolver.Bootstrap can already rebuild a catalog from scratch: read
// each configured bucket's persisted manifest (resolver.PersistManifests
// already writes these — the actual durable source of truth), then run a
// live storage sync pass to catch anything the manifests missed. That
// requires an S3 round-trip per configured bucket before the service can
// report ready. A Document captured from a running instance's catalog and
// Restored at the next startup lets a new process serve read traffic
// immediately from that prior state — Bootstrap's normal manifest+sync
// pass still runs afterward to catch up on anything that changed since
// the snapshot was taken, exactly as it would on a cold start. This is a
// cache warm-start, never a substitute for it.
//
// See docs/backup-and-disaster-recovery.md for the durability story this
// package is explicitly NOT part of: backing up the object storage
// buckets themselves (manifests and the actual objects together) remains
// the real disaster-recovery mechanism, unaffected by whether any
// process ever captures or restores a Document.
package snapshot

import (
	"encoding/json"
	"fmt"
	"sort"
	"time"

	"github.com/rachlenko/parparchik/golang/internal/catalog"
)

// documentVersion is bumped only if Document's shape changes
// incompatibly; Unmarshal rejects a Document from a newer, unrecognized
// version rather than guessing how to read it.
const documentVersion = 1

// Document is a point-in-time capture of every bucket represented in a
// Catalog, as their manifests.
type Document struct {
	Version    int                `json:"version"`
	CapturedAt time.Time          `json:"captured_at"`
	Buckets    []catalog.Manifest `json:"buckets"`
}

// Capture builds a Document from cat's current state. The set of buckets
// captured is derived entirely from cat.ListAll() (every distinct
// Entry.Bucket value) — Capture needs no separate bucket-name list passed
// in, and a bucket with zero entries currently registered contributes
// nothing to the Document (there is nothing to capture for it; Restore
// simply won't populate it, the same as it wouldn't exist in a live sync
// either).
func Capture(cat *catalog.Catalog) Document {
	buckets := distinctBuckets(cat.ListAll())

	doc := Document{
		Version:    documentVersion,
		CapturedAt: time.Now(),
		Buckets:    make([]catalog.Manifest, 0, len(buckets)),
	}
	for _, bucket := range buckets {
		doc.Buckets = append(doc.Buckets, cat.ManifestForBucket(bucket))
	}
	return doc
}

func distinctBuckets(entries []catalog.Entry) []string {
	seen := make(map[string]bool)
	var buckets []string
	for _, e := range entries {
		if !seen[e.Bucket] {
			seen[e.Bucket] = true
			buckets = append(buckets, e.Bucket)
		}
	}
	sort.Strings(buckets)
	return buckets
}

// Marshal encodes d as indented JSON.
func (d Document) Marshal() ([]byte, error) {
	body, err := json.MarshalIndent(d, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("snapshot: encode document: %w", err)
	}
	return body, nil
}

// Unmarshal decodes data into a Document, rejecting a Version this
// package doesn't recognize rather than silently misreading it.
func Unmarshal(data []byte) (Document, error) {
	var doc Document
	if err := json.Unmarshal(data, &doc); err != nil {
		return Document{}, fmt.Errorf("snapshot: decode document: %w", err)
	}
	if doc.Version != documentVersion {
		return Document{}, fmt.Errorf("snapshot: unsupported document version %d, want %d", doc.Version, documentVersion)
	}
	return doc, nil
}

// Restore replaces cat's entire contents with doc's, via
// catalog.Catalog.LoadManifests — the same method resolver.Bootstrap
// itself uses to load manifests read live from storage, so a Restored
// catalog's conflict resolution (priority-checked Register, applied per
// entry) is identical to what a live sync would have produced from the
// same manifest data.
func Restore(cat *catalog.Catalog, doc Document) error {
	manifests := make([]catalog.BucketManifest, len(doc.Buckets))
	for i, m := range doc.Buckets {
		content, err := json.Marshal(m)
		if err != nil {
			return fmt.Errorf("snapshot: re-encode manifest for bucket %q: %w", m.Bucket, err)
		}
		manifests[i] = catalog.BucketManifest{Bucket: m.Bucket, Content: content}
	}
	cat.LoadManifests(manifests)
	return nil
}
