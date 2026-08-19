package debian

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
			filename: "nginx_1.24.0-2ubuntu7_amd64.deb",
			want:     PackageRef{Name: "nginx", Version: "1.24.0-2ubuntu7", Architecture: "amd64"},
			ok:       true,
		},
		{
			name:     "arch-independent",
			filename: "bash-completion_2.11-6_all.deb",
			want:     PackageRef{Name: "bash-completion", Version: "2.11-6", Architecture: "all"},
			ok:       true,
		},
		{"not a deb", "nginx_1.24.0_amd64.rpm", PackageRef{}, false},
		{"missing architecture", "nginx_1.24.0.deb", PackageRef{}, false},
		{"too many segments", "nginx_1.24.0_amd64_extra.deb", PackageRef{}, false},
		{"empty segment", "nginx__amd64.deb", PackageRef{}, false},
		{"empty", "", PackageRef{}, false},
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

func TestPoolPath(t *testing.T) {
	cases := []struct {
		name      string
		component string
		ref       PackageRef
		want      string
	}{
		{
			name:      "regular package",
			component: "main",
			ref:       PackageRef{Name: "nginx", Version: "1.24.0-2ubuntu7", Architecture: "amd64"},
			want:      "pool/main/n/nginx/nginx_1.24.0-2ubuntu7_amd64.deb",
		},
		{
			name:      "lib-prefixed package uses a 4-char bucket",
			component: "main",
			ref:       PackageRef{Name: "libssl3", Version: "3.0.2-0ubuntu1", Architecture: "amd64"},
			want:      "pool/main/libs/libssl3/libssl3_3.0.2-0ubuntu1_amd64.deb",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got := PoolPath(tc.component, tc.ref)

			// Assert
			if got != tc.want {
				t.Errorf("PoolPath() = %q, want %q", got, tc.want)
			}
		})
	}
}

func TestFormat_RouteAndParseRoute(t *testing.T) {
	f := New()

	if got := f.Name(); got != "debian" {
		t.Errorf("Name() = %q, want debian", got)
	}

	key := "pool/main/n/nginx/nginx_1.24.0-2ubuntu7_amd64.deb"
	route := f.Route("apt-repo", key)
	if want := "/apt-repo/" + key; route != want {
		t.Errorf("Route() = %q, want %q", route, want)
	}

	bucket, gotKey, ok := f.ParseRoute(route)
	if !ok || bucket != "apt-repo" || gotKey != key {
		t.Errorf("ParseRoute(%q) = (%q, %q, %v), want (apt-repo, %q, true)", route, bucket, gotKey, ok, key)
	}

	if _, _, ok := f.ParseRoute("/apt-repo/Release"); ok {
		t.Error("ParseRoute() accepted a key that isn't a valid .deb filename")
	}
}
