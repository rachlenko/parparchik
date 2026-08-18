package httpapi

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/rachlenko/parparchik/golang/internal/catalog"
	"github.com/rachlenko/parparchik/golang/internal/config"
	"github.com/rachlenko/parparchik/golang/internal/metricsapi"
	"github.com/rachlenko/parparchik/golang/internal/objectstore"
	"github.com/rachlenko/parparchik/golang/internal/resolver"
)

// fakeStore is a minimal in-memory objectstore.Store for handler-level
// tests, so they don't need a real S3/MinIO endpoint.
type fakeStore struct {
	objects map[string]map[string]objectstore.Object
}

func newFakeStore() *fakeStore {
	return &fakeStore{objects: make(map[string]map[string]objectstore.Object)}
}

func (s *fakeStore) put(bucket, key string, size int64) {
	if s.objects[bucket] == nil {
		s.objects[bucket] = make(map[string]objectstore.Object)
	}
	s.objects[bucket][key] = objectstore.Object{Key: key, Size: size, LastModified: "t1"}
}

func (s *fakeStore) ListObjects(context.Context, string) ([]objectstore.Object, error) {
	return nil, nil
}

func (s *fakeStore) HeadObject(_ context.Context, bucket, key string) (*objectstore.Object, error) {
	obj, ok := s.objects[bucket][key]
	if !ok {
		return nil, nil
	}
	return &obj, nil
}

func (s *fakeStore) GetObject(context.Context, string, string) ([]byte, error) { return nil, nil }

func (s *fakeStore) PutObject(context.Context, string, string, []byte, string) error { return nil }

func (s *fakeStore) PublicURL(bucket, key string) string {
	return "http://public.example/" + bucket + "/" + key
}

func (s *fakeStore) PresignedURL(context.Context, string, string, time.Duration) (string, error) {
	return "http://presigned.example/signed", nil
}

func newTestAPI(t *testing.T, ready bool, opts ...Option) (*API, *catalog.Catalog, *fakeStore) {
	t.Helper()
	cfg := &config.Config{Buckets: []config.Bucket{
		{Name: "pub", ManifestKey: "m.json", Public: true},
		{Name: "priv", ManifestKey: "m.json", Public: false},
	}}
	store := newFakeStore()
	cat := catalog.New(cfg.BucketPriority, cfg.BucketType)
	res := resolver.New(cfg, cat, store)
	metrics := metricsapi.New()

	api := New(cfg, cat, res, store, metrics, opts...)
	t.Cleanup(api.Close)
	api.SetReady(ready)
	return api, cat, store
}

func decodeJSON(t *testing.T, rec *httptest.ResponseRecorder) map[string]any {
	t.Helper()
	var body map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode response body: %v (body: %s)", err, rec.Body.String())
	}
	return body
}

func TestRequireReady_BlocksBeforeBootstrapCompletes(t *testing.T) {
	// Arrange
	api, _, _ := newTestAPI(t, false)

	// Act
	rec := httptest.NewRecorder()
	api.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))

	// Assert
	if rec.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
}

func TestHealthAndReadiness_AlwaysRespondEvenWhenNotReady(t *testing.T) {
	// Arrange
	api, _, _ := newTestAPI(t, false)

	for _, path := range []string{"/healthcheck", "/readiness", "/redines"} {
		t.Run(path, func(t *testing.T) {
			// Act
			rec := httptest.NewRecorder()
			api.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, path, nil))

			// Assert: never 503-gated by requireReady, though /readiness and
			// /redines do report 503 with ready=false as their own payload.
			if rec.Code != http.StatusOK && rec.Code != http.StatusServiceUnavailable {
				t.Errorf("status = %d, want 200 or 503", rec.Code)
			}
			body := decodeJSON(t, rec)
			if body["ready"] != false {
				t.Errorf("body[ready] = %v, want false", body["ready"])
			}
		})
	}
}

func TestHandleStatus_DerivesBucketsByTypeNotPosition(t *testing.T) {
	// Arrange: private bucket listed FIRST in config — a regression guard
	// for the Lua original's bug of assuming index 0 is always public.
	cfg := &config.Config{Buckets: []config.Bucket{
		{Name: "priv-first", ManifestKey: "m.json", Public: false},
		{Name: "pub-second", ManifestKey: "m.json", Public: true},
	}}
	store := newFakeStore()
	cat := catalog.New(cfg.BucketPriority, cfg.BucketType)
	res := resolver.New(cfg, cat, store)
	api := New(cfg, cat, res, store, metricsapi.New())
	t.Cleanup(api.Close)
	api.SetReady(true)

	// Act
	rec := httptest.NewRecorder()
	api.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))

	// Assert
	body := decodeJSON(t, rec)
	if body["public_bucket"] != "pub-second" {
		t.Errorf("public_bucket = %v, want pub-second", body["public_bucket"])
	}
	if body["private_bucket"] != "priv-first" {
		t.Errorf("private_bucket = %v, want priv-first", body["private_bucket"])
	}
}

func TestHandleUpdate_MissingFilename(t *testing.T) {
	// Arrange
	api, _, _ := newTestAPI(t, true)

	// Act
	rec := httptest.NewRecorder()
	api.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/update", nil))

	// Assert
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleUpdate_InvalidKeyRejected(t *testing.T) {
	// Arrange
	api, _, _ := newTestAPI(t, true)

	// Act
	rec := httptest.NewRecorder()
	api.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/update?filename=../etc/passwd", nil))

	// Assert
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusBadRequest)
	}
}

func TestHandleUpdate_NotFound(t *testing.T) {
	// Arrange
	api, _, _ := newTestAPI(t, true)

	// Act
	rec := httptest.NewRecorder()
	api.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/update?filename=missing.txt", nil))

	// Assert
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleUpdate_FoundViaLiveHeadCheck(t *testing.T) {
	// Arrange
	api, _, store := newTestAPI(t, true)
	store.put("pub", "found.txt", 42)

	// Act
	rec := httptest.NewRecorder()
	api.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/update?filename=found.txt", nil))

	// Assert
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", rec.Code, rec.Body.String())
	}
}

func TestHandleRelocate_RequiresPOST(t *testing.T) {
	// Arrange
	api, _, _ := newTestAPI(t, true)

	// Act: a GET to /relocate should not hit the relocate handler at all —
	// the Lua original's method-agnostic routing let GET trigger a
	// state-mutating operation with no CSRF protection.
	rec := httptest.NewRecorder()
	api.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/relocate?filename=x", nil))

	// Assert
	if rec.Code == http.StatusOK {
		t.Errorf("GET /relocate should not succeed as if it were the relocate handler; status = %d", rec.Code)
	}
}

func TestHandleRelocate_NotFound(t *testing.T) {
	// Arrange
	api, _, _ := newTestAPI(t, true)

	// Act
	rec := httptest.NewRecorder()
	api.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodPost, "/relocate?filename=nowhere.txt", nil))

	// Assert
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestHandleDownload_RedirectsToPublicURL(t *testing.T) {
	// Arrange
	api, cat, store := newTestAPI(t, true)
	store.put("pub", "photo.jpg", 10)
	cat.Register("photo.jpg", "pub", 10, "t1")

	// Act
	rec := httptest.NewRecorder()
	api.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/pub/photo.jpg", nil))

	// Assert
	if rec.Code != http.StatusFound {
		t.Fatalf("status = %d, want %d (body: %s)", rec.Code, http.StatusFound, rec.Body.String())
	}
	if loc := rec.Header().Get("Location"); loc != "http://public.example/pub/photo.jpg" {
		t.Errorf("Location = %q", loc)
	}
}

func TestHandleDownload_InvalidKeyRejected(t *testing.T) {
	// Arrange
	api, _, _ := newTestAPI(t, true)

	// Act
	rec := httptest.NewRecorder()
	api.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/pub/..%2f..%2fetc%2fpasswd", nil))

	// Assert
	if rec.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want %d (body: %s)", rec.Code, http.StatusBadRequest, rec.Body.String())
	}
}

func TestHandleDownload_NotFound(t *testing.T) {
	// Arrange
	api, _, _ := newTestAPI(t, true)

	// Act
	rec := httptest.NewRecorder()
	api.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/pub/nope.jpg", nil))

	// Assert
	if rec.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusNotFound)
	}
}

func TestAuthMiddleware_RejectsMissingKey(t *testing.T) {
	// Arrange
	api, _, _ := newTestAPI(t, true, WithAuth(NewAuthConfig([]string{"secret"})))

	// Act
	rec := httptest.NewRecorder()
	api.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/status", nil))

	// Assert
	if rec.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusUnauthorized)
	}
}

func TestAuthMiddleware_AcceptsValidKey(t *testing.T) {
	// Arrange
	api, _, _ := newTestAPI(t, true, WithAuth(NewAuthConfig([]string{"secret"})))

	// Act
	req := httptest.NewRequest(http.MethodGet, "/status", nil)
	req.Header.Set("X-API-Key", "secret")
	rec := httptest.NewRecorder()
	api.Routes().ServeHTTP(rec, req)

	// Assert
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", rec.Code, http.StatusOK)
	}
}

func TestAuthMiddleware_DoesNotGateHealthProbes(t *testing.T) {
	// Arrange
	api, _, _ := newTestAPI(t, true, WithAuth(NewAuthConfig([]string{"secret"})))

	// Act: no API key supplied at all.
	rec := httptest.NewRecorder()
	api.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthcheck", nil))

	// Assert
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d — health probes must never require auth", rec.Code, http.StatusOK)
	}
}

func TestRateLimiter_BlocksAfterBurstExhausted(t *testing.T) {
	// Arrange: 0 requests/sec refill, burst of 1 — a second immediate
	// request must be rejected.
	api, _, _ := newTestAPI(t, true, WithRateLimit(0, 1))

	// Act
	first := httptest.NewRecorder()
	api.Routes().ServeHTTP(first, httptest.NewRequest(http.MethodGet, "/status", nil))
	second := httptest.NewRecorder()
	api.Routes().ServeHTTP(second, httptest.NewRequest(http.MethodGet, "/status", nil))

	// Assert
	if first.Code != http.StatusOK {
		t.Fatalf("first request status = %d, want %d (body: %s)", first.Code, http.StatusOK, first.Body.String())
	}
	if second.Code != http.StatusTooManyRequests {
		t.Errorf("second request status = %d, want %d", second.Code, http.StatusTooManyRequests)
	}
}

func TestRateLimiter_DoesNotGateHealthProbes(t *testing.T) {
	// Arrange: burst exhausted on the first call.
	api, _, _ := newTestAPI(t, true, WithRateLimit(0, 1))
	api.Routes().ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodGet, "/status", nil))

	// Act
	rec := httptest.NewRecorder()
	api.Routes().ServeHTTP(rec, httptest.NewRequest(http.MethodGet, "/healthcheck", nil))

	// Assert
	if rec.Code != http.StatusOK {
		t.Errorf("status = %d, want %d — health probes must never be rate limited", rec.Code, http.StatusOK)
	}
}
