package policy

import (
	"reflect"
	"sync"
	"testing"
	"time"
)

func TestAuditLog_RecordAndEntries(t *testing.T) {
	// Arrange
	log := NewAuditLog()
	entry := AuditEntry{Time: time.Now(), Ecosystem: "npm", Name: "left-pad", Version: "1.3.0", Decision: DecisionAllow}

	// Act
	log.Record(entry)
	got := log.Entries()

	// Assert: AuditEntry has a []string field, so equality needs
	// reflect.DeepEqual rather than == (structs with slice fields aren't
	// comparable with ==).
	if len(got) != 1 || !reflect.DeepEqual(got[0], entry) {
		t.Errorf("Entries() = %+v, want [%+v]", got, entry)
	}
}

func TestAuditLog_EntriesReturnsACopy(t *testing.T) {
	// Arrange
	log := NewAuditLog()
	log.Record(AuditEntry{Name: "pkg-a"})
	got := log.Entries()

	// Act: mutate the returned slice
	got[0].Name = "mutated"

	// Assert: the log's internal state is unaffected
	if again := log.Entries(); again[0].Name != "pkg-a" {
		t.Errorf("Entries()[0].Name = %q after mutating a prior copy, want it unaffected (\"pkg-a\")", again[0].Name)
	}
}

func TestAuditLog_RecordDoesNotAliasTheCallersReasonsSlice(t *testing.T) {
	// Arrange: regression test for a bug where AuditEntry.Reasons and a
	// caller-held Verdict.Reasons shared one backing array — mutating the
	// caller's slice silently mutated the "append-only" log's stored entry.
	log := NewAuditLog()
	reasons := []string{"original reason"}
	log.Record(AuditEntry{Name: "pkg", Reasons: reasons})

	// Act: mutate the slice that was passed into Record
	reasons[0] = "TAMPERED"

	// Assert: the log's stored copy is unaffected
	if got := log.Entries()[0].Reasons[0]; got != "original reason" {
		t.Errorf("Entries()[0].Reasons[0] = %q after mutating the caller's original slice, want it unaffected (\"original reason\")", got)
	}
}

func TestAuditLog_ConcurrentRecordIsSafe(t *testing.T) {
	// Arrange
	log := NewAuditLog()
	const goroutines = 50
	var wg sync.WaitGroup
	wg.Add(goroutines)

	// Act
	for i := 0; i < goroutines; i++ {
		go func() {
			defer wg.Done()
			log.Record(AuditEntry{Name: "pkg"})
		}()
	}
	wg.Wait()

	// Assert
	if got := len(log.Entries()); got != goroutines {
		t.Errorf("len(Entries()) = %d, want %d", got, goroutines)
	}
}
