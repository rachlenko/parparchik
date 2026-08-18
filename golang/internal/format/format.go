// Package format is the extension seam for repository formats.
//
// Today parparchik only understands one format: a flat, generic file
// registry addressed by an arbitrary key (internal/format/generic). The
// Format interface exists so that Maven, npm, PyPI, Docker (OCI
// Distribution), Helm, NuGet, Debian (APT), RPM (yum), Terraform (provider
// registry protocol), and ML-model repositories can each be added later as
// a self-contained package implementing this interface, without any change
// to internal/catalog, internal/objectstore, or internal/resolver.
//
// A format package is expected to own:
//   - its own path/route scheme (Route / ParseRoute below)
//   - any format-specific index documents it needs to serve
//     (e.g. Maven's maven-metadata.xml, npm's package.json responses,
//     Debian's Packages/Release files, RPM's repodata) — built on top of
//     objectstore.Store and catalog.Catalog, not by replacing them
//   - its own HTTP sub-router, mounted by cmd/parparchik alongside the
//     generic httpapi routes
//
// See docs/plans/ for the per-format task breakdown.
package format

// Format describes how a repository format maps stored objects to
// public-facing routes.
type Format interface {
	// Name identifies the format, e.g. "generic", "maven", "npm".
	Name() string

	// Route returns the public-facing path for an object stored under key
	// in the given bucket.
	Route(bucket, key string) string

	// ParseRoute extracts the bucket and key implied by an incoming
	// request path, when the format can resolve it directly from the path
	// alone (e.g. generic's /<bucket>/<key>). ok is false when the format
	// needs additional context (an index lookup, a virtual prefix) to
	// resolve the route, which the caller must then supply.
	ParseRoute(route string) (bucket, key string, ok bool)
}

// Registry maps a format name to its implementation, so cmd/parparchik can
// wire up whichever formats are enabled for a given deployment.
type Registry struct {
	formats map[string]Format
}

// NewRegistry creates an empty format Registry.
func NewRegistry() *Registry {
	return &Registry{formats: make(map[string]Format)}
}

// Register adds f to the registry, keyed by f.Name().
func (r *Registry) Register(f Format) {
	r.formats[f.Name()] = f
}

// Get looks up a format by name.
func (r *Registry) Get(name string) (Format, bool) {
	f, ok := r.formats[name]
	return f, ok
}
