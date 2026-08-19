package reporting

import (
	"testing"

	"github.com/rachlenko/parparchik/golang/internal/catalog"
	"github.com/rachlenko/parparchik/golang/internal/license"
	"github.com/rachlenko/parparchik/golang/internal/scan"
)

func boolPtr(b bool) *bool { return &b }

func sampleRecords() []Record {
	return []Record{
		{
			Entry: catalog.Entry{Key: "lodash-4.17.21.tgz", Bucket: "npm-cache"},
			Name:  "lodash", Version: "4.17.21",
			License: license.Result{Raw: "MIT", SPDXID: "MIT"},
		},
		{
			Entry: catalog.Entry{Key: "vulnerable-pkg-1.0.0.tgz", Bucket: "npm-cache"},
			Name:  "vulnerable-pkg", Version: "1.0.0",
			License: license.Result{Raw: "GPL-3.0-only", SPDXID: "GPL-3.0-only"},
			Scan:    scan.Result{Findings: []scan.Finding{{ID: "GHSA-1", Severity: scan.SeverityHigh}}},
		},
		{
			Entry: catalog.Entry{Key: "unknown-license.tgz", Bucket: "public-bucket"},
			Name:  "unknown-license-pkg", Version: "2.0.0",
		},
	}
}

func TestSearch_NameContains_CaseInsensitive(t *testing.T) {
	// Act
	got := Search(sampleRecords(), Query{NameContains: "LODASH"})

	// Assert
	if len(got) != 1 || got[0].Name != "lodash" {
		t.Errorf("Search(NameContains=LODASH) = %v, want [lodash]", got)
	}
}

func TestSearch_VersionEquals_ExactNotSubstring(t *testing.T) {
	// Arrange: a substring match would incorrectly match "4.17.21" against
	// a query for "4.17.2" — VersionEquals must not do that.
	got := Search(sampleRecords(), Query{VersionEquals: "4.17.2"})

	// Assert
	if len(got) != 0 {
		t.Errorf("Search(VersionEquals=4.17.2) = %v, want none (must be exact, not substring)", got)
	}
}

func TestSearch_LicenseSPDXID(t *testing.T) {
	// Act
	got := Search(sampleRecords(), Query{LicenseSPDXID: "GPL-3.0-only"})

	// Assert
	if len(got) != 1 || got[0].Name != "vulnerable-pkg" {
		t.Errorf("Search(LicenseSPDXID=GPL-3.0-only) = %v, want [vulnerable-pkg]", got)
	}
}

func TestSearch_LicenseSPDXID_EmptyQueryDoesNotMatchUnknownLicenseRecords(t *testing.T) {
	// Act: an empty query means "no filter", not "match records with no
	// normalized license".
	got := Search(sampleRecords(), Query{})

	// Assert
	if len(got) != len(sampleRecords()) {
		t.Errorf("Search(Query{}) = %v, want every record (empty query = no filter)", got)
	}
}

func TestSearch_Vulnerable(t *testing.T) {
	// Act
	vulnOnly := Search(sampleRecords(), Query{Vulnerable: boolPtr(true)})
	cleanOnly := Search(sampleRecords(), Query{Vulnerable: boolPtr(false)})

	// Assert
	if len(vulnOnly) != 1 || vulnOnly[0].Name != "vulnerable-pkg" {
		t.Errorf("Search(Vulnerable=true) = %v, want [vulnerable-pkg]", vulnOnly)
	}
	if len(cleanOnly) != 2 {
		t.Errorf("Search(Vulnerable=false) = %v, want 2 records", cleanOnly)
	}
}

func TestSearch_MinSeverity(t *testing.T) {
	// Act
	got := Search(sampleRecords(), Query{MinSeverity: scan.SeverityCritical})

	// Assert
	if len(got) != 0 {
		t.Errorf("Search(MinSeverity=CRITICAL) = %v, want none (highest finding is HIGH)", got)
	}

	got = Search(sampleRecords(), Query{MinSeverity: scan.SeverityHigh})
	if len(got) != 1 || got[0].Name != "vulnerable-pkg" {
		t.Errorf("Search(MinSeverity=HIGH) = %v, want [vulnerable-pkg]", got)
	}
}

func TestSearch_CombinedFilters(t *testing.T) {
	// Act
	got := Search(sampleRecords(), Query{NameContains: "pkg", Vulnerable: boolPtr(true)})

	// Assert
	if len(got) != 1 || got[0].Name != "vulnerable-pkg" {
		t.Errorf("Search(NameContains=pkg, Vulnerable=true) = %v, want [vulnerable-pkg]", got)
	}
}

func TestSearch_EmptyInputReturnsNil(t *testing.T) {
	// Act
	got := Search(nil, Query{})

	// Assert
	if got != nil {
		t.Errorf("Search(nil, ...) = %v, want nil", got)
	}
}
