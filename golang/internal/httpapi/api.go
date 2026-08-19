// Package httpapi wires the HTTP surface: routes, request handlers,
// readiness gating, and the optional auth/rate-limit middleware, delegating
// all actual logic to resolver/catalog/objectstore/metricsapi.
package httpapi

import (
	"encoding/json"
	"errors"
	"log/slog"
	"net/http"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"

	"github.com/rachlenko/parparchik/golang/internal/catalog"
	"github.com/rachlenko/parparchik/golang/internal/config"
	"github.com/rachlenko/parparchik/golang/internal/metricsapi"
	"github.com/rachlenko/parparchik/golang/internal/objectstore"
	"github.com/rachlenko/parparchik/golang/internal/resolver"
)

const presignExpiry = time.Hour

// API holds the dependencies every handler needs and tracks whether startup
// bootstrap has completed.
type API struct {
	cfg      *config.Config
	catalog  *catalog.Catalog
	resolver *resolver.Resolver
	store    objectstore.Store
	metrics  *metricsapi.Metrics

	auth        AuthConfig
	rateLimiter *ipRateLimiter

	ready atomic.Bool
}

// Option configures optional API behavior (see WithAuth, WithRateLimit).
type Option func(*API)

// WithAuth enables the API-key auth middleware on every route except
// /healthcheck and /readiness.
func WithAuth(auth AuthConfig) Option {
	return func(a *API) { a.auth = auth }
}

// WithRateLimit enables a per-client-IP request rate limit on every route.
func WithRateLimit(requestsPerSecond rate.Limit, burst int) Option {
	return func(a *API) { a.rateLimiter = newIPRateLimiter(requestsPerSecond, burst) }
}

// New builds an API. Call SetReady(true) once Bootstrap has completed.
func New(cfg *config.Config, cat *catalog.Catalog, res *resolver.Resolver, store objectstore.Store, metrics *metricsapi.Metrics, opts ...Option) *API {
	a := &API{cfg: cfg, catalog: cat, resolver: res, store: store, metrics: metrics}
	for _, opt := range opts {
		opt(a)
	}
	return a
}

// Close releases background resources (the rate limiter's cleanup timer).
func (a *API) Close() {
	if a.rateLimiter != nil {
		a.rateLimiter.Close()
	}
}

// SetReady flips the readiness flag reported by /healthcheck, /readiness,
// and required by every other handler.
func (a *API) SetReady(ready bool) {
	a.ready.Store(ready)
}

// IsReady reports the current readiness state.
func (a *API) IsReady() bool {
	return a.ready.Load()
}

// Routes builds the HTTP router. Static routes are registered before the
// wildcard download route so they take precedence, matching the nginx.conf
// location-block ordering in the original service. Auth and rate-limit
// middleware wrap every route except the health/readiness probes.
func (a *API) Routes() http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /status", a.requireReady(a.handleStatus))
	mux.HandleFunc("GET /list", a.requireReady(a.handleList))
	mux.HandleFunc("GET /update", a.requireReady(a.handleUpdate))
	mux.HandleFunc("POST /relocate", a.requireReady(a.handleRelocate))
	mux.HandleFunc("GET /metrics", a.requireReady(a.handleMetrics))
	mux.HandleFunc("GET /{bucket}/{key...}", a.requireReady(a.handleDownload))
	mux.HandleFunc("/", handleNotFound)

	var protected http.Handler = mux
	if a.rateLimiter != nil {
		protected = a.rateLimiter.middleware(protected)
	}
	protected = a.auth.middleware(protected)

	top := http.NewServeMux()
	top.Handle("/", protected)
	// Readiness/liveness probes must always respond, even before bootstrap
	// completes and without auth/rate-limit interference — a k8s probe
	// hitting these mid-startup or mid-incident should see a clean
	// "not ready yet", not a 401/429.
	top.HandleFunc("GET /healthcheck", a.handleHealthcheck)
	// /redines (sic) is a deliberate compatibility alias for a pre-existing
	// typo'd route in nginx-lua/nginx.conf, not a typo in this port — kept
	// so existing readiness-probe configs pointed at it keep working.
	top.HandleFunc("GET /redines", a.handleReadiness)
	top.HandleFunc("GET /readiness", a.handleReadiness)

	return top
}

func (a *API) requireReady(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if !a.IsReady() {
			writeJSON(w, http.StatusServiceUnavailable, map[string]any{"error": "service not initialized"})
			return
		}
		next(w, r)
	}
}

func (a *API) handleStatus(w http.ResponseWriter, r *http.Request) {
	buckets := make([]map[string]any, 0, len(a.cfg.Buckets))
	var publicBucket, privateBucket string
	for _, b := range a.cfg.Buckets {
		buckets = append(buckets, map[string]any{
			"name":         b.Name,
			"manifest_key": b.ManifestKey,
			"public":       b.Public,
			"kind":         string(b.Kind),
		})
		// Virtual repos have no storage of their own and no meaningful
		// public/private flag (Public is always its zero value, false) —
		// excluding them here keeps them from being misreported as the
		// convenience "private_bucket" just by being first in the list.
		if !b.HasStorage() {
			continue
		}
		switch {
		case b.Public && publicBucket == "":
			publicBucket = b.Name
		case !b.Public && privateBucket == "":
			privateBucket = b.Name
		}
	}

	writeJSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"buckets":        buckets,
		"region":         a.cfg.AWSRegion,
		"ready":          a.IsReady(),
		"file_count":     a.catalog.Count(),
		"public_bucket":  publicBucket,
		"private_bucket": privateBucket,
	})
}

func (a *API) handleReadiness(w http.ResponseWriter, r *http.Request) {
	if a.IsReady() {
		writeJSON(w, http.StatusOK, map[string]any{
			"status": "ready", "ready": true, "file_count": a.catalog.Count(),
		})
		return
	}
	writeJSON(w, http.StatusServiceUnavailable, map[string]any{
		"status": "not_ready", "ready": false, "file_count": a.catalog.Count(),
	})
}

func (a *API) handleHealthcheck(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok", "ready": a.IsReady()})
}

// handleList is a pure read against the in-memory catalog. Reconciliation
// against actual bucket contents happens in Bootstrap and a periodic
// background timer (see cmd/parparchik), not on every request — the Lua
// original ran a full multi-bucket listing plus a manifest PUT to every
// bucket on every GET /list call; see resolver.SyncRegistry's doc comment.
func (a *API) handleList(w http.ResponseWriter, r *http.Request) {
	entries := a.catalog.ListAll()
	writeJSON(w, http.StatusOK, map[string]any{"count": len(entries), "files": entries})
}

func (a *API) handleUpdate(w http.ResponseWriter, r *http.Request) {
	filename := r.URL.Query().Get("filename")
	if filename == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing 'filename' query parameter"})
		return
	}
	if err := validateKey(filename); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "filename": filename})
		return
	}

	entry, ok := a.catalog.Lookup(filename)
	if ok && !a.resolver.Exists(r.Context(), entry.Bucket, entry.Key) {
		ok = false
	}
	if !ok {
		resolved, err := a.resolver.ResolveMissingFile(r.Context(), filename)
		if err != nil {
			slog.Error("httpapi: update: resolve failed", "filename", filename, "error", err)
			writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to resolve file"})
			return
		}
		if resolved == nil {
			writeJSON(w, http.StatusNotFound, map[string]any{"error": "file not found", "filename": filename})
			return
		}
		entry = *resolved
	}

	writeJSON(w, http.StatusOK, map[string]any{"message": "file located", "file": entry})
}

func (a *API) handleRelocate(w http.ResponseWriter, r *http.Request) {
	filename := r.URL.Query().Get("filename")
	if filename == "" {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": "missing 'filename' query parameter"})
		return
	}
	if err := validateKey(filename); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error(), "filename": filename})
		return
	}

	result, err := a.resolver.Relocate(r.Context(), filename)
	switch {
	case errors.Is(err, resolver.ErrFileNotFound):
		writeJSON(w, http.StatusNotFound, map[string]any{
			"status": "fail", "error": "file not found in any bucket", "filename": filename,
		})
		return
	case err != nil:
		slog.Error("httpapi: relocate failed", "filename", filename, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{
			"status": "fail", "error": "failed to persist manifests", "filename": filename,
		})
		return
	}

	resp := map[string]any{"status": "ok", "file": result.Entry}
	if result.RelocatedFrom != "" {
		resp["relocated_from"] = result.RelocatedFrom
		resp["relocated_to"] = result.RelocatedTo
	}
	if result.Duplicate {
		resp["duplicate"] = true
		resp["note"] = "file exists in multiple buckets, highest-priority (first configured) bucket takes precedence"
	}
	writeJSON(w, http.StatusOK, resp)
}

func (a *API) handleDownload(w http.ResponseWriter, r *http.Request) {
	if err := validateKey(r.PathValue("key")); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]any{"error": err.Error()})
		return
	}

	entry, err := a.resolver.ResolveRoute(r.Context(), r.URL.Path)
	if err != nil {
		slog.Error("httpapi: download: resolve route failed", "route", r.URL.Path, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to resolve route"})
		return
	}
	if entry == nil {
		writeJSON(w, http.StatusNotFound, map[string]any{"error": "file not found", "route": r.URL.Path})
		return
	}

	url, err := a.downloadURL(r, *entry)
	if err != nil {
		slog.Error("httpapi: download: build url failed", "route", r.URL.Path, "error", err)
		writeJSON(w, http.StatusInternalServerError, map[string]any{"error": "failed to build download url"})
		return
	}
	http.Redirect(w, r, url, http.StatusFound)
}

func (a *API) downloadURL(r *http.Request, entry catalog.Entry) (string, error) {
	if entry.BucketType == "public" {
		return a.store.PublicURL(entry.Bucket, entry.Key), nil
	}
	return a.store.PresignedURL(r.Context(), entry.Bucket, entry.Key, presignExpiry)
}

func (a *API) handleMetrics(w http.ResponseWriter, r *http.Request) {
	a.metrics.Update(a.catalog.ListAll(), a.resolver.DuplicateCount())
	a.metrics.Handler().ServeHTTP(w, r)
}

func handleNotFound(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusNotFound, map[string]any{"error": "not found"})
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(body); err != nil {
		slog.Error("httpapi: write response failed", "error", err)
	}
}
