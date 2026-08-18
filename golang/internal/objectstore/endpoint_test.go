package objectstore

import "testing"

func TestResolveEndpointURL(t *testing.T) {
	cases := []struct {
		name     string
		endpoint string
		want     string
	}{
		{"bare host:port defaults to http", "minio:9000", "http://minio:9000"},
		{"explicit http scheme preserved", "http://minio:9000", "http://minio:9000"},
		{"explicit https scheme preserved", "https://s3.internal.example:443", "https://s3.internal.example:443"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got := resolveEndpointURL(tc.endpoint)

			// Assert
			if got != tc.want {
				t.Errorf("resolveEndpointURL(%q) = %q, want %q", tc.endpoint, got, tc.want)
			}
		})
	}
}
