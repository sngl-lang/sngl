// Package builtin registers the #[builtin] macro that marks ordinary SNGL
// declarations as compiler built-ins. Unlike user-facing macros (e.g.
// #[canvas.shape]), it annotates declarations in the built-ins/stdlib source
// itself; the checker runs the pre-expand pass over that source so the marks
// are in place before type-checking.
//
// See docs/superpowers/specs/2026-08-18-builtins-stdlib-split-design.md.
package builtin

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/expand"
)

func init() {
	// #[builtin("...")] is written bare, with no package segment, so it
	// resolves only in a file that dot-imports this package.
	expand.RegisterPre("internal/builtin", "builtin",
		[]expand.Param{{Name: "kind", Kind: expand.ArgString}}, builtinHandler)
}

// builtinHandler implements #[builtin("kind")], stamping the built-in kind onto
// the declaration it annotates. Node kinds mark a component; every other kind
// marks a struct.
//
// The mark, rather than the declaration's name, is what the compiler keys on:
// resolveNamedType returns the canonical singleton (or runs the generic
// construction) for the marked decl and the plain user type otherwise, so a
// user declaration shadows a built-in like any other name. For node kinds the
// checker dispatches a visual node whose target resolves to the marked
// component to the matching compiler construct (window -> ir.Window).
func builtinHandler(args expand.Args, decl ast.Stmt) (ast.Stmt, error) {
	raw := args.String("kind")
	kind := ast.BuiltinKind(raw)
	if !kind.Valid() {
		return decl, fmt.Errorf("unknown builtin kind %q (valid: %s)", raw, strings.Join(kindNames(), ", "))
	}

	if kind.IsNode() {
		comp, ok := decl.(*ast.ComponentDecl)
		if !ok {
			return decl, fmt.Errorf("#[builtin(%q)] requires a component declaration", raw)
		}
		comp.Builtin = kind
		return comp, nil
	}

	s, ok := decl.(*ast.StructDef)
	if !ok {
		return decl, fmt.Errorf("#[builtin(%q)] requires a struct declaration", raw)
	}
	s.Builtin = kind
	return s, nil
}

func kindNames() []string {
	all := ast.AllBuiltinKinds()
	names := make([]string, len(all))
	for i, k := range all {
		names[i] = string(k)
	}
	return names
}
