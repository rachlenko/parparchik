package cleanup

import (
	"sort"
	"testing"
	"time"

	"github.com/rachlenko/parparchik/golang/internal/catalog"
)

func rfc3339(t time.Time) string { return t.UTC().Format(time.RFC3339) }

func keys(entries []catalog.Entry) []string {
	out := make([]string, len(entries))
	for i, e := range entries {
		out[i] = e.Key
	}
	sort.Strings(out)
	return out
}

func TestRule_Plan_ZeroValueEvictsNothing(t *testing.T) {
	// Arrange
	now := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	entries := []catalog.Entry{
		{Key: "a", Bucket: "b", Size: 100, LastModified: rfc3339(now.Add(-365 * 24 * time.Hour))},
	}

	// Act
	got := Rule{}.Plan(entries, now)

	// Assert
	if len(got) != 0 {
		t.Errorf("Plan() = %v, want empty for the zero-value Rule", got)
	}
}

func TestRule_Plan_MaxAge(t *testing.T) {
	now := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	r := Rule{MaxAge: 30 * 24 * time.Hour}

	entries := []catalog.Entry{
		{Key: "old", Bucket: "b", Size: 10, LastModified: rfc3339(now.Add(-40 * 24 * time.Hour))},
		{Key: "new", Bucket: "b", Size: 10, LastModified: rfc3339(now.Add(-1 * time.Hour))},
		{Key: "unparseable-timestamp", Bucket: "b", Size: 10, LastModified: "not-a-time"},
	}

	// Act
	got := r.Plan(entries, now)

	// Assert
	if want := []string{"old"}; !equalKeys(keys(got), want) {
		t.Errorf("Plan() keys = %v, want %v", keys(got), want)
	}
}

func TestRule_Plan_MaxTotalSizeEvictsOldestFirstUntilUnderBudget(t *testing.T) {
	now := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	r := Rule{MaxTotalSize: 150}

	entries := []catalog.Entry{
		{Key: "oldest", Bucket: "b", Size: 100, LastModified: rfc3339(now.Add(-3 * time.Hour))},
		{Key: "middle", Bucket: "b", Size: 100, LastModified: rfc3339(now.Add(-2 * time.Hour))},
		{Key: "newest", Bucket: "b", Size: 100, LastModified: rfc3339(now.Add(-1 * time.Hour))},
	}
	// total = 300, budget = 150: evicting "oldest" (total=200) is not
	// enough, must also evict "middle" (total=100) to get under budget.

	// Act
	got := r.Plan(entries, now)

	// Assert
	if want := []string{"middle", "oldest"}; !equalKeys(keys(got), want) {
		t.Errorf("Plan() keys = %v, want %v", keys(got), want)
	}
}

func TestRule_Plan_MaxTotalSizeAlreadyUnderBudgetEvictsNothing(t *testing.T) {
	now := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	r := Rule{MaxTotalSize: 1000}
	entries := []catalog.Entry{
		{Key: "a", Bucket: "b", Size: 100, LastModified: rfc3339(now.Add(-1 * time.Hour))},
	}

	// Act
	got := r.Plan(entries, now)

	// Assert
	if len(got) != 0 {
		t.Errorf("Plan() = %v, want empty (already under budget)", got)
	}
}

func TestRule_Plan_MaxTotalSizeNeverEvictsUnparseableEntriesButCountsTheirBytes(t *testing.T) {
	now := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	r := Rule{MaxTotalSize: 50}
	entries := []catalog.Entry{
		{Key: "unparseable", Bucket: "b", Size: 100, LastModified: "not-a-time"},
		{Key: "parseable", Bucket: "b", Size: 100, LastModified: rfc3339(now.Add(-1 * time.Hour))},
	}
	// total = 200, budget = 50. Only "parseable" can ever be evicted, even
	// though evicting it alone (total=100) still leaves the bucket over
	// budget — "unparseable" is never touched.

	// Act
	got := r.Plan(entries, now)

	// Assert
	if want := []string{"parseable"}; !equalKeys(keys(got), want) {
		t.Errorf("Plan() keys = %v, want %v", keys(got), want)
	}
}

func TestRule_Plan_MaxVersionCountKeepsNewestPerGroup(t *testing.T) {
	now := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	groupOf := func(e catalog.Entry) string { return e.Bucket }
	r := Rule{MaxVersionCount: 2, GroupKey: groupOf}

	entries := []catalog.Entry{
		{Key: "v1", Bucket: "pkg-a", Size: 1, LastModified: rfc3339(now.Add(-3 * time.Hour))},
		{Key: "v2", Bucket: "pkg-a", Size: 1, LastModified: rfc3339(now.Add(-2 * time.Hour))},
		{Key: "v3", Bucket: "pkg-a", Size: 1, LastModified: rfc3339(now.Add(-1 * time.Hour))},
		{Key: "other-v1", Bucket: "pkg-b", Size: 1, LastModified: rfc3339(now.Add(-1 * time.Hour))},
	}

	// Act
	got := r.Plan(entries, now)

	// Assert: pkg-a has 3 entries, budget 2 -> oldest ("v1") evicted;
	// pkg-b has 1 entry, under budget -> untouched.
	if want := []string{"v1"}; !equalKeys(keys(got), want) {
		t.Errorf("Plan() keys = %v, want %v", keys(got), want)
	}
}

func TestRule_Plan_MaxTotalSizeTiesBreakDeterministicallyOnKey(t *testing.T) {
	now := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	r := Rule{MaxTotalSize: 100}
	sameTimestamp := rfc3339(now.Add(-1 * time.Hour))
	entries := []catalog.Entry{
		{Key: "c", Bucket: "b", Size: 100, LastModified: sameTimestamp},
		{Key: "a", Bucket: "b", Size: 100, LastModified: sameTimestamp},
		{Key: "b", Bucket: "b", Size: 100, LastModified: sameTimestamp},
	}
	// total = 300, budget = 100: two of the three same-timestamp entries
	// must be evicted; the tie must break on Key, evicting "a" and "b"
	// (lexicographically smallest) and keeping "c" — repeated to confirm
	// it isn't accidentally stable-by-input-order instead.

	for i := 0; i < 5; i++ {
		// Act
		got := r.Plan(entries, now)

		// Assert
		if want := []string{"a", "b"}; !equalKeys(keys(got), want) {
			t.Fatalf("run %d: Plan() keys = %v, want %v", i, keys(got), want)
		}
	}
}

func TestRule_Plan_MaxVersionCountGroupKeyEmptyStringExcludesEntry(t *testing.T) {
	now := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	r := Rule{
		MaxVersionCount: 1,
		GroupKey: func(e catalog.Entry) string {
			if e.Key == "unidentifiable" {
				return ""
			}
			return e.Bucket
		},
	}
	entries := []catalog.Entry{
		{Key: "unidentifiable", Bucket: "pkg-a", Size: 1, LastModified: rfc3339(now.Add(-3 * time.Hour))},
		{Key: "v1", Bucket: "pkg-a", Size: 1, LastModified: rfc3339(now.Add(-2 * time.Hour))},
		{Key: "v2", Bucket: "pkg-a", Size: 1, LastModified: rfc3339(now.Add(-1 * time.Hour))},
	}
	// "unidentifiable" maps to "" and is excluded from the group entirely
	// — the group is just [v1, v2], over budget by 1, so "v1" is evicted.
	// "unidentifiable" itself is never a candidate.

	// Act
	got := r.Plan(entries, now)

	// Assert
	if want := []string{"v1"}; !equalKeys(keys(got), want) {
		t.Errorf("Plan() keys = %v, want %v", keys(got), want)
	}
}

func TestRule_Plan_MaxVersionCountWithNilGroupKeyIsInert(t *testing.T) {
	now := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	r := Rule{MaxVersionCount: 1}
	entries := []catalog.Entry{
		{Key: "v1", Bucket: "pkg-a", Size: 1, LastModified: rfc3339(now.Add(-2 * time.Hour))},
		{Key: "v2", Bucket: "pkg-a", Size: 1, LastModified: rfc3339(now.Add(-1 * time.Hour))},
	}

	// Act
	got := r.Plan(entries, now)

	// Assert
	if len(got) != 0 {
		t.Errorf("Plan() = %v, want empty (GroupKey is nil, rule disabled)", got)
	}
}

func TestRule_Plan_UnionAcrossRulesDeduplicates(t *testing.T) {
	now := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	r := Rule{MaxAge: time.Hour, MaxTotalSize: 0}
	entries := []catalog.Entry{
		{Key: "old", Bucket: "b", Size: 10, LastModified: rfc3339(now.Add(-2 * time.Hour))},
	}

	// Act
	got := r.Plan(entries, now)

	// Assert
	if want := []string{"old"}; !equalKeys(keys(got), want) {
		t.Errorf("Plan() keys = %v, want %v (no duplicate entry even if multiple rules would flag it)", keys(got), want)
	}
}

func equalKeys(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != want[i] {
			return false
		}
	}
	return true
}
