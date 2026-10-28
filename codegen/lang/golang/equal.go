package golang

import (
	"strconv"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// SNGL's == is interp.Equal's: a struct field by field, a list element by
// element, an option by what it holds. Go's own == agrees for a scalar and a
// struct of scalars, and for nothing else: an option is a pointer, so two
// reads of one value are two pointers, and a slice -- or a struct holding one
// -- does not compile (`struct containing []string cannot be compared`). So
// == between values Go would get wrong is written out from their type, the
// way a copy is (valuecopy.go): a function literal per list, option or
// struct, evaluating each operand once.

// goComparesNatively reports whether Go's == between two values of t is
// SNGL's.
func goComparesNatively(t *ir.Type) bool {
	return comparesNatively(t, map[*ir.StructDef]bool{})
}

func comparesNatively(t *ir.Type, seen map[*ir.StructDef]bool) bool {
	if t == nil {
		return true
	}
	switch t.Kind {
	case ir.TypeList, ir.TypeOption:
		return false
	case ir.TypeStruct:
		def := ir.CopiedStruct(t)
		if def == nil {
			return true
		}
		if seen[def] {
			// A struct holding itself does so through a list or an option,
			// which answer for it.
			return true
		}
		seen[def] = true
		for _, f := range structFieldTypes(t) {
			if !comparesNatively(f.Type, seen) {
				return false
			}
		}
	}
	return true
}

// structFieldTypes is every field of a value of the struct type t, each at
// the value's type arguments.
func structFieldTypes(t *ir.Type) []ir.CopiedField {
	def := ir.CopiedStruct(t)
	if def == nil {
		return nil
	}
	var bind map[string]*ir.Type
	if len(def.TypeParams) > 0 && len(t.Elems) > 0 {
		bind = map[string]*ir.Type{}
		for i, tp := range def.TypeParams {
			if i < len(t.Elems) {
				bind[tp.Name] = t.Elems[i]
			}
		}
	}
	out := make([]ir.CopiedField, 0, len(def.Fields))
	for _, f := range def.Fields {
		if f == nil {
			continue
		}
		ft := f.Type
		if bind != nil {
			ft = ft.Substitute(bind)
		}
		out = append(out, ir.CopiedField{Field: f, Type: ft})
	}
	return out
}

// goEqualBinary is == or != between n's operands where Go's own would not be
// SNGL's, and false where it would -- a comparison with null included, which
// is the pointer's.
func goEqualBinary(n *ir.Binary, left, right string) (string, bool) {
	eq, ok := (&goEqualer{}).binary(n, left, right)
	if !ok || n.Op == ast.BinEq {
		return eq, ok
	}
	return "!" + eq, true
}

// goEqualer writes an equality, and records in h, where it is not nil, the
// helpers what it writes calls.
type goEqualer struct {
	n     int
	named map[*ir.StructDef]string
	h     *HelperSet
}

// binary is == between n's operands, or false where Go's own is SNGL's.
func (e *goEqualer) binary(n *ir.Binary, left, right string) (string, bool) {
	if n.Op != ast.BinEq && n.Op != ast.BinNeq {
		return "", false
	}
	lt, rt := n.Left.ExprType(), n.Right.ExprType()
	if lt == nil || rt == nil || lt.Kind == ir.TypeNull || rt.Kind == ir.TypeNull {
		return "", false
	}
	if optLeft, _, ok := OptionCompare(n); ok {
		opt, val, ot := left, right, lt
		if !optLeft {
			opt, val, ot = right, left, rt
		}
		if len(ot.Elems) != 1 || goComparesNatively(ot.Elems[0]) {
			// snglOptEq.
			return "", false
		}
		if e.h != nil {
			e.h.NeedOptHasBy = true
		}
		return "snglOptHasBy(" + opt + ", " + val + ", " + e.equalFunc(ot.Elems[0]) + ")", true
	}
	if goComparesNatively(lt) {
		return "", false
	}
	return e.equal(lt, left, right), true
}

func (e *goEqualer) fresh(prefix string) string {
	e.n++
	return prefix + strconv.Itoa(e.n)
}

// equalFunc is a Go func comparing two values of t.
func (e *goEqualer) equalFunc(t *ir.Type) string {
	x, y := e.fresh("_a"), e.fresh("_b")
	return "func(" + x + ", " + y + " " + IRTypeToGo(t) + ") bool { return " + e.equal(t, x, y) + " }"
}

// equal is the expression comparing a and b, two values of t, as SNGL's ==
// does. Each is evaluated once.
func (e *goEqualer) equal(t *ir.Type, a, b string) string {
	if goComparesNatively(t) {
		return "(" + a + " == " + b + ")"
	}
	switch t.Kind {
	case ir.TypeList, ir.TypeOption:
		elem := t.Elems[0]
		kind := "List"
		if t.Kind == ir.TypeOption {
			kind = "Opt"
		}
		if goComparesNatively(elem) {
			e.need(kind, false)
			return "sngl" + kind + "Equal(" + a + ", " + b + ")"
		}
		e.need(kind, true)
		return "sngl" + kind + "EqualBy(" + a + ", " + b + ", " + e.equalFunc(elem) + ")"
	case ir.TypeStruct:
		if e.named == nil {
			e.named = map[*ir.StructDef]string{}
		}
		def := ir.CopiedStruct(t)
		if name, ok := e.named[def]; ok {
			return name + "(" + a + ", " + b + ")"
		}
		goT := IRTypeToGo(t)
		if ir.Recursive(t) {
			name := e.fresh("_eq")
			e.named[def] = name
			x, y := e.fresh("_a"), e.fresh("_b")
			body := e.fields(t, x, y)
			delete(e.named, def)
			return "func() bool { var " + name + " func(" + goT + ", " + goT + ") bool; " + name + " = func(" + x + ", " + y + " " + goT +
				") bool { return " + body + " }; return " + name + "(" + a + ", " + b + ") }()"
		}
		x, y := e.fresh("_a"), e.fresh("_b")
		return "func(" + x + ", " + y + " " + goT + ") bool { return " + e.fields(t, x, y) + " }(" + a + ", " + b + ")"
	}
	return "(" + a + " == " + b + ")"
}

// need records the helper comparing a list or an option, by its elements' ==
// or by a func.
func (e *goEqualer) need(kind string, by bool) {
	if e.h == nil {
		return
	}
	switch {
	case kind == "List" && by:
		e.h.NeedListEqualBy = true
	case kind == "List":
		e.h.NeedListEqual = true
	case by:
		e.h.NeedOptEqualBy = true
	default:
		e.h.NeedOptEqual = true
	}
}

// fields is the conjunction comparing each field of x and y, two values of
// the struct type t.
func (e *goEqualer) fields(t *ir.Type, x, y string) string {
	fs := structFieldTypes(t)
	if len(fs) == 0 {
		return "true"
	}
	parts := make([]string, 0, len(fs))
	for _, f := range fs {
		name := "." + ExportName(f.Field.Name)
		parts = append(parts, e.equal(f.Type, x+name, y+name))
	}
	return strings.Join(parts, " && ")
}
