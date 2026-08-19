package license

import "testing"

func TestParsePythonMetadata(t *testing.T) {
	cases := []struct {
		name string
		data string
		want Result
	}{
		{
			name: "License header present",
			data: "Metadata-Version: 2.1\nName: requests\nVersion: 2.31.0\nLicense: Apache 2.0\n\nA long description.",
			want: Result{Raw: "Apache 2.0", SPDXID: "Apache-2.0"},
		},
		{
			name: "License header UNKNOWN falls back to classifier",
			data: "Metadata-Version: 2.1\nName: pkg\nLicense: UNKNOWN\n" +
				"Classifier: License :: OSI Approved :: MIT License\n\nDescription.",
			want: Result{Raw: "MIT License", SPDXID: "MIT"},
		},
		{
			name: "no License header, only classifier",
			data: "Metadata-Version: 2.1\nName: pkg\n" +
				"Classifier: License :: OSI Approved :: Apache Software License\n\nDescription.",
			want: Result{Raw: "Apache Software License", SPDXID: "Apache-2.0"},
		},
		{
			name: "non-license classifiers are ignored",
			data: "Metadata-Version: 2.1\nName: pkg\n" +
				"Classifier: Programming Language :: Python :: 3\n" +
				"Classifier: Operating System :: OS Independent\n" +
				"Classifier: License :: OSI Approved :: MIT License\n\nDescription.",
			want: Result{Raw: "MIT License", SPDXID: "MIT"},
		},
		{
			name: "dual-licensed: first License classifier wins",
			data: "Metadata-Version: 2.1\nName: pkg\n" +
				"Classifier: License :: OSI Approved :: MIT License\n" +
				"Classifier: License :: OSI Approved :: Apache Software License\n\nDescription.",
			want: Result{Raw: "MIT License", SPDXID: "MIT"},
		},
		{
			name: "neither header present",
			data: "Metadata-Version: 2.1\nName: pkg\nVersion: 1.0.0\n\nDescription.",
			want: Result{},
		},
		{
			name: "headers after the blank line are not scanned",
			data: "Metadata-Version: 2.1\nName: pkg\n\nLicense: MIT\n",
			want: Result{},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got, err := ParsePythonMetadata([]byte(tc.data))

			// Assert
			if err != nil {
				t.Fatalf("ParsePythonMetadata() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("ParsePythonMetadata() = %+v, want %+v", got, tc.want)
			}
		})
	}
}
