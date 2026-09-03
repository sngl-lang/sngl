package codegen

import (
	"fmt"
	"reflect"
	"strconv"
	"unicode"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// ApplyOptions writes the values of a checked options struct literal into the
// exported fields of a Go struct via reflection. dst must be a non-nil pointer
// to struct. Field names match SNGL camelCase to Go PascalCase. Missing fields
// keep their Go zero value.
//
// Supported value kinds:
//   - *ir.Literal   — string/int/float/bool; color values pass through as their
//     "#RRGGBB[AA]" raw form
//   - *ir.Ident     — resolved const reference; reads the const's initializer
//   - *ir.StructLit — recurses into a nested Go struct
//   - *ir.ListLit   — written into a Go slice
//
// Returns an error on type mismatch, unknown destination field, or unsupported
// expression kind.
func ApplyOptions(dst any, opts *ir.StructLit) error {
	if opts == nil || len(opts.Fields) == 0 {
		return nil
	}
	v := reflect.ValueOf(dst)
	if v.Kind() != reflect.Pointer || v.IsNil() {
		return fmt.Errorf("ApplyOptions: dst must be non-nil pointer, got %T", dst)
	}
	s := v.Elem()
	if s.Kind() != reflect.Struct {
		return fmt.Errorf("ApplyOptions: dst must point to struct, got %s", s.Kind())
	}
	return applyStructLit(s, opts)
}

// OptionsFromMap builds a synthetic StructLit (no Def, no AST) from a Go map.
// For in-process callers like snapshot drivers and tests where building the
// struct via the checker is overkill. Each map value is wrapped as an *ir.Literal
// shaped to match its dynamic type.
func OptionsFromMap(m map[string]any) *ir.StructLit {
	if len(m) == 0 {
		return &ir.StructLit{}
	}
	lit := &ir.StructLit{Fields: make([]ir.FieldInit, 0, len(m))}
	for k, v := range m {
		lit.Fields = append(lit.Fields, ir.FieldInit{Name: k, Value: literalFromGo(v)})
	}
	return lit
}

// OptionField returns the value expression for the named field, or (nil, false)
// if absent. Useful for callers that want to inspect a single option without
// populating a Go struct.
//
// The value is resolved through resolveConst, because an option written in an
// output block is whatever expression the source named -- `test=true` is an
// Ident for a library const, not a literal -- while the same option set from
// the CLI or by SetOptionField is a literal already. Every reader below wants
// the value, so the two spellings are made one here rather than in each of
// them: OptionBool was one of the readers that did not, and
// `bubbletea(test=true)` silently generated no test file while
// `--opt test=true` did.
func OptionField(opts *ir.StructLit, name string) (ir.Expr, bool) {
	if opts == nil {
		return nil, false
	}
	for _, f := range opts.Fields {
		if f.Name == name {
			return resolveConst(f.Value), true
		}
	}
	return nil, false
}

// OptionBool reads a bool-valued option from opts, returning false if absent
// or non-bool.
func OptionBool(opts *ir.StructLit, name string) bool {
	v, ok := OptionField(opts, name)
	if !ok {
		return false
	}
	lit, _ := v.(*ir.Literal)
	return lit != nil && lit.Value == "true"
}

// OptionString reads a string-valued option from opts, returning "" if absent
// or non-string. Strips a single pair of surrounding double quotes since
// source-parsed string literals store Raw with quotes while CLI/programmatic
// values store it without.
func OptionString(opts *ir.StructLit, name string) string {
	v, ok := OptionField(opts, name)
	if !ok {
		return ""
	}
	lit, _ := v.(*ir.Literal)
	if lit == nil {
		return ""
	}
	raw := lit.Value
	if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
		raw = raw[1 : len(raw)-1]
	}
	return raw
}

// SetOptionField inserts or replaces a field on opts. Mutates in place. Used by
// CLI plumbing to inject `projectDir` / `main` from the runtime environment
// without round-tripping through string parsing.
func SetOptionField(opts *ir.StructLit, name string, value any) {
	val := literalFromGo(value)
	for i, f := range opts.Fields {
		if f.Name == name {
			opts.Fields[i].Value = val
			return
		}
	}
	opts.Fields = append(opts.Fields, ir.FieldInit{Name: name, Value: val})
}

// applyStructLit writes the fields of lit into dst (a reflect.Value of kind
// Struct, must be addressable).
//
// Fields whose Go destination is not present on dst are skipped silently:
// the merged options envelope unions stdlib + lang + platform fields, but a
// given Go Config only declares the subset it cares about. Source-level
// option-name validation happens upstream in the checker.
func applyStructLit(dst reflect.Value, lit *ir.StructLit) error {
	// Build a tag→fieldIndex map so structs can rename their option keys via
	// `option:"<name>"` tags. The fallback path below uses exportName(f.Name).
	t := dst.Type()
	tagMap := make(map[string]int, t.NumField())
	for i := 0; i < t.NumField(); i++ {
		if tag := t.Field(i).Tag.Get("option"); tag != "" {
			tagMap[tag] = i
		}
	}
	for _, f := range lit.Fields {
		if f.Spread {
			return fmt.Errorf("ApplyOptions: spread not supported for field %q", f.Name)
		}
		var field reflect.Value
		if idx, ok := tagMap[f.Name]; ok {
			field = dst.Field(idx)
		} else {
			goName := exportName(f.Name)
			field = dst.FieldByName(goName)
		}
		if !field.IsValid() {
			continue
		}
		if !field.CanSet() {
			return fmt.Errorf("ApplyOptions: Go field for %q is not settable", f.Name)
		}
		if err := assignValue(field, f.Value); err != nil {
			return fmt.Errorf("option %q: %w", f.Name, err)
		}
	}
	return nil
}

// assignValue writes an ir.Expr into a reflect.Value. The destination's kind
// drives type coercion.
func assignValue(dst reflect.Value, e ir.Expr) error {
	e = resolveConst(e)
	switch v := e.(type) {
	case *ir.Literal:
		return assignLiteral(dst, v)
	case *ir.StructLit:
		if dst.Kind() == reflect.Pointer {
			if dst.IsNil() {
				dst.Set(reflect.New(dst.Type().Elem()))
			}
			return applyStructLit(dst.Elem(), v)
		}
		if dst.Kind() != reflect.Struct {
			return fmt.Errorf("cannot assign struct literal to %s", dst.Kind())
		}
		return applyStructLit(dst, v)
	case *ir.ListLit:
		return assignList(dst, v)
	case nil:
		return nil
	default:
		return fmt.Errorf("unsupported expression %T", e)
	}
}

func assignLiteral(dst reflect.Value, lit *ir.Literal) error {
	raw := lit.Value
	if dst.Kind() == reflect.Pointer {
		if dst.IsNil() {
			dst.Set(reflect.New(dst.Type().Elem()))
		}
		return assignLiteral(dst.Elem(), lit)
	}
	switch dst.Kind() {
	case reflect.String:
		dst.SetString(unquoteString(raw))
		return nil
	case reflect.Bool:
		b, err := strconv.ParseBool(raw)
		if err != nil {
			return fmt.Errorf("expected bool, got %q", raw)
		}
		dst.SetBool(b)
		return nil
	case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
		n, err := strconv.ParseInt(raw, 0, 64)
		if err != nil {
			return fmt.Errorf("expected int, got %q", raw)
		}
		dst.SetInt(n)
		return nil
	case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
		n, err := strconv.ParseUint(raw, 0, 64)
		if err != nil {
			return fmt.Errorf("expected uint, got %q", raw)
		}
		dst.SetUint(n)
		return nil
	case reflect.Float32, reflect.Float64:
		f, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			return fmt.Errorf("expected float, got %q", raw)
		}
		dst.SetFloat(f)
		return nil
	}
	return fmt.Errorf("cannot assign literal to %s", dst.Kind())
}

func assignList(dst reflect.Value, list *ir.ListLit) error {
	if dst.Kind() != reflect.Slice {
		return fmt.Errorf("cannot assign list to %s", dst.Kind())
	}
	out := reflect.MakeSlice(dst.Type(), len(list.Elems), len(list.Elems))
	for i, el := range list.Elems {
		if err := assignValue(out.Index(i), el); err != nil {
			return fmt.Errorf("[%d]: %w", i, err)
		}
	}
	dst.Set(out)
	return nil
}

// resolveConst follows an Ident to its referenced const initializer if the
// symbol is a constant Var. Other Idents pass through unchanged.
//
// The initializer it reaches is unfolded: the optimizer walks a program's
// body, and a const declared in a library is not in one. `true` is
// `lib/builtin/bool.sngl`'s `0 == 0` -- the language has no bool literal to
// declare it with -- so an option written `minify=true` arrives here as a
// comparison and has to be evaluated, not just followed.
func resolveConst(e ir.Expr) ir.Expr {
	switch v := e.(type) {
	case *ir.Ident:
		c, ok := v.Sym.(*ir.Var)
		if !ok || !c.IsConst || c.Init == nil {
			return e
		}
		return resolveConst(c.Init)
	case *ir.Binary:
		if folded := foldComparison(v); folded != nil {
			return folded
		}
	}
	return e
}

// foldComparison evaluates a comparison of two literals, and reports nil for
// anything else. It is deliberately not a general constant folder: the only
// expression that has to reach an option is the one a builtin bool const is
// declared as.
func foldComparison(b *ir.Binary) ir.Expr {
	if b.Op != ast.BinEq && b.Op != ast.BinNeq {
		return nil
	}
	l, ok := resolveConst(b.Left).(*ir.Literal)
	if !ok {
		return nil
	}
	r, ok := resolveConst(b.Right).(*ir.Literal)
	if !ok || l.Type != r.Type {
		return nil
	}
	eq := l.Value == r.Value
	if b.Op == ast.BinNeq {
		eq = !eq
	}
	return &ir.Literal{Type: ir.TypBool, Value: strconv.FormatBool(eq)}
}

// literalFromGo wraps a Go value as an ir.Literal so synthetic StructLits
// match the shape produced by the checker.
func literalFromGo(v any) ir.Expr {
	switch x := v.(type) {
	case ir.Expr:
		return x
	case string:
		return &ir.Literal{Type: ir.TypString, Value: x}
	case bool:
		return &ir.Literal{Type: ir.TypBool, Value: strconv.FormatBool(x)}
	case int:
		return &ir.Literal{Type: ir.TypInt, Value: strconv.Itoa(x)}
	case int64:
		return &ir.Literal{Type: ir.TypInt, Value: strconv.FormatInt(x, 10)}
	case float64:
		return &ir.Literal{Type: ir.TypFloat, Value: strconv.FormatFloat(x, 'g', -1, 64)}
	}
	return &ir.Literal{Type: ir.TypString, Value: fmt.Sprintf("%v", v)}
}

// unquoteString strips the surrounding delimiters from a raw literal source
// string (matches the parser's three string forms: "...", `...`, """...""").
func unquoteString(raw string) string {
	if len(raw) >= 6 && raw[:3] == `"""` && raw[len(raw)-3:] == `"""` {
		return raw[3 : len(raw)-3]
	}
	if len(raw) >= 2 {
		first, last := raw[0], raw[len(raw)-1]
		if (first == '"' && last == '"') || (first == '`' && last == '`') {
			if first == '"' {
				if s, err := strconv.Unquote(raw); err == nil {
					return s
				}
			}
			return raw[1 : len(raw)-1]
		}
	}
	return raw
}

// exportName capitalizes the first rune so a SNGL camelCase field name maps to
// an exported Go field name. Mirrors codegen/lang/golang.ExportName but inlined
// to avoid the package-level import cycle.
func exportName(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}
