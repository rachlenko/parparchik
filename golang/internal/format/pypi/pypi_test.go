package pypi

import "testing"

func TestNormalizeName(t *testing.T) {
	cases := []struct{ name, want string }{
		{"Friendly-Bard", "friendly-bard"},
		{"Friendly.Bard", "friendly-bard"},
		{"FRIENDLY_BARD", "friendly-bard"},
		{"friendly--bard", "friendly-bard"},
		{"friendly.-_bard", "friendly-bard"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got := NormalizeName(tc.name)

			// Assert
			if got != tc.want {
				t.Errorf("NormalizeName(%q) = %q, want %q", tc.name, got, tc.want)
			}
		})
	}
}

func TestParseFilename(t *testing.T) {
	cases := []struct {
		name        string
		filename    string
		wantName    string
		wantVersion string
		ok          bool
	}{
		{"sdist tar.gz", "requests-2.31.0.tar.gz", "requests", "2.31.0", true},
		{"sdist with hyphenated name", "my-cool-package-1.0.0.tar.gz", "my-cool-package", "1.0.0", true},
		{"wheel", "requests-2.31.0-py3-none-any.whl", "requests", "2.31.0", true},
		{"zip sdist", "requests-2.31.0.zip", "requests", "2.31.0", true},
		{"unsupported extension", "requests-2.31.0.egg", "", "", false},
		{"no version separator", "requests.tar.gz", "", "", false},
		{"malformed wheel", "requests-2.31.0.whl", "", "", false},
		{"empty", "", "", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			name, version, ok := ParseFilename(tc.filename)

			// Assert
			if ok != tc.ok {
				t.Fatalf("ParseFilename(%q) ok = %v, want %v", tc.filename, ok, tc.ok)
			}
			if ok && (name != tc.wantName || version != tc.wantVersion) {
				t.Errorf("ParseFilename(%q) = (%q, %q), want (%q, %q)", tc.filename, name, version, tc.wantName, tc.wantVersion)
			}
		})
	}
}

func TestFormat_RouteAndParseRoute(t *testing.T) {
	f := New()

	if got := f.Name(); got != "pypi" {
		t.Errorf("Name() = %q, want pypi", got)
	}

	key := "packages/requests-2.31.0.tar.gz"
	route := f.Route("pypi-mirror", key)
	if want := "/pypi-mirror/" + key; route != want {
		t.Errorf("Route() = %q, want %q", route, want)
	}

	bucket, gotKey, ok := f.ParseRoute(route)
	if !ok || bucket != "pypi-mirror" || gotKey != key {
		t.Errorf("ParseRoute(%q) = (%q, %q, %v), want (pypi-mirror, %q, true)", route, bucket, gotKey, ok, key)
	}

	if _, _, ok := f.ParseRoute("/pypi-mirror/not-a-distribution-file"); ok {
		t.Error("ParseRoute() accepted a key that isn't a valid distribution filename")
	}
}
