// Package policy composes scan.Result (Task 25) and license.Result
// (Task 26) into a single allow/quarantine/deny decision for one artifact,
// with an explicit, audited waiver mechanism for overriding a specific
// package+version, and an in-memory audit log of every decision made.
//
// Implemented: Ruleset (the rule evaluation itself: vulnerability
// severity, denied licenses, minimum package age), Waiver + waiver
// matching, AuditLog, and Engine (ties the three together).
//
// Not implemented (see docs/plans/ Task 28): wiring Engine.Evaluate into
// an actual request path. parparchik still has no upload endpoint (the
// same limitation noted in internal/scan, internal/license, and
// internal/sbom), and the other natural hook — the proxy fetch-through
// path (internal/resolver.resolveProxyRoute, Task 24) — fetches an
// arbitrary key without knowing its package ecosystem/name/version, since
// none of the format packages (Task 14-22) are mounted as HTTP
// sub-routers yet. Composing this engine with format-aware routing to
// gate real traffic is future work once a format package is mounted.
package policy

import (
	"fmt"
	"time"

	"github.com/rachlenko/parparchik/golang/internal/license"
	"github.com/rachlenko/parparchik/golang/internal/scan"
)

// Decision is the outcome of evaluating an Artifact against a Ruleset.
type Decision int

const (
	DecisionAllow Decision = iota
	DecisionQuarantine
	DecisionDeny
)

func (d Decision) String() string {
	switch d {
	case DecisionQuarantine:
		return "QUARANTINE"
	case DecisionDeny:
		return "DENY"
	default:
		return "ALLOW"
	}
}

// worse reports whether d is a stricter outcome than other (Deny stricter
// than Quarantine stricter than Allow).
func (d Decision) worse(other Decision) bool { return d > other }

// Artifact is the metadata a Ruleset evaluates. Ecosystem/Name/Version
// identify the package (matched against Waiver); Scan and License are the
// Task 25/26 outputs for it; PublishedAt is the zero value when unknown,
// which skips the package-age rule entirely rather than treating an
// unknown publish date as suspiciously recent.
type Artifact struct {
	Ecosystem   string
	Name        string
	Version     string
	PublishedAt time.Time
	Scan        scan.Result
	License     license.Result
}

// Verdict is the result of evaluating an Artifact: the strictest Decision
// reached by any rule that had an opinion, plus every reason along the
// way (including rules that voted Allow) — the audit log's whole point is
// showing why, not just what.
type Verdict struct {
	Decision Decision
	Reasons  []string
}

func (v Verdict) merge(d Decision, reason string) Verdict {
	next := v
	if d.worse(next.Decision) {
		next.Decision = d
	}
	next.Reasons = append(append([]string{}, next.Reasons...), reason)
	return next
}

// Ruleset is a composable set of policy thresholds. Its license and
// package-age rules are inert at the zero value (no DeniedLicenses, and
// MinPackageAge <= 0 disables the age rule) — but the vulnerability rule
// is NOT permissive by default: MaxSeverity and QuarantineSeverity both
// default to scan.SeverityUnknown, so a zero-value Ruleset denies any
// artifact with a finding above SeverityUnknown. A genuinely permissive
// default requires explicitly setting MaxSeverity (e.g. to
// scan.SeverityCritical to only deny the worst findings).
type Ruleset struct {
	// MaxSeverity is the highest scan.Severity still allowed at all; a
	// finding worse than this denies.
	MaxSeverity scan.Severity

	// QuarantineSeverity is the highest scan.Severity allowed without
	// review; a finding worse than this (but not worse than MaxSeverity)
	// quarantines instead of denying outright. Leaving it >= MaxSeverity
	// disables the quarantine tier for vulnerabilities (every finding above
	// MaxSeverity denies directly, matching scan.Policy's binary behavior).
	QuarantineSeverity scan.Severity

	// DeniedLicenses is a set of normalized SPDX identifiers (e.g.
	// "GPL-3.0-only") that deny an artifact outright. Only matched against
	// a License.Normalized() result — an ambiguous or unrecognized raw
	// license string is neither denied nor approved by this rule, the same
	// "don't guess" stance license.NormalizeSPDX itself takes.
	DeniedLicenses []string

	// MinPackageAge quarantines an artifact whose PublishedAt is more
	// recent than this — a real malware-mitigation technique, since most
	// malicious packages are pulled from public registries within days of
	// publication. Zero (or a zero Artifact.PublishedAt) disables the rule.
	MinPackageAge time.Duration
}

// Evaluate applies r to a as of now. now is an explicit parameter rather
// than an internal time.Now() call so the age rule is deterministically
// testable; Engine supplies the real clock.
//
// The vulnerability rule always contributes a reason (even a clean scan
// says so), so Verdict.Reasons is never empty — there is no "no rule had
// an opinion" case to guard against.
func (r Ruleset) Evaluate(a Artifact, now time.Time) Verdict {
	v := Verdict{Decision: DecisionAllow}

	if d, reason, ok := r.evaluateLicense(a); ok {
		v = v.merge(d, reason)
	}
	vulnDecision, vulnReason := r.evaluateVulnerabilities(a)
	v = v.merge(vulnDecision, vulnReason)
	if d, reason, ok := r.evaluateAge(a, now); ok {
		v = v.merge(d, reason)
	}

	return v
}

func (r Ruleset) evaluateLicense(a Artifact) (Decision, string, bool) {
	if !a.License.Normalized() {
		return DecisionAllow, "", false
	}
	for _, denied := range r.DeniedLicenses {
		if a.License.SPDXID == denied {
			return DecisionDeny, fmt.Sprintf("license %s is denied by policy", a.License.SPDXID), true
		}
	}
	return DecisionAllow, fmt.Sprintf("license %s is not denied by policy", a.License.SPDXID), true
}

func (r Ruleset) evaluateVulnerabilities(a Artifact) (Decision, string) {
	if !a.Scan.Vulnerable() {
		return DecisionAllow, "no known vulnerabilities found"
	}
	worst := a.Scan.MaxSeverity()
	count := len(a.Scan.Findings)
	switch {
	case worst > r.MaxSeverity:
		return DecisionDeny, fmt.Sprintf("%d finding(s), worst severity %s exceeds policy max %s", count, worst, r.MaxSeverity)
	case worst > r.QuarantineSeverity:
		return DecisionQuarantine, fmt.Sprintf("%d finding(s), worst severity %s exceeds quarantine threshold %s", count, worst, r.QuarantineSeverity)
	default:
		return DecisionAllow, fmt.Sprintf("%d finding(s), worst severity %s is within policy thresholds", count, worst)
	}
}

func (r Ruleset) evaluateAge(a Artifact, now time.Time) (Decision, string, bool) {
	if a.PublishedAt.IsZero() || r.MinPackageAge <= 0 {
		return DecisionAllow, "", false
	}
	age := now.Sub(a.PublishedAt)
	if age < r.MinPackageAge {
		return DecisionQuarantine, fmt.Sprintf("published %s ago, younger than policy minimum %s", age, r.MinPackageAge), true
	}
	return DecisionAllow, fmt.Sprintf("published %s ago, at or past policy minimum %s", age, r.MinPackageAge), true
}
