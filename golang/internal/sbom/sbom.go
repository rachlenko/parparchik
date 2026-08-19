// Package sbom generates and ingests CycloneDX Software Bill of Materials
// documents (https://cyclonedx.org/), combining catalog artifact identity
// with license.Result (Task 26) and scan.Finding (Task 25) data.
//
// CycloneDX, not SPDX: the plan allowed either; CycloneDX's JSON schema is
// simpler to both produce and round-trip for the component+license+
// vulnerability shape this project needs, and it's the format OSV.dev
// (already the scan package's data source) and most container/package
// registry tooling emit natively.
//
// Implemented: Generate (build a document from components this process
// already knows about) and Ingest (parse an externally-generated
// document's components back out — a "was this already scanned/licensed"
// read path, not a stored-SBOM cache).
//
// Not implemented (see docs/plans/ Task 27): a GET /sbom HTTP endpoint,
// and storing an ingested or generated SBOM alongside an artifact's
// catalog/manifest entry — this service has no upload endpoint yet (the
// same limitation noted in internal/scan and internal/license), so there's
// no request path to wire either into.
package sbom

import (
	"encoding/json"
	"fmt"

	"github.com/rachlenko/parparchik/golang/internal/license"
	"github.com/rachlenko/parparchik/golang/internal/scan"
)

const (
	bomFormat   = "CycloneDX"
	specVersion = "1.5"
)

// Component describes one artifact to include in a generated SBOM, or one
// artifact recovered from an ingested SBOM.
type Component struct {
	Name    string
	Version string

	// PURL is the component's package URL
	// (https://github.com/package-url/purl-spec), e.g.
	// "pkg:npm/lodash@4.17.21". Optional — omitted from the generated
	// document, and used as the vulnerability-affects reference in place
	// of a synthesized one, when non-empty.
	PURL string

	// License is the zero value when no license information is known for
	// this component — Generate then omits CycloneDX's "licenses" field
	// entirely rather than emitting an empty one.
	License license.Result

	// Findings is empty when no vulnerability scan has been run for this
	// component.
	Findings []scan.Finding
}

type document struct {
	BOMFormat       string             `json:"bomFormat"`
	SpecVersion     string             `json:"specVersion"`
	Version         int                `json:"version"`
	Components      []cdxComponent     `json:"components,omitempty"`
	Vulnerabilities []cdxVulnerability `json:"vulnerabilities,omitempty"`
}

type cdxComponent struct {
	Type     string             `json:"type"`
	BOMRef   string             `json:"bom-ref"`
	Name     string             `json:"name"`
	Version  string             `json:"version"`
	PURL     string             `json:"purl,omitempty"`
	Licenses []cdxLicenseChoice `json:"licenses,omitempty"`
}

type cdxLicenseChoice struct {
	License cdxLicense `json:"license"`
}

// cdxLicense mirrors CycloneDX's licenseChoice shape: ID is set when the
// license is a known SPDX identifier, Name otherwise — never both, per the
// CycloneDX schema.
type cdxLicense struct {
	ID   string `json:"id,omitempty"`
	Name string `json:"name,omitempty"`
}

type cdxVulnerability struct {
	ID          string        `json:"id"`
	Source      cdxVulnSource `json:"source,omitempty"`
	Ratings     []cdxRating   `json:"ratings,omitempty"`
	Description string        `json:"description,omitempty"`
	Affects     []cdxAffects  `json:"affects"`
}

// cdxVulnSource has no omitempty on Name: cdxVulnerability.Source is a
// non-pointer struct field, so an empty cdxVulnSource still always
// serializes as {"source":{}} regardless — omitempty is only meaningful on
// pointer/slice/map/string fields, not on a struct-typed field itself. It
// isn't needed anyway: Generate always sets Source.Name to "OSV".
type cdxVulnSource struct {
	Name string `json:"name"`
}

type cdxRating struct {
	Severity string `json:"severity"`
}

type cdxAffects struct {
	Ref string `json:"ref"`
}

// Generate builds a CycloneDX JSON SBOM document for the given components.
func Generate(components []Component) ([]byte, error) {
	doc := document{BOMFormat: bomFormat, SpecVersion: specVersion, Version: 1}

	for i, c := range components {
		ref := bomRef(c, i)

		comp := cdxComponent{Type: "library", BOMRef: ref, Name: c.Name, Version: c.Version, PURL: c.PURL}
		if c.License.Detected() {
			entry := cdxLicense{Name: c.License.Raw}
			if c.License.Normalized() {
				entry = cdxLicense{ID: c.License.SPDXID}
			}
			comp.Licenses = []cdxLicenseChoice{{License: entry}}
		}
		doc.Components = append(doc.Components, comp)

		for _, f := range c.Findings {
			doc.Vulnerabilities = append(doc.Vulnerabilities, cdxVulnerability{
				ID:          f.ID,
				Source:      cdxVulnSource{Name: "OSV"},
				Ratings:     []cdxRating{{Severity: severityString(f.Severity)}},
				Description: f.Summary,
				Affects:     []cdxAffects{{Ref: ref}},
			})
		}
	}

	body, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("sbom: encode document: %w", err)
	}
	return body, nil
}

// Ingest parses an externally-generated SBOM document and recovers its
// components (name, version, purl, license — not vulnerabilities, which
// CycloneDX associates with components only indirectly via "affects"
// references and which this package has no need to round-trip yet). Only
// CycloneDX is supported; a well-formed JSON document with a different (or
// missing) bomFormat is rejected rather than silently misparsed.
//
// A component with more than one license entry (a real shape tools like
// syft/cdxgen emit for dual-licensed packages) has every entry but the
// first silently dropped — license.Result models a single license per
// component (Task 26's scope), so there's nowhere to put the rest.
//
// Round-trip caveat: Ingest(Generate(x)) does not always reproduce
// x.License.Raw exactly. CycloneDX's licenseChoice is id-XOR-name — when
// License.Normalized() was true, Generate emits only {id: SPDXID} and
// necessarily discards the original Raw string (see licenseFromCDX below,
// which reconstructs Raw from the id on the way back). This only matters
// when Raw and SPDXID differ textually (e.g. Raw "MIT License" normalizing
// to SPDXID "MIT") — the common case for anything that went through
// internal/license's alias table.
func Ingest(data []byte) ([]Component, error) {
	var doc document
	if err := json.Unmarshal(data, &doc); err != nil {
		return nil, fmt.Errorf("sbom: parse document: %w", err)
	}
	if doc.BOMFormat != bomFormat {
		return nil, fmt.Errorf("sbom: unsupported bomFormat %q, want %q", doc.BOMFormat, bomFormat)
	}

	components := make([]Component, 0, len(doc.Components))
	for _, c := range doc.Components {
		comp := Component{Name: c.Name, Version: c.Version, PURL: c.PURL}
		if len(c.Licenses) > 0 {
			comp.License = licenseFromCDX(c.Licenses[0].License)
		}
		components = append(components, comp)
	}
	return components, nil
}

// licenseFromCDX reconstructs a license.Result from a CycloneDX license
// entry. When the entry carries an explicit SPDX id, that's trusted
// directly as SPDXID rather than re-derived by re-running NormalizeSPDX
// against whatever ends up in Raw — a producer that already emitted a
// structured {id: ...} has already done that classification.
//
// Trust caveat: because Ingest's whole purpose is parsing "externally
// generated" documents, that id is NOT itself validated against a known
// SPDX license list here — an adversarial or simply buggy external SBOM
// can set id to an arbitrary string and have it accepted as a
// machine-trustworthy SPDXID. A future consumer that treats
// Result.Normalized()/SPDXID from an Ingest'd document as carrying the
// same validation guarantee NormalizeSPDX's own output does (e.g. Task
// 28's policy engine, gating on SPDXID) needs to account for this —
// either re-validate id against a real SPDX license list, or only trust
// SPDXID this way for self-generated (not externally ingested) documents.
func licenseFromCDX(entry cdxLicense) license.Result {
	if entry.ID != "" {
		raw := entry.Name
		if raw == "" {
			raw = entry.ID
		}
		return license.Result{Raw: raw, SPDXID: entry.ID}
	}
	return license.Result{Raw: entry.Name, SPDXID: license.NormalizeSPDX(entry.Name)}
}

func bomRef(c Component, index int) string {
	if c.PURL != "" {
		return c.PURL
	}
	return fmt.Sprintf("component-%d-%s@%s", index, c.Name, c.Version)
}

func severityString(s scan.Severity) string {
	switch s {
	case scan.SeverityLow:
		return "low"
	case scan.SeverityMedium:
		return "medium"
	case scan.SeverityHigh:
		return "high"
	case scan.SeverityCritical:
		return "critical"
	default:
		return "unknown"
	}
}
