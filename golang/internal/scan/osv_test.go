package scan

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestOSVScanner_Scan_NoVulnerabilities(t *testing.T) {
	// Arrange
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{}`))
	}))
	defer srv.Close()

	scanner := NewOSVScannerWithEndpoint(srv.URL, nil)

	// Act
	result, err := scanner.Scan(context.Background(), "npm", "left-pad", "1.3.0")

	// Assert
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if result.Vulnerable() {
		t.Errorf("Vulnerable() = true, want false")
	}
}

func TestOSVScanner_Scan_FindingsParsedWithSeverity(t *testing.T) {
	// Arrange
	var gotBody map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewDecoder(r.Body).Decode(&gotBody)
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{
			"vulns": [
				{"id": "GHSA-aaaa", "summary": "prototype pollution", "database_specific": {"severity": "HIGH"}},
				{"id": "GHSA-bbbb", "summary": "minor issue", "database_specific": {"severity": "LOW"}},
				{"id": "GHSA-cccc", "summary": "unscored issue"}
			]
		}`))
	}))
	defer srv.Close()

	scanner := NewOSVScannerWithEndpoint(srv.URL, nil)

	// Act
	result, err := scanner.Scan(context.Background(), "npm", "vulnerable-pkg", "1.0.0")

	// Assert
	if err != nil {
		t.Fatalf("Scan() error = %v", err)
	}
	if !result.Vulnerable() {
		t.Fatal("Vulnerable() = false, want true")
	}
	if len(result.Findings) != 3 {
		t.Fatalf("len(Findings) = %d, want 3", len(result.Findings))
	}
	if result.MaxSeverity() != SeverityHigh {
		t.Errorf("MaxSeverity() = %v, want %v", result.MaxSeverity(), SeverityHigh)
	}

	pkg, _ := gotBody["package"].(map[string]any)
	if pkg["name"] != "vulnerable-pkg" || pkg["ecosystem"] != "npm" {
		t.Errorf("request package = %+v, want name=vulnerable-pkg ecosystem=npm", pkg)
	}
	if gotBody["version"] != "1.0.0" {
		t.Errorf("request version = %v, want 1.0.0", gotBody["version"])
	}
}

func TestOSVScanner_Scan_UnexpectedStatusIsAnError(t *testing.T) {
	// Arrange
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()

	scanner := NewOSVScannerWithEndpoint(srv.URL, nil)

	// Act
	_, err := scanner.Scan(context.Background(), "npm", "pkg", "1.0.0")

	// Assert
	if err == nil {
		t.Fatal("Scan() error = nil, want an error for a 500")
	}
}

func TestParseOSVSeverity(t *testing.T) {
	cases := []struct {
		raw  string
		want Severity
	}{
		{"LOW", SeverityLow},
		{"low", SeverityLow},
		{"MODERATE", SeverityMedium},
		{"MEDIUM", SeverityMedium},
		{"HIGH", SeverityHigh},
		{"CRITICAL", SeverityCritical},
		{"", SeverityUnknown},
		{"garbage", SeverityUnknown},
	}
	for _, tc := range cases {
		t.Run(tc.raw, func(t *testing.T) {
			// Act
			got := parseOSVSeverity(tc.raw)

			// Assert
			if got != tc.want {
				t.Errorf("parseOSVSeverity(%q) = %v, want %v", tc.raw, got, tc.want)
			}
		})
	}
}
