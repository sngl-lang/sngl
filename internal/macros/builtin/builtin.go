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
	expand.RegisterPre("builtin", "generic", genericHandler)
}

// stringArg reads a single string-literal macro argument. Macro arguments are
// constant expressions; these compiler tags are string constants (not name
// references), so they are written as `"color"` / `"list"`, not bare idents.
func stringArg(attr ast.MacroAttr) (string, error) {
	if len(attr.Args) != 1 {
		return "", errors.New("expected exactly one string argument")
	}
	lit, ok := attr.Args[0].(*ast.LiteralExpr)
	if !ok || lit.Kind != ast.LiteralStringQuoted {
		return "", errors.New(`argument must be a quoted string literal, e.g. ("color")`)
	}
	// LiteralExpr.Raw holds the already-unquoted string content.
	return lit.Raw, nil
}

// genericHandler implements #[builtin.generic(id)], marking a struct as a
// built-in generic type constructor (list/map/iter/ref/option). The checker
// resolves references to the marked name through scope and dispatches the
// type-argument construction by id, so the name is shadowable like any other
// declaration while the construction logic stays in the compiler.
func genericHandler(attr ast.MacroAttr, decl ast.Stmt) (ast.Stmt, error) {
	s, ok := decl.(*ast.StructDef)
	if !ok {
		return decl, errors.New("generic macro requires a struct declaration")
	}
	id, err := stringArg(attr)
	if err != nil {
		return decl, fmt.Errorf("builtin.generic: %w", err)
	}
	switch id {
	case "list", "map", "iter", "ref", "option":
		s.BuiltinGeneric = id
	default:
		return decl, fmt.Errorf("unknown generic id %q", id)
	}
	return s, nil
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
	kind, err := stringArg(attr)
	if err != nil {
		return decl, fmt.Errorf("builtin.stringrepr: %w", err)
	}
	switch kind {
	case "color", "date", "time", "dateTime":
		s.StringRepr = kind
	default:
		return decl, fmt.Errorf("unknown stringrepr kind %q", kind)
	}
	return s, nil
}
