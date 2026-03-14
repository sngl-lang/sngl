package golang

import (
	"fmt"
	"strings"
	"unicode"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	celast "github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/operators"
	"github.com/google/cel-go/common/types"
)

func init() {
	codegen.RegisterLang(&Translator{})
}

// Translator implements codegen.LangTranslator for Go.
type Translator struct{}

func (t *Translator) Lang() string { return "go" }

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
	case "int":
		return "int"
	case "float":
		return "float64"
	case "bool":
		return "bool"
	case "string", "color",
		"url", "email", "uuid", "regex", "base64", "ipv4", "ipv6", "hostname",
		"idn-email", "idn-hostname", "irl", "irl-reference", "url-reference",
		"url-template", "currency", "country-2", "country-3", "country-subdivision", "decimal":
		return "string"
	default:
		return "any"
	}
}

func (t *Translator) ExportName(name string) string {
	if name == "" {
		return name
	}
	runes := []rune(name)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}

// translateExpr converts a CEL AST expression into a Go expression string.
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
	default:
		return fmt.Sprintf("/* unsupported CEL kind %d */nil", e.Kind())
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
		return "m." + name + "()"
	}
	if scope.ModelFields[name] {
		return "m." + exportName(name)
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

	if goOp, ok := binaryOpMap[fn]; ok && len(args) == 2 {
		left := translateExpr(args[0], scope)
		right := translateExpr(args[1], scope)
		return "(" + left + " " + goOp + " " + right + ")"
	}

	if fn == operators.Conditional && len(args) == 3 {
		cond := translateExpr(args[0], scope)
		a := translateExpr(args[1], scope)
		b := translateExpr(args[2], scope)
		return "ternary(" + cond + ", " + a + ", " + b + ")"
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
		return "fmt.Sprint(" + translateExpr(args[0], scope) + ")"
	}

	if fn == "size" && len(args) == 1 {
		return "len(" + translateExpr(args[0], scope) + ")"
	}

	if fn == "int" && len(args) == 1 {
		return "int(" + translateExpr(args[0], scope) + ")"
	}

	if isMutationFunc(fn) {
		stmts := translateMutation(e, scope)
		return strings.Join(stmts, "\n")
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
	return "[]any{" + strings.Join(parts, ", ") + "}"
}

var binaryOpMap = map[string]string{
	operators.Add:           "+",
	operators.Subtract:      "-",
	operators.Multiply:      "*",
	operators.Divide:        "/",
	operators.Modulo:        "%",
	operators.Equals:        "==",
	operators.NotEquals:     "!=",
	operators.Less:          "<",
	operators.LessEquals:    "<=",
	operators.Greater:       ">",
	operators.GreaterEquals: ">=",
	operators.LogicalAnd:    "&&",
	operators.LogicalOr:     "||",
}

// translateMutation converts a CEL mutation call into Go assignment statements.
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
			return []string{target + " = append(" + target + ", " + value + ")"}
		}
	case "remove":
		if len(args) == 2 {
			target := translateMutationTarget(args[0], scope)
			idx := translateExpr(args[1], scope)
			return []string{target + " = append(" + target + "[:" + idx + "], " + target + "[" + idx + "+1:]...)"}
		}
	}

	return []string{"// unsupported mutation: " + fn}
}

func translateMutationTarget(e celast.Expr, scope *codegen.ExprScope) string {
	if e.Kind() == celast.IdentKind {
		name := e.AsIdent()
		if scope.ModelFields[name] {
			return "m." + exportName(name)
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

func exportName(s string) string {
	if s == "" {
		return s
	}
	runes := []rune(s)
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}
