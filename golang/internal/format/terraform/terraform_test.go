package terraform

import "testing"

func TestParseProviderVersionsPath(t *testing.T) {
	cases := []struct {
		name string
		path string
		want ProviderRef
		ok   bool
	}{
		{"valid", "/v1/providers/hashicorp/aws/versions", ProviderRef{Namespace: "hashicorp", Type: "aws"}, true},
		{"no leading slash", "v1/providers/hashicorp/aws/versions", ProviderRef{Namespace: "hashicorp", Type: "aws"}, true},
		{"wrong final segment", "/v1/providers/hashicorp/aws/download", ProviderRef{}, false},
		{"missing type", "/v1/providers/hashicorp/versions", ProviderRef{}, false},
		{"wrong protocol prefix", "/v1/modules/hashicorp/aws/versions", ProviderRef{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got, ok := ParseProviderVersionsPath(tc.path)

			// Assert
			if ok != tc.ok {
				t.Fatalf("ParseProviderVersionsPath(%q) ok = %v, want %v", tc.path, ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Errorf("ParseProviderVersionsPath(%q) = %+v, want %+v", tc.path, got, tc.want)
			}
		})
	}
}

func TestParseProviderDownloadPath(t *testing.T) {
	cases := []struct {
		name string
		path string
		want ProviderDownloadRef
		ok   bool
	}{
		{
			name: "valid",
			path: "/v1/providers/hashicorp/aws/5.31.0/download/linux/amd64",
			want: ProviderDownloadRef{
				ProviderRef: ProviderRef{Namespace: "hashicorp", Type: "aws"},
				Version:     "5.31.0", OS: "linux", Arch: "amd64",
			},
			ok: true,
		},
		{"missing download marker", "/v1/providers/hashicorp/aws/5.31.0/linux/amd64", ProviderDownloadRef{}, false},
		{"missing arch", "/v1/providers/hashicorp/aws/5.31.0/download/linux", ProviderDownloadRef{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got, ok := ParseProviderDownloadPath(tc.path)

			// Assert
			if ok != tc.ok {
				t.Fatalf("ParseProviderDownloadPath(%q) ok = %v, want %v", tc.path, ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Errorf("ParseProviderDownloadPath(%q) = %+v, want %+v", tc.path, got, tc.want)
			}
		})
	}
}

func TestParseModuleVersionsPath(t *testing.T) {
	cases := []struct {
		name string
		path string
		want ModuleRef
		ok   bool
	}{
		{"valid", "/v1/modules/hashicorp/consul/aws/versions", ModuleRef{Namespace: "hashicorp", Name: "consul", System: "aws"}, true},
		{"wrong final segment", "/v1/modules/hashicorp/consul/aws/1.0.0", ModuleRef{}, false},
		{"missing system", "/v1/modules/hashicorp/consul/versions", ModuleRef{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got, ok := ParseModuleVersionsPath(tc.path)

			// Assert
			if ok != tc.ok {
				t.Fatalf("ParseModuleVersionsPath(%q) ok = %v, want %v", tc.path, ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Errorf("ParseModuleVersionsPath(%q) = %+v, want %+v", tc.path, got, tc.want)
			}
		})
	}
}

func TestParseModuleDownloadPath(t *testing.T) {
	cases := []struct {
		name string
		path string
		want ModuleDownloadRef
		ok   bool
	}{
		{
			name: "valid",
			path: "/v1/modules/hashicorp/consul/aws/0.11.0/download",
			want: ModuleDownloadRef{
				ModuleRef: ModuleRef{Namespace: "hashicorp", Name: "consul", System: "aws"},
				Version:   "0.11.0",
			},
			ok: true,
		},
		{"missing download marker", "/v1/modules/hashicorp/consul/aws/0.11.0", ModuleDownloadRef{}, false},
		{"empty path", "", ModuleDownloadRef{}, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got, ok := ParseModuleDownloadPath(tc.path)

			// Assert
			if ok != tc.ok {
				t.Fatalf("ParseModuleDownloadPath(%q) ok = %v, want %v", tc.path, ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Errorf("ParseModuleDownloadPath(%q) = %+v, want %+v", tc.path, got, tc.want)
			}
		})
	}
}
