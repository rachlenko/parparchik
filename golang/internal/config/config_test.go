package config

import (
	"os"
	"reflect"
	"testing"
	"time"
)

// withEnv sets env vars for the duration of the test and restores whatever
// was there before, including unsetting vars that weren't previously set.
func withEnv(t *testing.T, kv map[string]string) {
	t.Helper()
	for k, v := range kv {
		prev, had := os.LookupEnv(k)
		if err := os.Setenv(k, v); err != nil {
			t.Fatalf("setenv %s: %v", k, err)
		}
		t.Cleanup(func() {
			var restoreErr error
			if had {
				restoreErr = os.Setenv(k, prev)
			} else {
				restoreErr = os.Unsetenv(k)
			}
			if restoreErr != nil {
				t.Errorf("restore env %s: %v", k, restoreErr)
			}
		})
	}
}

func clearParparchikEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"PARPARCHIK_BUCKETS", "PARPARCHIK_PUBLIC_BUCKET", "PARPARCHIK_PRIVATE_BUCKET",
		"PARPARCHIK_REGISTRY_MANIFEST_KEY", "PARPARCHIK_HOST", "PARPARCHIK_PORT",
		"PARPARCHIK_API_KEYS", "PARPARCHIK_RATE_LIMIT_PER_SECOND", "PARPARCHIK_RATE_LIMIT_BURST",
		"PARPARCHIK_SYNC_INTERVAL", "PARPARCHIK_PROXY_REPOS", "PARPARCHIK_VIRTUAL_REPOS",
		"S3_ENDPOINT", "S3_EXTERNAL_ENDPOINT", "AWS_REGION", "AWS_ACCESS_KEY_ID", "AWS_SECRET_ACCESS_KEY",
	} {
		prev, had := os.LookupEnv(k)
		if err := os.Unsetenv(k); err != nil {
			t.Fatalf("unsetenv %s: %v", k, err)
		}
		t.Cleanup(func() {
			if had {
				if err := os.Setenv(k, prev); err != nil {
					t.Errorf("restore env %s: %v", k, err)
				}
			}
		})
	}
}

func TestLoad_LegacyPublicPrivatePair(t *testing.T) {
	// Arrange
	clearParparchikEnv(t)
	withEnv(t, map[string]string{
		"PARPARCHIK_PUBLIC_BUCKET":  "pub",
		"PARPARCHIK_PRIVATE_BUCKET": "priv",
	})

	// Act
	cfg, err := Load()

	// Assert
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if len(cfg.Buckets) != 2 {
		t.Fatalf("len(Buckets) = %d, want 2", len(cfg.Buckets))
	}
	if cfg.Buckets[0].Name != "pub" || !cfg.Buckets[0].Public {
		t.Errorf("Buckets[0] = %+v, want public bucket %q", cfg.Buckets[0], "pub")
	}
	if cfg.Buckets[1].Name != "priv" || cfg.Buckets[1].Public {
		t.Errorf("Buckets[1] = %+v, want private bucket %q", cfg.Buckets[1], "priv")
	}
}

func TestLoad_LegacyPair_MissingOneVariable(t *testing.T) {
	// Arrange
	clearParparchikEnv(t)
	withEnv(t, map[string]string{"PARPARCHIK_PUBLIC_BUCKET": "pub"})

	// Act
	_, err := Load()

	// Assert
	if err == nil {
		t.Fatal("Load() error = nil, want error for missing private bucket")
	}
}

func TestLoad_BucketsList(t *testing.T) {
	// Arrange
	clearParparchikEnv(t)
	withEnv(t, map[string]string{
		"PARPARCHIK_BUCKETS": "alpha:alpha-manifest.json:public,beta::private,gamma",
	})

	// Act
	cfg, err := Load()

	// Assert
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	want := []Bucket{
		{Name: "alpha", ManifestKey: "alpha-manifest.json", Public: true, Kind: KindHosted},
		{Name: "beta", ManifestKey: defaultManifestKey, Public: false, Kind: KindHosted},
		{Name: "gamma", ManifestKey: defaultManifestKey, Public: false, Kind: KindHosted},
	}
	if len(cfg.Buckets) != len(want) {
		t.Fatalf("len(Buckets) = %d, want %d (%+v)", len(cfg.Buckets), len(want), cfg.Buckets)
	}
	for i, b := range want {
		if !reflect.DeepEqual(cfg.Buckets[i], b) {
			t.Errorf("Buckets[%d] = %+v, want %+v", i, cfg.Buckets[i], b)
		}
	}
}

func TestLoad_NoBucketsConfigured(t *testing.T) {
	// Arrange
	clearParparchikEnv(t)

	// Act
	_, err := Load()

	// Assert
	if err == nil {
		t.Fatal("Load() error = nil, want error when no buckets configured")
	}
}

func TestLoad_InvalidPort(t *testing.T) {
	// Arrange
	clearParparchikEnv(t)
	withEnv(t, map[string]string{
		"PARPARCHIK_PUBLIC_BUCKET":  "pub",
		"PARPARCHIK_PRIVATE_BUCKET": "priv",
		"PARPARCHIK_PORT":           "not-a-number",
	})

	// Act
	_, err := Load()

	// Assert
	if err == nil {
		t.Fatal("Load() error = nil, want error for invalid port")
	}
}

func TestLoad_Defaults(t *testing.T) {
	// Arrange
	clearParparchikEnv(t)
	withEnv(t, map[string]string{
		"PARPARCHIK_PUBLIC_BUCKET":  "pub",
		"PARPARCHIK_PRIVATE_BUCKET": "priv",
	})

	// Act
	cfg, err := Load()

	// Assert
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if cfg.Port != 8080 {
		t.Errorf("Port = %d, want 8080", cfg.Port)
	}
	if cfg.AWSRegion != "us-east-1" {
		t.Errorf("AWSRegion = %q, want us-east-1", cfg.AWSRegion)
	}
	if cfg.RateLimitPerSecond != 5 {
		t.Errorf("RateLimitPerSecond = %v, want 5", cfg.RateLimitPerSecond)
	}
	if cfg.RateLimitBurst != 10 {
		t.Errorf("RateLimitBurst = %d, want 10", cfg.RateLimitBurst)
	}
	if cfg.SyncInterval.String() != "5m0s" {
		t.Errorf("SyncInterval = %v, want 5m0s", cfg.SyncInterval)
	}
	if len(cfg.APIKeys) != 0 {
		t.Errorf("APIKeys = %v, want empty", cfg.APIKeys)
	}
}

func TestLoad_APIKeys(t *testing.T) {
	// Arrange
	clearParparchikEnv(t)
	withEnv(t, map[string]string{
		"PARPARCHIK_PUBLIC_BUCKET":  "pub",
		"PARPARCHIK_PRIVATE_BUCKET": "priv",
		"PARPARCHIK_API_KEYS":       "key-one, key-two,,key-three",
	})

	// Act
	cfg, err := Load()

	// Assert
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	want := []string{"key-one", "key-two", "key-three"}
	if len(cfg.APIKeys) != len(want) {
		t.Fatalf("APIKeys = %v, want %v", cfg.APIKeys, want)
	}
	for i, k := range want {
		if cfg.APIKeys[i] != k {
			t.Errorf("APIKeys[%d] = %q, want %q", i, cfg.APIKeys[i], k)
		}
	}
}

func TestConfig_BucketPriorityAndType(t *testing.T) {
	// Arrange
	cfg := &Config{Buckets: []Bucket{
		{Name: "first", Public: true},
		{Name: "second", Public: false},
	}}

	// Act / Assert
	if p := cfg.BucketPriority("first"); p != 0 {
		t.Errorf("BucketPriority(first) = %d, want 0", p)
	}
	if p := cfg.BucketPriority("second"); p != 1 {
		t.Errorf("BucketPriority(second) = %d, want 1", p)
	}
	if p := cfg.BucketPriority("unknown"); p != 2 {
		t.Errorf("BucketPriority(unknown) = %d, want 2 (len(Buckets))", p)
	}
	if got := cfg.BucketType("first"); got != "public" {
		t.Errorf("BucketType(first) = %q, want public", got)
	}
	if got := cfg.BucketType("second"); got != "private" {
		t.Errorf("BucketType(second) = %q, want private", got)
	}
	if got := cfg.BucketType("unknown"); got != "private" {
		t.Errorf("BucketType(unknown) = %q, want private (unknown buckets default to private)", got)
	}
}

func TestLoad_ProxyRepos(t *testing.T) {
	// Arrange
	clearParparchikEnv(t)
	withEnv(t, map[string]string{
		"PARPARCHIK_PUBLIC_BUCKET":  "pub",
		"PARPARCHIK_PRIVATE_BUCKET": "priv",
		"PARPARCHIK_PROXY_REPOS":    "npm-cache|https://registry.npmjs.org|public,internal-cache|https://internal.example/repo",
	})

	// Act
	cfg, err := Load()

	// Assert
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	if len(cfg.Buckets) != 4 {
		t.Fatalf("len(Buckets) = %d, want 4 (2 hosted + 2 proxy), got %+v", len(cfg.Buckets), cfg.Buckets)
	}
	want := []Bucket{
		{Name: "npm-cache", ManifestKey: defaultManifestKey, Public: true, Kind: KindProxy, UpstreamURL: "https://registry.npmjs.org"},
		{Name: "internal-cache", ManifestKey: defaultManifestKey, Public: false, Kind: KindProxy, UpstreamURL: "https://internal.example/repo"},
	}
	for i, b := range want {
		got := cfg.Buckets[2+i]
		if !reflect.DeepEqual(got, b) {
			t.Errorf("Buckets[%d] = %+v, want %+v", 2+i, got, b)
		}
	}
	// Proxy repos rank lower priority than hosted buckets by default.
	if cfg.BucketPriority("pub") >= cfg.BucketPriority("npm-cache") {
		t.Errorf("expected hosted bucket to outrank proxy repo: pub=%d npm-cache=%d",
			cfg.BucketPriority("pub"), cfg.BucketPriority("npm-cache"))
	}
}

func TestLoad_ProxyReposMalformedTokensIgnored(t *testing.T) {
	// Arrange
	clearParparchikEnv(t)
	withEnv(t, map[string]string{
		"PARPARCHIK_PUBLIC_BUCKET":  "pub",
		"PARPARCHIK_PRIVATE_BUCKET": "priv",
		"PARPARCHIK_PROXY_REPOS":    "missing-url,|https://example.com,valid|https://example.com",
	})

	// Act
	cfg, err := Load()

	// Assert
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	proxyCount := 0
	for _, b := range cfg.Buckets {
		if b.Kind == KindProxy {
			proxyCount++
			if b.Name != "valid" {
				t.Errorf("unexpected proxy repo registered from malformed token: %+v", b)
			}
		}
	}
	if proxyCount != 1 {
		t.Errorf("proxyCount = %d, want 1 (only the well-formed token)", proxyCount)
	}
}

func TestLoad_ProxyReposWithTTL(t *testing.T) {
	// Arrange
	clearParparchikEnv(t)
	withEnv(t, map[string]string{
		"PARPARCHIK_PUBLIC_BUCKET":  "pub",
		"PARPARCHIK_PRIVATE_BUCKET": "priv",
		"PARPARCHIK_PROXY_REPOS":    "npm-cache|https://registry.npmjs.org|public|1h,no-ttl-cache|https://example.com",
	})

	// Act
	cfg, err := Load()

	// Assert
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	withTTL, ok := cfg.BucketByName("npm-cache")
	if !ok {
		t.Fatal("BucketByName(npm-cache) not found")
	}
	if withTTL.CacheTTL != time.Hour {
		t.Errorf("CacheTTL = %v, want 1h", withTTL.CacheTTL)
	}
	noTTL, ok := cfg.BucketByName("no-ttl-cache")
	if !ok {
		t.Fatal("BucketByName(no-ttl-cache) not found")
	}
	if noTTL.CacheTTL != 0 {
		t.Errorf("CacheTTL = %v, want 0 (cache forever, TTL omitted)", noTTL.CacheTTL)
	}
}

func TestLoad_ProxyReposInvalidTTL(t *testing.T) {
	// Arrange
	clearParparchikEnv(t)
	withEnv(t, map[string]string{
		"PARPARCHIK_PUBLIC_BUCKET":  "pub",
		"PARPARCHIK_PRIVATE_BUCKET": "priv",
		"PARPARCHIK_PROXY_REPOS":    "npm-cache|https://registry.npmjs.org|public|not-a-duration",
	})

	// Act
	_, err := Load()

	// Assert
	if err == nil {
		t.Fatal("Load() error = nil, want an error for an invalid ttl duration")
	}
}

func TestLoad_VirtualRepos(t *testing.T) {
	// Arrange
	clearParparchikEnv(t)
	withEnv(t, map[string]string{
		"PARPARCHIK_PUBLIC_BUCKET":  "pub",
		"PARPARCHIK_PRIVATE_BUCKET": "priv",
		"PARPARCHIK_VIRTUAL_REPOS":  "all|pub+priv, empty-members|  ",
	})

	// Act
	cfg, err := Load()

	// Assert
	if err != nil {
		t.Fatalf("Load() error = %v, want nil", err)
	}
	virtual, ok := cfg.BucketByName("all")
	if !ok {
		t.Fatal("BucketByName(all) not found")
	}
	if virtual.Kind != KindVirtual {
		t.Errorf("Kind = %q, want %q", virtual.Kind, KindVirtual)
	}
	wantMembers := []string{"pub", "priv"}
	if !reflect.DeepEqual(virtual.Members, wantMembers) {
		t.Errorf("Members = %v, want %v", virtual.Members, wantMembers)
	}
	if virtual.HasStorage() {
		t.Error("HasStorage() = true for a virtual bucket, want false")
	}
	if _, ok := cfg.BucketByName("empty-members"); ok {
		t.Error("BucketByName(empty-members) found a bucket with zero valid members, want it skipped")
	}
}

func TestBucket_HasStorage(t *testing.T) {
	cases := []struct {
		kind RepoKind
		want bool
	}{
		{KindHosted, true},
		{KindProxy, true},
		{KindVirtual, false},
	}
	for _, tc := range cases {
		b := Bucket{Kind: tc.kind}
		if got := b.HasStorage(); got != tc.want {
			t.Errorf("Bucket{Kind: %q}.HasStorage() = %v, want %v", tc.kind, got, tc.want)
		}
	}
}

func TestLoad_VirtualReposRejectsDanglingMember(t *testing.T) {
	// Arrange
	clearParparchikEnv(t)
	withEnv(t, map[string]string{
		"PARPARCHIK_PUBLIC_BUCKET":  "pub",
		"PARPARCHIK_PRIVATE_BUCKET": "priv",
		"PARPARCHIK_VIRTUAL_REPOS":  "all|pub+does-not-exist",
	})

	// Act
	_, err := Load()

	// Assert
	if err == nil {
		t.Fatal("Load() error = nil, want an error for a virtual repo member that isn't a configured bucket")
	}
}

func TestLoad_VirtualReposRejectsNestedVirtualMember(t *testing.T) {
	// Arrange
	clearParparchikEnv(t)
	withEnv(t, map[string]string{
		"PARPARCHIK_PUBLIC_BUCKET":  "pub",
		"PARPARCHIK_PRIVATE_BUCKET": "priv",
		"PARPARCHIK_VIRTUAL_REPOS":  "inner|pub,outer|inner+priv",
	})

	// Act
	_, err := Load()

	// Assert
	if err == nil {
		t.Fatal("Load() error = nil, want an error for a virtual repo nesting another virtual repo as a member")
	}
}

func TestValidateVirtualRepoMembers(t *testing.T) {
	hosted := Bucket{Name: "pub", Kind: KindHosted}
	proxy := Bucket{Name: "npm-cache", Kind: KindProxy}
	virtual := Bucket{Name: "inner", Kind: KindVirtual, Members: []string{"pub"}}

	cases := []struct {
		name         string
		allBuckets   []Bucket
		virtualRepos []Bucket
		wantErr      bool
	}{
		{
			name:         "valid hosted and proxy members",
			allBuckets:   []Bucket{hosted, proxy, {Name: "outer", Kind: KindVirtual, Members: []string{"pub", "npm-cache"}}},
			virtualRepos: []Bucket{{Name: "outer", Kind: KindVirtual, Members: []string{"pub", "npm-cache"}}},
			wantErr:      false,
		},
		{
			name:         "dangling member",
			allBuckets:   []Bucket{hosted, {Name: "outer", Kind: KindVirtual, Members: []string{"missing"}}},
			virtualRepos: []Bucket{{Name: "outer", Kind: KindVirtual, Members: []string{"missing"}}},
			wantErr:      true,
		},
		{
			name:         "nested virtual member",
			allBuckets:   []Bucket{hosted, virtual, {Name: "outer", Kind: KindVirtual, Members: []string{"inner"}}},
			virtualRepos: []Bucket{virtual, {Name: "outer", Kind: KindVirtual, Members: []string{"inner"}}},
			wantErr:      true,
		},
		{
			name:         "no virtual repos configured",
			allBuckets:   []Bucket{hosted},
			virtualRepos: nil,
			wantErr:      false,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			err := validateVirtualRepoMembers(tc.allBuckets, tc.virtualRepos)

			// Assert
			if (err != nil) != tc.wantErr {
				t.Errorf("validateVirtualRepoMembers() error = %v, wantErr %v", err, tc.wantErr)
			}
		})
	}
}
