package javascript

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	celast "github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/operators"
	"github.com/google/cel-go/common/types"
)

func init() {
	codegen.RegisterLang(&Translator{})
}

// Translator implements codegen.LangTranslator for JavaScript.
type Translator struct{}

func (t *Translator) Lang() string { return "js" }

func (t *Translator) TranslateExpr(e celast.Expr, scope *codegen.ExprScope) string {
	return translateExpr(e, scope)
}

func (t *Translator) TranslateMutation(e celast.Expr, scope *codegen.ExprScope) []string {
	return translateMutation(e, scope)
}

func (t *Translator) TranslateLiteral(expr ast.Expr) string {
	if expr.Literal != nil {
		switch v := expr.Literal.(type) {
		case string:
			return fmt.Sprintf("%q", v)
		case int:
			return fmt.Sprintf("%d", v)
		case float64:
			return fmt.Sprintf("%v", v)
		case bool:
			if v {
				return "true"
			}
			return "false"
		}
	}
	return `""`
}

func (t *Translator) TypeToNative(hint string) string {
	switch hint {
	case "int", "float":
		return "number"
	case "bool":
		return "boolean"
	case "string":
		return "string"
	default:
		return "any"
	}
}

func (t *Translator) ExportName(name string) string {
	return name
}

// StructFields maps struct names to their ordered field names.
// Set this before translating expressions that contain struct constructors.
var StructFields map[string][]string

func translateExpr(e celast.Expr, scope *codegen.ExprScope) string {
	switch e.Kind() {
	case celast.LiteralKind:
		return translateLiteral(e)
	case celast.IdentKind:
		return translateIdent(e, scope)
	case celast.SelectKind:
		return translateSelect(e, scope)
	case celast.CallKind:
		return translateCall(e, scope)
	case celast.ListKind:
		return translateList(e, scope)
	case celast.StructKind:
		return translateStruct(e, scope)
	default:
		return fmt.Sprintf("/* unsupported CEL kind %d */null", e.Kind())
	}
}

func translateLiteral(e celast.Expr) string {
	v := e.AsLiteral()
	switch v.Type() {
	case types.StringType:
		return fmt.Sprintf("%q", v.Value())
	case types.IntType:
		return fmt.Sprintf("%d", v.Value())
	case types.DoubleType:
		return fmt.Sprintf("%v", v.Value())
	case types.BoolType:
		if v.Value().(bool) {
			return "true"
		}
		return "false"
	default:
		return fmt.Sprintf("%v", v.Value())
	}
}

func translateIdent(e celast.Expr, scope *codegen.ExprScope) string {
	name := e.AsIdent()
	if name == "event" && scope.EventVar != "" {
		return scope.EventVar
	}
	if scope.LocalVars[name] {
		return name
	}
	if scope.ComputedFields[name] {
		return "$" + name + "()"
	}
	if scope.ModelFields[name] {
		return "state." + name
	}
	return name
}

func translateSelect(e celast.Expr, scope *codegen.ExprScope) string {
	sel := e.AsSelect()
	operand := translateExpr(sel.Operand(), scope)
	return operand + "." + sel.FieldName()
}

func translateCall(e celast.Expr, scope *codegen.ExprScope) string {
	call := e.AsCall()
	fn := call.FunctionName()
	args := call.Args()

	if fn == operators.Divide && len(args) == 2 {
		left := translateExpr(args[0], scope)
		right := translateExpr(args[1], scope)
		if scope.NativeAST != nil {
			t := scope.NativeAST.GetType(e.ID())
			if t != nil && t == types.IntType {
				return "Math.trunc(" + left + " / " + right + ")"
			}
		}
		return "(" + left + " / " + right + ")"
	}

	if jsOp, ok := binaryOpMap[fn]; ok && len(args) == 2 {
		left := translateExpr(args[0], scope)
		right := translateExpr(args[1], scope)
		return "(" + left + " " + jsOp + " " + right + ")"
	}

	if fn == operators.Conditional && len(args) == 3 {
		cond := translateExpr(args[0], scope)
		a := translateExpr(args[1], scope)
		b := translateExpr(args[2], scope)
		return "(" + cond + " ? " + a + " : " + b + ")"
	}

	if fn == operators.LogicalNot && len(args) == 1 {
		return "!" + translateExpr(args[0], scope)
	}

	if fn == operators.Negate && len(args) == 1 {
		return "-" + translateExpr(args[0], scope)
	}

	if fn == operators.Index && len(args) == 2 {
		operand := translateExpr(args[0], scope)
		index := translateExpr(args[1], scope)
		return operand + "[" + index + "]"
	}

	if fn == "string" && len(args) == 1 {
		return "String(" + translateExpr(args[0], scope) + ")"
	}

	if fn == "size" && len(args) == 1 {
		return translateExpr(args[0], scope) + ".length"
	}

	if fn == "int" && len(args) == 1 {
		return "Math.trunc(" + translateExpr(args[0], scope) + ")"
	}

	if isMutationFunc(fn) {
		stmts := translateMutation(e, scope)
		return strings.Join(stmts, "\n")
	}

	// Struct constructor call
	if StructFields != nil {
		if fields, ok := StructFields[fn]; ok {
			var parts []string
			for i, f := range fields {
				val := "null"
				if i < len(args) {
					val = translateExpr(args[i], scope)
				}
				parts = append(parts, f+": "+val)
			}
			return "{" + strings.Join(parts, ", ") + "}"
		}
	}

	if call.IsMemberFunction() {
		target := translateExpr(call.Target(), scope)
		argStrs := make([]string, len(args))
		for i, a := range args {
			argStrs[i] = translateExpr(a, scope)
		}
		return target + "." + fn + "(" + strings.Join(argStrs, ", ") + ")"
	}

	argStrs := make([]string, len(args))
	for i, a := range args {
		argStrs[i] = translateExpr(a, scope)
	}
	return fn + "(" + strings.Join(argStrs, ", ") + ")"
}

func translateList(e celast.Expr, scope *codegen.ExprScope) string {
	list := e.AsList()
	elems := list.Elements()
	parts := make([]string, len(elems))
	for i, el := range elems {
		parts[i] = translateExpr(el, scope)
	}
	return "[" + strings.Join(parts, ", ") + "]"
}

func translateStruct(e celast.Expr, scope *codegen.ExprScope) string {
	s := e.AsStruct()
	var parts []string
	for _, field := range s.Fields() {
		sf := field.AsStructField()
		parts = append(parts, sf.Name()+": "+translateExpr(sf.Value(), scope))
	}
	return "{" + strings.Join(parts, ", ") + "}"
}

var binaryOpMap = map[string]string{
	operators.Add:           "+",
	operators.Subtract:      "-",
	operators.Multiply:      "*",
	operators.Modulo:        "%",
	operators.Equals:        "===",
	operators.NotEquals:     "!==",
	operators.Less:          "<",
	operators.LessEquals:    "<=",
	operators.Greater:       ">",
	operators.GreaterEquals: ">=",
	operators.LogicalAnd:    "&&",
	operators.LogicalOr:     "||",
}

func translateMutation(e celast.Expr, scope *codegen.ExprScope) []string {
	if e.Kind() == celast.ListKind {
		list := e.AsList()
		var stmts []string
		for _, el := range list.Elements() {
			stmts = append(stmts, translateMutation(el, scope)...)
		}
		return stmts
	}

	if e.Kind() != celast.CallKind {
		return []string{"// unsupported mutation expression"}
	}

	call := e.AsCall()
	fn := call.FunctionName()
	args := call.Args()

	switch fn {
	case "set":
		if len(args) == 2 {
			target := translateMutationTarget(args[0], scope)
			value := translateExpr(args[1], scope)
			return []string{target + " = " + value}
		}
	case "toggle":
		if len(args) == 1 {
			target := translateMutationTarget(args[0], scope)
			return []string{target + " = !" + target}
		}
	case "push":
		if len(args) == 2 {
			target := translateMutationTarget(args[0], scope)
			value := translateExpr(args[1], scope)
			return []string{target + ".push(" + value + ")"}
		}
	case "remove":
		if len(args) == 2 {
			target := translateMutationTarget(args[0], scope)
			idx := translateExpr(args[1], scope)
			return []string{target + ".splice(" + idx + ", 1)"}
		}
	}

	return []string{"// unsupported mutation: " + fn}
}

func translateMutationTarget(e celast.Expr, scope *codegen.ExprScope) string {
	if e.Kind() == celast.IdentKind {
		name := e.AsIdent()
		if scope.ModelFields[name] {
			return "state." + name
		}
		return name
	}
	if e.Kind() == celast.SelectKind {
		sel := e.AsSelect()
		operand := translateMutationTarget(sel.Operand(), scope)
		return operand + "." + sel.FieldName()
	}
	return translateExpr(e, scope)
}

func isMutationFunc(name string) bool {
	switch name {
	case "set", "toggle", "push", "remove":
		return true
	}
	return false
}
