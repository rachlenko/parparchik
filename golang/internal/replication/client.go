package replication

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/rachlenko/parparchik/golang/internal/catalog"
)

const (
	defaultTimeout      = 30 * time.Second
	defaultMaxBodyBytes = 1 << 30 // 1 GiB, matching proxycache.HTTPFetcher's bound

	apiKeyHeader = "X-API-Key"
)

// Client is what Puller needs from a remote parparchik instance.
type Client interface {
	// FetchManifest returns every entry the remote instance's catalog
	// currently reports (GET /list).
	FetchManifest(ctx context.Context) ([]catalog.Entry, error)

	// FetchObject downloads one entry's bytes the same way a real
	// downstream client does: GET the remote instance's own route for it
	// (which 302s to the object's actual storage location — S3, MinIO, or
	// a public CDN URL) and follow that redirect.
	FetchObject(ctx context.Context, entry catalog.Entry) ([]byte, error)
}

// HTTPClient implements Client against a real remote parparchik instance
// over its existing HTTP API — no new wire protocol or server-side
// endpoint. APIKey, when set, authenticates only the initial request to
// the remote parparchik instance; it is never forwarded to the redirect
// target FetchObject follows (see fetchRedirectTarget), since that target
// is a different host (object storage) this key has no business being
// sent to.
type HTTPClient struct {
	// BaseURL is the remote instance's address, e.g. "https://peer.example:8080".
	BaseURL string
	APIKey  string

	// Client performs the initial (non-redirect-following) request to
	// BaseURL. Nil uses a default client with defaultTimeout.
	Client *http.Client

	// MaxBodyBytes bounds how much of a response this reads into memory.
	// Zero means defaultMaxBodyBytes — there is no way to request unbounded.
	MaxBodyBytes int64
}

func (c *HTTPClient) httpClient() *http.Client {
	if c.Client != nil {
		return c.Client
	}
	return &http.Client{Timeout: defaultTimeout}
}

func (c *HTTPClient) maxBodyBytes() int64 {
	if c.MaxBodyBytes > 0 {
		return c.MaxBodyBytes
	}
	return defaultMaxBodyBytes
}

type listResponse struct {
	Files []catalog.Entry `json:"files"`
}

func (c *HTTPClient) FetchManifest(ctx context.Context) ([]catalog.Entry, error) {
	manifestURL := strings.TrimRight(c.BaseURL, "/") + "/list"

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, manifestURL, nil)
	if err != nil {
		return nil, fmt.Errorf("replication: build manifest request for %q: %w", manifestURL, err)
	}
	if c.APIKey != "" {
		req.Header.Set(apiKeyHeader, c.APIKey)
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("replication: fetch manifest from %q: %w", manifestURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("replication: fetch manifest from %q: unexpected status %d", manifestURL, resp.StatusCode)
	}

	body, err := readLimited(resp.Body, c.maxBodyBytes())
	if err != nil {
		return nil, fmt.Errorf("replication: read manifest from %q: %w", manifestURL, err)
	}

	var decoded listResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("replication: decode manifest from %q: %w", manifestURL, err)
	}
	return decoded.Files, nil
}

func (c *HTTPClient) FetchObject(ctx context.Context, entry catalog.Entry) ([]byte, error) {
	initialURL := strings.TrimRight(c.BaseURL, "/") + entry.Route

	client := c.httpClient()
	noRedirectClient := &http.Client{
		Timeout:   client.Timeout,
		Transport: client.Transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, initialURL, nil)
	if err != nil {
		return nil, fmt.Errorf("replication: build object request for %q: %w", initialURL, err)
	}
	if c.APIKey != "" {
		req.Header.Set(apiKeyHeader, c.APIKey)
	}

	resp, err := noRedirectClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("replication: fetch object %q: %w", initialURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	switch resp.StatusCode {
	case http.StatusFound, http.StatusMovedPermanently, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect:
		location := resp.Header.Get("Location")
		if location == "" {
			return nil, fmt.Errorf("replication: fetch object %q: redirect status %d with no Location header", initialURL, resp.StatusCode)
		}
		return c.fetchRedirectTarget(ctx, location)
	case http.StatusOK:
		return readLimited(resp.Body, c.maxBodyBytes())
	default:
		return nil, fmt.Errorf("replication: fetch object %q: unexpected status %d", initialURL, resp.StatusCode)
	}
}

// fetchRedirectTarget performs the plain, unauthenticated request a real
// browser or client would make after following GET /{bucket}/{key}'s
// redirect — deliberately built fresh, with no headers carried over from
// the request to the parparchik instance, so APIKey is never sent to
// whatever host the redirect target turns out to be.
func (c *HTTPClient) fetchRedirectTarget(ctx context.Context, location string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, location, nil)
	if err != nil {
		return nil, fmt.Errorf("replication: build redirect-target request for %q: %w", location, err)
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, fmt.Errorf("replication: fetch redirect target %q: %w", location, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("replication: fetch redirect target %q: unexpected status %d", location, resp.StatusCode)
	}
	return readLimited(resp.Body, c.maxBodyBytes())
}

func readLimited(r io.Reader, maxBytes int64) ([]byte, error) {
	limited := io.LimitReader(r, maxBytes+1)
	body, err := io.ReadAll(limited)
	if err != nil {
		return nil, err
	}
	if int64(len(body)) > maxBytes {
		return nil, errors.New("response exceeds byte limit")
	}
	return body, nil
}
