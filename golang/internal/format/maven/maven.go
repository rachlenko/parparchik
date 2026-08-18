// Package maven implements Maven2-layout coordinate parsing and the
// format.Format wrapper for it.
//
// Implemented: parsing/validating a Maven repository-layout path into its
// groupId/artifactId/version/classifier/extension parts (ParseCoordinate),
// and Route/ParseRoute so this can be registered like any other
// format.Format.
//
// Not implemented (see docs/plans/ Task 14): maven-metadata.xml generation
// (versioning, SNAPSHOT timestamping) and mounting a dedicated HTTP
// sub-router — those need catalog/objectstore wiring beyond what the
// format.Format interface itself covers.
package maven

import "strings"

// Coordinate identifies one Maven artifact.
type Coordinate struct {
	GroupID    string
	ArtifactID string
	Version    string
	Classifier string // empty if none
	Extension  string
}

// ParseCoordinate parses a Maven repository-layout path of the form
// <groupId-as-path>/<artifactId>/<version>/<artifactId>-<version>[-<classifier>].<ext>
// e.g. "com/example/foo/foo-core/1.2.3/foo-core-1.2.3-sources.jar".
func ParseCoordinate(path string) (Coordinate, bool) {
	path = strings.TrimPrefix(path, "/")
	segments := strings.Split(path, "/")
	if len(segments) < 4 {
		return Coordinate{}, false
	}

	file := segments[len(segments)-1]
	version := segments[len(segments)-2]
	artifactID := segments[len(segments)-3]
	groupSegments := segments[:len(segments)-3]

	if version == "" || artifactID == "" || len(groupSegments) == 0 {
		return Coordinate{}, false
	}
	for _, s := range groupSegments {
		if s == "" {
			return Coordinate{}, false
		}
	}
	groupID := strings.Join(groupSegments, ".")

	prefix := artifactID + "-" + version
	if !strings.HasPrefix(file, prefix) {
		return Coordinate{}, false
	}
	rest := strings.TrimPrefix(file, prefix)

	var classifier, extension string
	switch {
	case strings.HasPrefix(rest, "-"):
		rest = strings.TrimPrefix(rest, "-")
		idx := strings.Index(rest, ".")
		if idx <= 0 {
			return Coordinate{}, false
		}
		classifier = rest[:idx]
		extension = rest[idx+1:]
	case strings.HasPrefix(rest, "."):
		candidate := strings.TrimPrefix(rest, ".")
		// Reject e.g. path-version "1.0" against filename "foo-1.0.1.jar":
		// HasPrefix alone would accept "1.jar" as if it were the
		// extension, when "1" is actually leftover version digits — the
		// filename's real version ("1.0.1") just doesn't match the path's
		// version segment. A multi-part extension is fine as long as its
		// first dot-delimited component isn't purely numeric (real
		// extensions like "tar.gz" start with letters).
		if leftover, ok := beforeFirstDot(candidate); ok && isAllDigits(leftover) {
			return Coordinate{}, false
		}
		extension = candidate
	default:
		return Coordinate{}, false
	}
	if extension == "" {
		return Coordinate{}, false
	}

	return Coordinate{
		GroupID:    groupID,
		ArtifactID: artifactID,
		Version:    version,
		Classifier: classifier,
		Extension:  extension,
	}, true
}

// beforeFirstDot returns the portion of s before its first ".", if any.
func beforeFirstDot(s string) (string, bool) {
	idx := strings.Index(s, ".")
	if idx < 0 {
		return "", false
	}
	return s[:idx], true
}

func isAllDigits(s string) bool {
	if s == "" {
		return false
	}
	for _, r := range s {
		if r < '0' || r > '9' {
			return false
		}
	}
	return true
}

// Format is the Maven repository format.
type Format struct{}

// New returns a Maven Format.
func New() Format { return Format{} }

// Name implements format.Format.
func (Format) Name() string { return "maven" }

// Route implements format.Format.
func (Format) Route(bucket, key string) string {
	return "/" + bucket + "/" + key
}

// ParseRoute implements format.Format. It only accepts keys that parse as a
// valid Maven coordinate, which is stricter than generic's bare
// bucket/key split.
func (Format) ParseRoute(route string) (bucket, key string, ok bool) {
	trimmed := strings.TrimPrefix(route, "/")
	b, k, found := strings.Cut(trimmed, "/")
	if !found || b == "" || k == "" {
		return "", "", false
	}
	if _, valid := ParseCoordinate(k); !valid {
		return "", "", false
	}
	return b, k, true
}
