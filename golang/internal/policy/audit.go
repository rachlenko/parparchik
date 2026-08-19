package policy

import (
	"sync"
	"time"
)

// AuditEntry records one Engine.Evaluate decision.
type AuditEntry struct {
	Time      time.Time
	Ecosystem string
	Name      string
	Version   string
	Decision  Decision
	Reasons   []string

	// Waived is true when a Waiver overrode the Ruleset's own decision to
	// force DecisionAllow — Decision and Reasons already reflect the
	// post-waiver outcome, so Waived is what distinguishes "the rules
	// allowed this" from "a human overrode a denial".
	Waived bool
}

// AuditLog is a concurrency-safe, append-only, in-memory record of policy
// decisions. It does not persist across process restarts — a durable
// audit trail (e.g. writing entries to S3 or a database) is future work
// once there's a real request path calling Engine.Evaluate to generate
// entries worth persisting.
type AuditLog struct {
	mu      sync.Mutex
	entries []AuditEntry
}

// NewAuditLog returns an empty AuditLog ready to use.
func NewAuditLog() *AuditLog {
	return &AuditLog{}
}

// Record appends entry to the log. entry.Reasons is defensively copied
// into a fresh backing array before storing — Engine.Evaluate builds
// AuditEntry.Reasons from the same Verdict.Reasons slice it also returns
// to its caller, and without this copy a caller mutating its own returned
// Verdict would silently mutate the "append-only" log's stored entry too
// (the two slice headers would otherwise point at the same array).
func (l *AuditLog) Record(entry AuditEntry) {
	entry.Reasons = append([]string(nil), entry.Reasons...)

	l.mu.Lock()
	defer l.mu.Unlock()
	l.entries = append(l.entries, entry)
}

// Entries returns a copy of every entry recorded so far, oldest first.
// Each returned entry's Reasons is its own backing array, copied out of
// the log's storage — mutating a returned entry's Reasons cannot affect
// the log or any other call's returned copy.
func (l *AuditLog) Entries() []AuditEntry {
	l.mu.Lock()
	defer l.mu.Unlock()
	out := make([]AuditEntry, len(l.entries))
	copy(out, l.entries)
	for i := range out {
		out[i].Reasons = append([]string(nil), out[i].Reasons...)
	}
	return out
}
