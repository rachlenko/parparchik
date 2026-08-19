package policy

import (
	"fmt"
	"time"
)

// Engine ties a Ruleset, a set of Waivers, and an AuditLog together into
// the single entry point a caller uses: Evaluate.
type Engine struct {
	Rules   Ruleset
	Waivers []Waiver

	// Audit receives every Evaluate call's outcome when non-nil. A nil
	// Audit is a valid, deliberate choice (e.g. a caller doing a dry-run
	// evaluation that shouldn't pollute the real audit trail) — Evaluate
	// still works, it just doesn't record anything.
	Audit *AuditLog

	// Clock returns the current time; nil uses time.Now. Overriding it is
	// how tests exercise the age rule and waiver expiry deterministically
	// without depending on wall-clock time.
	Clock func() time.Time
}

func (e *Engine) now() time.Time {
	if e.Clock != nil {
		return e.Clock()
	}
	return time.Now()
}

// Evaluate applies e.Rules to a, then applies e.Waivers on top: a matching,
// unexpired Waiver forces the outcome to DecisionAllow regardless of what
// Rules decided, with a reason appended (not replacing the original
// reasons) explaining the override. The final outcome — waived or not — is
// recorded to e.Audit when set.
func (e *Engine) Evaluate(a Artifact) Verdict {
	now := e.now()
	verdict := e.Rules.Evaluate(a, now)

	waived := false
	if verdict.Decision != DecisionAllow {
		if w, ok := findWaiver(e.Waivers, a, now); ok {
			waived = true
			verdict = Verdict{
				Decision: DecisionAllow,
				Reasons:  append(append([]string{}, verdict.Reasons...), fmt.Sprintf("waived by %s: %s", w.ApprovedBy, w.Reason)),
			}
		}
	}

	if e.Audit != nil {
		e.Audit.Record(AuditEntry{
			Time:      now,
			Ecosystem: a.Ecosystem,
			Name:      a.Name,
			Version:   a.Version,
			Decision:  verdict.Decision,
			Reasons:   verdict.Reasons,
			Waived:    waived,
		})
	}

	return verdict
}
