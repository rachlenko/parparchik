package sbom

import (
	"encoding/json"
	"testing"

	"github.com/rachlenko/parparchik/golang/internal/license"
	"github.com/rachlenko/parparchik/golang/internal/scan"
)

func TestGenerate_BasicDocumentShape(t *testing.T) {
	// Arrange
	components := []Component{
		{Name: "lodash", Version: "4.17.21", PURL: "pkg:npm/lodash@4.17.21"},
	}

	// Act
	body, err := Generate(components)

	// Assert
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	var doc document
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("Generate() produced invalid JSON: %v", err)
	}
	if doc.BOMFormat != "CycloneDX" {
		t.Errorf("bomFormat = %q, want CycloneDX", doc.BOMFormat)
	}
	if doc.SpecVersion != "1.5" {
		t.Errorf("specVersion = %q, want 1.5", doc.SpecVersion)
	}
	if len(doc.Components) != 1 {
		t.Fatalf("len(components) = %d, want 1", len(doc.Components))
	}
	comp := doc.Components[0]
	if comp.Name != "lodash" || comp.Version != "4.17.21" || comp.PURL != "pkg:npm/lodash@4.17.21" {
		t.Errorf("component = %+v, want name=lodash version=4.17.21 purl=pkg:npm/lodash@4.17.21", comp)
	}
	if comp.BOMRef != "pkg:npm/lodash@4.17.21" {
		t.Errorf("bom-ref = %q, want the PURL when one is set", comp.BOMRef)
	}
}

func TestGenerate_ComponentWithNoLicenseOrFindingsOmitsThoseFields(t *testing.T) {
	// Arrange
	components := []Component{{Name: "unknown-pkg", Version: "1.0.0"}}

	// Act
	body, err := Generate(components)

	// Assert
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	var doc document
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("Generate() produced invalid JSON: %v", err)
	}
	if doc.Components[0].Licenses != nil {
		t.Errorf("Licenses = %+v, want nil (omitted, no license info)", doc.Components[0].Licenses)
	}
	if doc.Vulnerabilities != nil {
		t.Errorf("Vulnerabilities = %+v, want nil (omitted, no findings)", doc.Vulnerabilities)
	}
}

func TestGenerate_NormalizedLicenseUsesSPDXID(t *testing.T) {
	// Arrange
	components := []Component{{
		Name: "left-pad", Version: "1.3.0",
		License: license.Result{Raw: "MIT", SPDXID: "MIT"},
	}}

	// Act
	body, err := Generate(components)

	// Assert
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	var doc document
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	got := doc.Components[0].Licenses
	if len(got) != 1 || got[0].License.ID != "MIT" || got[0].License.Name != "" {
		t.Errorf("Licenses = %+v, want a single entry with ID=MIT and no Name", got)
	}
}

func TestGenerate_UnnormalizedLicenseUsesRawName(t *testing.T) {
	// Arrange
	components := []Component{{
		Name: "custom-pkg", Version: "1.0.0",
		License: license.Result{Raw: "My Custom License", SPDXID: ""},
	}}

	// Act
	body, err := Generate(components)

	// Assert
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	var doc document
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	got := doc.Components[0].Licenses
	if len(got) != 1 || got[0].License.Name != "My Custom License" || got[0].License.ID != "" {
		t.Errorf("Licenses = %+v, want a single entry with Name=\"My Custom License\" and no ID", got)
	}
}

func TestGenerate_FindingsBecomeVulnerabilitiesAffectingTheComponent(t *testing.T) {
	// Arrange
	components := []Component{{
		Name: "vulnerable-pkg", Version: "1.0.0", PURL: "pkg:npm/vulnerable-pkg@1.0.0",
		Findings: []scan.Finding{
			{ID: "GHSA-aaaa", Summary: "prototype pollution", Severity: scan.SeverityHigh},
		},
	}}

	// Act
	body, err := Generate(components)

	// Assert
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	var doc document
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if len(doc.Vulnerabilities) != 1 {
		t.Fatalf("len(Vulnerabilities) = %d, want 1", len(doc.Vulnerabilities))
	}
	vuln := doc.Vulnerabilities[0]
	if vuln.ID != "GHSA-aaaa" || vuln.Description != "prototype pollution" {
		t.Errorf("vuln = %+v, want ID=GHSA-aaaa Description=\"prototype pollution\"", vuln)
	}
	if len(vuln.Ratings) != 1 || vuln.Ratings[0].Severity != "high" {
		t.Errorf("Ratings = %+v, want a single \"high\" rating", vuln.Ratings)
	}
	if len(vuln.Affects) != 1 || vuln.Affects[0].Ref != "pkg:npm/vulnerable-pkg@1.0.0" {
		t.Errorf("Affects = %+v, want it to reference the component's PURL", vuln.Affects)
	}
}

func TestGenerate_BomRefFallsBackToSynthesizedRefWithoutPURL(t *testing.T) {
	// Arrange
	components := []Component{{Name: "no-purl-pkg", Version: "2.0.0"}}

	// Act
	body, err := Generate(components)

	// Assert
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}
	var doc document
	if err := json.Unmarshal(body, &doc); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	if doc.Components[0].BOMRef == "" {
		t.Error("bom-ref is empty, want a synthesized fallback ref")
	}
}

func TestIngest_RoundTripsGeneratedDocument(t *testing.T) {
	// Arrange
	original := []Component{
		{Name: "lodash", Version: "4.17.21", PURL: "pkg:npm/lodash@4.17.21", License: license.Result{Raw: "MIT", SPDXID: "MIT"}},
		{Name: "custom-pkg", Version: "1.0.0", License: license.Result{Raw: "My Custom License"}},
		{Name: "no-license-pkg", Version: "0.1.0"},
	}
	body, err := Generate(original)
	if err != nil {
		t.Fatalf("Generate() error = %v", err)
	}

	// Act
	got, err := Ingest(body)

	// Assert
	if err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}
	if len(got) != len(original) {
		t.Fatalf("len(Ingest()) = %d, want %d", len(got), len(original))
	}
	for i, want := range original {
		if got[i].Name != want.Name || got[i].Version != want.Version || got[i].PURL != want.PURL {
			t.Errorf("component[%d] = %+v, want name/version/purl matching %+v", i, got[i], want)
		}
		if got[i].License != want.License {
			t.Errorf("component[%d].License = %+v, want %+v", i, got[i].License, want.License)
		}
	}
}

func TestIngest_EmptyComponentsDocument(t *testing.T) {
	// Arrange
	empty := `{"bomFormat": "CycloneDX", "specVersion": "1.5", "version": 1}`

	// Act
	got, err := Ingest([]byte(empty))

	// Assert
	if err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}
	if len(got) != 0 {
		t.Errorf("len(Ingest()) = %d, want 0 for a document with no components field", len(got))
	}
}

func TestIngest_MultiLicenseArrayKeepsOnlyFirstEntry(t *testing.T) {
	// Arrange
	doc := `{
		"bomFormat": "CycloneDX", "specVersion": "1.5", "version": 1,
		"components": [{
			"type": "library", "bom-ref": "dual-licensed@1.0.0",
			"name": "dual-licensed", "version": "1.0.0",
			"licenses": [
				{"license": {"id": "MIT"}},
				{"license": {"id": "Apache-2.0"}}
			]
		}]
	}`

	// Act
	got, err := Ingest([]byte(doc))

	// Assert
	if err != nil {
		t.Fatalf("Ingest() error = %v", err)
	}
	if len(got) != 1 {
		t.Fatalf("len(Ingest()) = %d, want 1", len(got))
	}
	if got[0].License.SPDXID != "MIT" {
		t.Errorf("License.SPDXID = %q, want MIT (first entry only, Apache-2.0 dropped)", got[0].License.SPDXID)
	}
}

func TestIngest_RejectsNonCycloneDXDocument(t *testing.T) {
	// Arrange
	notCycloneDX := `{"bomFormat": "SPDX", "specVersion": "2.3"}`

	// Act
	_, err := Ingest([]byte(notCycloneDX))

	// Assert
	if err == nil {
		t.Fatal("Ingest() error = nil, want an error for a non-CycloneDX bomFormat")
	}
}

func TestIngest_RejectsMalformedJSON(t *testing.T) {
	// Act
	_, err := Ingest([]byte(`not json`))

	// Assert
	if err == nil {
		t.Fatal("Ingest() error = nil, want an error for malformed JSON")
	}
}

func TestSeverityString(t *testing.T) {
	cases := []struct {
		sev  scan.Severity
		want string
	}{
		{scan.SeverityUnknown, "unknown"},
		{scan.SeverityLow, "low"},
		{scan.SeverityMedium, "medium"},
		{scan.SeverityHigh, "high"},
		{scan.SeverityCritical, "critical"},
	}
	for _, tc := range cases {
		if got := severityString(tc.sev); got != tc.want {
			t.Errorf("severityString(%v) = %q, want %q", tc.sev, got, tc.want)
		}
	}
}
