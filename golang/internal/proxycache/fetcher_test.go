package proxycache

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestHTTPFetcher_Success(t *testing.T) {
	// Arrange
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/lodash/lodash-4.17.21.tgz" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write([]byte("tarball-bytes"))
	}))
	defer srv.Close()

	f := NewHTTPFetcher()

	// Act
	body, contentType, ok, err := f.Fetch(context.Background(), srv.URL, "lodash/lodash-4.17.21.tgz")

	// Assert
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if !ok {
		t.Fatal("Fetch() ok = false, want true")
	}
	if string(body) != "tarball-bytes" {
		t.Errorf("Fetch() body = %q, want %q", body, "tarball-bytes")
	}
	if contentType != "application/gzip" {
		t.Errorf("Fetch() contentType = %q, want application/gzip", contentType)
	}
}

func TestHTTPFetcher_NotFoundIsNotAnError(t *testing.T) {
	// Arrange
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	defer srv.Close()

	f := NewHTTPFetcher()

	// Act
	_, _, ok, err := f.Fetch(context.Background(), srv.URL, "missing.txt")

	// Assert
	if err != nil {
		t.Fatalf("Fetch() error = %v, want nil for a 404", err)
	}
	if ok {
		t.Error("Fetch() ok = true, want false for a 404")
	}
}

func TestHTTPFetcher_UnexpectedStatusIsAnError(t *testing.T) {
	// Arrange
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	f := NewHTTPFetcher()

	// Act
	_, _, ok, err := f.Fetch(context.Background(), srv.URL, "file.txt")

	// Assert
	if err == nil {
		t.Fatal("Fetch() error = nil, want an error for a 500")
	}
	if ok {
		t.Error("Fetch() ok = true, want false on error")
	}
}

func TestHTTPFetcher_JoinsURLAndKeyCleanly(t *testing.T) {
	// Arrange
	var gotPath string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		_, _ = w.Write([]byte("ok"))
	}))
	defer srv.Close()

	f := NewHTTPFetcher()

	// Act: trailing slash on the base URL, leading slash on the key —
	// both should be normalized to a single separator.
	_, _, _, err := f.Fetch(context.Background(), srv.URL+"/", "/pkg/file.txt")

	// Assert
	if err != nil {
		t.Fatalf("Fetch() error = %v", err)
	}
	if gotPath != "/pkg/file.txt" {
		t.Errorf("request path = %q, want /pkg/file.txt", gotPath)
	}
}
