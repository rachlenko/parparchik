package nuget

import "testing"

func TestParsePackageFilename(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		want     PackageRef
		ok       bool
	}{
		{"simple", "MyPackage.1.0.0.nupkg", PackageRef{ID: "MyPackage", Version: "1.0.0"}, true},
		{"dotted id", "Newtonsoft.Json.13.0.3.nupkg", PackageRef{ID: "Newtonsoft.Json", Version: "13.0.3"}, true},
		{"prerelease version", "MyPackage.1.0.0-beta.1.nupkg", PackageRef{ID: "MyPackage", Version: "1.0.0-beta.1"}, true},
		{"multi-word dotted id with prerelease", "MyCompany.MyPackage.2.0.0-rc.1.nupkg", PackageRef{ID: "MyCompany.MyPackage", Version: "2.0.0-rc.1"}, true},
		{"not a nupkg", "MyPackage.1.0.0.zip", PackageRef{}, false},
		{"no version segment", "MyPackage.nupkg", PackageRef{}, false},
		{"id only, no digits anywhere", "MyPackage.latest.nupkg", PackageRef{}, false},
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

func TestFormat_RouteAndParseRoute(t *testing.T) {
	f := New()

	if got := f.Name(); got != "nuget" {
		t.Errorf("Name() = %q, want nuget", got)
	}

	key := "packages/Newtonsoft.Json.13.0.3.nupkg"
	route := f.Route("nuget-feed", key)
	if want := "/nuget-feed/" + key; route != want {
		t.Errorf("Route() = %q, want %q", route, want)
	}

	bucket, gotKey, ok := f.ParseRoute(route)
	if !ok || bucket != "nuget-feed" || gotKey != key {
		t.Errorf("ParseRoute(%q) = (%q, %q, %v), want (nuget-feed, %q, true)", route, bucket, gotKey, ok, key)
	}

	if _, _, ok := f.ParseRoute("/nuget-feed/index.json"); ok {
		t.Error("ParseRoute() accepted a key that isn't a valid NuGet package filename")
	}
}
