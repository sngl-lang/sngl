package ir

import "reflect"

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
func RewriteTypes(root any, f func(*Type) *Type) {
	w := &typeRewriter{seen: map[uintptr]bool{}, f: f}
	w.walk(reflect.ValueOf(root))
}

type typeRewriter struct {
	seen map[uintptr]bool
	f    func(*Type) *Type
}

func (w *typeRewriter) walk(v reflect.Value) {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() || !isIRType(v.Type().Elem()) || v.Type() == typePtrType {
			return
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
			// A declaration's package is where it came from, not part of it.
			if v.Type() == declInfoType && v.Type().Field(i).Name == "Pkg" {
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
