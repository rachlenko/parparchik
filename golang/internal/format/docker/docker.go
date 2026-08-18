// Package docker implements OCI Distribution Spec v2 path parsing:
// "/v2/<name>/manifests/<reference>" and "/v2/<name>/blobs/<digest>".
//
// This package deliberately does NOT implement format.Format. OCI
// Distribution routes are not bucket-prefixed the way Format.Route/
// ParseRoute assumes (every other format here maps onto "/<bucket>/<key>");
// they live under a single global "/v2/" namespace where <name> itself can
// contain slashes (e.g. "library/nginx"). Forcing that into the
// bucket+key shape would either misparse real Docker registry paths or
// silently redefine what "bucket" means for this one format. Mounting
// Docker support for real needs a dedicated "/v2/" sub-router in
// cmd/parparchik (see docs/plans/ Task 17), not a format.Format
// implementation — these parsing primitives are what that sub-router
// would build on.
package docker

import (
	"regexp"
	"strings"
)

const (
	v2Prefix      = "v2/"
	manifestsPart = "/manifests/"
	blobsPart     = "/blobs/"
)

var digestRe = regexp.MustCompile(`^[a-z0-9]+(?:[.+_-][a-z0-9]+)*:[a-fA-F0-9]{32,}$`)

// IsDigest reports whether s looks like an OCI content digest, e.g.
// "sha256:e3b0c4...".
func IsDigest(s string) bool {
	return digestRe.MatchString(s)
}

// ManifestRef identifies a manifest by repository name and reference (a tag
// or a digest).
type ManifestRef struct {
	Name      string
	Reference string
}

// ParseManifestPath parses "/v2/<name>/manifests/<reference>".
func ParseManifestPath(path string) (ManifestRef, bool) {
	name, ref, ok := parseV2Path(path, manifestsPart)
	if !ok {
		return ManifestRef{}, false
	}
	return ManifestRef{Name: name, Reference: ref}, true
}

// BlobRef identifies a content-addressed blob by repository name and
// digest.
type BlobRef struct {
	Name   string
	Digest string
}

// ParseBlobPath parses "/v2/<name>/blobs/<digest>". The digest must look
// like a valid OCI digest (ParseManifestPath does not require this of its
// reference, since a manifest reference may legitimately be a tag).
func ParseBlobPath(path string) (BlobRef, bool) {
	name, digest, ok := parseV2Path(path, blobsPart)
	if !ok || !IsDigest(digest) {
		return BlobRef{}, false
	}
	return BlobRef{Name: name, Digest: digest}, true
}

func parseV2Path(path, marker string) (name, value string, ok bool) {
	path = strings.TrimPrefix(path, "/")
	if !strings.HasPrefix(path, v2Prefix) {
		return "", "", false
	}
	rest := strings.TrimPrefix(path, v2Prefix)

	idx := strings.LastIndex(rest, marker)
	if idx <= 0 {
		return "", "", false
	}
	name = rest[:idx]
	value = rest[idx+len(marker):]
	if name == "" || value == "" {
		return "", "", false
	}
	return name, value, true
}
