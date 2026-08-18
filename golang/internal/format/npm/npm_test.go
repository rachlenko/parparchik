package npm

import "testing"

func TestParseKey(t *testing.T) {
	cases := []struct {
		name string
		key  string
		want PackageRef
		ok   bool
	}{
		{"unscoped metadata", "lodash", PackageRef{Name: "lodash"}, true},
		{"unscoped tarball", "lodash/-/lodash-4.17.21.tgz", PackageRef{Name: "lodash", Version: "4.17.21"}, true},
		{"scoped metadata", "@types/node", PackageRef{Scope: "types", Name: "node"}, true},
		{"scoped tarball", "@types/node/-/node-20.11.0.tgz", PackageRef{Scope: "types", Name: "node", Version: "20.11.0"}, true},
		{"leading slash", "/lodash", PackageRef{Name: "lodash"}, true},
		{"empty", "", PackageRef{}, false},
		{"bare @", "@", PackageRef{}, false},
		{"tarball name mismatch", "lodash/-/other-4.17.21.tgz", PackageRef{}, false},
		{"tarball missing version", "lodash/-/lodash-.tgz", PackageRef{}, false},
		{"tarball not tgz", "lodash/-/lodash-4.17.21.tar.gz", PackageRef{}, false},
		{"empty tarball segment", "lodash/-/", PackageRef{}, false},
		{"slash in bare name is rejected", "somepkg/versions/1.0.0", PackageRef{}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got, ok := ParseKey(tc.key)

			// Assert
			if ok != tc.ok {
				t.Fatalf("ParseKey(%q) ok = %v, want %v (got %+v)", tc.key, ok, tc.ok, got)
			}
			if ok && got != tc.want {
				t.Errorf("ParseKey(%q) = %+v, want %+v", tc.key, got, tc.want)
			}
		})
	}
}

func TestPackageRef_FullName(t *testing.T) {
	if got := (PackageRef{Name: "lodash"}).FullName(); got != "lodash" {
		t.Errorf("FullName() = %q, want lodash", got)
	}
	if got := (PackageRef{Scope: "types", Name: "node"}).FullName(); got != "@types/node" {
		t.Errorf("FullName() = %q, want @types/node", got)
	}
}

func TestFormat_RouteAndParseRoute(t *testing.T) {
	f := New()

	if got := f.Name(); got != "npm" {
		t.Errorf("Name() = %q, want npm", got)
	}

	key := "@types/node/-/node-20.11.0.tgz"
	route := f.Route("npm-registry", key)
	if want := "/npm-registry/" + key; route != want {
		t.Errorf("Route() = %q, want %q", route, want)
	}

	bucket, gotKey, ok := f.ParseRoute(route)
	if !ok || bucket != "npm-registry" || gotKey != key {
		t.Errorf("ParseRoute(%q) = (%q, %q, %v), want (npm-registry, %q, true)", route, bucket, gotKey, ok, key)
	}

	if _, _, ok := f.ParseRoute("/npm-registry/@"); ok {
		t.Error("ParseRoute() accepted an invalid npm key")
	}
}
