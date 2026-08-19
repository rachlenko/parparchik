// Package reporting searches and summarizes artifact Records — a join of
// catalog identity (internal/catalog.Entry) with optional License (Task
// 26), Scan (Task 25), and Policy (Task 28) data — across every
// repository.
//
// This package does not itself source License/Scan/Policy data: nothing
// in this codebase attaches those results to a catalog.Entry today (this
// service has no upload endpoint, so nothing has ever run a real license
// detection, vulnerability scan, or policy evaluation against a real
// artifact and recorded the result anywhere Search/StorageUsagePerRepository
// could read it from). A caller assembles []Record from whatever data it
// actually has; this package only searches, summarizes, and exports it.
// StorageUsagePerRepository is the one report this package can compute
// from real, already-populated data alone — catalog.Entry.Size and
// catalog.Entry.Bucket need no enrichment.
//
// Implemented: Record, Query, Search, PolicyViolationsReport,
// LicenseBreakdown, StorageUsagePerRepository, and JSON/CSV export for
// each.
//
// Not implemented (see docs/plans/ Task 34): a search/report HTTP
// endpoint — the plan itself scopes this task as "API-only, no web UI,"
// but even the API side needs a real source of enriched []Record data to
// serve, which (per above) doesn't exist in this codebase yet. This
// package's Search/report functions operate on a []Record a caller
// already has in memory; wiring an endpoint that builds that []Record
// from live catalog/license/scan/policy state is future work, same
// "standalone, tested primitive" scope several other packages this
// session were left at.
package reporting

import (
	"strings"

	"github.com/rachlenko/parparchik/golang/internal/catalog"
	"github.com/rachlenko/parparchik/golang/internal/license"
	"github.com/rachlenko/parparchik/golang/internal/policy"
	"github.com/rachlenko/parparchik/golang/internal/scan"
)

// Record joins one catalog entry with whatever enrichment data a caller
// has for it. Name/Version are optional parsed package identity (empty
// when unknown — no format package's key parser is wired to catalog
// entries yet, see internal/format's package doc). License is the zero
// value when unknown. Scan is the zero value when the artifact was never
// scanned — indistinguishable, by design, from "scanned and found clean"
// (scan.Result itself has no "was this ever run" flag; see
// internal/scan). PolicyVerdict is nil when no policy evaluation was ever
// recorded for this artifact, distinct from an Allow verdict.
type Record struct {
	Entry         catalog.Entry
	Name          string
	Version       string
	License       license.Result
	Scan          scan.Result
	PolicyVerdict *policy.Verdict
}

// Query filters Search. Every non-zero field narrows the result; a Query
// with every field at its zero value matches everything.
type Query struct {
	// NameContains matches Record.Name as a case-insensitive substring.
	// Empty means no filter.
	NameContains string

	// VersionEquals matches Record.Version exactly (versions are not
	// substring-matched — "1.2" incorrectly matching "1.20" would be
	// actively wrong, not just imprecise). Empty means no filter.
	VersionEquals string

	// LicenseSPDXID matches Record.License.SPDXID exactly. Empty means no
	// filter — including against a Record with no normalized license at
	// all; querying for a specific SPDX ID never accidentally matches
	// "license unknown".
	LicenseSPDXID string

	// Vulnerable, non-nil, filters on Record.Scan.Vulnerable(). nil means
	// no filter.
	Vulnerable *bool

	// MinSeverity, when set above its zero value (scan.SeverityUnknown),
	// keeps only Records whose Scan.MaxSeverity() is at or above this —
	// distinct from Vulnerable, since a Record can be "vulnerable" only
	// with SeverityUnknown findings, which this filter (correctly) never
	// matches unless MinSeverity is left at SeverityUnknown too.
	MinSeverity scan.Severity
}

// Search returns every Record in records matching every non-zero field of
// q, preserving records' relative order.
func Search(records []Record, q Query) []Record {
	var result []Record
	if len(records) > 0 {
		result = make([]Record, 0, len(records))
	}
	for _, r := range records {
		if !matches(r, q) {
			continue
		}
		result = append(result, r)
	}
	return result
}

func matches(r Record, q Query) bool {
	if q.NameContains != "" && !strings.Contains(strings.ToLower(r.Name), strings.ToLower(q.NameContains)) {
		return false
	}
	if q.VersionEquals != "" && r.Version != q.VersionEquals {
		return false
	}
	if q.LicenseSPDXID != "" && r.License.SPDXID != q.LicenseSPDXID {
		return false
	}
	if q.Vulnerable != nil && r.Scan.Vulnerable() != *q.Vulnerable {
		return false
	}
	if q.MinSeverity > scan.SeverityUnknown && r.Scan.MaxSeverity() < q.MinSeverity {
		return false
	}
	return true
}
