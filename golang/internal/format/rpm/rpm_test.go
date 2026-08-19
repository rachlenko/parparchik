package rpm

import "testing"

func TestParsePackageFilename(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		want     PackageRef
		ok       bool
	}{
		{
			name:     "simple",
			filename: "nginx-1.24.0-2.el9.x86_64.rpm",
			want:     PackageRef{Name: "nginx", Version: "1.24.0", Release: "2.el9", Architecture: "x86_64"},
			ok:       true,
		},
		{
			name:     "hyphenated name",
			filename: "nginx-module-njs-1.24.0-1.el9.x86_64.rpm",
			want:     PackageRef{Name: "nginx-module-njs", Version: "1.24.0", Release: "1.el9", Architecture: "x86_64"},
			ok:       true,
		},
		{
			name:     "noarch package",
			filename: "bash-completion-2.11-6.el9.noarch.rpm",
			want:     PackageRef{Name: "bash-completion", Version: "2.11", Release: "6.el9", Architecture: "noarch"},
			ok:       true,
		},
		{"not an rpm", "nginx-1.24.0-2.el9.x86_64.deb", PackageRef{}, false},
		{"too few hyphen segments", "nginx-1.24.0.x86_64.rpm", PackageRef{}, false},
		{"empty", "", PackageRef{}, false},
		{
			// The knownArchitectures allowlist is what makes this correctly
			// rejected instead of misparsing "el9" (the release field's own
			// embedded dot) as if it were the architecture.
			name:     "missing architecture suffix is rejected, not misparsed",
			filename: "nginx-1.24.0-2.el9.rpm",
			want:     PackageRef{},
			ok:       false,
		},
		{"unknown architecture is rejected", "nginx-1.24.0-2.el9.riscv64.rpm", PackageRef{}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got, ok := ParsePackageFilename(tc.filename)

			// Assert
			if ok != tc.ok {
				t.Fatalf("ParsePackageFilename(%q) ok = %v, want %v (got %+v)", tc.filename, ok, tc.ok, got)
			}
			if ok && got != tc.want {
				t.Errorf("ParsePackageFilename(%q) = %+v, want %+v", tc.filename, got, tc.want)
			}
		})
	}
}

func TestFormat_RouteAndParseRoute(t *testing.T) {
	f := New()

	if got := f.Name(); got != "rpm" {
		t.Errorf("Name() = %q, want rpm", got)
	}

	key := "packages/nginx-1.24.0-2.el9.x86_64.rpm"
	route := f.Route("yum-repo", key)
	if want := "/yum-repo/" + key; route != want {
		t.Errorf("Route() = %q, want %q", route, want)
	}

	bucket, gotKey, ok := f.ParseRoute(route)
	if !ok || bucket != "yum-repo" || gotKey != key {
		t.Errorf("ParseRoute(%q) = (%q, %q, %v), want (yum-repo, %q, true)", route, bucket, gotKey, ok, key)
	}

	if _, _, ok := f.ParseRoute("/yum-repo/repodata/repomd.xml"); ok {
		t.Error("ParseRoute() accepted a key that isn't a valid RPM filename")
	}
}
