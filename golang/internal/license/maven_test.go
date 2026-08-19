package license

import "testing"

func TestParseMavenPOM(t *testing.T) {
	cases := []struct {
		name string
		xml  string
		want Result
	}{
		{
			name: "single license",
			xml: `<project>
				<licenses>
					<license>
						<name>Apache License, Version 2.0</name>
						<url>https://www.apache.org/licenses/LICENSE-2.0.txt</url>
					</license>
				</licenses>
			</project>`,
			want: Result{Raw: "Apache License, Version 2.0", SPDXID: "Apache-2.0"},
		},
		{
			name: "dual-licensed POM returns the first entry",
			xml: `<project>
				<licenses>
					<license><name>MIT</name></license>
					<license><name>Apache-2.0</name></license>
				</licenses>
			</project>`,
			want: Result{Raw: "MIT", SPDXID: "MIT"},
		},
		{
			name: "no licenses element",
			xml:  `<project><groupId>com.example</groupId></project>`,
			want: Result{},
		},
		{
			name: "empty licenses element",
			xml:  `<project><licenses></licenses></project>`,
			want: Result{},
		},
		{
			name: "license entry with no name (only a url)",
			xml: `<project>
				<licenses>
					<license><url>https://example.com/LICENSE</url></license>
				</licenses>
			</project>`,
			want: Result{},
		},
		{
			// 100% of real Maven Central POMs declare this default
			// namespace; encoding/xml matches unqualified struct tags
			// against elements in any namespace, but this is worth its
			// own regression test rather than relying on that implicitly.
			name: "real-world namespaced POM",
			xml: `<project xmlns="http://maven.apache.org/POM/4.0.0">
				<licenses>
					<license>
						<name>The Apache License, Version 2.0</name>
						<url>https://www.apache.org/licenses/LICENSE-2.0.txt</url>
					</license>
				</licenses>
			</project>`,
			want: Result{Raw: "The Apache License, Version 2.0", SPDXID: ""},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			got, err := ParseMavenPOM([]byte(tc.xml))

			// Assert
			if err != nil {
				t.Fatalf("ParseMavenPOM() error = %v", err)
			}
			if got != tc.want {
				t.Errorf("ParseMavenPOM() = %+v, want %+v", got, tc.want)
			}
		})
	}
}

func TestParseMavenPOM_MalformedXML(t *testing.T) {
	// Act
	_, err := ParseMavenPOM([]byte(`<not-valid-xml`))

	// Assert
	if err == nil {
		t.Fatal("ParseMavenPOM() error = nil, want an error for malformed XML")
	}
}
