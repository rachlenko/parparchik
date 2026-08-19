package license

import "testing"

func TestParseNPMPackageJSON(t *testing.T) {
	cases := []struct {
		name string
		json string
		want Result
	}{
		{
			name: "modern string license",
			json: `{"name": "lodash", "license": "MIT"}`,
			want: Result{Raw: "MIT", SPDXID: "MIT"},
		},
		{
			name: "unrecognized SPDX expression still captured as raw",
			json: `{"name": "dual", "license": "(MIT OR Apache-2.0)"}`,
			want: Result{Raw: "(MIT OR Apache-2.0)", SPDXID: ""},
		},
		{
			name: "legacy single license object",
			json: `{"name": "old-pkg", "license": {"type": "ISC", "url": "https://example.com"}}`,
			want: Result{Raw: "ISC", SPDXID: "ISC"},
		},
		{
			name: "legacy licenses array",
			json: `{"name": "older-pkg", "licenses": [{"type": "Apache-2.0", "url": "https://example.com"}]}`,
			want: Result{Raw: "Apache-2.0", SPDXID: "Apache-2.0"},
		},
		{
			name: "no license field at all",
			json: `{"name": "unlicensed-pkg"}`,
			want: Result{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got, err := ParseNPMPackageJSON([]byte(tc.json))

			// Assert
			if err != nil {
				t.Fatalf("ParseNPMPackageJSON() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("ParseNPMPackageJSON() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseNPMPackageJSON_MalformedJSON(t *testing.T) {
	// Act
	_, err := ParseNPMPackageJSON([]byte(`not json`))

	// Assert
	if err == nil {
		t.Fatal("ParseNPMPackageJSON() error = nil, want an error for malformed JSON")
	}
}
