package main

import "reflect"

// omitFields walks v recursively and zeros any exported struct fields
// whose names appear in the omit set. v must be a pointer for mutation
// to take effect. Safe only when the value won't be used after dumping.
func omitFields(v any, omit map[string]bool) {
	omitWalk(reflect.ValueOf(v), omit, make(map[uintptr]bool))
}

func omitWalk(v reflect.Value, omit map[string]bool, visited map[uintptr]bool) {
	switch v.Kind() {
	case reflect.Pointer:
		if v.IsNil() {
			return
		}
		ptr := v.Pointer()
		if visited[ptr] {
			return
		}
		visited[ptr] = true
		omitWalk(v.Elem(), omit, visited)

	case reflect.Struct:
		t := v.Type()
		for i := range t.NumField() {
			f := v.Field(i)
			sf := t.Field(i)
			if !sf.IsExported() {
				continue
			}
			if omit[sf.Name] {
				f.Set(reflect.Zero(f.Type()))
				continue
			}
			omitWalk(f, omit, visited)
		}

	case reflect.Slice:
		for i := range v.Len() {
			omitWalk(v.Index(i), omit, visited)
		}

	case reflect.Interface:
		if !v.IsNil() {
			omitWalk(v.Elem(), omit, visited)
		}

	case reflect.Map:
		iter := v.MapRange()
		for iter.Next() {
			val := iter.Value()
			// Map values aren't addressable. Recurse through
			// pointers/interfaces to reach settable structs.
			if val.Kind() == reflect.Pointer || val.Kind() == reflect.Interface {
				omitWalk(val, omit, visited)
			}
		}
	}
}
