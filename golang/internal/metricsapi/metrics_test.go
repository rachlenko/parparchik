package metricsapi

import (
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/rachlenko/parparchik/golang/internal/catalog"
)

func TestUpdate_VolumeAndDuplicateGauges(t *testing.T) {
	// Arrange
	m := New()
	entries := []catalog.Entry{
		{Key: "a", Bucket: "pub"},
		{Key: "b", Bucket: "pub"},
		{Key: "c", Bucket: "priv"},
	}

	// Act
	m.Update(entries, 2)

	// Assert
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/metrics", nil)
	m.Handler().ServeHTTP(rec, req)
	body := rec.Body.String()

	for _, want := range []string{
		`parparchik_volume_files{volume="pub"} 2`,
		`parparchik_volume_files{volume="priv"} 1`,
		`parparchik_duplicate_files 2`,
	} {
		if !strings.Contains(body, want) {
			t.Errorf("metrics output missing %q; got:\n%s", want, body)
		}
	}
}

func TestUpdate_UploadWindowCounters(t *testing.T) {
	// Arrange
	m := New()
	now := time.Now().UTC()
	entries := []catalog.Entry{
		{Key: "recent", LastModified: now.Add(-2 * 24 * time.Hour).Format(time.RFC3339)},
		{Key: "old", LastModified: now.Add(-60 * 24 * time.Hour).Format(time.RFC3339)},
		{Key: "unparseable", LastModified: "not-a-timestamp"},
	}

	// Act
	m.Update(entries, 0)

	// Assert
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()

	if !strings.Contains(body, "parparchik_uploads_per_week 1") {
		t.Errorf("expected exactly 1 upload within the last week; got:\n%s", body)
	}
	if !strings.Contains(body, "parparchik_uploads_per_month 1") {
		t.Errorf("expected exactly 1 upload within the last month; got:\n%s", body)
	}
}

func TestUpdate_ResetsStaleBucketLabels(t *testing.T) {
	// Arrange: first update has a "temp" bucket, second update doesn't.
	m := New()
	m.Update([]catalog.Entry{{Key: "a", Bucket: "temp"}}, 0)

	// Act
	m.Update([]catalog.Entry{{Key: "a", Bucket: "permanent"}}, 0)

	// Assert
	rec := httptest.NewRecorder()
	m.Handler().ServeHTTP(rec, httptest.NewRequest("GET", "/metrics", nil))
	body := rec.Body.String()

	if strings.Contains(body, `volume="temp"`) {
		t.Errorf("stale bucket label \"temp\" should have been reset; got:\n%s", body)
	}
	if !strings.Contains(body, `volume="permanent"`) {
		t.Errorf("expected current bucket label \"permanent\"; got:\n%s", body)
	}
}
