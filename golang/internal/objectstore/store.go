// Package objectstore defines the storage abstraction parparchik uses to
// talk to S3-compatible object storage, and provides an implementation
// backed by aws-sdk-go-v2. Format packages and the resolver depend only on
// the Store interface, so a different backend (filesystem, GCS, Azure Blob)
// can be added without touching catalog or routing logic.
package objectstore

import (
	"context"
	"time"
)

// Object describes a stored file's metadata.
type Object struct {
	Key          string
	Size         int64
	LastModified string
}

// Store is the storage backend contract. Implementations must be safe for
// concurrent use.
type Store interface {
	// ListObjects returns every object in bucket, excluding none of the
	// caller's own filtering — that is the resolver's job.
	ListObjects(ctx context.Context, bucket string) ([]Object, error)

	// HeadObject returns the object's metadata, or (nil, nil) if it does
	// not exist. A non-nil error indicates a request/transport failure,
	// distinct from "not found".
	HeadObject(ctx context.Context, bucket, key string) (*Object, error)

	// GetObject returns an object's full content.
	GetObject(ctx context.Context, bucket, key string) ([]byte, error)

	// PutObject writes content to bucket/key.
	PutObject(ctx context.Context, bucket, key string, content []byte, contentType string) error

	// DeleteObject removes bucket/key. Deleting an already-absent key is
	// not an error — S3's DeleteObject API itself is idempotent this way,
	// and callers (e.g. internal/cleanup) that retry a partially-failed
	// batch should not have to distinguish "already gone" from "just
	// deleted".
	DeleteObject(ctx context.Context, bucket, key string) error

	// PublicURL returns an unsigned URL for a public bucket's object.
	PublicURL(bucket, key string) string

	// PresignedURL returns a time-limited signed URL for a private
	// bucket's object.
	PresignedURL(ctx context.Context, bucket, key string, expires time.Duration) (string, error)
}
