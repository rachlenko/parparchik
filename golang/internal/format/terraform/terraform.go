// Package terraform implements path parsing for the Terraform Registry
// Protocol (https://developer.hashicorp.com/terraform/internals/provider-registry-protocol
// and .../module-registry-protocol): provider version listing/download and
// module version listing/download paths.
//
// This package deliberately does NOT implement format.Format, for the same
// reason internal/format/docker doesn't: the registry protocol's paths
// live under global "/v1/providers/..." and "/v1/modules/..." namespaces
// rather than the bucket-prefixed "/<bucket>/<key>" shape every other
// format here uses. Mounting Terraform registry support for real needs a
// dedicated sub-router built on these primitives (see docs/plans/ Task
// 22), not a format.Format implementation.
//
// Not implemented: the actual JSON response bodies the protocol requires
// (version lists, download metadata with SHA256SUMS/signature URLs) and
// mounting a dedicated HTTP sub-router.
package terraform

import "strings"

const (
	providersPrefix = "v1/providers/"
	modulesPrefix   = "v1/modules/"
)

// ProviderRef identifies a Terraform provider.
type ProviderRef struct {
	Namespace string
	Type      string
}

// ParseProviderVersionsPath parses
// "/v1/providers/{namespace}/{type}/versions".
func ParseProviderVersionsPath(path string) (ProviderRef, bool) {
	segments, ok := trimPrefixSegments(path, providersPrefix)
	if !ok || len(segments) != 3 || segments[2] != "versions" {
		return ProviderRef{}, false
	}
	if segments[0] == "" || segments[1] == "" {
		return ProviderRef{}, false
	}
	return ProviderRef{Namespace: segments[0], Type: segments[1]}, true
}

// ProviderDownloadRef identifies one platform-specific provider version
// package.
type ProviderDownloadRef struct {
	ProviderRef
	Version string
	OS      string
	Arch    string
}

// ParseProviderDownloadPath parses
// "/v1/providers/{namespace}/{type}/{version}/download/{os}/{arch}".
func ParseProviderDownloadPath(path string) (ProviderDownloadRef, bool) {
	segments, ok := trimPrefixSegments(path, providersPrefix)
	if !ok || len(segments) != 6 || segments[3] != "download" {
		return ProviderDownloadRef{}, false
	}
	for _, s := range []string{segments[0], segments[1], segments[2], segments[4], segments[5]} {
		if s == "" {
			return ProviderDownloadRef{}, false
		}
	}
	return ProviderDownloadRef{
		ProviderRef: ProviderRef{Namespace: segments[0], Type: segments[1]},
		Version:     segments[2],
		OS:          segments[4],
		Arch:        segments[5],
	}, true
}

// ModuleRef identifies a Terraform module for a specific target system
// (provider), e.g. namespace="hashicorp", name="consul", system="aws".
type ModuleRef struct {
	Namespace string
	Name      string
	System    string
}

// ParseModuleVersionsPath parses
// "/v1/modules/{namespace}/{name}/{system}/versions".
func ParseModuleVersionsPath(path string) (ModuleRef, bool) {
	segments, ok := trimPrefixSegments(path, modulesPrefix)
	if !ok || len(segments) != 4 || segments[3] != "versions" {
		return ModuleRef{}, false
	}
	if segments[0] == "" || segments[1] == "" || segments[2] == "" {
		return ModuleRef{}, false
	}
	return ModuleRef{Namespace: segments[0], Name: segments[1], System: segments[2]}, true
}

// ModuleDownloadRef identifies one module version's download.
type ModuleDownloadRef struct {
	ModuleRef
	Version string
}

// ParseModuleDownloadPath parses
// "/v1/modules/{namespace}/{name}/{system}/{version}/download".
func ParseModuleDownloadPath(path string) (ModuleDownloadRef, bool) {
	segments, ok := trimPrefixSegments(path, modulesPrefix)
	if !ok || len(segments) != 5 || segments[4] != "download" {
		return ModuleDownloadRef{}, false
	}
	if segments[0] == "" || segments[1] == "" || segments[2] == "" || segments[3] == "" {
		return ModuleDownloadRef{}, false
	}
	return ModuleDownloadRef{
		ModuleRef: ModuleRef{Namespace: segments[0], Name: segments[1], System: segments[2]},
		Version:   segments[3],
	}, true
}

// trimPrefixSegments strips a leading "/" and the given prefix, then splits
// the remainder on "/". ok is false if path doesn't start with prefix.
func trimPrefixSegments(path, prefix string) ([]string, bool) {
	path = strings.TrimPrefix(path, "/")
	rest, ok := strings.CutPrefix(path, prefix)
	if !ok {
		return nil, false
	}
	return strings.Split(rest, "/"), true
}
