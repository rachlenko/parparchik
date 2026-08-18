package scan

import "testing"

func TestPolicy_Evaluate(t *testing.T) {
	cases := []struct {
		name        string
		policy      Policy
		result      Result
		wantAllowed bool
	}{
		{
			name:        "no findings always allowed",
			policy:      Policy{MaxSeverity: SeverityLow},
			result:      Result{},
			wantAllowed: true,
		},
		{
			name:        "finding at or below max severity allowed",
			policy:      Policy{MaxSeverity: SeverityHigh},
			result:      Result{Findings: []Finding{{ID: "A", Severity: SeverityMedium}}},
			wantAllowed: true,
		},
		{
			name:        "finding above max severity denied",
			policy:      Policy{MaxSeverity: SeverityMedium},
			result:      Result{Findings: []Finding{{ID: "A", Severity: SeverityCritical}}},
			wantAllowed: false,
		},
		{
			name:        "worst of multiple findings determines outcome",
			policy:      Policy{MaxSeverity: SeverityMedium},
			result:      Result{Findings: []Finding{{ID: "A", Severity: SeverityLow}, {ID: "B", Severity: SeverityHigh}}},
			wantAllowed: false,
		},
		{
			name:        "unknown severity finding allowed under a zero-value policy",
			policy:      Policy{}, // MaxSeverity defaults to SeverityUnknown
			result:      Result{Findings: []Finding{{ID: "A", Severity: SeverityUnknown}}},
			wantAllowed: true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			decision := tc.policy.Evaluate(tc.result)

			// Assert
			if decision.Allowed != tc.wantAllowed {
				t.Errorf("Evaluate() Allowed = %v, want %v (reason: %s)", decision.Allowed, tc.wantAllowed, decision.Reason)
			}
			if decision.Reason == "" {
				t.Error("Evaluate() Reason is empty, want an explanation")
			}
		})
	}
}

func TestSeverity_String(t *testing.T) {
	cases := []struct {
		sev  Severity
		want string
	}{
		{SeverityUnknown, "UNKNOWN"},
		{SeverityLow, "LOW"},
		{SeverityMedium, "MEDIUM"},
		{SeverityHigh, "HIGH"},
		{SeverityCritical, "CRITICAL"},
	}
	for _, tc := range cases {
		if got := tc.sev.String(); got != tc.want {
			t.Errorf("Severity(%d).String() = %q, want %q", tc.sev, got, tc.want)
		}
	}
}
