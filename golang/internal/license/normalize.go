package license

import "strings"

// spdxAliases maps common, non-SPDX-exact license strings — as they
// actually appear in real package metadata — to their SPDX license
// identifier (https://spdx.org/licenses/). Not exhaustive: SPDX lists
// hundreds of licenses; this covers the handful that account for the
// overwhelming majority of real-world open source packages. Matching is
// case-insensitive with internal whitespace collapsed. Deliberately does
// NOT include ambiguous strings like "BSD" alone (2-Clause vs 3-Clause is
// not determinable from that string) or "Public Domain" (not an SPDX
// license identifier at all) — those come back unnormalized (SPDXID
// empty) rather than guessed.
var spdxAliases = map[string]string{
	"mit":             "MIT",
	"mit license":     "MIT",
	"the mit license": "MIT",

	"apache 2.0":                  "Apache-2.0",
	"apache-2.0":                  "Apache-2.0",
	"apache license 2.0":          "Apache-2.0",
	"apache license, version 2.0": "Apache-2.0",
	"apache software license":     "Apache-2.0",

	"bsd-2-clause": "BSD-2-Clause",
	"bsd-3-clause": "BSD-3-Clause",

	"isc":         "ISC",
	"isc license": "ISC",

	// Bare "gpl-3.0" etc. are deprecated SPDX forms; map them to the
	// current "-only" identifier. The current identifiers also get an
	// identity entry each, so a package that already declares the exact,
	// unambiguous, current SPDX string still normalizes (a package
	// declaring "GPL-3.0-or-later" is NOT the same license as "-only" and
	// is listed separately, not aliased to "-only").
	"gpl-3.0":                         "GPL-3.0-only",
	"gpl-3.0-only":                    "GPL-3.0-only",
	"gpl-3.0-or-later":                "GPL-3.0-or-later",
	"gplv3":                           "GPL-3.0-only",
	"gnu general public license v3.0": "GPL-3.0-only",
	"gpl-2.0":                         "GPL-2.0-only",
	"gpl-2.0-only":                    "GPL-2.0-only",
	"gpl-2.0-or-later":                "GPL-2.0-or-later",
	"gplv2":                           "GPL-2.0-only",
	"gnu general public license v2.0": "GPL-2.0-only",

	"lgpl-3.0":                             "LGPL-3.0-only",
	"lgpl-3.0-only":                        "LGPL-3.0-only",
	"lgpl-3.0-or-later":                    "LGPL-3.0-or-later",
	"lgpl-2.1":                             "LGPL-2.1-only",
	"lgpl-2.1-only":                        "LGPL-2.1-only",
	"lgpl-2.1-or-later":                    "LGPL-2.1-or-later",
	"gnu lesser general public license v3": "LGPL-3.0-only",

	"mpl-2.0":                    "MPL-2.0",
	"mozilla public license 2.0": "MPL-2.0",

	"unlicense": "Unlicense",
	// Deliberately NOT mapping npm's "UNLICENSED" string here: it means
	// the opposite of SPDX's "Unlicense" — npm's own docs define
	// `"license": "UNLICENSED"` as "no license granted, proprietary, do
	// not use" (paired with `"private": true`), while SPDX's Unlicense is
	// a public-domain-equivalent, maximally permissive dedication.
	// Mapping one to the other would tell a downstream policy check
	// (Task 28) that an explicitly-proprietary package is one of the most
	// permissive licenses that exists. Left unrecognized (SPDXID empty)
	// like any other unmapped string — a caller can and should treat
	// Raw == "UNLICENSED" (case-insensitive) as its own explicit signal.

	"cc0-1.0": "CC0-1.0",
	"cc0":     "CC0-1.0",
}

// NormalizeSPDX attempts to map a raw license string to a known SPDX
// license identifier. Returns "" if unrecognized.
func NormalizeSPDX(raw string) string {
	// strings.Fields already discards leading/trailing whitespace as part
	// of splitting on runs of internal whitespace, so no separate
	// TrimSpace is needed before it.
	key := strings.Join(strings.Fields(strings.ToLower(raw)), " ")
	return spdxAliases[key]
}
