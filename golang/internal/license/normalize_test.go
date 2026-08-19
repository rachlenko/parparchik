package license

import "testing"

func TestNormalizeSPDX(t *testing.T) {
	cases := []struct {
		raw  string
		want string
	}{
		{"MIT", "MIT"},
		{"mit", "MIT"},
		{"  MIT  ", "MIT"},
		{"MIT License", "MIT"},
		{"Apache-2.0", "Apache-2.0"},
		{"Apache License, Version 2.0", "Apache-2.0"},
		{"Apache   License,   Version   2.0", "Apache-2.0"}, // internal whitespace collapsed
		{"ISC", "ISC"},
		{"GPL-3.0", "GPL-3.0-only"},
		{"GNU General Public License v3.0", "GPL-3.0-only"},
		// Regression: the canonical, already-current SPDX identifiers must
		// normalize to themselves, not just their deprecated bare aliases.
		{"GPL-3.0-only", "GPL-3.0-only"},
		{"GPL-3.0-or-later", "GPL-3.0-or-later"},
		{"GPL-2.0-only", "GPL-2.0-only"},
		{"LGPL-3.0-only", "LGPL-3.0-only"},
		{"LGPL-2.1-only", "LGPL-2.1-only"},
		{"BSD", ""},           // ambiguous, deliberately not normalized
		{"Public Domain", ""}, // not an SPDX identifier
		{"", ""},
		{"SomeTotallyUnknownLicense-9.9", ""},
		{"Unlicense", "Unlicense"},
		// Regression: npm's "UNLICENSED" means the opposite of SPDX's
		// "Unlicense" (proprietary/no-rights-granted vs. maximally
		// permissive public-domain dedication) and must NOT normalize to
		// it, case-insensitively.
		{"UNLICENSED", ""},
		{"unlicensed", ""},
	}

	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			// Act
			got := NormalizeSPDX(tc.raw)

			// Assert
			if got != tc.want {
				t.Errorf("NormalizeSPDX(%q) = %q, want %q", tc.raw, got, tc.want)
			}
		})
	}
}

func TestResult_DetectedAndNormalized(t *testing.T) {
	cases := []struct {
		name           string
		result         Result
		wantDetected   bool
		wantNormalized bool
	}{
		{"empty result", Result{}, false, false},
		{"raw only, unrecognized", Result{Raw: "SomeCustomLicense"}, true, false},
		{"raw and normalized", Result{Raw: "MIT", SPDXID: "MIT"}, true, true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act / Assert
			if got := tc.result.Detected(); got != tc.wantDetected {
				t.Errorf("Detected() = %v, want %v", got, tc.wantDetected)
			}
			if got := tc.result.Normalized(); got != tc.wantNormalized {
				t.Errorf("Normalized() = %v, want %v", got, tc.wantNormalized)
			}
		})
	}
}
