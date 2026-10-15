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

// RecordArgs makes the record a call hands an intrinsic that reads one by
// field name (ir.RecordArg) a struct literal of its own: `i18n.format(t,
// {name=n})` keeps its literal, and any other operand is read a field at a
// time. The record stays the struct it was checked as; each language's
// emitter spells the string-keyed map its runtime takes from the literal's
// fields (codegen.RegisterRecordSpeller), so the map's catch-all value type
// exists only in emitted code.
//
// An operand that is not a plain path carries a spread site
// (ir.Select.Spread), so passSpreadOnce evaluates it once ahead of the
// statement, as it does a spread argument's. The interpreter reads a struct as
// the map itself and needs no rewrite.
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
		if lit := recordLiteral(call.Args[i].Value); lit != nil {
			call.Args[i].Value = lit
		}
		return e, nil
	})
}

// recordLiteral is the struct literal standing for a struct value, field by
// field, or nil where v is not one or already is one.
func recordLiteral(v ir.Expr) *ir.StructLit {
	t := v.ExprType()
	if t == nil || t.Kind != ir.TypeStruct {
		return nil
	}
	if lit, ok := v.(*ir.StructLit); ok && !hasSpreadField(lit) {
		return nil
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
	lit := &ir.StructLit{Type: t, Def: sd}
	for _, f := range sd.Fields {
		ft := f.Type
		if bindings != nil {
			ft = ft.Substitute(bindings)
		}
		lit.Fields = append(lit.Fields, ir.FieldInit{Name: f.Name, Value: &ir.Select{Type: ft, Operand: v, Field: f.Name, Spread: site}})
	}
	return lit
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
