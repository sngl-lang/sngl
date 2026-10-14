package checker

import (
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// varSpecType is the type a var declares where it is written: the one it
// names, or none yet when it takes its initializer's (typeFromInit). A var
// naming neither has nothing to say what it holds, which is refused rather
// than left a type that accepts anything.
func (c *checker) varSpecType(pos ast.Pos, names []string, te ast.TypeExpr, init ast.Expr) *ir.Type {
	if te == nil && init == nil {
		c.error(pos, "var %s needs a type or an initializer", strings.Join(names, ", "))
	}
	return c.resolveType(te)
}

// typeFromInit is the type a var or const declaration holds: the one it was
// annotated with, or with none, its initializer's. An annotation is read as
// dyn by the time this runs, which is how the absence of one arrives. An
// initializer yielding nothing leaves the annotation, since there is no type
// to take; what it is held to otherwise is coerce's, which ran first.
func (c *checker) typeFromInit(declared *ir.Type, init ir.Expr, at ast.Pos) *ir.Type {
	t := exprType(init)
	if t.Kind == ir.TypeVoid || declared.Kind != ir.TypeInvalid {
		return declared
	}
	return c.inferredType(t, at)
}

// inferredType is t, taken by a declaration that named no type of its own --
// a var, a const, a context, a prop default, a function's result. Two types
// say nothing a declaration could hold, and are refused where the type is
// inferred, as an empty list is; dyn stands for them after.
//
// `null` says nothing about what it is the absence of, so a type that is or
// holds it would be a declaration only `null` is assignable to: a context no
// provider could set, a var no write could change.
//
// A type parameter no enclosing declaration has in scope is the other: a call
// that bound none of its callee's parameters leaves one in its result, and
// `var xs = empty()` held a `list[T]` naming a type nothing declares. One in
// scope -- the enclosing generic's own -- is a type like any other. In scope
// means the name here resolves to the parameter the type refers to (its
// Decl), so a callee's `T` is not taken for the `T` of the generic calling it.
func (c *checker) inferredType(t *ir.Type, at ast.Pos) *ir.Type {
	if holdsNull(t) {
		c.error(at, "cannot infer a type from null; add a type annotation")
		return ir.TypInvalid
	}
	if name := c.unboundTypeParam(t); name != "" {
		c.error(at, "cannot infer %s: nothing here binds it; add a type annotation", name)
		return ir.TypInvalid
	}
	return t
}

// holdsNull reports whether t is null or is built from it.
func holdsNull(t *ir.Type) bool {
	found := false
	walkTypeParts(t, func(p *ir.Type) bool {
		found = p.Kind == ir.TypeNull
		return !found
	})
	return found
}

// unboundTypeParam names a type parameter t holds that no declaration in
// scope declares, or "" where there is none.
func (c *checker) unboundTypeParam(t *ir.Type) string {
	var found string
	walkTypeParts(t, func(p *ir.Type) bool {
		if p.Kind != ir.TypeTypeParam {
			return true
		}
		sym, ok := c.scope.Lookup(p.ParamName)
		if declared, isParam := sym.(*ir.TypeParamSym); ok && isParam {
			// A type built with no reference to its parameter is matched by
			// name, the only thing it carries.
			if p.Decl == nil || p.Decl == ir.Symbol(declared) {
				return true
			}
		}
		found = p.ParamName
		return false
	})
	return found
}

// walkTypeParts visits t and what it is built from -- elements, a function's
// parameters and result, an anonymous struct's fields -- until visit returns
// false.
func walkTypeParts(t *ir.Type, visit func(*ir.Type) bool) {
	seen := map[*ir.StructDef]bool{}
	var walk func(*ir.Type) bool
	walk = func(t *ir.Type) bool {
		if t == nil {
			return true
		}
		if !visit(t) {
			return false
		}
		for _, e := range t.Elems {
			if !walk(e) {
				return false
			}
		}
		if t.Sig != nil {
			for _, p := range t.Sig.Params {
				if !walk(p.Type) {
					return false
				}
			}
			if !walk(t.Sig.Return) {
				return false
			}
		}
		// An anonymous struct is its fields; a declared one was annotated.
		if sd, ok := t.Decl.(*ir.StructDef); ok && sd.Anon && !seen[sd] {
			seen[sd] = true
			for _, f := range sd.Fields {
				if !walk(f.Type) {
					return false
				}
			}
		}
		return true
	}
	walk(t)
}
