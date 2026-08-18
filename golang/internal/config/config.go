// Package config loads parparchik configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// Bucket describes one configured storage bucket: its S3 name, the key its
// file-registry manifest is persisted under, and whether it serves public
// (unsigned) or private (presigned) download URLs.
type Bucket struct {
	Name        string
	ManifestKey string
	Public      bool
}

// Config holds all runtime configuration for the service.
type Config struct {
	AWSRegion          string
	S3Endpoint         string
	S3ExternalEndpoint string
	AWSAccessKeyID     string
	AWSSecretAccessKey string
	Host               string
	Port               int
	Buckets            []Bucket

	// APIKeys, when non-empty, requires every route except /healthcheck and
	// /readiness to present one of these keys (see httpapi.AuthConfig).
	// Left empty by default to preserve the Lua original's open-by-default
	// behavior for local/dev use — see the security review notes on why
	// this must be set for anything reachable outside a trusted network.
	APIKeys []string

	// RateLimitPerSecond and RateLimitBurst configure the per-client-IP
	// request rate limit (see httpapi.WithRateLimit). Defaults are
	// deliberately conservative for an unauthenticated deployment; raise
	// them for trusted/internal use.
	RateLimitPerSecond float64
	RateLimitBurst     int

	// SyncInterval is how often the background reconciliation loop
	// re-lists every bucket and refreshes manifests (in addition to the
	// one-time reconcile Bootstrap performs at startup).
	SyncInterval time.Duration
}

const defaultManifestKey = ".parparchik/files.json"

func env(name, def string) string {
	if v := os.Getenv(name); v != "" {
		return v
	}
	return def
}

// Load reads configuration from the environment. Buckets can be configured
// either as a single comma-separated PARPARCHIK_BUCKETS list of
// "name:manifest_key:public" tokens, or as the legacy pair of
// PARPARCHIK_PUBLIC_BUCKET / PARPARCHIK_PRIVATE_BUCKET variables.
func Load() (*Config, error) {
	port, err := strconv.Atoi(env("PARPARCHIK_PORT", "8080"))
	if err != nil {
		return nil, fmt.Errorf("config: invalid PARPARCHIK_PORT: %w", err)
	}

	cfg := &Config{
		AWSRegion:          env("AWS_REGION", "us-east-1"),
		S3Endpoint:         env("S3_ENDPOINT", ""),
		S3ExternalEndpoint: env("S3_EXTERNAL_ENDPOINT", ""),
		AWSAccessKeyID:     env("AWS_ACCESS_KEY_ID", ""),
		AWSSecretAccessKey: env("AWS_SECRET_ACCESS_KEY", ""),
		Host:               env("PARPARCHIK_HOST", "0.0.0.0"),
		Port:               port,
	}

	buckets, err := loadBuckets()
	if err != nil {
		return nil, err
	}
	cfg.Buckets = buckets

	if raw := env("PARPARCHIK_API_KEYS", ""); raw != "" {
		for _, k := range strings.Split(raw, ",") {
			if k = strings.TrimSpace(k); k != "" {
				cfg.APIKeys = append(cfg.APIKeys, k)
			}
		}
	}

	rateLimit, err := strconv.ParseFloat(env("PARPARCHIK_RATE_LIMIT_PER_SECOND", "5"), 64)
	if err != nil {
		return nil, fmt.Errorf("config: invalid PARPARCHIK_RATE_LIMIT_PER_SECOND: %w", err)
	}
	cfg.RateLimitPerSecond = rateLimit

	burst, err := strconv.Atoi(env("PARPARCHIK_RATE_LIMIT_BURST", "10"))
	if err != nil {
		return nil, fmt.Errorf("config: invalid PARPARCHIK_RATE_LIMIT_BURST: %w", err)
	}
	cfg.RateLimitBurst = burst

	syncInterval, err := time.ParseDuration(env("PARPARCHIK_SYNC_INTERVAL", "5m"))
	if err != nil {
		return nil, fmt.Errorf("config: invalid PARPARCHIK_SYNC_INTERVAL: %w", err)
	}
	cfg.SyncInterval = syncInterval

	return cfg, nil
}

func loadBuckets() ([]Bucket, error) {
	if raw := env("PARPARCHIK_BUCKETS", ""); raw != "" {
		return parseBucketsList(raw), nil
	}

	pub := env("PARPARCHIK_PUBLIC_BUCKET", "")
	priv := env("PARPARCHIK_PRIVATE_BUCKET", "")
	if pub == "" || priv == "" {
		return nil, fmt.Errorf("config: set PARPARCHIK_BUCKETS or both PARPARCHIK_PUBLIC_BUCKET and PARPARCHIK_PRIVATE_BUCKET")
	}
	manifest := env("PARPARCHIK_REGISTRY_MANIFEST_KEY", defaultManifestKey)
	return []Bucket{
		{Name: pub, ManifestKey: manifest, Public: true},
		{Name: priv, ManifestKey: manifest, Public: false},
	}, nil
}

// parseBucketsList parses "name:manifest_key:public,name2:manifest_key2" into
// Bucket entries, in the order given (which also defines conflict-resolution
// priority: earlier buckets outrank later ones).
func parseBucketsList(raw string) []Bucket {
	var buckets []Bucket
	for _, token := range strings.Split(raw, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		parts := strings.Split(token, ":")
		if parts[0] == "" {
			continue
		}
		b := Bucket{Name: parts[0], ManifestKey: defaultManifestKey}
		if len(parts) > 1 && parts[1] != "" {
			b.ManifestKey = parts[1]
		}
		if len(parts) > 2 && parts[2] == "public" {
			b.Public = true
		}
		buckets = append(buckets, b)
	}
	return buckets
}

// BucketByName returns the configured bucket with the given name.
func (c *Config) BucketByName(name string) (Bucket, bool) {
	for _, b := range c.Buckets {
		if b.Name == name {
			return b, true
		}
	}
	return Bucket{}, false
}

// IsPublic reports whether the named bucket is configured as public.
func (c *Config) IsPublic(name string) bool {
	b, ok := c.BucketByName(name)
	return ok && b.Public
}

// BucketType returns "public" or "private" for the named bucket.
func (c *Config) BucketType(name string) string {
	if c.IsPublic(name) {
		return "public"
	}
	return "private"
}

// BucketPriority returns the index of the named bucket in configuration
// order (lower value = higher priority). Unknown buckets sort last.
func (c *Config) BucketPriority(name string) int {
	for i, b := range c.Buckets {
		if b.Name == name {
			return i
		}
	}
	return len(c.Buckets)
}
