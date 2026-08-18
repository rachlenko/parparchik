// Package metricsapi renders parparchik's Prometheus metrics from the
// current catalog state, using the same metric names as the Lua
// implementation's hand-written text renderer.
package metricsapi

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"

	"github.com/rachlenko/parparchik/golang/internal/catalog"
)

const (
	week  = 7 * 24 * time.Hour
	month = 31 * 24 * time.Hour
)

// Metrics holds the registered Prometheus collectors.
type Metrics struct {
	registry        *prometheus.Registry
	volumeFiles     *prometheus.GaugeVec
	duplicateFiles  prometheus.Gauge
	uploadsPerWeek  prometheus.Gauge
	uploadsPerMonth prometheus.Gauge
}

// New creates a Metrics instance with its own registry, so tests can create
// isolated instances without colliding on the global default registry.
func New() *Metrics {
	m := &Metrics{
		registry: prometheus.NewRegistry(),
		volumeFiles: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "parparchik_volume_files",
			Help: "Current number of known files by bucket.",
		}, []string{"volume"}),
		duplicateFiles: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "parparchik_duplicate_files",
			Help: "Number of file keys that exist in more than one S3 bucket.",
		}),
		uploadsPerWeek: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "parparchik_uploads_per_week",
			Help: "Known uploaded file versions modified during the last 7 days.",
		}),
		uploadsPerMonth: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "parparchik_uploads_per_month",
			Help: "Known uploaded file versions modified during the last 31 days.",
		}),
	}
	m.registry.MustRegister(m.volumeFiles, m.duplicateFiles, m.uploadsPerWeek, m.uploadsPerMonth)
	return m
}

// Update recomputes every gauge from the given catalog entries and duplicate
// count.
func (m *Metrics) Update(entries []catalog.Entry, duplicates int) {
	m.volumeFiles.Reset()

	bucketCounts := make(map[string]float64)
	now := time.Now()
	var weekCount, monthCount float64

	for _, entry := range entries {
		bucket := entry.Bucket
		if bucket == "" {
			bucket = "unknown"
		}
		bucketCounts[bucket]++

		if t, ok := parseLastModified(entry.LastModified); ok {
			age := now.Sub(t)
			if age >= 0 && age <= week {
				weekCount++
			}
			if age >= 0 && age <= month {
				monthCount++
			}
		}
	}

	for bucket, count := range bucketCounts {
		m.volumeFiles.WithLabelValues(bucket).Set(count)
	}
	m.duplicateFiles.Set(float64(duplicates))
	m.uploadsPerWeek.Set(weekCount)
	m.uploadsPerMonth.Set(monthCount)
}

// Handler returns the http.Handler that serves this Metrics' Prometheus
// text-format exposition.
func (m *Metrics) Handler() http.Handler {
	return promhttp.HandlerFor(m.registry, promhttp.HandlerOpts{})
}

// parseLastModified accepts RFC3339 timestamps (what objectstore emits) as
// well as the plain "YYYY-MM-DDTHH:MM:SSZ" form the Lua service wrote.
func parseLastModified(s string) (time.Time, bool) {
	if s == "" {
		return time.Time{}, false
	}
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	return time.Time{}, false
}
