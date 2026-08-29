// Package imports provides shared import-path parsing and lightweight
// alias resolution used by the checker, for imports and for the packages that
// `#[...]` marks name.
package imports

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
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

// ResolveAliases scans import declarations in docs and returns a map of
// alias → ImportRef. No IR building; scheme and URI only. Used to resolve the
// package a #[alias.name] mark names.
func ResolveAliases(docs []*ast.Document) map[string]ImportRef {
	// First pass: collect redirect declarations (import "A" => "B" with no alias).
	// These are pure path redirects, not namespace-introducing imports.
	replaces := map[string]string{}
	for _, doc := range docs {
		for _, stmt := range doc.Stmts {
			imp, ok := stmt.(*ast.Import)
			if !ok || imp.Replace == "" || imp.Alias != "" {
				continue
			}
			replaces[imp.Path] = imp.Replace
		}
	}

	// Second pass: build alias → ImportRef for non-redirect imports.
	out := make(map[string]ImportRef)
	for _, doc := range docs {
		for _, stmt := range doc.Stmts {
			imp, ok := stmt.(*ast.Import)
			if !ok {
				continue
			}
			// Skip pure redirect declarations; they don't introduce a namespace.
			if imp.Replace != "" && imp.Alias == "" {
				continue
			}
			// A dot import flattens the package into the current scope and binds
			// no namespace, so it contributes no macro alias. Deriving one from
			// the path would let #[<pkg>.macro] resolve against a name that is
			// not in scope. Its macros are reachable unqualified instead — see
			// DotPackages.
			if imp.IsDot() {
				continue
			}
			// Resolve the effective target path through the redirects map.
			path := imp.Path
			if imp.Replace != "" {
				path = imp.Replace
			} else if mapped, ok := replaces[imp.Path]; ok {
				path = mapped
			}
			scheme, uri := ParseScheme(path)
			alias := imp.Alias
			if alias == "" {
				alias = NamespaceFromPath(imp.Path)
			}
			// Skip degenerate paths where no alias could be derived.
			if alias == "" {
				continue
			}
			out[alias] = ImportRef{Scheme: scheme, URI: uri}
		}
	}
	return out
}

// DotPackages returns the package URIs dot-imported by these documents, in
// source order. A macro one of them declares is written unqualified — the dot
// import is what brings `#[builtin(...)]` into a file.
func DotPackages(docs []*ast.Document) []string {
	var out []string
	for _, doc := range docs {
		for _, stmt := range doc.Stmts {
			imp, ok := stmt.(*ast.Import)
			if !ok || !imp.IsDot() {
				continue
			}
			if _, uri := ParseScheme(imp.Path); uri != "" {
				out = append(out, uri)
			}
		}
	}
	return out
}
