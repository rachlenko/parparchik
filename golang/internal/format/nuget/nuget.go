// Package nuget implements NuGet package filename parsing and the
// format.Format wrapper for it.
//
// Implemented: ParsePackageFilename and Route/ParseRoute.
//
// Not implemented (see docs/plans/ Task 19): the NuGet V3 API (service
// index, package base address resource, registration resource) and
// mounting a dedicated HTTP sub-router.
package nuget

import "strings"

// PackageRef identifies one NuGet package.
type PackageRef struct {
	ID      string
	Version string
}

// ParsePackageFilename parses a NuGet package filename of the form
// "<id>.<version>.nupkg", e.g. "Newtonsoft.Json.13.0.3.nupkg" or
// "MyCompany.MyPackage.1.0.0-beta.1.nupkg". Unlike Maven/npm/Helm, NuGet
// separates id from version with "." rather than "-", and both the id and
// the version may themselves contain dots (the id via namespacing, the
// version via semver + prerelease labels) — the split point is the first
// "."-delimited segment that begins with a digit, matching how NuGet
// itself parses its own package filenames.
//
// Known limitation: an id segment that itself starts with a digit (e.g.
// "Company.2FA.Library") would be misparsed as the start of the version.
// NuGet's own tooling avoids this by validating the candidate version
// against the full SemVer grammar rather than "starts with a digit"; not
// implemented here — flag if this format sees real traffic with such ids.
func ParsePackageFilename(filename string) (PackageRef, bool) {
	base, ok := strings.CutSuffix(filename, ".nupkg")
	if !ok {
		return PackageRef{}, false
	}

	parts := strings.Split(base, ".")
	if len(parts) < 2 {
		return PackageRef{}, false
	}

	versionIdx := -1
	for i := 1; i < len(parts); i++ { // i=0 is always part of the id
		if p := parts[i]; p != "" && p[0] >= '0' && p[0] <= '9' {
			versionIdx = i
			break
		}
	}
	if versionIdx < 0 {
		return PackageRef{}, false
	}

	id := strings.Join(parts[:versionIdx], ".")
	version := strings.Join(parts[versionIdx:], ".")
	if id == "" || version == "" {
		return PackageRef{}, false
	}
	return PackageRef{ID: id, Version: version}, true
}

// Format is the NuGet repository format.
type Format struct{}

// New returns a NuGet Format.
func New() Format { return Format{} }

// Name implements format.Format.
func (Format) Name() string { return "nuget" }

// Route implements format.Format.
func (Format) Route(bucket, key string) string {
	return "/" + bucket + "/" + key
}

// ParseRoute implements format.Format. It only accepts keys whose final
// path segment parses as a valid NuGet package filename.
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
	if _, valid := ParsePackageFilename(filename); !valid {
		return "", "", false
	}
	return b, k, true
}
