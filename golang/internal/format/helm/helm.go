// Package helm implements Helm chart package filename parsing and the
// format.Format wrapper for it.
//
// Implemented: ParseChartFilename and Route/ParseRoute.
//
// Not implemented (see docs/plans/ Task 18): index.yaml generation (the
// chart repository index Helm clients fetch to discover available
// charts/versions) and mounting a dedicated HTTP sub-router.
package helm

import "strings"

// ChartRef identifies one packaged Helm chart.
type ChartRef struct {
	Name    string
	Version string
}

// ParseChartFilename parses a Helm chart package filename of the form
// "<name>-<version>.tgz", e.g. "nginx-ingress-4.10.1.tgz" or
// "myapp-1.2.3-rc.1.tgz". Helm chart names may themselves contain hyphens,
// so — same ambiguity as Maven/PyPI filenames, and resolved the same way
// tooling in that ecosystem does — the version is taken to start at the
// first "-"-delimited segment that begins with a digit.
func ParseChartFilename(filename string) (ChartRef, bool) {
	base, ok := strings.CutSuffix(filename, ".tgz")
	if !ok {
		return ChartRef{}, false
	}

	parts := strings.Split(base, "-")
	if len(parts) < 2 {
		return ChartRef{}, false
	}

	versionIdx := -1
	for i := 1; i < len(parts); i++ { // i=0 is always part of the name
		if p := parts[i]; p != "" && p[0] >= '0' && p[0] <= '9' {
			versionIdx = i
			break
		}
	}
	if versionIdx < 0 {
		return ChartRef{}, false
	}

	name := strings.Join(parts[:versionIdx], "-")
	version := strings.Join(parts[versionIdx:], "-")
	if name == "" || version == "" {
		return ChartRef{}, false
	}
	return ChartRef{Name: name, Version: version}, true
}

// Format is the Helm chart repository format.
type Format struct{}

// New returns a Helm Format.
func New() Format { return Format{} }

// Name implements format.Format.
func (Format) Name() string { return "helm" }

// Route implements format.Format.
func (Format) Route(bucket, key string) string {
	return "/" + bucket + "/" + key
}

// ParseRoute implements format.Format. It only accepts keys whose final
// path segment parses as a valid Helm chart package filename.
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
	if _, valid := ParseChartFilename(filename); !valid {
		return "", "", false
	}
	return b, k, true
}
