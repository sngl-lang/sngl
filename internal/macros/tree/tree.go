// Package tree registers the marks that describe a segmented component tree:
// #[tree.kind], which says a component is a member of a named family, and
// #[tree.children], which says a component hosts one.
//
// The name is opaque. Drawing is the first user and rich text and menus are
// the next, so nothing here may know what a shape is — the checker compares
// the two names and reports a mismatch.
package tree

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/expand"
)

func init() {
	expand.RegisterPre("internal/tree", "kind", []expand.Param{
		{Name: "name", Kind: expand.ArgString},
	}, kindHandler)
	expand.RegisterPre("internal/tree", "children", []expand.Param{
		{Name: "name", Kind: expand.ArgString},
	}, childrenHandler)
}

func kindHandler(args expand.Args, decl ast.Stmt) (ast.Stmt, error) {
	return apply(decl, "kind", args.String("name"), ast.TreeTaggable.SetTreeKind)
}

func childrenHandler(args expand.Args, decl ast.Stmt) (ast.Stmt, error) {
	return apply(decl, "children", args.String("name"), ast.TreeTaggable.SetTreeChildren)
}

func apply(decl ast.Stmt, mark, name string, set func(ast.TreeTaggable, string) error) (ast.Stmt, error) {
	if name == "" {
		return decl, fmt.Errorf("#[tree.%s] requires a non-empty tree name", mark)
	}
	taggable, ok := decl.(ast.TreeTaggable)
	if !ok {
		return decl, fmt.Errorf("#[tree.%s(%q)] cannot mark %T; only a component is a node in a tree", mark, name, decl)
	}
	if err := set(taggable, name); err != nil {
		return decl, fmt.Errorf("#[tree.%s(%q)]: %w", mark, name, err)
	}
	return decl, nil
}
