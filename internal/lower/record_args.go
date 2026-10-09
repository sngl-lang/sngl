package lower

import (
	"duckfam.us/sngl/ir"
)

// passRecordArgs is RecordArgs for a pipeline that lowers checked IR without
// optimizing it first -- a unit test, a test body's emitter. Where the
// optimizer ran, it finds only maps and changes nothing.
var passRecordArgs = pass{
	name:    "RecordArgs",
	enabled: func(Features) bool { return true },
	apply:   func(pkg *ir.Package, _ Features, _ Options) error { return RecordArgs(pkg) },
}

// RecordArgs rewrites the record a call hands an intrinsic that reads one by
// field name (ir.RecordArg) into the string-keyed map every runtime takes:
// `i18n.format(t, {name=n})` reaches each language's emitter as
// `i18n.format(t, {"name"=n})`, as the checker used to build it.
//
// A literal's fields become the entries in the order written. Any other
// operand is read a field at a time, and one that is not a plain path carries
// a spread site (ir.Select.Spread), so passSpreadOnce evaluates it once ahead
// of the statement, as it does a spread argument's.
//
// It runs on checked IR ahead of the first optimize, beside DataFamilies:
// converted there, the record's type is named by nothing, and the shake drops
// an anonymous struct no target has a reason to declare. The interpreter reads
// a struct as the map itself and needs no rewrite.
func RecordArgs(pkg *ir.Package) error {
	return ir.RewriteExprs(pkg, func(e ir.Expr) (ir.Expr, error) {
		call, ok := e.(*ir.Call)
		if !ok {
			return e, nil
		}
		i := ir.RecordArg(call.Func)
		if i < 0 || i >= len(call.Args) {
			return e, nil
		}
		if m := recordAsMap(call.Args[i].Value); m != nil {
			call.Args[i].Value = m
		}
		return e, nil
	})
}

// recordAsMap is the map literal standing for a struct value, or nil where v
// is not one.
func recordAsMap(v ir.Expr) *ir.MapLitIR {
	t := v.ExprType()
	if t == nil || t.Kind != ir.TypeStruct {
		return nil
	}
	m := &ir.MapLitIR{Type: ir.MapOf(ir.TypString, ir.TypDyn)}
	entry := func(name string, value ir.Expr) {
		m.Entries = append(m.Entries, ir.MapEntry{
			Key:   &ir.Literal{Type: ir.TypString, Value: name},
			Value: value,
		})
	}
	// A literal names its own fields, typed by a declaration or not: one
	// holding a dyn value interns none.
	if lit, ok := v.(*ir.StructLit); ok && !hasSpreadField(lit) {
		for _, f := range lit.Fields {
			entry(f.Name, f.Value)
		}
		return m
	}
	sd, _ := t.Decl.(*ir.StructDef)
	if sd == nil {
		return nil
	}
	var bindings map[string]*ir.Type
	if len(sd.TypeParams) > 0 && len(t.Elems) == len(sd.TypeParams) {
		bindings = make(map[string]*ir.Type, len(sd.TypeParams))
		for i, tp := range sd.TypeParams {
			bindings[tp.Name] = t.Elems[i]
		}
	}
	site := 0
	if !isPlainPath(v) {
		site = ir.NewSpreadID()
	}
	for _, f := range sd.Fields {
		ft := f.Type
		if bindings != nil {
			ft = ft.Substitute(bindings)
		}
		entry(f.Name, &ir.Select{Type: ft, Operand: v, Field: f.Name, Spread: site})
	}
	return m
}

func hasSpreadField(lit *ir.StructLit) bool {
	for _, f := range lit.Fields {
		if f.Spread || f.Name == "" {
			return true
		}
	}
	return false
}

// isPlainPath reports whether reading v once per field is the same as reading
// it once: a name, a literal, or a field or constant index of one -- the test
// the checker makes of a spread's operand.
func isPlainPath(v ir.Expr) bool {
	switch x := v.(type) {
	case *ir.Ident, *ir.Literal:
		return true
	case *ir.Select:
		return isPlainPath(x.Operand)
	case *ir.Index:
		_, lit := x.Idx.(*ir.Literal)
		return lit && isPlainPath(x.Operand)
	case *ir.Conversion:
		return isPlainPath(x.Operand)
	}
	return false
}
