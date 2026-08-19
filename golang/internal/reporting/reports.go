package reporting

import (
	"sort"

	"github.com/rachlenko/parparchik/golang/internal/policy"
)

// PolicyViolationsReport returns every Record whose PolicyVerdict is
// recorded and not policy.DecisionAllow — a Record with a nil
// PolicyVerdict (never evaluated) is not a violation, it's an absence of
// data, and is excluded rather than treated as either an allow or a
// violation.
func PolicyViolationsReport(records []Record) []Record {
	var result []Record
	for _, r := range records {
		if r.PolicyVerdict != nil && r.PolicyVerdict.Decision != policy.DecisionAllow {
			result = append(result, r)
		}
	}
	return result
}

// LicenseBreakdownEntry summarizes how many Records carry a given
// normalized SPDX identifier.
type LicenseBreakdownEntry struct {
	// SPDXID is "" for the bucket of Records with no normalized license
	// (License.Normalized() == false) — grouped together rather than
	// split by whatever varied raw license text they carry, since this is
	// a breakdown by SPDX identifier specifically.
	SPDXID string
	Count  int
}

// LicenseBreakdown groups records by License.SPDXID (Records with an
// unnormalized license are grouped under SPDXID == ""), sorted by Count
// descending, then SPDXID ascending as a deterministic tiebreak.
func LicenseBreakdown(records []Record) []LicenseBreakdownEntry {
	counts := make(map[string]int)
	for _, r := range records {
		counts[r.License.SPDXID]++
	}

	result := make([]LicenseBreakdownEntry, 0, len(counts))
	for spdxID, count := range counts {
		result = append(result, LicenseBreakdownEntry{SPDXID: spdxID, Count: count})
	}
	sort.Slice(result, func(i, j int) bool {
		if result[i].Count != result[j].Count {
			return result[i].Count > result[j].Count
		}
		return result[i].SPDXID < result[j].SPDXID
	})
	return result
}

// StorageUsageEntry summarizes one repository's (bucket's) total object
// size and count, computed directly from Record.Entry — the one report
// in this package needing no License/Scan/Policy enrichment at all.
type StorageUsageEntry struct {
	Bucket      string
	TotalBytes  int64
	ObjectCount int
}

// StorageUsagePerRepository groups records by Entry.Bucket, sorted by
// Bucket ascending.
func StorageUsagePerRepository(records []Record) []StorageUsageEntry {
	byBucket := make(map[string]*StorageUsageEntry)
	for _, r := range records {
		bucket := r.Entry.Bucket
		entry, ok := byBucket[bucket]
		if !ok {
			entry = &StorageUsageEntry{Bucket: bucket}
			byBucket[bucket] = entry
		}
		entry.TotalBytes += r.Entry.Size
		entry.ObjectCount++
	}

	// Insertion order doesn't matter here — the sort below fully orders
	// the output by Bucket regardless of map iteration order.
	result := make([]StorageUsageEntry, 0, len(byBucket))
	for _, entry := range byBucket {
		result = append(result, *entry)
	}
	sort.Slice(result, func(i, j int) bool { return result[i].Bucket < result[j].Bucket })
	return result
}
