package checker

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// collectBuiltins finds the declarations the compiler holds a direct reference
// to and binds them to their fields.
//
// The #[builtin] macro only stamps a kind; what a kind then requires is checked
// here, where the reference is stored, because that is where the requirement
// comes from — `window` has to be a component precisely because the checker
// asks it for the type it gives every ir.Window. Checking it in the macro put
// the assertion in a package with no stake in the answer, and left the macro
// knowing which kinds belong on which declaration forms.
//
// The predeclared constants are the other half: PLATFORM and LANGUAGE are
// declared with a placeholder value because the real one is not known until a
// build picks a target, and this is where the compiler supplies it.
func (c *checker) collectBuiltins(pkgs ...*ir.Package) {
	for _, pkg := range pkgs {
		if pkg == nil {
			continue
		}
		for _, sym := range pkg.Symbols.Root.Symbols {
			switch kind := symBuiltin(sym); kind {
			case ast.BuiltinWindow:
				bindBuiltin(c, &c.windowComp, kind, sym)
			case ast.BuiltinPlatform:
				if v := bindBuiltin(c, &c.platformConst, kind, sym); v != nil {
					// No initializer: the value arrives when a build picks a
					// target, and the optimizer reads it off the mark.
					v.Type = TypString
					v.Init = nil
				}
			case ast.BuiltinLanguage:
				if v := bindBuiltin(c, &c.languageConst, kind, sym); v != nil {
					v.Type = TypString
					v.Init = nil
				}
			}
		}
	}
}

// bindBuiltin assigns the declaration carrying kind to *dst, reporting when it
// is the wrong declaration form or when another declaration already claimed
// the kind. Returns the bound value, or the zero value when it did neither.
//
// A kind classifies one declaration; two sharing a mark would be two
// incompatible things rather than aliases, so the second is an error and not a
// silent overwrite.
func bindBuiltin[T ir.Symbol](c *checker, dst *T, kind ast.BuiltinKind, sym ir.Symbol) T {
	var zero T
	got, ok := sym.(T)
	if !ok {
		c.error(symPos(sym), "#[builtin(%q)] requires a %T, got %T", string(kind), zero, sym)
		return zero
	}
	if any(*dst) != any(zero) {
		c.error(symPos(sym), "#[builtin(%q)] is declared more than once", string(kind))
		return zero
	}
	*dst = got
	return got
}

// symBuiltin returns the #[builtin] kind a symbol carries, or BuiltinNone.
func symBuiltin(sym ir.Symbol) ast.BuiltinKind {
	switch d := sym.(type) {
	case *ir.Component:
		return d.Builtin
	case *ir.StructDef:
		return d.Builtin
	case *ir.Var:
		return d.Builtin
	}
	return ast.BuiltinNone
}

// symPos is the declaration position of a symbol, for diagnostics.
func symPos(sym ir.Symbol) ast.Pos {
	switch d := sym.(type) {
	case *ir.Component:
		if d.AST != nil {
			return d.AST.Pos
		}
	case *ir.StructDef:
		if d.AST != nil {
			return d.AST.Pos
		}
	case *ir.Var:
		return varPos(d)
	}
	return ast.Pos{}
}
