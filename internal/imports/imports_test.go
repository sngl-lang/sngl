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

func TestResolveAliasesReplaceMap(t *testing.T) {
	// A redirect declaration (Replace != "", Alias == "") should redirect later
	// imports of the same path to the replacement, not appear as a namespace itself.
	docs := []*ast.Document{
		{Stmts: []ast.Stmt{
			// Pure redirect: "internal://canvas" => "my://canvas-override"
			&ast.Import{Path: "internal://canvas", Replace: "my://canvas-override"},
			// Regular import of the redirected path — should resolve via replace map.
			&ast.Import{Path: "internal://canvas", Alias: "canvas"},
		}},
	}
	got := imports.ResolveAliases(docs)

	// The redirect declaration itself must NOT appear as an alias.
	if _, ok := got["canvas"]; !ok {
		t.Fatal("expected alias 'canvas' from aliased import")
	}
	// The aliased import should have been redirected to my://canvas-override.
	if got["canvas"].Scheme != "my" || got["canvas"].URI != "canvas-override" {
		t.Errorf("canvas: expected {my canvas-override}, got %+v", got["canvas"])
	}

	// A plain (no-alias) import of the redirected path should also be resolved.
	docs2 := []*ast.Document{
		{Stmts: []ast.Stmt{
			&ast.Import{Path: "internal://canvas", Replace: "my://canvas-override"},
			&ast.Import{Path: "internal://canvas"},
		}},
	}
	got2 := imports.ResolveAliases(docs2)
	if got2["canvas"].Scheme != "my" || got2["canvas"].URI != "canvas-override" {
		t.Errorf("canvas (no alias): expected {my canvas-override}, got %+v", got2["canvas"])
	}
}

func TestResolveAliasesBlankAlias(t *testing.T) {
	// A degenerate path like "go://" produces an empty namespace; it must be skipped.
	docs := []*ast.Document{
		{Stmts: []ast.Stmt{
			&ast.Import{Path: "go://"},
			&ast.Import{Path: "internal://canvas"},
		}},
	}
	got := imports.ResolveAliases(docs)
	if _, ok := got[""]; ok {
		t.Error("blank alias must not appear in result map")
	}
	if got["canvas"].Scheme != "internal" {
		t.Errorf("canvas: got %+v", got["canvas"])
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
