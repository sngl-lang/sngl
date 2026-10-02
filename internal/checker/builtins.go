package checker

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// bindBuiltinRole binds the declaration a #[builtin] kind names to the field
// the compiler reaches it through. It runs as the mark is applied, so the
// reference is in place the moment the declaration is registered rather than
// after a scan looking for one.
//
// The mark only stamps a kind; what a kind then requires is checked here,
// where the reference is stored, because that is where the requirement comes
// from — `context` has to be a component precisely because the checker
// resolves `context #name(default)` against its declaration.
//
// The predeclared constants are the other half: PLATFORM and LANGUAGE are
// declared with a placeholder value because the real one is not known until a
// build picks a target, and this is where the compiler supplies it.
func (c *checker) bindBuiltinRole(kind ir.BuiltinKind, sym ir.Symbol) {
	bound := false
	switch kind {
	case ir.BuiltinContext:
		bound = bindBuiltin(c, &c.contextComp, kind, sym) != nil
	case ir.BuiltinErrorBoundary:
		bound = bindBuiltin(c, &c.boundaryComp, kind, sym) != nil
	case ir.BuiltinTreeRoot:
		bound = bindBuiltin(c, &c.rootTree, kind, sym) != nil
	case ir.BuiltinNull:
		if v := bindBuiltin(c, &c.nullConst, kind, sym); v != nil {
			// Neither the type nor the value can be spelled, so the written
			// ones are placeholders the compiler replaces.
			v.Type = TypNull
			v.Init = &ir.Literal{Type: TypNull, Value: "null"}
			bound = true
		}
	case ir.BuiltinDuration:
		if u := bindBuiltin(c, &c.durationUnit, kind, sym); u != nil {
			// Handed to phases with no scope of their own; the Go importer
			// maps time.Duration through it.
			ir.RegisterDurationUnit(u.SymType())
			bound = true
		}
	case ir.BuiltinPlatform:
		bound = bindBuiltin(c, &c.platformType, kind, sym) != nil
	case ir.BuiltinLanguage:
		bound = bindBuiltin(c, &c.languageType, kind, sym) != nil
	case ir.BuiltinTargetPlatform:
		if v := bindBuiltin(c, &c.platformConst, kind, sym); v != nil {
			// No initializer: the value arrives when a build picks a target,
			// and the optimizer reads it off the mark. The type is filled in
			// by typeTargetConsts once both halves are registered — the two
			// declarations are in different files of lib/builtin and nothing
			// orders them.
			v.Init = nil
			bound = true
		}
	case ir.BuiltinTargetLanguage:
		if v := bindBuiltin(c, &c.languageConst, kind, sym); v != nil {
			v.Init = nil
			bound = true
		}
	}
	if bound && c.inLibSource() {
		// Library packages are shared across the checks one build runs, and a
		// check that reached one from the cache registered nothing: it takes
		// the reference from here instead.
		c.libs.roles[kind] = sym
	}
}

// bindBuiltin assigns the declaration carrying kind to *dst, reporting when it
// is the wrong declaration form or when another declaration already claimed
// the kind. Returns the bound value, or the zero value when it did neither.
//
// A kind classifies one declaration; two sharing a mark would be two
// incompatible things rather than aliases, so the second is an error and not a
// silent overwrite.
func bindBuiltin[T ir.Symbol](c *checker, dst *T, kind ir.BuiltinKind, sym ir.Symbol) T {
	var zero T
	got, ok := sym.(T)
	if !ok {
		c.error(symPos(sym), "#[builtin(%q)] requires a %T, got %T", string(kind), zero, sym)
		return zero
	}
	if any(*dst) == any(got) {
		return got // already bound to this declaration; see libCache.roles
	}
	if any(*dst) != any(zero) {
		c.error(symPos(sym), "#[builtin(%q)] is declared more than once", string(kind))
		return zero
	}
	*dst = got
	return got
}

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
	case *ir.UnitDef:
		if d.AST != nil {
			return d.AST.Pos
		}
	}
	return ast.Pos{}
}
