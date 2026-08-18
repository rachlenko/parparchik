// Package proxycache implements the upstream-fetch side of proxy
// repositories (config.KindProxy): given a key that missed the local
// cache, fetch it from a configured upstream URL. Caching the result and
// registering it in the catalog is the resolver's job, not this package's —
// this package only knows how to talk to an upstream.
package proxycache

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultTimeout = 30 * time.Second

	// defaultMaxBodyBytes bounds how much of an upstream response
	// HTTPFetcher buffers into memory. Proxy repositories fetch arbitrary
	// upstream artifacts (jars, Docker layers, tarballs), so an unbounded
	// io.ReadAll here would let a very large or slow-to-produce response —
	// from a misbehaving, compromised, or simply large upstream — exhaust
	// process memory. 1 GiB comfortably covers real-world package
	// artifacts; override via HTTPFetcher.MaxBodyBytes for a tighter or
	// looser bound.
	defaultMaxBodyBytes = 1 << 30 // 1 GiB
)

// Fetcher retrieves an object from an upstream repository. ok is false
// (with a nil error) when the upstream reports the key doesn't exist
// there — that's a normal "not found", not a failure. A non-nil error
// means the fetch itself failed (network error, unexpected status code).
type Fetcher interface {
	Fetch(ctx context.Context, upstreamURL, key string) (body []byte, contentType string, ok bool, err error)
}

// HTTPFetcher fetches upstreamURL+"/"+key over plain HTTP(S) GET. This
// covers any upstream that serves artifacts at a predictable path (a raw
// file server, another parparchik instance, S3 static website hosting,
// etc.) — protocol-aware upstreams (the real npm/PyPI/Docker registry
// APIs) need a format-specific fetcher, not this generic one.
type HTTPFetcher struct {
	Client *http.Client
	// MaxBodyBytes bounds the response size read into memory. Zero means
	// "use defaultMaxBodyBytes" — there is no way to request "unbounded".
	MaxBodyBytes int64
}

// NewHTTPFetcher returns an HTTPFetcher with a bounded request timeout and
// response size.
func NewHTTPFetcher() *HTTPFetcher {
	return &HTTPFetcher{Client: &http.Client{Timeout: defaultTimeout}, MaxBodyBytes: defaultMaxBodyBytes}
}

func (f *HTTPFetcher) Fetch(ctx context.Context, upstreamURL, key string) ([]byte, string, bool, error) {
	url := strings.TrimRight(upstreamURL, "/") + "/" + strings.TrimLeft(key, "/")

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, "", false, fmt.Errorf("proxycache: build request for %q: %w", url, err)
	}

	resp, err := f.Client.Do(req)
	if err != nil {
		return nil, "", false, fmt.Errorf("proxycache: fetch %q: %w", url, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, "", false, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", false, fmt.Errorf("proxycache: fetch %q: unexpected status %d", url, resp.StatusCode)
	}

	maxBytes := f.MaxBodyBytes
	if maxBytes <= 0 {
		maxBytes = defaultMaxBodyBytes
	}
	limited := io.LimitReader(resp.Body, maxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, "", false, fmt.Errorf("proxycache: read body from %q: %w", url, err)
	}
	if int64(len(body)) > maxBytes {
		return nil, "", false, fmt.Errorf("proxycache: response from %q exceeds %d byte limit", url, maxBytes)
	}

	return body, resp.Header.Get("Content-Type"), true, nil
}
