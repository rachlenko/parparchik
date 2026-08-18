package objectstore

import (
	"context"
	"testing"

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
