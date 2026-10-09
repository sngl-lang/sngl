package checker

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// typeFromInit is the type a var or const declaration holds: the one it was
// annotated with, or with none, its initializer's. An annotation is read as
// dyn by the time this runs, which is how the absence of one arrives. An
// initializer yielding nothing leaves the annotation, since there is no type
// to take; what it is held to otherwise is coerce's, which ran first.
func (c *checker) typeFromInit(declared *ir.Type, init ir.Expr, at ast.Pos) *ir.Type {
	t := exprType(init)
	if t.Kind == ir.TypeVoid || declared.Kind != ir.TypeDyn {
		return declared
	}
	return c.inferredType(t, at)
}

// inferredType is t, taken by a declaration that named no type of its own --
// a var, a const, a context, a prop default, a function's result. `null` says
// nothing about what it is the absence of, so a type that is or holds it
// would be a declaration only `null` is assignable to: a context no provider
// could set, a var no write could change. It is refused where the type is
// inferred, as an empty list is, and dyn stands for it after.
func (c *checker) inferredType(t *ir.Type, at ast.Pos) *ir.Type {
	if holdsNull(t) {
		c.error(at, "cannot infer a type from null; add a type annotation")
		return ir.TypDyn
	}
	return t
}

// holdsNull reports whether t is null or is built from it: an element, a
// parameter, a result, or an anonymous struct's field.
func holdsNull(t *ir.Type) bool {
	return holdsNullSeen(t, map[*ir.StructDef]bool{})
}

func holdsNullSeen(t *ir.Type, seen map[*ir.StructDef]bool) bool {
	if t == nil {
		return false
	}
	if t.Kind == ir.TypeNull {
		return true
	}
	for _, e := range t.Elems {
		if holdsNullSeen(e, seen) {
			return true
		}
	}
	if t.Sig != nil {
		for _, p := range t.Sig.Params {
			if holdsNullSeen(p.Type, seen) {
				return true
			}
		}
		if holdsNullSeen(t.Sig.Return, seen) {
			return true
		}
	}
	// An anonymous struct is its fields, so a field typed null is the struct's;
	// a declared one was annotated.
	if sd, ok := t.Decl.(*ir.StructDef); ok && sd.Anon && !seen[sd] {
		seen[sd] = true
		for _, f := range sd.Fields {
			if holdsNullSeen(f.Type, seen) {
				return true
			}
		}
	}
	return false
}
