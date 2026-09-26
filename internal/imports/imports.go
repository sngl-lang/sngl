// Package imports provides shared import-path parsing and lightweight
// alias resolution used by the checker, for imports and for the packages that
// `#[...]` marks name.
package imports

import (
	"strings"
)

// ImportRef holds the scheme and URI for a resolved import path.
type ImportRef struct {
	Scheme string // e.g. "internal", "go", "platform"
	URI    string // e.g. "canvas", "pkg/path"
}

// ParseScheme extracts the scheme and URI from an import path.
//
// The scheme is everything before the first colon and the URI is everything
// after it, authority marker included: "sngl:ui" is ("sngl", "ui") and
// "http://example.com/x" is ("http", "//example.com/x"). Whether a "//"
// follows is the scheme handler's business, not this function's -- which is
// what lets a URI carry another URI, as in
// "openapi3:https://example.com/spec.json".
//
// A path with no colon, or whose prefix is not a well-formed scheme name, is a
// directory import and comes back as ("", path).
func ParseScheme(path string) (scheme, uri string) {
	before, after, ok := strings.Cut(path, ":")
	if !ok || !isSchemeName(before) {
		return "", path
	}
	return before, after
}

// isSchemeName reports whether s is a URI scheme name (RFC 3986): a letter
// followed by letters, digits, "+", "-" or ".".
func isSchemeName(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z':
		case i > 0 && (r >= '0' && r <= '9' || r == '+' || r == '-' || r == '.'):
		default:
			return false
		}
	}
	return true
}

// NamespaceFromPath derives a namespace alias from an import path.
// Uses the last path segment: "widgets/counter" → "counter".
func NamespaceFromPath(path string) string {
	_, uri := ParseScheme(path)
	if i := strings.LastIndex(uri, "/"); i >= 0 {
		return uri[i+1:]
	}
	return uri
}
