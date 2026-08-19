package reporting

import (
	"testing"

	"github.com/rachlenko/parparchik/golang/internal/catalog"
	"github.com/rachlenko/parparchik/golang/internal/license"
	"github.com/rachlenko/parparchik/golang/internal/policy"
)

func TestPolicyViolationsReport(t *testing.T) {
	// Arrange
	allow := &policy.Verdict{Decision: policy.DecisionAllow}
	deny := &policy.Verdict{Decision: policy.DecisionDeny}
	quarantine := &policy.Verdict{Decision: policy.DecisionQuarantine}
	records := []Record{
		{Entry: catalog.Entry{Key: "allowed"}, PolicyVerdict: allow},
		{Entry: catalog.Entry{Key: "denied"}, PolicyVerdict: deny},
		{Entry: catalog.Entry{Key: "quarantined"}, PolicyVerdict: quarantine},
		{Entry: catalog.Entry{Key: "never-evaluated"}},
	}

	// Act
	got := PolicyViolationsReport(records)

	// Assert
	if len(got) != 2 {
		t.Fatalf("len(PolicyViolationsReport()) = %d, want 2 (deny + quarantine, not allow or never-evaluated)", len(got))
	}
	keys := map[string]bool{got[0].Entry.Key: true, got[1].Entry.Key: true}
	if !keys["denied"] || !keys["quarantined"] {
		t.Errorf("PolicyViolationsReport() keys = %v, want denied and quarantined", keys)
	}
}

func TestPolicyViolationsReport_NilVerdictIsExcludedNotTreatedAsViolation(t *testing.T) {
	// Arrange
	records := []Record{{Entry: catalog.Entry{Key: "never-evaluated"}}}

	// Act
	got := PolicyViolationsReport(records)

	// Assert
	if len(got) != 0 {
		t.Errorf("PolicyViolationsReport() = %v, want none (a nil PolicyVerdict is absence of data, not a violation)", got)
	}
}

func TestLicenseBreakdown_GroupsAndSortsByCountThenSPDXID(t *testing.T) {
	// Arrange
	records := []Record{
		{License: license.Result{SPDXID: "MIT"}},
		{License: license.Result{SPDXID: "MIT"}},
		{License: license.Result{SPDXID: "Apache-2.0"}},
		{License: license.Result{SPDXID: "BSD-3-Clause"}},
		{}, // unnormalized/unknown license
	}

	// Act
	got := LicenseBreakdown(records)

	// Assert
	want := []LicenseBreakdownEntry{
		{SPDXID: "MIT", Count: 2},
		{SPDXID: "", Count: 1},
		{SPDXID: "Apache-2.0", Count: 1},
		{SPDXID: "BSD-3-Clause", Count: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("LicenseBreakdown() = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("LicenseBreakdown()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestLicenseBreakdown_EmptyInput(t *testing.T) {
	// Act
	got := LicenseBreakdown(nil)

	// Assert
	if len(got) != 0 {
		t.Errorf("LicenseBreakdown(nil) = %v, want empty", got)
	}
}

func TestStorageUsagePerRepository(t *testing.T) {
	// Arrange
	records := []Record{
		{Entry: catalog.Entry{Bucket: "npm-cache", Size: 100}},
		{Entry: catalog.Entry{Bucket: "npm-cache", Size: 200}},
		{Entry: catalog.Entry{Bucket: "public-bucket", Size: 50}},
	}

	// Act
	got := StorageUsagePerRepository(records)

	// Assert
	want := []StorageUsageEntry{
		{Bucket: "npm-cache", TotalBytes: 300, ObjectCount: 2},
		{Bucket: "public-bucket", TotalBytes: 50, ObjectCount: 1},
	}
	if len(got) != len(want) {
		t.Fatalf("StorageUsagePerRepository() = %+v, want %+v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Errorf("StorageUsagePerRepository()[%d] = %+v, want %+v", i, got[i], want[i])
		}
	}
}

func TestStorageUsagePerRepository_EmptyInput(t *testing.T) {
	// Act
	got := StorageUsagePerRepository(nil)

	// Assert
	if len(got) != 0 {
		t.Errorf("StorageUsagePerRepository(nil) = %v, want empty", got)
	}
}
