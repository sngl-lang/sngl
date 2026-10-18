package openapi

import (
	"errors"
	"fmt"
	"reflect"

	"duckfam.us/sngl/internal/buildhost"
	"duckfam.us/sngl/internal/interp"
	"duckfam.us/sngl/ir"
)

// The openapi: scheme's handler (lib/x/scheme/openapi) parses the document it
// read through this.
func init() {
	buildhost.RegisterIntrinsic("openapi.parse", parseIntrinsic)
}

// parseIntrinsic answers openapi.parse: the document read into values of the
// declaration's return type. It reads nothing but its argument -- the handler
// read the file, which recorded it -- so it records nothing.
func parseIntrinsic(_ *buildhost.Recorder, call *ir.Call, args []any) (any, error) {
	src, _ := args[0].(string)
	if call == nil || call.Func == nil || call.Func.Return == nil {
		return nil, errors.New("openapi.parse: no declared return type")
	}
	spec, err := Parse([]byte(src))
	if err != nil {
		return nil, err
	}
	return value(reflect.ValueOf(*spec), call.Func.Return)
}

// value is v built as a value of t: a struct by its fields' `sngl` tags, a
// slice as a list, a pointer as an option, and a string, a bool or a Kind as
// itself -- an atom enum's value is its member's name.
func value(v reflect.Value, t *ir.Type) (any, error) {
	switch v.Kind() {
	case reflect.String:
		return v.String(), nil
	case reflect.Bool:
		return v.Bool(), nil
	case reflect.Int:
		return int(v.Int()), nil
	case reflect.Pointer:
		if v.IsNil() {
			return nil, nil
		}
		if t.Kind != ir.TypeOption || len(t.Elems) != 1 {
			return nil, fmt.Errorf("openapi.parse: %s is no option", t)
		}
		return value(v.Elem(), t.Elems[0])
	case reflect.Slice:
		if t.Kind != ir.TypeList || len(t.Elems) != 1 {
			return nil, fmt.Errorf("openapi.parse: %s is no list", t)
		}
		out := make([]any, v.Len())
		for i := range out {
			x, err := value(v.Index(i), t.Elems[0])
			if err != nil {
				return nil, err
			}
			out[i] = x
		}
		return out, nil
	case reflect.Struct:
		def, _ := t.Decl.(*ir.StructDef)
		if def == nil {
			return nil, fmt.Errorf("openapi.parse: %s is no struct", t)
		}
		s := interp.NewStruct(def, t)
		for i := range v.NumField() {
			name := v.Type().Field(i).Tag.Get("sngl")
			if name == "" {
				continue
			}
			var ft *ir.Type
			for _, f := range def.Fields {
				if f.Name == name {
					ft = f.Type
				}
			}
			if ft == nil {
				return nil, fmt.Errorf("openapi.parse: %s declares no field %s", def.Name, name)
			}
			x, err := value(v.Field(i), ft)
			if err != nil {
				return nil, err
			}
			s.Set(name, x)
		}
		return s, nil
	}
	return nil, fmt.Errorf("openapi.parse: a Go %s has no SNGL value", v.Type())
}
