package ir

import (
	"reflect"
	"strings"
)

// RewriteTypes replaces, in place, every *Type held anywhere in the IR
// reachable from root -- a field, a slice element, a map value -- with f of
// it, where f returns a different type. f sees the whole type and answers for
// what it holds; RewriteTypes does not descend into one.
//
// A type is held in more places than any walk over the IR's nodes names: an
// expression's type, a declaration's, a signature's, a field's, a cell of an
// analysis. So the walk is the cloner's, by reflection over the object graph,
// and it reaches whatever a new IR kind adds without being told.
//
// A frozen declaration is left alone, and nothing under it is visited: it is
// a library load's, which every check and every build share (see Freeze).
// So is a package other than root that is the library's or frozen: a
// namespace a call names holds the library package it imports, and through
// that package's imports and overrides a node instance, which no Frozen flag
// guards, reaches a library slot. A target's package is not frozen until a
// check merges its overrides, so the path says it is the library's. A
// scheme's package, which the build makes, is rewritten with the program. win32's placement retyped `sngl:ui/draw`'s once
// `sngl:platform/win32/api` imported `sngl:language/go`.
func RewriteTypes(root any, f func(*Type) *Type) {
	w := &typeRewriter{seen: map[uintptr]bool{}, f: f}
	if p, ok := root.(*Package); ok {
		w.root = p
	}
	w.walk(reflect.ValueOf(root))
}

var packagePtrType = reflect.TypeFor[*Package]()

type typeRewriter struct {
	seen map[uintptr]bool
	f    func(*Type) *Type
	root *Package
}

func (w *typeRewriter) walk(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() || !isIRType(v.Type().Elem()) || v.Type() == typePtrType {
			return
		}
		if v.Type() == packagePtrType {
			if p := v.Interface().(*Package); p != w.root && (p.Frozen || strings.HasPrefix(p.Path, "sngl:")) {
				return
			}
		}
		if w.seen[v.Pointer()] {
			return
		}
		w.seen[v.Pointer()] = true
		e := v.Elem()
		if e.Kind() == reflect.Struct {
			if fz := e.FieldByName("Frozen"); fz.IsValid() && fz.Kind() == reflect.Bool && fz.Bool() {
				return
			}
		}
		w.walk(e)
	case reflect.Interface:
		if !v.IsNil() {
			w.walk(v.Elem())
		}
	case reflect.Struct:
		if !isIRType(v.Type()) {
			return
		}
		for i := range v.NumField() {
			fv := v.Field(i)
			if !v.Type().Field(i).IsExported() {
				continue
			}
			// A declaration's package is where it came from, not part of it,
			// and a reference the cloner shares is no clone's to write.
			if kind := planOf(v.Type())[i]; kind == fieldDeclPkg || kind == fieldShare {
				continue
			}
			w.slot(fv)
		}
	case reflect.Slice, reflect.Array:
		for i := range v.Len() {
			w.slot(v.Index(i))
		}
	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			val := iter.Value()
			if val.Type() == typePtrType {
				if nt := w.replace(val); nt != nil {
					v.SetMapIndex(iter.Key(), reflect.ValueOf(nt))
				}
				continue
			}
			w.walk(val)
		}
	}
}

// slot rewrites a settable value that may hold a *Type itself, or walks
// into what it holds.
func (w *typeRewriter) slot(fv reflect.Value) {
	if fv.Type() == typePtrType {
		if nt := w.replace(fv); nt != nil && fv.CanSet() {
			fv.Set(reflect.ValueOf(nt))
		}
		return
	}
	w.walk(fv)
}

// replace is f's answer for a *Type value, or nil where it keeps the one it
// was handed.
func (w *typeRewriter) replace(v reflect.Value) *Type {
	if v.IsNil() {
		return nil
	}
	t := v.Interface().(*Type)
	if nt := w.f(t); nt != nil && nt != t {
		return nt
	}
	return nil
}
