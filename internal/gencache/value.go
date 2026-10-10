package gencache

import (
	"fmt"
	"reflect"
	"strconv"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/internal/parser"
	"duckfam.us/sngl/pkg/go/consteval"
)

// EncodeValue writes a Go value as SNGL, for a producer whose output is data
// rather than declarations. It is the encoding the compile-time evaluator
// reports values in, so one reader serves both.
func EncodeValue(v any) ([]byte, error) { return consteval.Encode(v) }

// DecodeValue reads SNGL written by EncodeValue back into out, which must be a
// pointer. A field the Go type does not have is an error, so a value stored
// for a different shape of type is produced again rather than read partially.
func DecodeValue(src []byte, out any) error {
	e, err := parser.ParseNativeValue("gencache value", src)
	if err != nil {
		return err
	}
	rv := reflect.ValueOf(out)
	if rv.Kind() != reflect.Pointer || rv.IsNil() {
		return fmt.Errorf("gencache: DecodeValue into %T, want a non-nil pointer", out)
	}
	return decode(e, rv.Elem())
}

func decode(e ast.Expr, v reflect.Value) error {
	if isNull(e) {
		v.SetZero()
		return nil
	}
	switch v.Kind() {
	case reflect.Pointer:
		p := reflect.New(v.Type().Elem())
		if err := decode(e, p.Elem()); err != nil {
			return err
		}
		v.Set(p)
		return nil
	case reflect.Struct:
		s, ok := e.(*ast.StructExpr)
		if !ok {
			return fmt.Errorf("want a %s literal, have %T", v.Type(), e)
		}
		for _, f := range s.Fields {
			fv := v.FieldByName(f.Name)
			if !fv.IsValid() || !fv.CanSet() {
				return fmt.Errorf("%s has no field %s", v.Type(), f.Name)
			}
			if err := decode(f.Value, fv); err != nil {
				return fmt.Errorf("%s.%s: %w", v.Type().Name(), f.Name, err)
			}
		}
		return nil
	case reflect.Slice:
		l, ok := e.(*ast.ListExpr)
		if !ok {
			return fmt.Errorf("want a list, have %T", e)
		}
		out := reflect.MakeSlice(v.Type(), len(l.Elements), len(l.Elements))
		for i, el := range l.Elements {
			if err := decode(el, out.Index(i)); err != nil {
				return fmt.Errorf("[%d]: %w", i, err)
			}
		}
		v.Set(out)
		return nil
	case reflect.Map:
		out := reflect.MakeMap(v.Type())
		switch m := e.(type) {
		case *ast.MapLit:
			for _, ent := range m.Entries {
				k := reflect.New(v.Type().Key()).Elem()
				if err := decode(ent.Key, k); err != nil {
					return fmt.Errorf("map key: %w", err)
				}
				val := reflect.New(v.Type().Elem()).Elem()
				if err := decode(ent.Value, val); err != nil {
					return fmt.Errorf("[%v]: %w", k, err)
				}
				out.SetMapIndex(k, val)
			}
		case *ast.StructExpr:
			// `{}` reads as an empty struct literal as readily as an empty map.
			if s := m; s.Name != "" || len(s.Fields) > 0 {
				return fmt.Errorf("want a map, have a %s literal", s.Name)
			}
		default:
			return fmt.Errorf("want a map, have %T", e)
		}
		v.Set(out)
		return nil
	}

	if id, ok := e.(*ast.IdentExpr); ok && v.Kind() == reflect.Bool && (id.Name == "true" || id.Name == "false") {
		v.SetBool(id.Name == "true")
		return nil
	}
	lit, neg, err := literal(e)
	if err != nil {
		return err
	}
	switch v.Kind() {
	case reflect.String:
		s, ok := lit.StringValue()
		if !ok {
			return fmt.Errorf("want a string, have %s", lit.Raw)
		}
		v.SetString(s)
	case reflect.Bool:
		b, err := strconv.ParseBool(lit.Raw)
		if err != nil {
			return fmt.Errorf("want a bool, have %s", lit.Raw)
		}
		v.SetBool(b)
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(neg+lit.Raw, 10, v.Type().Bits())
		if err != nil {
			return err
		}
		v.SetInt(n)
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
		n, err := strconv.ParseUint(neg+lit.Raw, 10, v.Type().Bits())
		if err != nil {
			return err
		}
		v.SetUint(n)
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(neg+lit.Raw, v.Type().Bits())
		if err != nil {
			return err
		}
		v.SetFloat(f)
	default:
		return fmt.Errorf("cannot decode into %s", v.Type())
	}
	return nil
}

// literal unwraps a literal, and a negated one, which is how a negative
// number is written.
func literal(e ast.Expr) (*ast.LiteralExpr, string, error) {
	if u, ok := e.(*ast.UnaryExpr); ok && u.Op == ast.UnaryNeg {
		lit, ok := u.Operand.(*ast.LiteralExpr)
		if !ok {
			return nil, "", fmt.Errorf("want a literal, have -%T", u.Operand)
		}
		return lit, "-", nil
	}
	lit, ok := e.(*ast.LiteralExpr)
	if !ok {
		return nil, "", fmt.Errorf("want a literal, have %T", e)
	}
	return lit, "", nil
}

func isNull(e ast.Expr) bool {
	switch n := e.(type) {
	case *ast.LiteralExpr:
		return n.Kind == ast.LiteralNull
	case *ast.IdentExpr:
		return n.Name == "null"
	}
	return false
}
