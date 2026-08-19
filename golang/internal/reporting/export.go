package reporting

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"fmt"
	"strconv"
)

// ToJSON marshals v (any of this package's report types, or a []Record)
// as indented JSON.
func ToJSON(v any) ([]byte, error) {
	body, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("reporting: encode JSON: %w", err)
	}
	return body, nil
}

var recordCSVHeader = []string{
	"bucket", "key", "name", "version", "license_spdx_id",
	"vulnerable", "max_severity", "policy_decision", "size_bytes", "last_modified",
}

// RecordsToCSV renders records as CSV with a fixed column set (see
// recordCSVHeader). A Record with no PolicyVerdict renders
// policy_decision as an empty cell, not "ALLOW" — an absent evaluation is
// not the same as an explicit allow decision, and this export
// deliberately preserves that distinction rather than defaulting it away.
func RecordsToCSV(records []Record) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)

	if err := w.Write(recordCSVHeader); err != nil {
		return nil, fmt.Errorf("reporting: write CSV header: %w", err)
	}
	for _, r := range records {
		decision := ""
		if r.PolicyVerdict != nil {
			decision = r.PolicyVerdict.Decision.String()
		}
		row := []string{
			r.Entry.Bucket,
			r.Entry.Key,
			r.Name,
			r.Version,
			r.License.SPDXID,
			strconv.FormatBool(r.Scan.Vulnerable()),
			r.Scan.MaxSeverity().String(),
			decision,
			strconv.FormatInt(r.Entry.Size, 10),
			r.Entry.LastModified,
		}
		if err := w.Write(row); err != nil {
			return nil, fmt.Errorf("reporting: write CSV row for %q: %w", r.Entry.Key, err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("reporting: flush CSV: %w", err)
	}
	return buf.Bytes(), nil
}

var licenseBreakdownCSVHeader = []string{"license_spdx_id", "count"}

// LicenseBreakdownToCSV renders entries as CSV. An entry with SPDXID ==
// "" (see LicenseBreakdownEntry's doc comment) renders as an empty cell,
// not a literal string like "unknown" — left for the reader of the CSV to
// interpret, consistent with every other empty-string convention in this
// export.
func LicenseBreakdownToCSV(entries []LicenseBreakdownEntry) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)

	if err := w.Write(licenseBreakdownCSVHeader); err != nil {
		return nil, fmt.Errorf("reporting: write CSV header: %w", err)
	}
	for _, e := range entries {
		row := []string{e.SPDXID, strconv.Itoa(e.Count)}
		if err := w.Write(row); err != nil {
			return nil, fmt.Errorf("reporting: write CSV row for %q: %w", e.SPDXID, err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("reporting: flush CSV: %w", err)
	}
	return buf.Bytes(), nil
}

var storageUsageCSVHeader = []string{"bucket", "total_bytes", "object_count"}

// StorageUsageToCSV renders entries as CSV.
func StorageUsageToCSV(entries []StorageUsageEntry) ([]byte, error) {
	var buf bytes.Buffer
	w := csv.NewWriter(&buf)

	if err := w.Write(storageUsageCSVHeader); err != nil {
		return nil, fmt.Errorf("reporting: write CSV header: %w", err)
	}
	for _, e := range entries {
		row := []string{e.Bucket, strconv.FormatInt(e.TotalBytes, 10), strconv.Itoa(e.ObjectCount)}
		if err := w.Write(row); err != nil {
			return nil, fmt.Errorf("reporting: write CSV row for %q: %w", e.Bucket, err)
		}
	}
	w.Flush()
	if err := w.Error(); err != nil {
		return nil, fmt.Errorf("reporting: flush CSV: %w", err)
	}
	return buf.Bytes(), nil
}
