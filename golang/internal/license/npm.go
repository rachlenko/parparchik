package license

import (
	"encoding/json"
	"fmt"
)

type npmLicenseObject struct {
	Type string `json:"type"`
}

type npmPackageJSON struct {
	License  json.RawMessage    `json:"license"`
	Licenses []npmLicenseObject `json:"licenses"`
}

// ParseNPMPackageJSON extracts the declared license from an npm
// package.json's content. Handles the modern form
// (`"license": "<SPDX expression>"`, a bare string — most packages today)
// and the legacy forms npm deprecated but older packages on the registry
// still use: `"license": {"type": "MIT", "url": "..."}` and
// `"licenses": [{"type": "MIT", "url": "..."}, ...]` — for the last form,
// only the first array entry is used, the same "first entry is
// authoritative" choice ParseMavenPOM makes for a dual-licensed pom.xml.
// Does not evaluate a full SPDX expression (e.g. "(MIT OR Apache-2.0)")
// beyond passing it through as Raw — NormalizeSPDX only matches single,
// simple identifiers.
func ParseNPMPackageJSON(data []byte) (Result, error) {
	var pkg npmPackageJSON
	if err := json.Unmarshal(data, &pkg); err != nil {
		return Result{}, fmt.Errorf("license: parse package.json: %w", err)
	}

	if len(pkg.License) > 0 {
		var asString string
		if err := json.Unmarshal(pkg.License, &asString); err == nil && asString != "" {
			return newResult(asString), nil
		}
		var asObject npmLicenseObject
		if err := json.Unmarshal(pkg.License, &asObject); err == nil && asObject.Type != "" {
			return newResult(asObject.Type), nil
		}
	}

	if len(pkg.Licenses) > 0 && pkg.Licenses[0].Type != "" {
		return newResult(pkg.Licenses[0].Type), nil
	}

	return Result{}, nil
}
