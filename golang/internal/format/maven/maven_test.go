package maven

import "testing"

func TestParseCoordinate(t *testing.T) {
	cases := []struct {
		name string
		path string
		want Coordinate
		ok   bool
	}{
		{
			name: "simple jar",
			path: "com/example/foo/foo-core/1.2.3/foo-core-1.2.3.jar",
			want: Coordinate{GroupID: "com.example.foo", ArtifactID: "foo-core", Version: "1.2.3", Extension: "jar"},
			ok:   true,
		},
		{
			name: "with classifier",
			path: "com/example/foo/foo-core/1.2.3/foo-core-1.2.3-sources.jar",
			want: Coordinate{GroupID: "com.example.foo", ArtifactID: "foo-core", Version: "1.2.3", Classifier: "sources", Extension: "jar"},
			ok:   true,
		},
		{
			name: "leading slash",
			path: "/org/apache/commons/commons-lang3/3.14.0/commons-lang3-3.14.0.pom",
			want: Coordinate{GroupID: "org.apache.commons", ArtifactID: "commons-lang3", Version: "3.14.0", Extension: "pom"},
			ok:   true,
		},
		{
			name: "snapshot version",
			path: "com/example/foo/1.0.0-SNAPSHOT/foo-1.0.0-SNAPSHOT.jar",
			want: Coordinate{GroupID: "com.example", ArtifactID: "foo", Version: "1.0.0-SNAPSHOT", Extension: "jar"},
			ok:   true,
		},
		{"too few segments", "foo/1.0/foo-1.0.jar", Coordinate{}, false},
		{"file does not match artifact-version prefix", "com/example/foo/1.0/bar-1.0.jar", Coordinate{}, false},
		{"no extension", "com/example/foo/1.0/foo-1.0", Coordinate{}, false},
		{"empty group segment", "com//foo/1.0/foo-1.0.jar", Coordinate{}, false},
		{"classifier with no extension", "com/example/foo/1.0/foo-1.0-sources", Coordinate{}, false},
		{"empty path", "", Coordinate{}, false},
		{
			// Regression: filename version "1.0.1" doesn't actually match
			// the path's version segment "1.0" — the leftover ".1" after
			// stripping the "foo-1.0" prefix is version digits, not a
			// legitimate extension, and must be rejected rather than
			// silently accepted as Extension: "1.jar".
			name: "filename version longer than path version segment is rejected",
			path: "com/example/foo/1.0/foo-1.0.1.jar",
			want: Coordinate{},
			ok:   false,
		},
		{
			// Multi-part extensions starting with letters (not leftover
			// version digits) must still be accepted.
			name: "multi-dot extension like tar.gz is accepted",
			path: "com/example/foo/1.0/foo-1.0.tar.gz",
			want: Coordinate{GroupID: "com.example", ArtifactID: "foo", Version: "1.0", Extension: "tar.gz"},
			ok:   true,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got, ok := ParseCoordinate(tc.path)

			// Assert
			if ok != tc.ok {
				t.Fatalf("ParseCoordinate(%q) ok = %v, want %v (got %+v)", tc.path, ok, tc.ok, got)
			}
			if ok && got != tc.want {
				t.Errorf("ParseCoordinate(%q) = %+v, want %+v", tc.path, got, tc.want)
			}
		})
	}
}

func TestFormat_RouteAndParseRoute(t *testing.T) {
	f := New()

	if got := f.Name(); got != "maven" {
		t.Errorf("Name() = %q, want maven", got)
	}

	key := "com/example/foo/1.0/foo-1.0.jar"
	route := f.Route("maven-releases", key)
	if want := "/maven-releases/" + key; route != want {
		t.Errorf("Route() = %q, want %q", route, want)
	}

	bucket, gotKey, ok := f.ParseRoute(route)
	if !ok || bucket != "maven-releases" || gotKey != key {
		t.Errorf("ParseRoute(%q) = (%q, %q, %v), want (maven-releases, %q, true)", route, bucket, gotKey, ok, key)
	}

	if _, _, ok := f.ParseRoute("/maven-releases/not-a-valid-maven-path"); ok {
		t.Error("ParseRoute() accepted a key that isn't a valid Maven coordinate")
	}
}
