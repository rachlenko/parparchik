package docker

import "testing"

func TestParseManifestPath(t *testing.T) {
	cases := []struct {
		name string
		path string
		want ManifestRef
		ok   bool
	}{
		{"simple name, tag", "/v2/nginx/manifests/latest", ManifestRef{Name: "nginx", Reference: "latest"}, true},
		{"namespaced name", "/v2/library/nginx/manifests/1.27", ManifestRef{Name: "library/nginx", Reference: "1.27"}, true},
		{"digest reference", "/v2/nginx/manifests/sha256:" + hash64, ManifestRef{Name: "nginx", Reference: "sha256:" + hash64}, true},
		{"missing v2 prefix", "/nginx/manifests/latest", ManifestRef{}, false},
		{"missing reference", "/v2/nginx/manifests/", ManifestRef{}, false},
		{"missing name", "/v2/manifests/latest", ManifestRef{}, false},
		{"not a manifests path", "/v2/nginx/blobs/sha256:" + hash64, ManifestRef{}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got, ok := ParseManifestPath(tc.path)

			// Assert
			if ok != tc.ok {
				t.Fatalf("ParseManifestPath(%q) ok = %v, want %v", tc.path, ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Errorf("ParseManifestPath(%q) = %+v, want %+v", tc.path, got, tc.want)
			}
		})
	}
}

func TestParseBlobPath(t *testing.T) {
	cases := []struct {
		name string
		path string
		want BlobRef
		ok   bool
	}{
		{"valid digest", "/v2/nginx/blobs/sha256:" + hash64, BlobRef{Name: "nginx", Digest: "sha256:" + hash64}, true},
		{"namespaced name", "/v2/library/nginx/blobs/sha256:" + hash64, BlobRef{Name: "library/nginx", Digest: "sha256:" + hash64}, true},
		{"reference is a tag, not a digest", "/v2/nginx/blobs/latest", BlobRef{}, false},
		{"not a blobs path", "/v2/nginx/manifests/sha256:" + hash64, BlobRef{}, false},
		{"missing name", "/v2/blobs/sha256:" + hash64, BlobRef{}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got, ok := ParseBlobPath(tc.path)

			// Assert
			if ok != tc.ok {
				t.Fatalf("ParseBlobPath(%q) ok = %v, want %v", tc.path, ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Errorf("ParseBlobPath(%q) = %+v, want %+v", tc.path, got, tc.want)
			}
		})
	}
}

func TestIsDigest(t *testing.T) {
	if !IsDigest("sha256:" + hash64) {
		t.Error("IsDigest() = false for a well-formed sha256 digest")
	}
	if IsDigest("latest") {
		t.Error("IsDigest() = true for a plain tag")
	}
	if IsDigest("sha256:tooshort") {
		t.Error("IsDigest() = true for a too-short hex digest")
	}
}

// hash64 is a syntactically valid (though not cryptographically meaningful)
// 64-hex-character digest body, reused across test cases.
const hash64 = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b85"
