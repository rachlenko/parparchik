package policy

import (
	"testing"
	"time"
)

func TestFindWaiver(t *testing.T) {
	now := time.Date(2026, 8, 19, 0, 0, 0, 0, time.UTC)

	cases := []struct {
		name    string
		waivers []Waiver
		want    bool
	}{
		{
			name:    "exact ecosystem+name+version match",
			waivers: []Waiver{{Ecosystem: "npm", Name: "left-pad", Version: "1.3.0"}},
			want:    true,
		},
		{
			name:    "version-wildcard waiver matches any version",
			waivers: []Waiver{{Ecosystem: "npm", Name: "left-pad"}},
			want:    true,
		},
		{
			name:    "different name does not match",
			waivers: []Waiver{{Ecosystem: "npm", Name: "other-pkg"}},
			want:    false,
		},
		{
			name:    "different ecosystem does not match",
			waivers: []Waiver{{Ecosystem: "pypi", Name: "left-pad"}},
			want:    false,
		},
		{
			name:    "specific-version waiver does not match a different version",
			waivers: []Waiver{{Ecosystem: "npm", Name: "left-pad", Version: "1.0.0"}},
			want:    false,
		},
		{
			name:    "expired waiver does not match",
			waivers: []Waiver{{Ecosystem: "npm", Name: "left-pad", ExpiresAt: now.Add(-time.Hour)}},
			want:    false,
		},
		{
			name:    "waiver expiring at exactly now still matches",
			waivers: []Waiver{{Ecosystem: "npm", Name: "left-pad", ExpiresAt: now}},
			want:    true,
		},
		{
			name:    "waiver expiring in the future matches",
			waivers: []Waiver{{Ecosystem: "npm", Name: "left-pad", ExpiresAt: now.Add(time.Hour)}},
			want:    true,
		},
		{
			name:    "no waivers configured",
			waivers: nil,
			want:    false,
		},
	}

	a := Artifact{Ecosystem: "npm", Name: "left-pad", Version: "1.3.0"}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			_, ok := findWaiver(tc.waivers, a, now)

			// Assert
			if ok != tc.want {
				t.Errorf("findWaiver() ok = %v, want %v", ok, tc.want)
			}
		})
	}
}

func TestFindWaiver_FirstMatchInSliceOrderWins(t *testing.T) {
	// Arrange
	waivers := []Waiver{
		{Ecosystem: "npm", Name: "left-pad", Version: "1.3.0", ApprovedBy: "exact-version-waiver"},
		{Ecosystem: "npm", Name: "left-pad", ApprovedBy: "wildcard-waiver"},
	}
	a := Artifact{Ecosystem: "npm", Name: "left-pad", Version: "1.3.0"}

	// Act
	got, ok := findWaiver(waivers, a, time.Now())

	// Assert
	if !ok {
		t.Fatal("findWaiver() ok = false, want true")
	}
	if got.ApprovedBy != "exact-version-waiver" {
		t.Errorf("ApprovedBy = %q, want the first matching waiver in slice order", got.ApprovedBy)
	}
}
