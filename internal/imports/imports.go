// Package imports provides shared import-path parsing and lightweight
// alias resolution used by both the checker and the expand pass.
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
// Returns ("go", "pkg/path") for "go://pkg/path", or ("", path) for directory imports.
func ParseScheme(path string) (scheme, uri string) {
	if before, after, ok := strings.Cut(path, "://"); ok {
		return before, after
	}
	return "", path
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
// alias → ImportRef. No IR building; scheme and URI only. Used by the
// expand pass to resolve #[alias.name] macro attributes before type-checking.
func ResolveAliases(docs []*ast.Document) map[string]ImportRef {
	out := make(map[string]ImportRef)
	for _, doc := range docs {
		for _, stmt := range doc.Stmts {
			imp, ok := stmt.(*ast.Import)
			if !ok {
				continue
			}
			path := imp.Path
			if imp.Replace != "" {
				path = imp.Replace
			}
			scheme, uri := ParseScheme(path)
			alias := imp.Alias
			if alias == "" {
				alias = NamespaceFromPath(imp.Path)
			}
			out[alias] = ImportRef{Scheme: scheme, URI: uri}
		}
	}
	return out
}
