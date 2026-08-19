package replication

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/rachlenko/parparchik/golang/internal/catalog"
)

func TestHTTPClient_FetchManifest(t *testing.T) {
	// Arrange
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/list" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"count":1,"files":[{"key":"a.txt","bucket":"bucket-a","route":"/bucket-a/a.txt","size":10,"last_modified":"2026-08-01T00:00:00Z"}]}`))
	}))
	defer srv.Close()

	client := &HTTPClient{BaseURL: srv.URL}

	// Act
	entries, err := client.FetchManifest(context.Background())

	// Assert
	if err != nil {
		t.Fatalf("FetchManifest() error = %v", err)
	}
	if len(entries) != 1 || entries[0].Key != "a.txt" || entries[0].Bucket != "bucket-a" {
		t.Errorf("FetchManifest() = %+v, want a single a.txt/bucket-a entry", entries)
	}
}

func TestHTTPClient_FetchManifest_SendsAPIKey(t *testing.T) {
	// Arrange
	var gotKey string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKey = r.Header.Get("X-API-Key")
		_, _ = w.Write([]byte(`{"count":0,"files":[]}`))
	}))
	defer srv.Close()

	client := &HTTPClient{BaseURL: srv.URL, APIKey: "secret-key"}

	// Act
	if _, err := client.FetchManifest(context.Background()); err != nil {
		t.Fatalf("FetchManifest() error = %v", err)
	}

	// Assert
	if gotKey != "secret-key" {
		t.Errorf("X-API-Key sent to the remote instance = %q, want secret-key", gotKey)
	}
}

func TestHTTPClient_FetchManifest_UsesExplicitClient(t *testing.T) {
	// Arrange
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"count":0,"files":[]}`))
	}))
	defer srv.Close()

	client := &HTTPClient{BaseURL: srv.URL, Client: &http.Client{}}

	// Act
	_, err := client.FetchManifest(context.Background())

	// Assert
	if err != nil {
		t.Fatalf("FetchManifest() error = %v", err)
	}
}

func TestHTTPClient_FetchManifest_MalformedJSON(t *testing.T) {
	// Arrange
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`not json`))
	}))
	defer srv.Close()

	client := &HTTPClient{BaseURL: srv.URL}

	// Act
	_, err := client.FetchManifest(context.Background())

	// Assert
	if err == nil {
		t.Fatal("FetchManifest() error = nil, want an error for malformed JSON")
	}
}

func TestHTTPClient_FetchManifest_ExceedsMaxBodyBytes(t *testing.T) {
	// Arrange
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"count":0,"files":[]}`))
	}))
	defer srv.Close()

	client := &HTTPClient{BaseURL: srv.URL, MaxBodyBytes: 4}

	// Act
	_, err := client.FetchManifest(context.Background())

	// Assert
	if err == nil {
		t.Fatal("FetchManifest() error = nil, want an error for a response exceeding MaxBodyBytes")
	}
}

func TestHTTPClient_FetchManifest_TransportFailureIsAnError(t *testing.T) {
	// Arrange: a pre-canceled context makes Client.Do fail immediately and
	// deterministically, exercising the Do() error branch without relying
	// on real network flakiness.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"count":0,"files":[]}`))
	}))
	defer srv.Close()

	client := &HTTPClient{BaseURL: srv.URL}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Act
	_, err := client.FetchManifest(ctx)

	// Assert
	if err == nil {
		t.Fatal("FetchManifest() error = nil, want an error for a canceled context")
	}
}

func TestHTTPClient_FetchManifest_ErrorStatus(t *testing.T) {
	// Arrange
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer srv.Close()

	client := &HTTPClient{BaseURL: srv.URL}

	// Act
	_, err := client.FetchManifest(context.Background())

	// Assert
	if err == nil {
		t.Fatal("FetchManifest() error = nil, want an error for a 401")
	}
}

func TestHTTPClient_FetchObject_FollowsRedirectToStorage(t *testing.T) {
	// Arrange: a second server standing in for the object storage host the
	// parparchik instance's GET /{bucket}/{key} redirects to.
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("object-bytes"))
	}))
	defer storage.Close()

	parparchik := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/bucket-a/a.txt" {
			http.NotFound(w, r)
			return
		}
		http.Redirect(w, r, storage.URL+"/presigned/a.txt", http.StatusFound)
	}))
	defer parparchik.Close()

	client := &HTTPClient{BaseURL: parparchik.URL}
	entry := catalog.Entry{Key: "a.txt", Bucket: "bucket-a", Route: "/bucket-a/a.txt"}

	// Act
	body, err := client.FetchObject(context.Background(), entry)

	// Assert
	if err != nil {
		t.Fatalf("FetchObject() error = %v", err)
	}
	if string(body) != "object-bytes" {
		t.Errorf("FetchObject() = %q, want object-bytes", body)
	}
}

func TestHTTPClient_FetchObject_DoesNotForwardAPIKeyToRedirectTarget(t *testing.T) {
	// Arrange: regression test for a real credential-leak risk — the
	// initial request to the parparchik instance carries the API key, but
	// the redirect target (a different host: S3/MinIO/a CDN) must never
	// see it.
	var gotKeyAtStorage string
	sawHeader := false
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotKeyAtStorage = r.Header.Get("X-API-Key")
		sawHeader = r.Header.Get("X-API-Key") != ""
		_, _ = w.Write([]byte("object-bytes"))
	}))
	defer storage.Close()

	parparchik := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-Key") != "secret-key" {
			t.Errorf("X-API-Key at the parparchik instance = %q, want secret-key", r.Header.Get("X-API-Key"))
		}
		http.Redirect(w, r, storage.URL+"/presigned/a.txt", http.StatusFound)
	}))
	defer parparchik.Close()

	client := &HTTPClient{BaseURL: parparchik.URL, APIKey: "secret-key"}
	entry := catalog.Entry{Key: "a.txt", Bucket: "bucket-a", Route: "/bucket-a/a.txt"}

	// Act
	if _, err := client.FetchObject(context.Background(), entry); err != nil {
		t.Fatalf("FetchObject() error = %v", err)
	}

	// Assert
	if sawHeader {
		t.Errorf("X-API-Key was forwarded to the redirect target: %q, want it never sent there", gotKeyAtStorage)
	}
}

func TestHTTPClient_FetchObject_RedirectWithNoLocationIsAnError(t *testing.T) {
	// Arrange
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusFound)
	}))
	defer srv.Close()

	client := &HTTPClient{BaseURL: srv.URL}
	entry := catalog.Entry{Key: "a.txt", Bucket: "bucket-a", Route: "/bucket-a/a.txt"}

	// Act
	_, err := client.FetchObject(context.Background(), entry)

	// Assert
	if err == nil {
		t.Fatal("FetchObject() error = nil, want an error for a redirect with no Location header")
	}
}

func TestHTTPClient_FetchObject_DirectOKResponse(t *testing.T) {
	// Arrange: not how the real handleDownload behaves today (it always
	// redirects), but Client's contract doesn't require a redirect — a
	// direct 200 must also work.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("direct-bytes"))
	}))
	defer srv.Close()

	client := &HTTPClient{BaseURL: srv.URL}
	entry := catalog.Entry{Key: "a.txt", Bucket: "bucket-a", Route: "/bucket-a/a.txt"}

	// Act
	body, err := client.FetchObject(context.Background(), entry)

	// Assert
	if err != nil {
		t.Fatalf("FetchObject() error = %v", err)
	}
	if string(body) != "direct-bytes" {
		t.Errorf("FetchObject() = %q, want direct-bytes", body)
	}
}

func TestHTTPClient_FetchObject_TransportFailureIsAnError(t *testing.T) {
	// Arrange
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("bytes"))
	}))
	defer srv.Close()

	client := &HTTPClient{BaseURL: srv.URL}
	entry := catalog.Entry{Key: "a.txt", Bucket: "bucket-a", Route: "/bucket-a/a.txt"}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	// Act
	_, err := client.FetchObject(ctx, entry)

	// Assert
	if err == nil {
		t.Fatal("FetchObject() error = nil, want an error for a canceled context")
	}
}

func TestHTTPClient_FetchObject_ErrorStatus(t *testing.T) {
	// Arrange
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	client := &HTTPClient{BaseURL: srv.URL}
	entry := catalog.Entry{Key: "a.txt", Bucket: "bucket-a", Route: "/bucket-a/a.txt"}

	// Act
	_, err := client.FetchObject(context.Background(), entry)

	// Assert
	if err == nil {
		t.Fatal("FetchObject() error = nil, want an error for a 500")
	}
}

func TestHTTPClient_FetchObject_RedirectTargetExceedsMaxBodyBytes(t *testing.T) {
	// Arrange
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("way too much content for the limit"))
	}))
	defer storage.Close()

	parparchik := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, storage.URL+"/x", http.StatusFound)
	}))
	defer parparchik.Close()

	client := &HTTPClient{BaseURL: parparchik.URL, MaxBodyBytes: 4}
	entry := catalog.Entry{Key: "a.txt", Bucket: "bucket-a", Route: "/bucket-a/a.txt"}

	// Act
	_, err := client.FetchObject(context.Background(), entry)

	// Assert
	if err == nil {
		t.Fatal("FetchObject() error = nil, want an error for a redirect-target response exceeding MaxBodyBytes")
	}
}

func TestHTTPClient_FetchObject_InvalidBaseURLIsAnError(t *testing.T) {
	// Arrange: a control character in the URL makes http.NewRequestWithContext
	// itself fail, exercising FetchObject's request-build error path.
	client := &HTTPClient{BaseURL: "http://\x7f"}
	entry := catalog.Entry{Key: "a.txt", Bucket: "bucket-a", Route: "/bucket-a/a.txt"}

	// Act
	_, err := client.FetchObject(context.Background(), entry)

	// Assert
	if err == nil {
		t.Fatal("FetchObject() error = nil, want an error for an invalid BaseURL")
	}
}

func TestHTTPClient_FetchObject_RedirectTargetTransportFailureIsAnError(t *testing.T) {
	// Arrange: the redirect Location is syntactically valid but points at
	// a port nothing listens on, so the connection is refused
	// deterministically and immediately — exercising fetchRedirectTarget's
	// own Do() error branch specifically (distinct from a request-build
	// error or the initial request's own transport failure).
	parparchik := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, "http://127.0.0.1:1/unreachable", http.StatusFound)
	}))
	defer parparchik.Close()

	client := &HTTPClient{BaseURL: parparchik.URL}
	entry := catalog.Entry{Key: "a.txt", Bucket: "bucket-a", Route: "/bucket-a/a.txt"}

	// Act
	_, err := client.FetchObject(context.Background(), entry)

	// Assert
	if err == nil {
		t.Fatal("FetchObject() error = nil, want an error when the redirect target's connection is refused")
	}
}

func TestHTTPClient_FetchObject_RedirectTargetErrorStatus(t *testing.T) {
	// Arrange
	storage := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	}))
	defer storage.Close()

	parparchik := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, storage.URL+"/x", http.StatusFound)
	}))
	defer parparchik.Close()

	client := &HTTPClient{BaseURL: parparchik.URL}
	entry := catalog.Entry{Key: "a.txt", Bucket: "bucket-a", Route: "/bucket-a/a.txt"}

	// Act
	_, err := client.FetchObject(context.Background(), entry)

	// Assert
	if err == nil {
		t.Fatal("FetchObject() error = nil, want an error when the redirect target itself errors")
	}
}
