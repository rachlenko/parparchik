package httpapi

import (
	"errors"
	"strings"
	"unicode"
)

// maxKeyLength mirrors S3's own object key length limit.
const maxKeyLength = 1024

var (
	errEmptyKey        = errors.New("key must not be empty")
	errKeyTooLong      = errors.New("key exceeds maximum length")
	errKeyLeadingSlash = errors.New("key must not start with '/'")
	errKeyTraversal    = errors.New("key must not contain '..' path segments")
	errKeyControlChar  = errors.New("key must not contain control characters")
)

// validateKey rejects object keys that could be used for path traversal or
// to otherwise abuse an S3-compatible backend that maps keys onto real
// filesystem paths. The Lua original passed client-supplied filename/route
// values straight through to S3 HEAD/GET/PUT calls with no validation at
// all — see the nginx-lua security review findings on unsanitized key
// input. Every handler that takes a key from client input must call this
// before using it.
func validateKey(key string) error {
	switch {
	case key == "":
		return errEmptyKey
	case len(key) > maxKeyLength:
		return errKeyTooLong
	case strings.HasPrefix(key, "/"):
		return errKeyLeadingSlash
	case containsDotDotSegment(key):
		return errKeyTraversal
	case containsControlByte(key):
		return errKeyControlChar
	}
	return nil
}

func containsDotDotSegment(key string) bool {
	for _, segment := range strings.Split(key, "/") {
		if segment == ".." {
			return true
		}
	}
	return false
}

func containsControlByte(key string) bool {
	for _, r := range key {
		if unicode.IsControl(r) {
			return true
		}
	}
	return false
}
