// Package debian implements Debian/APT .deb package filename parsing and
// pool-path layout, plus the format.Format wrapper for them.
//
// Implemented: ParsePackageFilename, PoolPath, and Route/ParseRoute.
//
// Not implemented (see docs/plans/ Task 20): Release/Packages(.gz) index
// generation and mounting a dedicated HTTP sub-router.
package debian

import "strings"

// PackageRef identifies one .deb package.
type PackageRef struct {
	Name         string
	Version      string
	Architecture string
}

// ParsePackageFilename parses a .deb filename of the form
// "<name>_<version>_<architecture>.deb", e.g.
// "nginx_1.24.0-2ubuntu7_amd64.deb". Unlike Maven/npm/Helm/NuGet, Debian's
// own naming convention already delimits name/version/architecture with
// underscores, so there's no name-vs-version ambiguity to resolve — this
// is a much simpler split than the other formats' filename parsers.
func ParsePackageFilename(filename string) (PackageRef, bool) {
	base, ok := strings.CutSuffix(filename, ".deb")
	if !ok {
		return PackageRef{}, false
	}

	parts := strings.Split(base, "_")
	if len(parts) != 3 {
		return PackageRef{}, false
	}
	name, version, arch := parts[0], parts[1], parts[2]
	if name == "" || version == "" || arch == "" {
		return PackageRef{}, false
	}
	return PackageRef{Name: name, Version: version, Architecture: arch}, true
}

// PoolPath returns the conventional APT "pool" layout path for a package,
// e.g. pool/main/n/nginx/nginx_1.24.0-2ubuntu7_amd64.deb — bucketed by the
// first letter of the package name (or "lib<x>" for packages starting with
// "lib", APT's own long-standing convention to avoid one giant "l/"
// directory).
func PoolPath(component string, ref PackageRef) string {
	letter := poolLetter(ref.Name)
	filename := ref.Name + "_" + ref.Version + "_" + ref.Architecture + ".deb"
	return strings.Join([]string{"pool", component, letter, ref.Name, filename}, "/")
}

func poolLetter(name string) string {
	if strings.HasPrefix(name, "lib") && len(name) > 3 {
		return name[:4]
	}
	if name == "" {
		return ""
	}
	return name[:1]
}

// Format is the Debian/APT repository format.
type Format struct{}

// New returns a Debian Format.
func New() Format { return Format{} }

// Name implements format.Format.
func (Format) Name() string { return "debian" }

// Route implements format.Format.
func (Format) Route(bucket, key string) string {
	return "/" + bucket + "/" + key
}

// ParseRoute implements format.Format. It only accepts keys whose final
// path segment parses as a valid .deb filename.
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
