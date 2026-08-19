package reporting

import (
	"encoding/csv"
	"encoding/json"
	"strings"
	"testing"

	"github.com/rachlenko/parparchik/golang/internal/catalog"
	"github.com/rachlenko/parparchik/golang/internal/license"
	"github.com/rachlenko/parparchik/golang/internal/policy"
	"github.com/rachlenko/parparchik/golang/internal/scan"
)

func TestToJSON(t *testing.T) {
	// Arrange
	entries := []LicenseBreakdownEntry{{SPDXID: "MIT", Count: 3}}

	// Act
	body, err := ToJSON(entries)

	// Assert
	if err != nil {
		t.Fatalf("ToJSON() error = %v", err)
	}
	var decoded []LicenseBreakdownEntry
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("ToJSON() produced invalid JSON: %v", err)
	}
	if len(decoded) != 1 || decoded[0].SPDXID != "MIT" || decoded[0].Count != 3 {
		t.Errorf("decoded = %+v, want [{MIT 3}]", decoded)
	}
}

func TestToJSON_UnmarshalableValueIsAnError(t *testing.T) {
	// Act
	_, err := ToJSON(make(chan int))

	// Assert
	if err == nil {
		t.Fatal("ToJSON(chan) error = nil, want an error for an unmarshalable value")
	}
}

func TestRecordsToCSV(t *testing.T) {
	// Arrange
	verdict := &policy.Verdict{Decision: policy.DecisionDeny}
	records := []Record{
		{
			Entry: catalog.Entry{Bucket: "npm-cache", Key: "pkg-1.0.0.tgz", Size: 1234, LastModified: "2026-08-01T00:00:00Z"},
			Name:  "pkg", Version: "1.0.0",
			License:       license.Result{SPDXID: "MIT"},
			Scan:          scan.Result{Findings: []scan.Finding{{ID: "GHSA-1", Severity: scan.SeverityHigh}}},
			PolicyVerdict: verdict,
		},
	}

	// Act
	body, err := RecordsToCSV(records)

	// Assert
	if err != nil {
		t.Fatalf("RecordsToCSV() error = %v", err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil {
		t.Fatalf("RecordsToCSV() produced invalid CSV: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("len(rows) = %d, want 2 (header + 1 record)", len(rows))
	}
	if rows[0][0] != "bucket" {
		t.Errorf("header[0] = %q, want bucket", rows[0][0])
	}
	dataRow := rows[1]
	want := []string{"npm-cache", "pkg-1.0.0.tgz", "pkg", "1.0.0", "MIT", "true", "HIGH", "DENY", "1234", "2026-08-01T00:00:00Z"}
	if len(dataRow) != len(want) {
		t.Fatalf("dataRow = %v, want %v", dataRow, want)
	}
	for i := range want {
		if dataRow[i] != want[i] {
			t.Errorf("dataRow[%d] = %q, want %q", i, dataRow[i], want[i])
		}
	}
}

func TestRecordsToCSV_NilPolicyVerdictRendersEmptyCell(t *testing.T) {
	// Arrange
	records := []Record{{Entry: catalog.Entry{Bucket: "b", Key: "k"}}}

	// Act
	body, err := RecordsToCSV(records)

	// Assert
	if err != nil {
		t.Fatalf("RecordsToCSV() error = %v", err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil {
		t.Fatalf("invalid CSV: %v", err)
	}
	// policy_decision is column index 7 (see recordCSVHeader).
	if rows[1][7] != "" {
		t.Errorf("policy_decision cell = %q, want empty for a Record with no PolicyVerdict", rows[1][7])
	}
}

func TestRecordsToCSV_EmptyInputProducesHeaderOnly(t *testing.T) {
	// Act
	body, err := RecordsToCSV(nil)

	// Assert
	if err != nil {
		t.Fatalf("RecordsToCSV(nil) error = %v", err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil {
		t.Fatalf("invalid CSV: %v", err)
	}
	if len(rows) != 1 {
		t.Errorf("len(rows) = %d, want 1 (header only)", len(rows))
	}
}

func TestLicenseBreakdownToCSV(t *testing.T) {
	// Arrange
	entries := []LicenseBreakdownEntry{{SPDXID: "MIT", Count: 2}, {SPDXID: "", Count: 1}}

	// Act
	body, err := LicenseBreakdownToCSV(entries)

	// Assert
	if err != nil {
		t.Fatalf("LicenseBreakdownToCSV() error = %v", err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil {
		t.Fatalf("invalid CSV: %v", err)
	}
	if len(rows) != 3 {
		t.Fatalf("len(rows) = %d, want 3 (header + 2 entries)", len(rows))
	}
	if rows[1][0] != "MIT" || rows[1][1] != "2" {
		t.Errorf("rows[1] = %v, want [MIT 2]", rows[1])
	}
	if rows[2][0] != "" || rows[2][1] != "1" {
		t.Errorf("rows[2] = %v, want [\"\" 1]", rows[2])
	}
}

func TestStorageUsageToCSV(t *testing.T) {
	// Arrange
	entries := []StorageUsageEntry{{Bucket: "npm-cache", TotalBytes: 300, ObjectCount: 2}}

	// Act
	body, err := StorageUsageToCSV(entries)

	// Assert
	if err != nil {
		t.Fatalf("StorageUsageToCSV() error = %v", err)
	}
	rows, err := csv.NewReader(strings.NewReader(string(body))).ReadAll()
	if err != nil {
		t.Fatalf("invalid CSV: %v", err)
	}
	if len(rows) != 2 || rows[1][0] != "npm-cache" || rows[1][1] != "300" || rows[1][2] != "2" {
		t.Errorf("rows = %v, want header + [npm-cache 300 2]", rows)
	}
}
