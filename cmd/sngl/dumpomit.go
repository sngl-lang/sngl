package main

import "reflect"

// omitted is a copy of v without the named fields, and with hidden without
// every field JSON leaves out either. A copy, because what a dump reaches is
// shared: the library IR every check reads and the ASTs parsed once per
// process, which a dump run in-process must leave as it found them.
func omitted(v any, omit map[string]bool, hidden bool) any {
	p := pruner{omit: omit, hidden: hidden, copies: map[pointerKey]reflect.Value{}}
	return p.copy(reflect.ValueOf(v)).Interface()
}

type pruner struct {
	omit   map[string]bool
	hidden bool
	// copies is the copy made of each pointer, so a value shared in the
	// original is one value in the copy, and a cycle ends. Keyed by type as
	// well, since a struct and its first field share an address.
	copies map[pointerKey]reflect.Value
}

func (p pruner) copy(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return v
		}
		key := pointerKey{v.Pointer(), v.Type()}
		if cp, ok := p.copies[key]; ok {
			return cp
		}
		cp := reflect.New(v.Type().Elem())
		p.copies[key] = cp
		cp.Elem().Set(p.copy(v.Elem()))
		return cp

	case reflect.Struct:
		cp := reflect.New(v.Type()).Elem()
		cp.Set(v)
		t := v.Type()
		for i := range t.NumField() {
			sf := t.Field(i)
			if !sf.IsExported() {
				continue
			}
			f := cp.Field(i)
			if p.omit[sf.Name] || p.hidden && sf.Tag.Get("json") == "-" {
				f.Set(reflect.Zero(f.Type()))
				continue
			}
			f.Set(p.copy(v.Field(i)))
		}
		return cp

	case reflect.Slice:
		if v.IsNil() {
			return v
		}
		cp := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
		for i := range v.Len() {
			cp.Index(i).Set(p.copy(v.Index(i)))
		}
		return cp

	case reflect.Map:
		if v.IsNil() {
			return v
		}
		cp := reflect.MakeMapWithSize(v.Type(), v.Len())
		iter := v.MapRange()
		for iter.Next() {
			cp.SetMapIndex(iter.Key(), p.copy(iter.Value()))
		}
		return cp

	case reflect.Interface:
		if v.IsNil() {
			return v
		}
		cp := reflect.New(v.Type()).Elem()
		cp.Set(p.copy(v.Elem()))
		return cp
	}
	return v
}

type pointerKey struct {
	addr uintptr
	typ  reflect.Type
}
