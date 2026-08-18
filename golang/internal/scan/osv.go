package scan

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultOSVEndpoint = "https://api.osv.dev/v1/query"
	defaultOSVTimeout  = 15 * time.Second

	// maxResponseBytes bounds how much of an OSV response is buffered into
	// memory — defensive against a misbehaving endpoint, matching the same
	// pattern proxycache.HTTPFetcher uses. A single package's vulnerability
	// list is normally a few KB to low hundreds of KB even for a
	// heavily-affected package.
	maxResponseBytes = 8 << 20 // 8 MiB
)

// OSVScanner queries osv.dev (https://osv.dev), a free, open vulnerability
// database aggregating GHSA, PYSEC, npm advisories, and more — no API key
// required.
type OSVScanner struct {
	endpoint string
	client   *http.Client
}

// NewOSVScanner returns an OSVScanner using the public osv.dev endpoint.
func NewOSVScanner() *OSVScanner {
	return &OSVScanner{
		endpoint: defaultOSVEndpoint,
		client:   &http.Client{Timeout: defaultOSVTimeout},
	}
}

// NewOSVScannerWithEndpoint overrides the endpoint (for tests, or a
// self-hosted OSV-compatible index).
func NewOSVScannerWithEndpoint(endpoint string, client *http.Client) *OSVScanner {
	if client == nil {
		client = &http.Client{Timeout: defaultOSVTimeout}
	}
	return &OSVScanner{endpoint: endpoint, client: client}
}

type osvQuery struct {
	Version string     `json:"version,omitempty"`
	Package osvPackage `json:"package"`
}

type osvPackage struct {
	Name      string `json:"name"`
	Ecosystem string `json:"ecosystem"`
}

// osvQueryResponse omits OSV's next_page_token field. A single query
// response is paginated when a package has an unusually large number of
// vulnerabilities; this client does not follow pagination, so Scan can
// silently return only the first page for such a package. Not currently
// testable without a live/synthetic multi-page fixture — a known,
// documented gap rather than an oversight.
type osvQueryResponse struct {
	Vulns []osvVuln `json:"vulns"`
}

type osvVuln struct {
	ID               string        `json:"id"`
	Summary          string        `json:"summary"`
	DatabaseSpecific osvDBSpecific `json:"database_specific"`
	SeverityScores   []osvSeverity `json:"severity"`
}

type osvDBSpecific struct {
	Severity string `json:"severity"`
}

type osvSeverity struct {
	Type  string `json:"type"`
	Score string `json:"score"`
}

// Scan implements Scanner.
func (s *OSVScanner) Scan(ctx context.Context, ecosystem, name, version string) (Result, error) {
	body, err := json.Marshal(osvQuery{
		Version: version,
		Package: osvPackage{Name: name, Ecosystem: ecosystem},
	})
	if err != nil {
		return Result{}, fmt.Errorf("scan: osv: encode query: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.endpoint, bytes.NewReader(body))
	if err != nil {
		return Result{}, fmt.Errorf("scan: osv: build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return Result{}, fmt.Errorf("scan: osv: query %s/%s@%s: %w", ecosystem, name, version, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return Result{}, fmt.Errorf("scan: osv: query %s/%s@%s: unexpected status %d", ecosystem, name, version, resp.StatusCode)
	}

	respBody, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return Result{}, fmt.Errorf("scan: osv: read response: %w", err)
	}
	if len(respBody) > maxResponseBytes {
		return Result{}, fmt.Errorf("scan: osv: response for %s/%s@%s exceeds %d byte limit", ecosystem, name, version, maxResponseBytes)
	}

	var parsed osvQueryResponse
	if err := json.Unmarshal(respBody, &parsed); err != nil {
		return Result{}, fmt.Errorf("scan: osv: decode response: %w", err)
	}

	findings := make([]Finding, 0, len(parsed.Vulns))
	for _, v := range parsed.Vulns {
		findings = append(findings, Finding{
			ID:       v.ID,
			Summary:  v.Summary,
			Severity: parseOSVSeverity(v.DatabaseSpecific.Severity),
		})
	}
	return Result{Findings: findings}, nil
}

// parseOSVSeverity maps OSV's database_specific.severity string to
// Severity. IMPORTANT SCOPE LIMITATION: database_specific.severity is a
// convention GHSA-derived entries (npm, and some others) tend to set, but
// most non-GHSA OSV sources — PyPI's PYSEC advisories, Go's GO-*
// advisories, OSS-Fuzz entries, many Maven advisories — generally do NOT
// populate it; they only carry a raw CVSS vector string in the top-level
// `severity` array (parsed into osvVuln.SeverityScores but never converted
// to a Severity bucket — CVSS-vector parsing is not implemented here). For
// those ecosystems, essentially every finding degrades to SeverityUnknown,
// and Policy.Evaluate does not count Unknown findings toward MaxSeverity,
// so a real CRITICAL vulnerability in e.g. a PyPI package can currently
// evaluate as Allowed. Before wiring Policy.Evaluate into an actual
// gate for any non-GHSA-heavy ecosystem (Task 28), add CVSS vector
// parsing or otherwise treat Vulnerable()==true as its own signal
// independent of severity.
func parseOSVSeverity(raw string) Severity {
	switch strings.ToUpper(raw) {
	case "LOW":
		return SeverityLow
	case "MODERATE", "MEDIUM":
		return SeverityMedium
	case "HIGH":
		return SeverityHigh
	case "CRITICAL":
		return SeverityCritical
	default:
		return SeverityUnknown
	}
}
