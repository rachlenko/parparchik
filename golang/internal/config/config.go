// Package config loads parparchik configuration from environment variables.
package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"
)

// RepoKind distinguishes how a Bucket's contents are populated.
type RepoKind string

const (
	// KindHosted is a bucket parparchik owns: content arrives by external
	// means (e.g. `mc cp`) and parparchik only indexes and routes it. This
	// is the only kind that existed before proxy repository support.
	KindHosted RepoKind = "hosted"

	// KindProxy is a fetch-through cache in front of UpstreamURL: a miss in
	// the cache bucket (or an entry older than CacheTTL) triggers an
	// upstream fetch, and the result is cached for subsequent requests. See
	// resolver.resolveProxyRoute.
	KindProxy RepoKind = "proxy"

	// KindVirtual aggregates other buckets (hosted or proxy) under one
	// route namespace, resolved in Members order. A virtual bucket has no
	// storage of its own — every place that lists/heads/persists against
	// Config.Buckets' underlying S3 buckets must skip KindVirtual entries.
	// See resolver.resolveVirtualRoute and Bucket.HasStorage.
	KindVirtual RepoKind = "virtual"
)

// Bucket describes one configured repository: its S3 name, the key its
// file-registry manifest is persisted under, whether it serves public
// (unsigned) or private (presigned) download URLs, and — for proxy
// repositories — the upstream it fetches through and how long a cached
// entry stays valid before being re-fetched.
type Bucket struct {
	Name        string
	ManifestKey string
	Public      bool
	Kind        RepoKind
	UpstreamURL string // only set when Kind == KindProxy

	// CacheTTL bounds how long a proxy-cached object is served before
	// resolver.resolveProxyRoute re-fetches it from UpstreamURL. Zero means
	// "cache forever" (the original proxy-repository behavior — no
	// revalidation until the entry is explicitly relocated/removed). Only
	// meaningful when Kind == KindProxy.
	CacheTTL time.Duration

	// Members lists the buckets (by name, in resolution-priority order)
	// this virtual repository aggregates. Only meaningful when Kind ==
	// KindVirtual.
	Members []string
}

// HasStorage reports whether this bucket has a real, backing S3 bucket —
// true for hosted and proxy repositories, false for virtual ones. Any code
// that lists, heads, or writes objects against Config.Buckets must skip
// entries where this is false.
func (b Bucket) HasStorage() bool {
	return b.Kind != KindVirtual
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

// Load reads configuration from the environment. Hosted buckets can be
// configured either as a single comma-separated PARPARCHIK_BUCKETS list of
// "name:manifest_key:public" tokens, or as the legacy pair of
// PARPARCHIK_PUBLIC_BUCKET / PARPARCHIK_PRIVATE_BUCKET variables. Proxy
// repositories are configured separately via PARPARCHIK_PROXY_REPOS (see
// parseProxyRepos) and appended after the hosted buckets. Virtual
// repositories are configured via PARPARCHIK_VIRTUAL_REPOS (see
// parseVirtualRepos) and appended last.
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
	proxyRepos, err := parseProxyRepos(env("PARPARCHIK_PROXY_REPOS", ""))
	if err != nil {
		return nil, err
	}
	buckets = append(buckets, proxyRepos...)
	virtualRepos := parseVirtualRepos(env("PARPARCHIK_VIRTUAL_REPOS", ""))
	buckets = append(buckets, virtualRepos...)
	cfg.Buckets = buckets

	if err := validateVirtualRepoMembers(buckets, virtualRepos); err != nil {
		return nil, err
	}

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
		{Name: pub, ManifestKey: manifest, Public: true, Kind: KindHosted},
		{Name: priv, ManifestKey: manifest, Public: false, Kind: KindHosted},
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
		b := Bucket{Name: parts[0], ManifestKey: defaultManifestKey, Kind: KindHosted}
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

// parseProxyRepos parses PARPARCHIK_PROXY_REPOS: a comma-separated list of
// "name|upstream_url|public|ttl" tokens (pipe-separated, since upstream
// URLs already contain colons from their scheme). "public" and "ttl" are
// both optional; "ttl" (a Go duration string, e.g. "1h", "30m") defaults to
// 0 — cache forever — when omitted. Each proxy repo gets its own S3 cache
// bucket named after the repo; ManifestKey defaults the same as hosted
// buckets. Proxy repos are appended after hosted buckets, so they rank
// lower priority by default in any code that walks Config.Buckets in order
// (e.g. Config.BucketPriority). Returns an error for a malformed ttl token
// rather than silently ignoring it — an operator who typed a duration
// wrong deserves a startup failure, not a silently-forever cache.
func parseProxyRepos(raw string) ([]Bucket, error) {
	var buckets []Bucket
	for _, token := range strings.Split(raw, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		parts := strings.Split(token, "|")
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			continue
		}
		b := Bucket{
			Name:        parts[0],
			ManifestKey: defaultManifestKey,
			Kind:        KindProxy,
			UpstreamURL: parts[1],
		}
		if len(parts) > 2 && parts[2] == "public" {
			b.Public = true
		}
		if len(parts) > 3 && parts[3] != "" {
			ttl, err := time.ParseDuration(parts[3])
			if err != nil {
				return nil, fmt.Errorf("config: invalid PARPARCHIK_PROXY_REPOS ttl for %q: %w", parts[0], err)
			}
			b.CacheTTL = ttl
		}
		buckets = append(buckets, b)
	}
	return buckets, nil
}

// parseVirtualRepos parses PARPARCHIK_VIRTUAL_REPOS: a comma-separated list
// of "name|member1+member2+..." tokens ("+"-joined members, since "|"
// already separates the outer fields and "," separates repos). Members are
// resolved in the given order — see resolver.resolveVirtualRoute. A token
// naming zero valid members is skipped. Member names are not validated
// against other buckets here — Load calls validateVirtualRepoMembers
// afterward, once every hosted/proxy/virtual bucket has been parsed, to
// fail loudly on a dangling or nested-virtual member rather than let it
// degrade to a silent, unexplained 404 at request time.
func parseVirtualRepos(raw string) []Bucket {
	var buckets []Bucket
	for _, token := range strings.Split(raw, ",") {
		token = strings.TrimSpace(token)
		if token == "" {
			continue
		}
		parts := strings.Split(token, "|")
		if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
			continue
		}
		var members []string
		for _, m := range strings.Split(parts[1], "+") {
			if m = strings.TrimSpace(m); m != "" {
				members = append(members, m)
			}
		}
		if len(members) == 0 {
			continue
		}
		buckets = append(buckets, Bucket{Name: parts[0], Kind: KindVirtual, Members: members})
	}
	return buckets
}

// validateVirtualRepoMembers rejects a dangling member reference (a name
// that doesn't match any bucket in allBuckets) or a member that is itself
// a virtual repository (nesting is unsupported — see
// resolver.resolveVirtualRoute, which skips rather than recurses into one
// at request time; this validation is what keeps that runtime skip from
// ever actually firing against a config Load() accepted). Consistent with
// how an invalid PARPARCHIK_PROXY_REPOS ttl token fails Load() outright
// rather than silently degrading.
func validateVirtualRepoMembers(allBuckets, virtualRepos []Bucket) error {
	byName := make(map[string]Bucket, len(allBuckets))
	for _, b := range allBuckets {
		byName[b.Name] = b
	}

	for _, virtual := range virtualRepos {
		for _, memberName := range virtual.Members {
			member, ok := byName[memberName]
			if !ok {
				return fmt.Errorf("config: virtual repo %q: member %q is not a configured bucket", virtual.Name, memberName)
			}
			if member.Kind == KindVirtual {
				return fmt.Errorf("config: virtual repo %q: member %q is itself a virtual repo, nesting is not supported", virtual.Name, memberName)
			}
		}
	}
	return nil
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
