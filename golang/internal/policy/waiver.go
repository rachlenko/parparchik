package policy

import "time"

// Waiver is an explicit, audited override that forces a Decision to
// DecisionAllow for a specific package, regardless of what Ruleset.Evaluate
// decided — e.g. "we've manually reviewed GHSA-xxxx for left-pad@1.3.0 and
// accepted the risk". A Waiver never suppresses the underlying Verdict's
// Reasons; Engine appends why the waiver applied on top of them, so the
// audit trail always shows both what was denied/quarantined and why it was
// let through anyway.
type Waiver struct {
	Ecosystem string
	Name      string

	// Version scopes the waiver to one exact version. Empty matches every
	// version of Name — a broader override, used sparingly (e.g. a license
	// exception for a package that's known to be mislabeled upstream across
	// releases) rather than the common case.
	Version string

	// Reason and ApprovedBy are recorded verbatim into the audit log —
	// this package does not validate or look up ApprovedBy against any
	// identity system.
	Reason     string
	ApprovedBy string

	// ExpiresAt is the zero value for a waiver that never expires. A
	// waiver is still considered active at the exact expiry instant
	// (matching stops once now is strictly after ExpiresAt).
	ExpiresAt time.Time
}

func (w Waiver) matches(a Artifact, now time.Time) bool {
	if w.Ecosystem != a.Ecosystem || w.Name != a.Name {
		return false
	}
	if w.Version != "" && w.Version != a.Version {
		return false
	}
	if !w.ExpiresAt.IsZero() && now.After(w.ExpiresAt) {
		return false
	}
	return true
}

// findWaiver returns the first waiver in waivers that matches a as of now,
// in slice order — callers that need "most specific wins" should order an
// exact-version waiver before a version-wildcard one for the same package.
func findWaiver(waivers []Waiver, a Artifact, now time.Time) (Waiver, bool) {
	for _, w := range waivers {
		if w.matches(a, now) {
			return w, true
		}
	}
	return Waiver{}, false
}
