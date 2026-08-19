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
	expand.RegisterPre("builtin", "stringrepr", stringReprHandler)
}

// stringReprHandler implements #[builtin.stringrepr(kind)], marking a struct as
// a string-representable value type (color/date/time/dateTime). Downstream code
// reads ir.StructDef.StringRepr rather than the struct's name, so the coercion
// behaviour travels with the type.
func stringReprHandler(attr ast.MacroAttr, decl ast.Stmt) (ast.Stmt, error) {
	s, ok := decl.(*ast.StructDef)
	if !ok {
		return decl, errors.New("stringrepr macro requires a struct declaration")
	}
	if len(attr.Args) != 1 {
		return decl, errors.New("stringrepr requires exactly one kind argument, e.g. #[builtin.stringrepr(color)]")
	}
	id, ok := attr.Args[0].(*ast.IdentExpr)
	if !ok {
		return decl, errors.New("stringrepr kind must be a bare identifier (color/date/time/dateTime)")
	}
	switch id.Name {
	case "color", "date", "time", "dateTime":
		s.StringRepr = id.Name
	default:
		return decl, fmt.Errorf("unknown stringrepr kind %q", id.Name)
	}
	return s, nil
}
