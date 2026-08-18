// Package npm implements npm package/tarball key parsing and the
// format.Format wrapper for it.
//
// Implemented: parsing a registry key into its scoped/unscoped package name
// and, for tarball keys, its version (ParseKey), plus Route/ParseRoute.
//
// Not implemented (see docs/plans/ Task 15): the npm registry metadata API
// (`GET /<pkg>` returning package.json-shaped version listings) and
// mounting a dedicated HTTP sub-router.
package npm

import "strings"

const tarballMarker = "/-/"

// PackageRef identifies an npm package, and optionally a specific tarball
// version of it.
type PackageRef struct {
	Scope   string // without the leading "@"; empty if unscoped
	Name    string
	Version string // empty when this refers to package metadata, not a tarball
}

// FullName returns the conventional "@scope/name" (or bare "name") form.
func (p PackageRef) FullName() string {
	if p.Scope == "" {
		return p.Name
	}
	return "@" + p.Scope + "/" + p.Name
}

// ParseKey parses an npm registry key: either "<name>" / "@scope/name"
// (package metadata) or ".../-/<name>-<version>.tgz" (a tarball).
func ParseKey(key string) (PackageRef, bool) {
	key = strings.TrimPrefix(key, "/")

	var scope string
	if strings.HasPrefix(key, "@") {
		scopePart, rest, found := strings.Cut(key, "/")
		if !found || scopePart == "@" {
			return PackageRef{}, false
		}
		scope = strings.TrimPrefix(scopePart, "@")
		key = rest
	}

	if idx := strings.Index(key, tarballMarker); idx >= 0 {
		name := key[:idx]
		tarball := key[idx+len(tarballMarker):]
		if name == "" || tarball == "" {
			return PackageRef{}, false
		}
		prefix := name + "-"
		if !strings.HasSuffix(tarball, ".tgz") || !strings.HasPrefix(tarball, prefix) {
			return PackageRef{}, false
		}
		version := strings.TrimSuffix(strings.TrimPrefix(tarball, prefix), ".tgz")
		if version == "" {
			return PackageRef{}, false
		}
		return PackageRef{Scope: scope, Name: name, Version: version}, true
	}

	// Bare package name (metadata reference, no tarball marker). Unscoped
	// npm package names never contain "/" — reject anything that does
	// rather than accepting it as a name, which ParseRoute's doc comment
	// ("only accepts keys that parse as a valid npm reference") requires.
	if key == "" || strings.Contains(key, "/") {
		return PackageRef{}, false
	}
	return PackageRef{Scope: scope, Name: key}, true
}

// Format is the npm repository format.
type Format struct{}

// New returns an npm Format.
func New() Format { return Format{} }

// Name implements format.Format.
func (Format) Name() string { return "npm" }

// Route implements format.Format.
func (Format) Route(bucket, key string) string {
	return "/" + bucket + "/" + key
}

// ParseRoute implements format.Format. It only accepts keys that parse as a
// valid npm package/tarball reference.
func (Format) ParseRoute(route string) (bucket, key string, ok bool) {
	trimmed := strings.TrimPrefix(route, "/")
	b, k, found := strings.Cut(trimmed, "/")
	if !found || b == "" || k == "" {
		return "", "", false
	}
	if _, valid := ParseKey(k); !valid {
		return "", "", false
	}
	return b, k, true
}
