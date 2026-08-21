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
			// DotMacroPackages.
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

// DotMacroPackages returns the package URIs dot-imported by these documents,
// in source order. A macro in one of them is written unqualified — the dot
// import is what brings `#[builtin(...)]` into a file.
func DotMacroPackages(docs []*ast.Document) []string {
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
