package policy

import (
	"strings"
	"testing"
	"time"

	"github.com/rachlenko/parparchik/golang/internal/license"
	"github.com/rachlenko/parparchik/golang/internal/scan"
)

func TestEngine_Evaluate_AllowedArtifactIsRecordedUnwaived(t *testing.T) {
	// Arrange
	audit := NewAuditLog()
	engine := &Engine{Rules: Ruleset{MaxSeverity: scan.SeverityHigh}, Audit: audit}
	a := Artifact{Ecosystem: "npm", Name: "left-pad", Version: "1.3.0"}

	// Act
	v := engine.Evaluate(a)

	// Assert
	if v.Decision != DecisionAllow {
		t.Errorf("Decision = %v, want DecisionAllow", v.Decision)
	}
	entries := audit.Entries()
	if len(entries) != 1 {
		t.Fatalf("len(Entries()) = %d, want 1", len(entries))
	}
	if entries[0].Waived {
		t.Error("Waived = true, want false for an artifact no waiver applies to")
	}
}

func TestEngine_Evaluate_DeniedArtifactWithoutWaiverStaysDenied(t *testing.T) {
	// Arrange
	engine := &Engine{Rules: Ruleset{DeniedLicenses: []string{"GPL-3.0-only"}}}
	a := Artifact{
		Ecosystem: "npm", Name: "copyleft-pkg", Version: "1.0.0",
		License: license.Result{Raw: "GPL-3.0-only", SPDXID: "GPL-3.0-only"},
	}

	// Act
	v := engine.Evaluate(a)

	// Assert
	if v.Decision != DecisionDeny {
		t.Errorf("Decision = %v, want DecisionDeny", v.Decision)
	}
}

func TestEngine_Evaluate_MatchingWaiverForcesAllowAndIsRecorded(t *testing.T) {
	// Arrange
	audit := NewAuditLog()
	engine := &Engine{
		Rules:   Ruleset{DeniedLicenses: []string{"GPL-3.0-only"}},
		Waivers: []Waiver{{Ecosystem: "npm", Name: "copyleft-pkg", Reason: "legal reviewed and approved", ApprovedBy: "legal-team"}},
		Audit:   audit,
	}
	a := Artifact{
		Ecosystem: "npm", Name: "copyleft-pkg", Version: "1.0.0",
		License: license.Result{Raw: "GPL-3.0-only", SPDXID: "GPL-3.0-only"},
	}

	// Act
	v := engine.Evaluate(a)

	// Assert
	if v.Decision != DecisionAllow {
		t.Errorf("Decision = %v, want DecisionAllow (waived)", v.Decision)
	}
	found := false
	for _, r := range v.Reasons {
		if strings.Contains(r, "waived by legal-team") {
			found = true
		}
	}
	if !found {
		t.Errorf("Reasons = %v, want one mentioning the waiver approver", v.Reasons)
	}
	entries := audit.Entries()
	if len(entries) != 1 || !entries[0].Waived {
		t.Errorf("Entries() = %+v, want a single entry with Waived=true", entries)
	}
}

func TestEngine_Evaluate_ExpiredWaiverDoesNotApply(t *testing.T) {
	// Arrange
	fixedNow := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)
	engine := &Engine{
		Rules:   Ruleset{DeniedLicenses: []string{"GPL-3.0-only"}},
		Waivers: []Waiver{{Ecosystem: "npm", Name: "copyleft-pkg", ExpiresAt: fixedNow.Add(-time.Hour)}},
		Clock:   func() time.Time { return fixedNow },
	}
	a := Artifact{
		Ecosystem: "npm", Name: "copyleft-pkg", Version: "1.0.0",
		License: license.Result{Raw: "GPL-3.0-only", SPDXID: "GPL-3.0-only"},
	}

	// Act
	v := engine.Evaluate(a)

	// Assert
	if v.Decision != DecisionDeny {
		t.Errorf("Decision = %v, want DecisionDeny (waiver expired)", v.Decision)
	}
}

func TestEngine_Evaluate_NilAuditIsSafe(t *testing.T) {
	// Arrange
	engine := &Engine{Rules: Ruleset{MaxSeverity: scan.SeverityHigh}}
	a := Artifact{Ecosystem: "npm", Name: "pkg", Version: "1.0.0"}

	// Act / Assert: must not panic with Audit left nil
	engine.Evaluate(a)
}

func TestEngine_Evaluate_WaiverIsNotAppliedToAnAlreadyAllowedArtifact(t *testing.T) {
	// Arrange: a waiver present for a package that the rules already allow
	// should not itself flip the audit entry's Waived flag.
	audit := NewAuditLog()
	engine := &Engine{
		Rules:   Ruleset{MaxSeverity: scan.SeverityHigh},
		Waivers: []Waiver{{Ecosystem: "npm", Name: "clean-pkg"}},
		Audit:   audit,
	}
	a := Artifact{Ecosystem: "npm", Name: "clean-pkg", Version: "1.0.0"}

	// Act
	engine.Evaluate(a)

	// Assert
	entries := audit.Entries()
	if len(entries) != 1 || entries[0].Waived {
		t.Errorf("Entries() = %+v, want a single entry with Waived=false", entries)
	}
}
