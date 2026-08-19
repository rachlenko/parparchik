package license

import (
	"bufio"
	"bytes"
	"fmt"
	"strings"
)

const classifierLicensePrefix = "Classifier: License :: OSI Approved"

// ParsePythonMetadata extracts the declared license from a PyPI
// PKG-INFO/METADATA file's content — an RFC 822-style header block
// terminated by the first blank line (the long description follows).
// Prefers the "License:" header; falls back to a
// "Classifier: License :: OSI Approved :: <Name>" header when License: is
// absent, empty, or the common placeholder "UNKNOWN" — many packages only
// convey their real license via the Classifier, leaving License: unset or
// generic. A dual-licensed package may declare more than one such
// Classifier line; only the first is used, matching ParseMavenPOM's same
// "first entry is authoritative" choice for a dual-licensed pom.xml.
//
// Not handled: PEP 639 / Core Metadata 2.4's newer "License-Expression:"
// header (an SPDX expression, the modern replacement for the free-text
// License: field). Doesn't false-match here (it doesn't share the
// "License:" prefix), just isn't read yet — a growing blind spot as more
// packages adopt it; add a case for it when that's a real gap in practice.
func ParsePythonMetadata(data []byte) (Result, error) {
	var licenseHeader, classifierLicense string

	scanner := bufio.NewScanner(bytes.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		if line == "" {
			break
		}
		switch {
		case strings.HasPrefix(line, "License:"):
			licenseHeader = strings.TrimSpace(strings.TrimPrefix(line, "License:"))
		case classifierLicense == "" && strings.HasPrefix(line, classifierLicensePrefix):
			rest := strings.TrimPrefix(line, classifierLicensePrefix)
			rest = strings.TrimPrefix(strings.TrimSpace(rest), ":: ")
			if name := strings.TrimSpace(rest); name != "" {
				classifierLicense = name
			}
		}
	}
	if err := scanner.Err(); err != nil {
		return Result{}, fmt.Errorf("license: parse Python metadata: %w", err)
	}

	if licenseHeader != "" && !strings.EqualFold(licenseHeader, "UNKNOWN") {
		return newResult(licenseHeader), nil
	}
	if classifierLicense != "" {
		return newResult(classifierLicense), nil
	}
	return Result{}, nil
}
