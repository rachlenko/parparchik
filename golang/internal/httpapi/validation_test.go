package httpapi

import (
	"strings"
	"testing"
)

func TestValidateKey(t *testing.T) {
	cases := []struct {
		name    string
		key     string
		wantErr error
	}{
		{"valid simple key", "photo.jpg", nil},
		{"valid nested key", "a/b/c.txt", nil},
		{"empty", "", errEmptyKey},
		{"leading slash", "/etc/passwd", errKeyLeadingSlash},
		{"dot dot traversal", "../secret", errKeyTraversal},
		{"dot dot in middle segment", "a/../b", errKeyTraversal},
		{"control character", "file\x00name", errKeyControlChar},
		{"newline", "file\nname", errKeyControlChar},
		{"too long", strings.Repeat("a", maxKeyLength+1), errKeyTooLong},
		{"exactly max length", strings.Repeat("a", maxKeyLength), nil},
		{"single dot segment is fine", "./file", nil},
		{"dotdot as substring but not a segment", "file..name", nil},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			// Act
			err := validateKey(tc.key)

			// Assert
			if err != tc.wantErr {
				t.Errorf("validateKey(%q) = %v, want %v", tc.key, err, tc.wantErr)
			}
		})
	}
}
