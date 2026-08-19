package policy

import (
	"testing"
	"time"

	"github.com/rachlenko/parparchik/golang/internal/license"
	"github.com/rachlenko/parparchik/golang/internal/scan"
)

func TestRuleset_Evaluate_CleanArtifactIsAllowed(t *testing.T) {
	// Arrange
	r := Ruleset{MaxSeverity: scan.SeverityHigh}
	a := Artifact{Ecosystem: "npm", Name: "left-pad", Version: "1.3.0"}

	// Act
	v := r.Evaluate(a, time.Now())

	// Assert
	if v.Decision != DecisionAllow {
		t.Errorf("Decision = %v, want DecisionAllow", v.Decision)
	}
	if len(v.Reasons) == 0 {
		t.Error("Reasons is empty, want at least the vulnerability rule's own reason (it always contributes one)")
	}
}

func TestRuleset_Evaluate_ZeroValueDeniesAnyKnownVulnerability(t *testing.T) {
	// Arrange: regression test pinning a deliberately non-obvious default —
	// MaxSeverity/QuarantineSeverity both default to scan.SeverityUnknown,
	// so an unconfigured Ruleset denies any finding above that, not "denies
	// nothing" as a naive reading of "zero value" might suggest.
	r := Ruleset{}
	a := Artifact{
		Ecosystem: "npm", Name: "pkg", Version: "1.0.0",
		Scan: scan.Result{Findings: []scan.Finding{{ID: "GHSA-1", Severity: scan.SeverityLow}}},
	}

	// Act
	v := r.Evaluate(a, time.Now())

	// Assert
	if v.Decision != DecisionDeny {
		t.Errorf("Decision = %v, want DecisionDeny (zero-value Ruleset is not permissive for known vulnerabilities)", v.Decision)
	}
}

func TestRuleset_Evaluate_NormalizedLicenseNotInDenylistIsAllowed(t *testing.T) {
	// Arrange
	r := Ruleset{DeniedLicenses: []string{"GPL-3.0-only"}}
	a := Artifact{
		Ecosystem: "npm", Name: "pkg", Version: "1.0.0",
		License: license.Result{Raw: "MIT", SPDXID: "MIT"},
	}

	// Act
	v := r.Evaluate(a, time.Now())

	// Assert
	if v.Decision != DecisionAllow {
		t.Errorf("Decision = %v, want DecisionAllow", v.Decision)
	}
	found := false
	for _, reason := range v.Reasons {
		if reason == "license MIT is not denied by policy" {
			found = true
		}
	}
	if !found {
		t.Errorf("Reasons = %v, want one confirming the normalized license was checked and allowed", v.Reasons)
	}
}

func TestRuleset_Evaluate_VulnerabilitySeverity(t *testing.T) {
	cases := []struct {
		name     string
		findings []scan.Finding
		want     Decision
	}{
		{"no findings", nil, DecisionAllow},
		{"within quarantine threshold", []scan.Finding{{ID: "GHSA-1", Severity: scan.SeverityLow}}, DecisionAllow},
		{"above quarantine, within max", []scan.Finding{{ID: "GHSA-2", Severity: scan.SeverityMedium}}, DecisionQuarantine},
		{"above max", []scan.Finding{{ID: "GHSA-3", Severity: scan.SeverityCritical}}, DecisionDeny},
	}

	r := Ruleset{QuarantineSeverity: scan.SeverityLow, MaxSeverity: scan.SeverityHigh}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			a := Artifact{Ecosystem: "npm", Name: "pkg", Version: "1.0.0", Scan: scan.Result{Findings: tc.findings}}

			// Act
			v := r.Evaluate(a, time.Now())

			// Assert
			if v.Decision != tc.want {
				t.Errorf("Decision = %v, want %v", v.Decision, tc.want)
			}
		})
	}
}

func TestRuleset_Evaluate_DeniedLicenseDeniesOutright(t *testing.T) {
	// Arrange
	r := Ruleset{DeniedLicenses: []string{"GPL-3.0-only"}}
	a := Artifact{
		Ecosystem: "npm", Name: "copyleft-pkg", Version: "1.0.0",
		License: license.Result{Raw: "GPL-3.0-only", SPDXID: "GPL-3.0-only"},
	}

	// Act
	v := r.Evaluate(a, time.Now())

	// Assert
	if v.Decision != DecisionDeny {
		t.Errorf("Decision = %v, want DecisionDeny", v.Decision)
	}
}

func TestRuleset_Evaluate_UnnormalizedLicenseIsNeitherDeniedNorApproved(t *testing.T) {
	// Arrange: an ambiguous raw license string that never normalized should
	// not be judged against DeniedLicenses at all (the "don't guess" stance
	// license.NormalizeSPDX itself takes).
	r := Ruleset{DeniedLicenses: []string{"GPL-3.0-only"}}
	a := Artifact{
		Ecosystem: "npm", Name: "ambiguous-pkg", Version: "1.0.0",
		License: license.Result{Raw: "Some Custom License"},
	}

	// Act
	v := r.Evaluate(a, time.Now())

	// Assert
	if v.Decision != DecisionAllow {
		t.Errorf("Decision = %v, want DecisionAllow (unnormalized license skips the rule)", v.Decision)
	}
}

func TestRuleset_Evaluate_PackageAge(t *testing.T) {
	now := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	r := Ruleset{MinPackageAge: 4 * 24 * time.Hour}

	cases := []struct {
		name        string
		publishedAt time.Time
		want        Decision
	}{
		{"unknown publish date skips the rule", time.Time{}, DecisionAllow},
		{"published 1 hour ago, younger than 4 days", now.Add(-1 * time.Hour), DecisionQuarantine},
		{"published 5 days ago, older than 4 days", now.Add(-5 * 24 * time.Hour), DecisionAllow},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Arrange
			a := Artifact{Ecosystem: "npm", Name: "pkg", Version: "1.0.0", PublishedAt: tc.publishedAt}

			// Act
			v := r.Evaluate(a, now)

			// Assert
			if v.Decision != tc.want {
				t.Errorf("Decision = %v, want %v", v.Decision, tc.want)
			}
		})
	}
}

func TestRuleset_Evaluate_WorstDecisionAcrossRulesWins(t *testing.T) {
	// Arrange: a denied license (Deny) alongside a low-severity finding
	// (would be Allow on its own) — the merged Verdict must be Deny.
	r := Ruleset{
		QuarantineSeverity: scan.SeverityLow,
		MaxSeverity:        scan.SeverityHigh,
		DeniedLicenses:     []string{"GPL-3.0-only"},
	}
	a := Artifact{
		Ecosystem: "npm", Name: "pkg", Version: "1.0.0",
		Scan:    scan.Result{Findings: []scan.Finding{{ID: "GHSA-1", Severity: scan.SeverityLow}}},
		License: license.Result{Raw: "GPL-3.0-only", SPDXID: "GPL-3.0-only"},
	}

	// Act
	v := r.Evaluate(a, time.Now())

	// Assert
	if v.Decision != DecisionDeny {
		t.Errorf("Decision = %v, want DecisionDeny (worst of the two rules)", v.Decision)
	}
	if len(v.Reasons) < 2 {
		t.Errorf("Reasons = %v, want at least 2 (one per applicable rule)", v.Reasons)
	}
}

func TestDecisionString(t *testing.T) {
	cases := []struct {
		d    Decision
		want string
	}{
		{DecisionAllow, "ALLOW"},
		{DecisionQuarantine, "QUARANTINE"},
		{DecisionDeny, "DENY"},
	}
	for _, tc := range cases {
		if got := tc.d.String(); got != tc.want {
			t.Errorf("Decision(%d).String() = %q, want %q", tc.d, got, tc.want)
		}
	}
}
