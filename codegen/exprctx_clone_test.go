package codegen

import (
	"reflect"
	"testing"
)

// Clone is a hand-written field list, and a field left out of it is not a
// compile error -- the clone just silently loses that part of the scope. That
// is how a window-scoped translation lost its window: every name in the body
// resolved against package scope instead, so a read of the window's own state
// rendered as a bare identifier no target had declared.
//
// This walks the struct by reflection, so a new field fails here by name until
// Clone carries it.
func TestCloneCarriesEveryField(t *testing.T) {
	// Fields Clone deliberately re-allocates rather than aliasing: the copy
	// must be equivalent, not identical, so they are compared by content.
	deepCopied := map[string]bool{
		"Locals": true, "Renames": true, "RawFieldAccess": true,
		"MethodFields": true, "IdentRewrites": true,
	}

	rt := reflect.TypeFor[ExprCtx]()
	orig := reflect.New(rt).Elem()
	// Fill every field with a distinguishable non-zero value.
	for i := range rt.NumField() {
		f := orig.Field(i)
		switch f.Kind() {
		case reflect.String:
			f.SetString("x")
		case reflect.Bool:
			f.SetBool(true)
		case reflect.Map:
			m := reflect.MakeMap(f.Type())
			ev := reflect.New(f.Type().Elem()).Elem()
			if ev.Kind() == reflect.Bool {
				ev.SetBool(true)
			}
			m.SetMapIndex(mapKey(f.Type().Key()), ev)
			f.Set(m)
		case reflect.Pointer, reflect.Interface:
			if f.Kind() == reflect.Pointer {
				f.Set(reflect.New(f.Type().Elem()))
			}
		}
	}

	src := orig.Addr().Interface().(*ExprCtx)
	got := reflect.ValueOf(src.Clone()).Elem()

	for i := range rt.NumField() {
		name := rt.Field(i).Name
		want, have := orig.Field(i), got.Field(i)
		if want.IsZero() {
			continue // nothing to lose (an interface field left nil above)
		}
		if have.IsZero() {
			t.Errorf("ExprCtx.Clone drops %s\n"+
				"\tEvery field must be carried by Clone, or the scope it holds is\n"+
				"\tsilently lost in every cloned translation context.", name)
			continue
		}
		if deepCopied[name] {
			continue // re-allocated on purpose; non-zero is the assertion
		}
		if want.Kind() == reflect.Map {
			continue // shared maps compare equal by header; non-zero suffices
		}
		if want.Comparable() && want.Interface() != have.Interface() {
			t.Errorf("ExprCtx.Clone changed %s", name)
		}
	}
}

// mapKey is a distinguishable non-zero key of kt. Not every map here is keyed
// by a string any more -- ModelParamFuncs is keyed by *ir.Func -- and
// converting "k" to a pointer type panics.
func mapKey(kt reflect.Type) reflect.Value {
	if kt.Kind() == reflect.Pointer {
		return reflect.New(kt.Elem())
	}
	return reflect.ValueOf("k").Convert(kt)
}
