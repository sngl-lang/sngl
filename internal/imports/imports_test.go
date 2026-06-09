package imports_test

import (
	"testing"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/imports"
)

func TestResolveAliases(t *testing.T) {
	docs := []*ast.Document{
		{Stmts: []ast.Stmt{
			&ast.Import{Path: "internal://canvas"},
			&ast.Import{Path: "internal://storage", Alias: "store"},
			&ast.Import{Path: "go://mypkg/widget"},
		}},
	}
	got := imports.ResolveAliases(docs)

	if got["canvas"].Scheme != "internal" || got["canvas"].URI != "canvas" {
		t.Errorf("canvas: got %+v", got["canvas"])
	}
	if got["store"].Scheme != "internal" || got["store"].URI != "storage" {
		t.Errorf("store: got %+v", got["store"])
	}
	if got["widget"].Scheme != "go" || got["widget"].URI != "mypkg/widget" {
		t.Errorf("widget: got %+v", got["widget"])
	}
}

func TestParseScheme(t *testing.T) {
	scheme, uri := imports.ParseScheme("go://pkg/path")
	if scheme != "go" || uri != "pkg/path" {
		t.Errorf("got scheme=%q uri=%q", scheme, uri)
	}
	scheme, uri = imports.ParseScheme("relative/path")
	if scheme != "" || uri != "relative/path" {
		t.Errorf("got scheme=%q uri=%q", scheme, uri)
	}
}

func TestNamespaceFromPath(t *testing.T) {
	if got := imports.NamespaceFromPath("internal://canvas"); got != "canvas" {
		t.Errorf("got %q", got)
	}
	if got := imports.NamespaceFromPath("widgets/counter"); got != "counter" {
		t.Errorf("got %q", got)
	}
}
