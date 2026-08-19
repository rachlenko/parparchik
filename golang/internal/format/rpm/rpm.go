// Package rpm implements RPM package filename parsing, plus the
// format.Format wrapper for it.
//
// Implemented: ParsePackageFilename and Route/ParseRoute.
//
// Not implemented (see docs/plans/ Task 21): repodata generation
// (repomd.xml, primary.xml.gz — the yum/dnf repository index) and
// mounting a dedicated HTTP sub-router.
package rpm

import "strings"

// PackageRef identifies one RPM package.
type PackageRef struct {
	Name         string
	Version      string
	Release      string
	Architecture string
}

// knownArchitectures is the set of architecture tags this parser accepts
// as the filename's final "."-delimited segment. Validating against this
// (rather than accepting whatever the last "." happens to precede) is what
// distinguishes a genuine architecture suffix from a release field's own
// embedded dot (e.g. "2.el9") in a filename that's missing its
// architecture suffix entirely — see ParsePackageFilename's doc comment.
// RPM's architecture set is small and changes rarely; extend as needed.
var knownArchitectures = map[string]bool{
	"x86_64": true, "aarch64": true, "noarch": true, "i686": true, "i386": true,
	"armv7hl": true, "ppc64le": true, "ppc64": true, "s390x": true, "src": true,
}

// ParsePackageFilename parses an RPM filename of the form
// "<name>-<version>-<release>.<arch>.rpm", e.g.
// "nginx-1.24.0-2.el9.x86_64.rpm". RPM's own naming convention fixes the
// last two "-"-delimited segments as version and release (release itself
// commonly embeds a "." before the distro tag, e.g. "2.el9" — that's kept
// as part of Release rather than split further, matching how RPM tooling
// treats the release field as opaque), then the last "."-delimited segment
// of what remains as the architecture — validated against
// knownArchitectures, since without that check a filename missing its
// architecture suffix entirely (e.g. a truncated "nginx-1.24.0-2.el9.rpm")
// would otherwise "successfully" misread the release field's own embedded
// dot ("2.el9") as arch="el9". The name is everything before that, and may
// itself contain hyphens.
//
// Residual known limitation: a real, unlisted architecture tag (or a
// distro release tag that coincidentally collides with a listed one, e.g.
// a hypothetical "el.src" release) will still be misparsed or rejected;
// this is a fixed allowlist, not a full RPM header parse.
func ParsePackageFilename(filename string) (PackageRef, bool) {
	base, ok := strings.CutSuffix(filename, ".rpm")
	if !ok {
		return PackageRef{}, false
	}

	archIdx := strings.LastIndex(base, ".")
	if archIdx <= 0 || archIdx == len(base)-1 {
		return PackageRef{}, false
	}
	arch := base[archIdx+1:]
	if !knownArchitectures[arch] {
		return PackageRef{}, false
	}
	rest := base[:archIdx]

	parts := strings.Split(rest, "-")
	if len(parts) < 3 {
		return PackageRef{}, false
	}
	release := parts[len(parts)-1]
	version := parts[len(parts)-2]
	name := strings.Join(parts[:len(parts)-2], "-")
	if name == "" || version == "" || release == "" {
		return PackageRef{}, false
	}
	return PackageRef{Name: name, Version: version, Release: release, Architecture: arch}, true
}

// Format is the RPM (yum/dnf) repository format.
type Format struct{}

// New returns an RPM Format.
func New() Format { return Format{} }

// Name implements format.Format.
func (Format) Name() string { return "rpm" }

// Route implements format.Format.
func (Format) Route(bucket, key string) string {
	return "/" + bucket + "/" + key
}

// ParseRoute implements format.Format. It only accepts keys whose final
// path segment parses as a valid RPM filename.
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
