package license

import (
	"encoding/xml"
	"fmt"
)

type mavenPOM struct {
	Licenses struct {
		License []struct {
			Name string `xml:"name"`
		} `xml:"license"`
	} `xml:"licenses"`
}

// ParseMavenPOM extracts the declared license from a Maven pom.xml's
// content's <licenses><license><name>...</name></license></licenses>
// block. A POM may declare more than one license (dual-licensing); this
// returns only the first, consistent with how most tooling treats the
// first entry as the primary/authoritative one absent a specific need to
// model multiple simultaneous licenses.
func ParseMavenPOM(data []byte) (Result, error) {
	var pom mavenPOM
	if err := xml.Unmarshal(data, &pom); err != nil {
		return Result{}, fmt.Errorf("license: parse pom.xml: %w", err)
	}
	if len(pom.Licenses.License) == 0 {
		return Result{}, nil
	}
	return newResult(pom.Licenses.License[0].Name), nil
}
