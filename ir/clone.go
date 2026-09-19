package ir

import "reflect"

// irPkgPath is the import path of this package; used by the cloner to decide
// which values to deep-copy (ir types) versus share (AST nodes, platform
// metadata carried in `any` fields, and other foreign types).
var irPkgPath = reflect.TypeFor[Package]().PkgPath()

// ClonePackage returns a deep, independent copy of pkg. Every internal
// cross-reference — Ident.Sym, Call.Func, StructLit.Def, Type.Decl, the
// Symbols table, and the LiftedCaptures/AddressedVars maps keyed by node
// pointer — is re-pointed to the corresponding cloned node, so the result
// can be optimized and lowered without mutating the original. This is what
// makes multi-target builds correct: each target lowers its own clone.
//
// What is NOT cloned (shared with the original):
//   - AST backreferences (*ast.*): the AST is immutable across compiler
//     phases, so cloning it would be wasteful and pointless.
//   - The immutable global primitive Type singletons (TypInt, TypString,
//     …): several codegen sites compare against these by pointer identity,
//     so they must stay shared.
//   - Values reached through `any`/interface fields (e.g. the
//     platform-specific data in Type.Meta): treated as opaque metadata.
//
// Cloning works by reflection over the object graph with a pointer-identity
// map, so aliasing and cycles are preserved automatically and new IR node
// kinds need no per-type clone code.
func ClonePackage(pkg *Package) *Package {
	if pkg == nil {
		return nil
	}
	return newCloner().clone(reflect.ValueOf(pkg)).Interface().(*Package)
}

// CloneExpr returns a deep, independent copy of one expression, on the same
// terms as ClonePackage. Its use is a value that several call sites splice into
// one tree: later phases mutate IR in place, so they must not share a node.
func CloneExpr(e Expr) Expr {
	if e == nil {
		return nil
	}
	return newCloner().clone(reflect.ValueOf(e)).Interface().(Expr)
}

// CloneExprSharingDecls copies e's expression *spine* while leaving every
// declaration it references shared: a symbol it names (an *ir.Var, *ir.Param,
// *ir.Context, *ir.Component, a *ir.Func a call resolves to), and the *ir.Type
// and *ir.StructDef its nodes are typed by. CloneExpr's rule is the opposite
// and is right for a whole package -- there the declarations are being copied
// too, so a reference re-points to the copy. For one expression they are not:
// a clone whose Ident.Sym pointed at a fresh *ir.Var would name a binding
// nothing declares, and one whose Call.Func pointed at a fresh *ir.Func would
// duplicate the callee.
//
// Its use is an expression several positions splice: later phases rewrite IR
// in place, so the positions must not share a node while naming the same
// declarations. A lambda is the one *ir.Func that is not a reference -- it is
// the expression's own body -- so that one is copied.
func CloneExprSharingDecls(e Expr) Expr {
	if e == nil {
		return nil
	}
	c := newCloner()
	c.shareDecls = true
	return c.clone(reflect.ValueOf(e)).Interface().(Expr)
}

func newCloner() *cloner {
	c := &cloner{seen: map[uintptr]reflect.Value{}}
	// Keep the immutable global primitive Type singletons shared: map each
	// to itself so any pointer to one clones back to the same value.
	for _, t := range []*Type{TypDyn, TypBool, TypInt, TypFloat, TypString, TypNull, TypVoid} {
		rv := reflect.ValueOf(t)
		c.seen[rv.Pointer()] = rv
	}
	return c
}

type cloner struct {
	// shareDecls leaves a referenced declaration shared rather than copied;
	// see CloneExprSharingDecls.
	shareDecls bool
	// deepNext overrides shareDecls for the next pointer hop alone, which is
	// how a lambda's body is copied while the declarations inside it stay
	// shared.
	deepNext bool
	// seen maps an original pointer address to its clone (as a reflect.Value
	// of the same pointer type), so a node reached from multiple places —
	// e.g. a *Var in pkg.Vars and referenced again via Ident.Sym — clones
	// once and all references share the single clone.
	seen map[uintptr]reflect.Value
}

func (c *cloner) clone(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		deep := c.deepNext
		c.deepNext = false
		if v.IsNil() {
			return v
		}
		ptr := v.Pointer()
		if cv, ok := c.seen[ptr]; ok {
			return cv
		}
		if c.shareDecls && !deep && isDeclType(v.Type().Elem()) {
			return v
		}
		// Share anything that isn't one of our IR types (AST nodes,
		// foreign structs). Returning the original pointer preserves its
		// identity across the whole clone.
		if !isIRType(v.Type().Elem()) {
			return v
		}
		nv := reflect.New(v.Type().Elem())
		c.seen[ptr] = nv // register before recursing to handle cycles
		nv.Elem().Set(c.clone(v.Elem()))
		return nv

	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		cloned := c.clone(v.Elem())
		out := reflect.New(v.Type()).Elem()
		out.Set(cloned)
		return out

	case reflect.Struct:
		// Only deep-copy our own struct types; share foreign structs
		// (ast.Pos, platform metadata) as-is. This also guarantees we never
		// recurse into a struct with unexported fields.
		if !isIRType(v.Type()) {
			return v
		}
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.NumField(); i++ {
			// A lambda's Func is the expression's own body rather than a
			// declaration it names, so it is copied even in share mode. One
			// hop only: the declarations reached from inside that body are
			// references like any other.
			c.deepNext = carriesFuncBody(v.Type()) && v.Type().Field(i).Name == "Func"
			out.Field(i).Set(c.clone(v.Field(i)))
		}
		c.deepNext = false
		return out

	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(c.clone(v.Index(i)))
		}
		return out

	case reflect.Map:
		if v.IsNil() {
			return v
		}
		out := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			out.SetMapIndex(c.clone(iter.Key()), c.clone(iter.Value()))
		}
		return out

	case reflect.Array:
		out := reflect.New(v.Type()).Elem()
		for i := 0; i < v.Len(); i++ {
			out.Index(i).Set(c.clone(v.Index(i)))
		}
		return out

	default:
		// Primitives, funcs, chans: value copy is the identity here.
		return v
	}
}

// declTypes is what CloneExprSharingDecls shares: the kinds an expression
// *refers* to. Every one is a declaration with an identity a reference is
// matched against -- by pointer, all over the compiler -- so copying it does
// not produce an equivalent expression, it produces one that names nobody.
var declTypes = map[reflect.Type]bool{
	reflect.TypeFor[Var]():       true,
	reflect.TypeFor[Param]():     true,
	reflect.TypeFor[LoopVar]():   true,
	reflect.TypeFor[Func]():      true,
	reflect.TypeFor[Component](): true,
	reflect.TypeFor[Context]():   true,
	reflect.TypeFor[SlotDecl]():  true,
	reflect.TypeFor[Window]():    true,
	reflect.TypeFor[StructDef](): true,
	reflect.TypeFor[EnumDef]():   true,
	reflect.TypeFor[UnitDef]():   true,
	reflect.TypeFor[Type]():      true,
}

func isDeclType(t reflect.Type) bool { return declTypes[t] }

// carriesFuncBody reports whether a Func reached through t's "Func" field is
// that expression's body rather than a declaration it calls.
func carriesFuncBody(t reflect.Type) bool {
	return t == reflect.TypeFor[Lambda]() || t == reflect.TypeFor[Closure]()
}

// isIRType reports whether t belongs to this package and should be deep-copied.
func isIRType(t reflect.Type) bool {
	return t.PkgPath() == irPkgPath
}
