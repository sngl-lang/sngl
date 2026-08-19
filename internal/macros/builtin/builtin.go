// Package builtin registers the #[builtin.*] macros that mark ordinary SNGL
// declarations as compiler built-ins. Unlike user-facing macros (e.g.
// #[canvas.shape]), these annotate declarations in the built-ins/stdlib source
// itself; the checker runs the pre-expand pass over that source so the marks
// are in place before type-checking.
//
// See docs/superpowers/specs/2026-08-18-builtins-stdlib-split-design.md.
package builtin

import (
	"errors"
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/expand"
)

func init() {
	expand.RegisterPre("builtin", "stringrepr",
		[]expand.Param{{Name: "kind", Kind: expand.ArgString}}, stringReprHandler)
	expand.RegisterPre("builtin", "generic",
		[]expand.Param{{Name: "id", Kind: expand.ArgString}}, genericHandler)
}

// genericHandler implements #[builtin.generic("id")], marking a struct as a
// built-in generic type constructor (list/map/iter/ref/option). The checker
// resolves references to the marked name through scope and dispatches the
// type-argument construction by id, so the name is shadowable like any other
// declaration while the construction logic stays in the compiler.
func genericHandler(args expand.Args, decl ast.Stmt) (ast.Stmt, error) {
	s, ok := decl.(*ast.StructDef)
	if !ok {
		return decl, errors.New("generic macro requires a struct declaration")
	}
	id := ast.BuiltinKind(args.String("id"))
	if !id.IsGeneric() {
		return decl, fmt.Errorf("unknown generic id %q", args.String("id"))
	}
	s.Builtin = id
	return s, nil
}

// stringReprHandler implements #[builtin.stringrepr("kind")], marking a struct
// as a string-representable value type (color/date/time/dateTime). Downstream
// code reads ir.StructDef.StringRepr rather than the struct's name, so the
// coercion behaviour travels with the type.
func stringReprHandler(args expand.Args, decl ast.Stmt) (ast.Stmt, error) {
	s, ok := decl.(*ast.StructDef)
	if !ok {
		return decl, errors.New("stringrepr macro requires a struct declaration")
	}
	kind := ast.BuiltinKind(args.String("kind"))
	if !kind.IsStringRepr() {
		return decl, fmt.Errorf("unknown stringrepr kind %q", args.String("kind"))
	}
	s.Builtin = kind
	return s, nil
}
