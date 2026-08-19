package helm

import "testing"

func TestParseChartFilename(t *testing.T) {
	cases := []struct {
		name     string
		filename string
		want     ChartRef
		ok       bool
	}{
		{"simple", "myapp-1.2.3.tgz", ChartRef{Name: "myapp", Version: "1.2.3"}, true},
		{"hyphenated name", "nginx-ingress-4.10.1.tgz", ChartRef{Name: "nginx-ingress", Version: "4.10.1"}, true},
		{"pre-release version", "myapp-1.2.3-rc.1.tgz", ChartRef{Name: "myapp", Version: "1.2.3-rc.1"}, true},
		{"multi-word name with pre-release", "my-cool-app-2.0.0-beta.2.tgz", ChartRef{Name: "my-cool-app", Version: "2.0.0-beta.2"}, true},
		{"not a tgz", "myapp-1.2.3.tar.gz", ChartRef{}, false},
		{"no version segment", "myapp.tgz", ChartRef{}, false},
		{"name only, no digits anywhere", "myapp-latest.tgz", ChartRef{}, false},
		{"empty", "", ChartRef{}, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got, ok := ParseChartFilename(tc.filename)

			// Assert
			if ok != tc.ok {
				t.Fatalf("ParseChartFilename(%q) ok = %v, want %v (got %+v)", tc.filename, ok, tc.ok, got)
			}
			if ok && got != tc.want {
				t.Errorf("ParseChartFilename(%q) = %+v, want %+v", tc.filename, got, tc.want)
			}
		})
	}
}

func TestFormat_RouteAndParseRoute(t *testing.T) {
	f := New()

	if got := f.Name(); got != "helm" {
		t.Errorf("Name() = %q, want helm", got)
	}

	key := "charts/nginx-ingress-4.10.1.tgz"
	route := f.Route("helm-repo", key)
	if want := "/helm-repo/" + key; route != want {
		t.Errorf("Route() = %q, want %q", route, want)
	}

	bucket, gotKey, ok := f.ParseRoute(route)
	if !ok || bucket != "helm-repo" || gotKey != key {
		t.Errorf("ParseRoute(%q) = (%q, %q, %v), want (helm-repo, %q, true)", route, bucket, gotKey, ok, key)
	}

	if _, _, ok := f.ParseRoute("/helm-repo/index.yaml"); ok {
		t.Error("ParseRoute() accepted a key that isn't a valid chart package filename")
	}
}
