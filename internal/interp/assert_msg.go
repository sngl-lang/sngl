package interp

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
)

// RunAssert evaluates the expression passed to t.assert/t.must and, on
// failure, returns an *AssertError whose message reads as a complete
// statement of what the assertion expected vs what it observed. A second
// return value carries any runtime-evaluation error (always fatal).
//
// Operand rendering rule: each operand is shown as its evaluated value;
// when the operand is a non-literal, non-multiline expression, the source
// form follows in parens — e.g. "0 (c.count)". Literals and multiline
// expressions don't include the source form, since the value already
// stands for itself or wouldn't fit cleanly on the line.
func (env *Env) RunAssert(expr ir.Expr) (*AssertError, error) {
	switch e := expr.(type) {
	case *ir.Binary:
		switch e.Op {
		case ast.BinEq, ast.BinNeq, ast.BinLt, ast.BinLte, ast.BinGt, ast.BinGte:
			return env.assertCompare(expr, e)
		}
	case *ir.Unary:
		if e.Op == ast.UnaryNot {
			return env.assertNot(expr, e)
		}
	case *ir.Call:
		if container, needle, ok := containsCallShape(e); ok {
			return env.assertContains(expr, container, needle)
		}
	}
	return env.assertDefault(expr)
}

func (env *Env) assertCompare(top ir.Expr, e *ir.Binary) (*AssertError, error) {
	left, err := env.Eval(e.Left)
	if err != nil {
		return nil, err
	}
	right, err := env.Eval(e.Right)
	if err != nil {
		return nil, err
	}
	pass, err := compareOp(e.Op, left, right)
	if err != nil {
		return nil, err
	}
	if pass {
		return nil, nil
	}

	var msg string
	switch e.Op {
	case ast.BinEq:
		msg = formatEqFailure(e.Left, e.Right, left, right)
	case ast.BinNeq:
		msg = formatNeqFailure(e.Left, e.Right, left, right)
	default:
		msg = "wanted " + operandRepr(e.Left, left) + " " + binOpSym(e.Op) + " " + operandRepr(e.Right, right)
	}
	return &AssertError{Expr: top, Got: false, Msg: msg}, nil
}

// formatEqFailure: equality failed, so left != right. The literal side
// (when exactly one side is literal) reads as the wanted target; the
// other side carries the observed value with its source.
func formatEqFailure(le, re ir.Expr, lv, rv any) string {
	lLit := isLiteral(le)
	rLit := isLiteral(re)
	switch {
	case lLit && !rLit:
		return "wanted " + formatValue(lv) + ", got " + operandRepr(re, rv)
	case !lLit && rLit:
		return "wanted " + formatValue(rv) + ", got " + operandRepr(le, lv)
	default:
		return "wanted " + operandRepr(le, lv) + ", got " + operandRepr(re, rv)
	}
}

// formatNeqFailure: inequality failed, so both sides were equal. Phrase
// it as the value we didn't want, attributing it to the non-literal side.
func formatNeqFailure(le, re ir.Expr, lv, rv any) string {
	lLit := isLiteral(le)
	rLit := isLiteral(re)
	switch {
	case lLit && !rLit:
		return "didn't want " + formatValue(lv) + " (" + sourceOrEmpty(re) + ")"
	case !lLit && rLit:
		return "didn't want " + formatValue(rv) + " (" + sourceOrEmpty(le) + ")"
	case !lLit && !rLit:
		ls := sourceOrEmpty(le)
		rs := sourceOrEmpty(re)
		if ls != "" && rs != "" {
			return "didn't want " + formatValue(lv) + " (" + ls + " == " + rs + ")"
		}
		return "didn't want " + formatValue(lv)
	default:
		return "didn't want " + formatValue(lv)
	}
}

func (env *Env) assertNot(top ir.Expr, e *ir.Unary) (*AssertError, error) {
	v, err := env.Eval(e.Operand)
	if err != nil {
		return nil, err
	}
	b, ok := v.(bool)
	if !ok {
		return nil, fmt.Errorf("! requires bool operand, got %T", v)
	}
	if !b {
		return nil, nil
	}
	msg := "didn't want true"
	if src := includableSrc(e.Operand); src != "" {
		msg += " (" + src + ")"
	}
	return &AssertError{Expr: top, Got: true, Msg: msg}, nil
}

func (env *Env) assertContains(top ir.Expr, containerExpr, needleExpr ir.Expr) (*AssertError, error) {
	container, err := env.Eval(containerExpr)
	if err != nil {
		return nil, err
	}
	switch container.(type) {
	case []any, string:
	default:
		return env.assertDefault(top)
	}
	needle, err := env.Eval(needleExpr)
	if err != nil {
		return nil, err
	}
	if containsValue(container, needle) {
		return nil, nil
	}
	msg := "wanted " + operandRepr(containerExpr, container) +
		" to contain " + operandRepr(needleExpr, needle)
	return &AssertError{Expr: top, Got: false, Msg: msg}, nil
}

func (env *Env) assertDefault(expr ir.Expr) (*AssertError, error) {
	v, err := env.Eval(expr)
	if err != nil {
		return nil, err
	}
	b, ok := v.(bool)
	if !ok {
		return nil, fmt.Errorf("t.assert() requires bool argument, got %T (%v)", v, v)
	}
	if b {
		return nil, nil
	}
	msg := "wanted true, got false"
	if src := includableSrc(expr); src != "" {
		msg += " (" + src + ")"
	}
	return &AssertError{Expr: expr, Got: v, Msg: msg}, nil
}

// operandRepr renders an operand as "<value>" or "<value> (<source>)"
// depending on whether the source form adds information.
func operandRepr(e ir.Expr, val any) string {
	s := formatValue(val)
	if src := includableSrc(e); src != "" {
		return s + " (" + src + ")"
	}
	return s
}

// includableSrc returns the source form of e suitable for inline display,
// or "" when e is a literal (source is redundant with value) or the
// rendered source spans multiple lines (would break message layout).
func includableSrc(e ir.Expr) string {
	if isLiteral(e) {
		return ""
	}
	src := sourceOrEmpty(e)
	if src == "" || strings.ContainsRune(src, '\n') {
		return ""
	}
	return src
}

func sourceOrEmpty(e ir.Expr) string {
	if a := IRASTOf(e); a != nil {
		return parser.FormatExpr(a)
	}
	return ""
}

func isLiteral(e ir.Expr) bool {
	_, ok := e.(*ir.Literal)
	return ok
}

func binOpSym(op ast.BinaryOp) string {
	switch op {
	case ast.BinEq:
		return "=="
	case ast.BinNeq:
		return "!="
	case ast.BinLt:
		return "<"
	case ast.BinLte:
		return "<="
	case ast.BinGt:
		return ">"
	case ast.BinGte:
		return ">="
	}
	return "?"
}

// containsCallShape recognizes list.contains / string.contains calls in
// either of the two IR shapes the checker can produce:
//   - normalized: Func != nil with Func.Receiver in {"list","string"} and
//     Func.Name=="contains"; Args=[container, needle]
//   - receiver-form: Func == nil with Receiver carrying the container value
//     and the method name reachable via methodNameFromCall; Args=[needle]
//
// Receiver-typed dispatch is decided at runtime in assertContains based on
// the evaluated container — the checker leaves component-field receivers
// typed Dyn, so a static type guard would miss the most common case.
func containsCallShape(c *ir.Call) (container, needle ir.Expr, ok bool) {
	if c.Func != nil && c.Func.Name == "contains" &&
		(c.Func.Receiver == "list" || c.Func.Receiver == "string") &&
		len(c.Args) == 2 {
		return c.Args[0].Value, c.Args[1].Value, true
	}
	if c.Receiver != nil && methodNameFromCall(c) == "contains" && len(c.Args) == 1 {
		return c.Receiver, c.Args[0].Value, true
	}
	return nil, nil, false
}

func compareOp(op ast.BinaryOp, left, right any) (bool, error) {
	switch op {
	case ast.BinEq:
		if lu, ok := left.(unitValue); ok {
			if ru, ok := right.(unitValue); ok {
				return lu.Equal(ru), nil
			}
			return false, nil
		}
		return equals(left, right), nil
	case ast.BinNeq:
		if lu, ok := left.(unitValue); ok {
			if ru, ok := right.(unitValue); ok {
				return !lu.Equal(ru), nil
			}
			return true, nil
		}
		return !equals(left, right), nil
	case ast.BinLt, ast.BinLte, ast.BinGt, ast.BinGte:
		if err := orderable(left, right); err != nil {
			return false, err
		}
		switch cmp := compareNum(left, right); op {
		case ast.BinLt:
			return cmp < 0, nil
		case ast.BinLte:
			return cmp <= 0, nil
		case ast.BinGt:
			return cmp > 0, nil
		default:
			return cmp >= 0, nil
		}
	}
	return false, fmt.Errorf("compareOp: not a comparison op %d", op)
}

func containsValue(container, needle any) bool {
	if list, ok := container.([]any); ok {
		for _, v := range list {
			if equals(v, needle) {
				return true
			}
		}
		return false
	}
	if s, ok := container.(string); ok {
		return strings.Contains(s, fmt.Sprintf("%v", needle))
	}
	return false
}

// formatValue renders a runtime value for assertion messages. Strings are
// quoted, lists bracketed, units use their suffixed form, structs print
// field=value pairs.
func formatValue(v any) string {
	switch val := v.(type) {
	case nil:
		return "null"
	case string:
		return fmt.Sprintf("%q", val)
	case bool:
		if val {
			return "true"
		}
		return "false"
	case unitValue:
		return val.String()
	case []any:
		parts := make([]string, len(val))
		for i, item := range val {
			parts[i] = formatValue(item)
		}
		return "[" + strings.Join(parts, ", ") + "]"
	case *Struct:
		parts := make([]string, len(val.Fields))
		for i, f := range val.Fields {
			parts[i] = f.Name + ": " + formatValue(f.Value)
		}
		return "{" + strings.Join(parts, ", ") + "}"
	case map[string]any:
		keys := make([]string, 0, len(val))
		for k := range val {
			keys = append(keys, k)
		}
		sortStrings(keys)
		parts := make([]string, len(keys))
		for i, k := range keys {
			parts[i] = k + ": " + formatValue(val[k])
		}
		return "{" + strings.Join(parts, ", ") + "}"
	}
	return fmt.Sprintf("%v", v)
}

func sortStrings(s []string) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && s[j-1] > s[j]; j-- {
			s[j-1], s[j] = s[j], s[j-1]
		}
	}
}

func IRASTOf(e ir.Expr) ast.Expr {
	switch x := e.(type) {
	case *ir.Literal:
		if x.AST != nil {
			return x.AST
		}
	case *ir.Ident:
		if x.AST != nil {
			return x.AST
		}
	case *ir.Binary:
		if x.AST != nil {
			return x.AST
		}
	case *ir.Unary:
		if x.AST != nil {
			return x.AST
		}
	case *ir.Ternary:
		if x.AST != nil {
			return x.AST
		}
	case *ir.Call:
		if x.AST != nil {
			return x.AST
		}
	case *ir.Conversion:
		if x.AST != nil {
			return x.AST
		}
	case *ir.Select:
		if x.AST != nil {
			return x.AST
		}
	case *ir.Index:
		if x.AST != nil {
			return x.AST
		}
	case *ir.StructLit:
		if x.AST != nil {
			return x.AST
		}
	case *ir.ListLit:
		if x.AST != nil {
			return x.AST
		}
	case *ir.Lambda:
		if x.AST != nil {
			return x.AST
		}
	}
	return nil
}
