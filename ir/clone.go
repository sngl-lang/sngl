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
//   - Foreign values reached through `any`/interface fields (e.g. the
//     platform-specific data in Type.Meta): treated as opaque metadata.
//
// Cloning works by reflection over the object graph with a pointer-identity
// map, so aliasing and cycles are preserved automatically and new IR node
// kinds need no per-type clone code.
func ClonePackage(pkg *Package) *Package {
	if pkg == nil {
		return nil
	}
	c := &cloner{seen: map[uintptr]reflect.Value{}}
	// Keep the immutable global primitive Type singletons shared: map each
	// to itself so any pointer to one clones back to the same value.
	for _, t := range []*Type{TypDyn, TypBool, TypInt, TypFloat, TypString, TypNull, TypVoid, TypShape} {
		rv := reflect.ValueOf(t)
		c.seen[rv.Pointer()] = rv
	}
	return c.clone(reflect.ValueOf(pkg)).Interface().(*Package)
}

type cloner struct {
	// seen maps an original pointer address to its clone (as a reflect.Value
	// of the same pointer type), so a node reached from multiple places —
	// e.g. a *Var in pkg.Vars and referenced again via Ident.Sym — clones
	// once and all references share the single clone.
	seen map[uintptr]reflect.Value
}

func (c *cloner) clone(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		ptr := v.Pointer()
		if cv, ok := c.seen[ptr]; ok {
			return cv
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
			out.Field(i).Set(c.clone(v.Field(i)))
		}
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

// isIRType reports whether t belongs to this package and should be deep-copied.
func isIRType(t reflect.Type) bool {
	return t.PkgPath() == irPkgPath
}
