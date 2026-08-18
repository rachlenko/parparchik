// Package generic implements the flat, bucket/key file-routing format that
// parparchik has always supported — the direct Go equivalent of the Lua
// implementation's routing behavior.
package generic

import "strings"

// Format is the generic repository format: routes are literally
// /<bucket>/<key>.
type Format struct{}

// New returns a generic Format.
func New() Format { return Format{} }

// Name implements format.Format.
func (Format) Name() string { return "generic" }

// Route implements format.Format.
func (Format) Route(bucket, key string) string {
	return "/" + bucket + "/" + key
}

// ParseRoute implements format.Format. It splits the first path segment off
// as the bucket name and treats the remainder as the key.
func (Format) ParseRoute(route string) (bucket, key string, ok bool) {
	trimmed := strings.TrimPrefix(route, "/")
	bucket, key, found := strings.Cut(trimmed, "/")
	if !found || bucket == "" || key == "" {
		return "", "", false
	}
	return bucket, key, true
}
