// Package scan provides vulnerability scanning primitives: looking up
// known vulnerabilities for a package+version, and evaluating them against
// a policy to decide whether the package should be allowed.
//
// Implemented: the Scanner interface, an OSVScanner backed by osv.dev's
// free API (no Sonatype/JFrog license needed), and Policy.Evaluate.
//
// Not implemented (see docs/plans/ Task 25 and Task 28): wiring this into
// an actual ingest/quarantine hook. parparchik has no upload endpoint
// today — objects arrive in S3 by external means (`mc cp`, a CI pipeline,
// etc.) — so there is no single "upload" moment to gate synchronously.
// The natural hook point is catalog discovery of a previously-unseen key
// during resolver.SyncRegistry, but scanning every arbitrary S3 key
// requires first knowing its package ecosystem/name/version, which means
// this needs to compose with a format package's key parser (maven, npm,
// pypi, ...) before it can run automatically. That composition, plus a
// scheduled full-catalog scan job, is Task 28's policy-engine work, not
// this package's.
package scan

import "context"

// Severity mirrors CVSS-style severity buckets used by most vulnerability
// databases (including OSV).
type Severity int

const (
	SeverityUnknown Severity = iota
	SeverityLow
	SeverityMedium
	SeverityHigh
	SeverityCritical
)

// Finding describes one known vulnerability affecting a scanned
// package+version.
type Finding struct {
	ID       string // e.g. "GHSA-xxxx-xxxx-xxxx" or "CVE-2024-12345"
	Summary  string
	Severity Severity
}

// Result is the outcome of scanning one package+version.
type Result struct {
	Findings []Finding
}

// Vulnerable reports whether the scan found any known vulnerabilities.
func (r Result) Vulnerable() bool { return len(r.Findings) > 0 }

// MaxSeverity returns the highest severity among the result's findings, or
// SeverityUnknown if there are none.
func (r Result) MaxSeverity() Severity {
	max := SeverityUnknown
	for _, f := range r.Findings {
		if f.Severity > max {
			max = f.Severity
		}
	}
	return max
}

// Scanner looks up known vulnerabilities for one package+version in a
// given ecosystem (e.g. "npm", "PyPI", "Maven" — see osv.dev's ecosystem
// list for the exact spelling each backend expects).
type Scanner interface {
	Scan(ctx context.Context, ecosystem, name, version string) (Result, error)
}
