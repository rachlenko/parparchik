// Command parparchik is the Go implementation of the parparchik S3 file
// routing/registry service — a drop-in replacement for the C++, Python, and
// OpenResty+Lua implementations in this repository: same REST API, same
// environment variables, same MinIO/S3 stack.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"golang.org/x/time/rate"

	"github.com/rachlenko/parparchik/golang/internal/catalog"
	"github.com/rachlenko/parparchik/golang/internal/config"
	"github.com/rachlenko/parparchik/golang/internal/httpapi"
	"github.com/rachlenko/parparchik/golang/internal/metricsapi"
	"github.com/rachlenko/parparchik/golang/internal/objectstore"
	"github.com/rachlenko/parparchik/golang/internal/resolver"
)

const (
	readHeaderTimeout = 10 * time.Second
	shutdownTimeout   = 15 * time.Second
	bootstrapTimeout  = 60 * time.Second
)

func main() {
	if err := run(); err != nil {
		slog.Error("parparchik: fatal", "error", err)
		os.Exit(1)
	}
}

func run() error {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	cfg, err := config.Load()
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}
	if len(cfg.APIKeys) == 0 {
		slog.Warn("parparchik: no PARPARCHIK_API_KEYS configured — every route except /healthcheck and /readiness is reachable without authentication; do not run this way outside a trusted local network")
	}

	store, err := objectstore.NewS3Store(ctx, cfg)
	if err != nil {
		return fmt.Errorf("create object store: %w", err)
	}

	cat := catalog.New(cfg.BucketPriority, cfg.BucketType)
	res := resolver.New(cfg, cat, store)
	metrics := metricsapi.New()

	api := httpapi.New(cfg, cat, res, store, metrics,
		httpapi.WithAuth(httpapi.NewAuthConfig(cfg.APIKeys)),
		httpapi.WithRateLimit(rate.Limit(cfg.RateLimitPerSecond), cfg.RateLimitBurst),
	)
	defer api.Close()

	server := &http.Server{
		Addr:              fmt.Sprintf("%s:%d", cfg.Host, cfg.Port),
		Handler:           api.Routes(),
		ReadHeaderTimeout: readHeaderTimeout,
	}

	serverErr := make(chan error, 1)
	go func() {
		slog.Info("parparchik: listening", "addr", server.Addr)
		if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			serverErr <- err
			return
		}
		serverErr <- nil
	}()

	go bootstrap(ctx, res, api)
	go periodicSync(ctx, res, cfg.SyncInterval)

	select {
	case err := <-serverErr:
		return err
	case <-ctx.Done():
	}

	slog.Info("parparchik: shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownTimeout)
	defer cancel()
	if err := server.Shutdown(shutdownCtx); err != nil {
		return fmt.Errorf("graceful shutdown: %w", err)
	}
	return <-serverErr
}

// bootstrap loads manifests and performs the initial reconcile before
// flipping the API to ready. It runs in its own goroutine so the HTTP
// server can already accept /healthcheck and /readiness traffic (reporting
// not-ready) while this is in progress, matching how a k8s rollout expects
// a pod to behave during startup.
func bootstrap(parent context.Context, res *resolver.Resolver, api *httpapi.API) {
	ctx, cancel := context.WithTimeout(parent, bootstrapTimeout)
	defer cancel()

	if err := res.Bootstrap(ctx); err != nil {
		slog.Error("parparchik: bootstrap failed", "error", err)
		return
	}
	api.SetReady(true)
	slog.Info("parparchik: ready")
}

// periodicSync keeps the catalog reconciled against actual bucket contents
// on an interval, so files uploaded directly to storage (outside this
// service) are eventually discovered without depending on a client calling
// a particular endpoint.
func periodicSync(ctx context.Context, res *resolver.Resolver, interval time.Duration) {
	if interval <= 0 {
		return
	}
	ticker := time.NewTicker(interval)
	defer ticker.Stop()

	for {
		select {
		case <-ticker.C:
			if err := res.SyncRegistry(ctx); err != nil {
				slog.Error("parparchik: periodic sync failed", "error", err)
			}
		case <-ctx.Done():
			return
		}
	}
}
