package httpapi

import (
	"crypto/subtle"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/time/rate"
)

// AuthConfig gates every route (except /healthcheck and /readiness, which
// must always answer for k8s probes) behind a shared-secret API key,
// checked against the X-API-Key header or an "Authorization: Bearer <key>"
// header.
//
// The Lua original had no authentication at all: any unauthenticated
// client could enumerate every registered file via GET /list and then
// download "private" bucket objects via presigned URLs the server itself
// generated — see the nginx-lua security review's CRITICAL finding #1.
// Leaving APIKeys empty here reproduces that same open-by-default behavior
// (useful for local dev, e.g. this project's docker-compose stack), so
// cmd/parparchik logs a loud warning at startup when no keys are
// configured — this must never be the default for anything reachable
// outside a trusted local network.
type AuthConfig struct {
	apiKeys [][]byte
}

// NewAuthConfig builds an AuthConfig from a list of accepted API keys.
// Empty/blank entries are ignored.
func NewAuthConfig(keys []string) AuthConfig {
	var set [][]byte
	for _, k := range keys {
		if k = strings.TrimSpace(k); k != "" {
			set = append(set, []byte(k))
		}
	}
	return AuthConfig{apiKeys: set}
}

// Enabled reports whether any API key is configured.
func (a AuthConfig) Enabled() bool { return len(a.apiKeys) > 0 }

// valid checks key against every configured key using a constant-time
// comparison, so a byte-by-byte early-exit map/string comparison can't leak
// timing information about how much of a guessed key was correct.
func (a AuthConfig) valid(key string) bool {
	provided := []byte(key)
	for _, k := range a.apiKeys {
		if len(k) == len(provided) && subtle.ConstantTimeCompare(k, provided) == 1 {
			return true
		}
	}
	return false
}

func (a AuthConfig) middleware(next http.Handler) http.Handler {
	if !a.Enabled() {
		return next
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := r.Header.Get("X-API-Key")
		if key == "" {
			key = strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		}
		if !a.valid(key) {
			writeJSON(w, http.StatusUnauthorized, map[string]any{"error": "unauthorized"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

// ipRateLimiter enforces a per-client-IP request rate, addressing the
// nginx-lua security review's HIGH finding that GET /list (which triggers a
// full bucket listing plus a manifest PUT to every bucket) had no rate
// limiting at all. Idle client entries are periodically evicted so this
// does not grow without bound under a churn of distinct source IPs.
//
// Limitation: this keys on r.RemoteAddr (see clientIP), which is only the
// real client IP when this service is exposed directly. Behind a reverse
// proxy or ingress (the deployment shape cmd/parparchik's k8s-oriented
// readiness handling implies), RemoteAddr is the proxy's IP and every
// client collapses into one shared bucket. This is a deliberate scope cut,
// not an oversight: trusting a forwarded-for header safely requires
// configuring which upstream hops are trusted (proxy count or CIDR list),
// which this package does not yet do. Deploy behind a proxy only if the
// proxy itself enforces rate limiting, or extend this with a
// trusted-proxy-aware header lookup before relying on it as the sole
// mitigation in that topology.
type ipRateLimiter struct {
	mu      sync.Mutex
	entries map[string]*rateLimiterEntry
	rate    rate.Limit
	burst   int
	done    chan struct{}
}

type rateLimiterEntry struct {
	limiter  *rate.Limiter
	lastSeen time.Time
}

const rateLimiterIdleTimeout = 10 * time.Minute

// newIPRateLimiter creates a limiter allowing r requests/second per client
// IP, with the given burst. Call Close when done to stop its cleanup timer.
func newIPRateLimiter(r rate.Limit, burst int) *ipRateLimiter {
	l := &ipRateLimiter{
		entries: make(map[string]*rateLimiterEntry),
		rate:    r,
		burst:   burst,
		done:    make(chan struct{}),
	}
	go l.cleanupLoop()
	return l
}

func (l *ipRateLimiter) cleanupLoop() {
	ticker := time.NewTicker(rateLimiterIdleTimeout)
	defer ticker.Stop()
	for {
		select {
		case <-ticker.C:
			cutoff := time.Now().Add(-rateLimiterIdleTimeout)
			l.mu.Lock()
			for ip, e := range l.entries {
				if e.lastSeen.Before(cutoff) {
					delete(l.entries, ip)
				}
			}
			l.mu.Unlock()
		case <-l.done:
			return
		}
	}
}

// Close stops the background cleanup goroutine.
func (l *ipRateLimiter) Close() { close(l.done) }

func (l *ipRateLimiter) allow(ip string) bool {
	l.mu.Lock()
	e, ok := l.entries[ip]
	if !ok {
		e = &rateLimiterEntry{limiter: rate.NewLimiter(l.rate, l.burst)}
		l.entries[ip] = e
	}
	e.lastSeen = time.Now()
	limiter := e.limiter
	l.mu.Unlock()
	return limiter.Allow()
}

func (l *ipRateLimiter) middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !l.allow(clientIP(r)) {
			writeJSON(w, http.StatusTooManyRequests, map[string]any{"error": "rate limit exceeded"})
			return
		}
		next.ServeHTTP(w, r)
	})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}
