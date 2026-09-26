package fixtures

// The import stub. A handful of fixtures import a directory package, and
// resolving one for real would mean a directory per fixture on disk; these two
// synthetic packages are what those fixtures are written against. Moved here
// from internal/checker's test package with the walk that used it.

import (
	"fmt"
	"io/fs"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

type mockResolver struct {
	pkgs map[string]string // import path → SNGL source
}

func (m *mockResolver) Resolve(_ fs.FS, path string) ([]*ast.Document, error) {
	src, ok := m.pkgs[path]
	if !ok {
		return nil, fmt.Errorf("package %q not found", path)
	}
	doc, err := parser.Parse(path+".sngl", []byte(src))
	if err != nil {
		return nil, err
	}
	return []*ast.Document{doc}, nil
}

func (m *mockResolver) ResolveScheme(scheme, uri, dir string) (*ir.NativeImport, error) {
	return nil, fmt.Errorf("scheme imports not supported in tests")
}

func (m *mockResolver) ResolveSchemeFS(scheme, uri, dir string) ([]*ast.Document, fs.FS, error) {
	return nil, nil, nil
}

func newTestResolver() *mockResolver {
	return &mockResolver{pkgs: map[string]string{
		"widgets": `
import . "sngl:ui"

component Counter(label = "") node {
    var count = 0
    text(value=label)
}

component _helper() node {
    text(value="private")
}
`,
		"mainlib": `
import . "sngl:ui"

component main node {
    text(value="oops")
}
`,
	}}
}
