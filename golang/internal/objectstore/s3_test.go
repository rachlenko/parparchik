package objectstore

import (
	"context"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/rachlenko/parparchik/golang/internal/config"
)

func TestS3Store_PublicURL(t *testing.T) {
	cases := []struct {
		name     string
		cfg      *config.Config
		bucket   string
		key      string
		wantHost string
	}{
		{
			name:     "bare external endpoint defaults to http",
			cfg:      &config.Config{AWSRegion: "us-east-1", S3ExternalEndpoint: "cdn.example.com"},
			bucket:   "b",
			key:      "k",
			wantHost: "http://cdn.example.com/b/k",
		},
		{
			name:     "scheme-qualified external endpoint is preserved, not double-prefixed",
			cfg:      &config.Config{AWSRegion: "us-east-1", S3ExternalEndpoint: "https://cdn.example.com"},
			bucket:   "b",
			key:      "k",
			wantHost: "https://cdn.example.com/b/k",
		},
		{
			name:     "falls back to internal S3_ENDPOINT when no external endpoint set",
			cfg:      &config.Config{AWSRegion: "us-east-1", S3Endpoint: "https://minio.internal:9000"},
			bucket:   "b",
			key:      "k",
			wantHost: "https://minio.internal:9000/b/k",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			store, err := NewS3Store(context.Background(), tc.cfg)
			if err != nil {
				t.Fatalf("NewS3Store() error = %v", err)
			}

			// Act
			got := store.PublicURL(tc.bucket, tc.key)

			// Assert
			if got != tc.wantHost {
				t.Errorf("PublicURL() = %q, want %q", got, tc.wantHost)
			}
		})
	}
}

func TestS3Store_PresignedURL_UsesExternalEndpointNotInternal(t *testing.T) {
	// Arrange: internal S3_ENDPOINT (only reachable inside a Docker
	// network) differs from S3_EXTERNAL_ENDPOINT (what an outside client
	// must use) — the docker-compose MinIO shape. A presigned URL that
	// used the internal host would be unresolvable by any client that
	// isn't on that Docker network — see s3.go's NewS3Store doc comment on
	// presignClient for the incident this test guards against.
	cfg := &config.Config{
		AWSRegion:          "us-east-1",
		S3Endpoint:         "minio:9000",
		S3ExternalEndpoint: "localhost:9000",
		AWSAccessKeyID:     "minioadmin",
		AWSSecretAccessKey: "minioadmin",
	}
	store, err := NewS3Store(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewS3Store() error = %v", err)
	}

	// Act
	presigned, err := store.PresignedURL(context.Background(), "private-bucket", "secret.txt", time.Hour)

	// Assert
	if err != nil {
		t.Fatalf("PresignedURL() error = %v", err)
	}
	u, err := url.Parse(presigned)
	if err != nil {
		t.Fatalf("PresignedURL() returned an unparseable URL %q: %v", presigned, err)
	}
	if u.Host != "localhost:9000" {
		t.Errorf("PresignedURL() host = %q, want localhost:9000 (the external endpoint, not minio:9000)", u.Host)
	}
	if !strings.Contains(u.Path, "private-bucket/secret.txt") {
		t.Errorf("PresignedURL() path = %q, want it to contain private-bucket/secret.txt", u.Path)
	}
	if q := u.Query().Get("X-Amz-Signature"); q == "" {
		t.Error("PresignedURL() has no X-Amz-Signature query parameter")
	}
}

func TestS3Store_PresignedURL_NoCustomEndpointUsesDefaultAWSHost(t *testing.T) {
	// Arrange: no S3_ENDPOINT at all (real AWS S3) — presigning should use
	// the SDK's default endpoint resolution, same as the main client.
	cfg := &config.Config{
		AWSRegion:          "eu-west-1",
		AWSAccessKeyID:     "AKIAEXAMPLE",
		AWSSecretAccessKey: "secretexample",
	}
	store, err := NewS3Store(context.Background(), cfg)
	if err != nil {
		t.Fatalf("NewS3Store() error = %v", err)
	}

	// Act
	presigned, err := store.PresignedURL(context.Background(), "my-bucket", "my-key", time.Hour)

	// Assert
	if err != nil {
		t.Fatalf("PresignedURL() error = %v", err)
	}
	u, err := url.Parse(presigned)
	if err != nil {
		t.Fatalf("PresignedURL() returned an unparseable URL %q: %v", presigned, err)
	}
	if !strings.Contains(u.Host, "amazonaws.com") {
		t.Errorf("PresignedURL() host = %q, want an amazonaws.com host", u.Host)
	}
}

func TestS3Store_PublicURL_NoEndpointFallsBackToAWSVirtualHostedStyle(t *testing.T) {
	// Arrange
	store, err := NewS3Store(context.Background(), &config.Config{AWSRegion: "eu-west-1"})
	if err != nil {
		t.Fatalf("NewS3Store() error = %v", err)
	}

	// Act
	got := store.PublicURL("my-bucket", "my-key")

	// Assert
	want := "https://my-bucket.s3.eu-west-1.amazonaws.com/my-key"
	if got != want {
		t.Errorf("PublicURL() = %q, want %q", got, want)
	}
}
