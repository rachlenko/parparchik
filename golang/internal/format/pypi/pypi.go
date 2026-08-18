// Package pypi implements PyPI project-name normalization (PEP 503) and
// distribution-filename parsing, plus the format.Format wrapper for them.
//
// Implemented: NormalizeName, ParseFilename, and Route/ParseRoute.
//
// Not implemented (see docs/plans/ Task 16): the PEP 503 simple index and
// PEP 691 JSON API responses, and mounting a dedicated HTTP sub-router.
package pypi

import (
	"regexp"
	"strings"
)

var normalizeRunRe = regexp.MustCompile(`[-_.]+`)

// NormalizeName applies PEP 503 project-name normalization: lowercase, with
// runs of "-", "_", "." collapsed to a single "-".
func NormalizeName(name string) string {
	return strings.ToLower(normalizeRunRe.ReplaceAllString(name, "-"))
}

var distExtensions = []string{".tar.gz", ".whl", ".zip"}

// ParseFilename extracts the (unnormalized) project name and version from a
// PyPI-style distribution filename, e.g. "requests-2.31.0.tar.gz" (sdist)
// or "requests-2.31.0-py3-none-any.whl" (wheel).
func ParseFilename(filename string) (name, version string, ok bool) {
	var ext string
	for _, e := range distExtensions {
		if strings.HasSuffix(filename, e) {
			ext = e
			break
		}
	}
	if ext == "" {
		return "", "", false
	}
	base := strings.TrimSuffix(filename, ext)

	if ext == ".whl" {
		// <name>-<version>-<python tag>-<abi tag>-<platform tag>.whl
		parts := strings.Split(base, "-")
		if len(parts) < 5 || parts[0] == "" || parts[1] == "" {
			return "", "", false
		}
		return parts[0], parts[1], true
	}

	// sdist: <name>-<version>; name may itself contain hyphens, so the
	// version is the last "-"-separated segment.
	idx := strings.LastIndex(base, "-")
	if idx <= 0 || idx == len(base)-1 {
		return "", "", false
	}
	return base[:idx], base[idx+1:], true
}

// Format is the PyPI repository format.
type Format struct{}

// New returns a PyPI Format.
func New() Format { return Format{} }

// Name implements format.Format.
func (Format) Name() string { return "pypi" }

// Route implements format.Format.
func (Format) Route(bucket, key string) string {
	return "/" + bucket + "/" + key
}

// ParseRoute implements format.Format. It only accepts keys whose final
// path segment parses as a valid distribution filename.
func (Format) ParseRoute(route string) (bucket, key string, ok bool) {
	trimmed := strings.TrimPrefix(route, "/")
	b, k, found := strings.Cut(trimmed, "/")
	if !found || b == "" || k == "" {
		return "", "", false
	}
	filename := k
	if idx := strings.LastIndex(k, "/"); idx >= 0 {
		filename = k[idx+1:]
	}
	if _, _, valid := ParseFilename(filename); !valid {
		return "", "", false
	}
	return b, k, true
}
