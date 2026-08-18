package generic

import "testing"

func TestFormat_Route(t *testing.T) {
	f := New()

	// Act
	route := f.Route("my-bucket", "path/to/file.txt")

	// Assert
	if want := "/my-bucket/path/to/file.txt"; route != want {
		t.Errorf("Route() = %q, want %q", route, want)
	}
}

func TestFormat_ParseRoute(t *testing.T) {
	f := New()
	cases := []struct {
		name       string
		route      string
		wantBucket string
		wantKey    string
		wantOK     bool
	}{
		{"simple", "/bucket/key.txt", "bucket", "key.txt", true},
		{"nested key", "/bucket/a/b/c.txt", "bucket", "a/b/c.txt", true},
		{"missing leading slash", "bucket/key.txt", "bucket", "key.txt", true},
		{"no key", "/bucket/", "", "", false},
		{"no key no trailing slash", "/bucket", "", "", false},
		{"empty", "", "", "", false},
		{"root only", "/", "", "", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			bucket, key, ok := f.ParseRoute(tc.route)

			// Assert
			if ok != tc.wantOK {
				t.Fatalf("ParseRoute(%q) ok = %v, want %v", tc.route, ok, tc.wantOK)
			}
			if !ok {
				return
			}
			if bucket != tc.wantBucket || key != tc.wantKey {
				t.Errorf("ParseRoute(%q) = (%q, %q), want (%q, %q)", tc.route, bucket, key, tc.wantBucket, tc.wantKey)
			}
		})
	}
}
