// Package license extracts a package's declared license from its
// ecosystem-specific metadata and normalizes it to an SPDX identifier
// where possible.
//
// This is deliberately a set of small, independent adapters — one per
// ecosystem (npm's package.json, PyPI's PKG-INFO/METADATA, Maven's
// pom.xml) — rather than one universal parser, since each ecosystem
// encodes license information in an unrelated file format with its own
// quirks. Not implemented (see docs/plans/ Task 26): adapters for the
// other formats added in Tasks 18-22 (Helm charts declare no license
// field at all in their own metadata; NuGet's .nuspec, Debian's
// debian/copyright, and RPM's spec-file License: tag each need their own
// adapter, added when there's a real need to gate on them); wiring
// results into catalog entry metadata or a policy check (Task 28).
package license

import "strings"

// Result is a detected license for one package.
type Result struct {
	// Raw is the license string as found in the package's metadata, with
	// leading/trailing whitespace trimmed but otherwise unmodified. Empty
	// if no license information was found at all.
	Raw string

	// SPDXID is the SPDX license identifier Raw was normalized to, or
	// empty if NormalizeSPDX didn't recognize it. A non-empty Raw with an
	// empty SPDXID means "a license was declared, but this package
	// couldn't map it to a known SPDX identifier" — still useful
	// information (surface Raw to a human), just not machine-comparable
	// against an SPDX-identifier-based policy.
	SPDXID string
}

// Detected reports whether any license information was found.
func (r Result) Detected() bool { return r.Raw != "" }

// Normalized reports whether Raw was successfully mapped to a known SPDX
// identifier.
func (r Result) Normalized() bool { return r.SPDXID != "" }

func newResult(raw string) Result {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return Result{}
	}
	return Result{Raw: raw, SPDXID: NormalizeSPDX(raw)}
}
