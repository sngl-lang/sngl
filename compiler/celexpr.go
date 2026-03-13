package compiler

import (
	"fmt"
	"strings"

	celast "github.com/google/cel-go/common/ast"
	"github.com/google/cel-go/common/operators"
	"github.com/google/cel-go/common/types"
)

// exprContext holds state needed to translate CEL AST nodes into Go expressions.
type exprContext struct {
	modelFields    map[string]bool     // bind/computed names → prefix with "m."
	computedFields map[string]bool     // computed names → call as methods m.name()
	localVars      map[string]bool     // for-loop vars, component params → no prefix
	structNames    map[string][]string // struct name → ordered field names
	nativeAST      *celast.AST         // for GetType(id)
	eventVar       string              // what "event" maps to
}

// translateExpr converts a CEL AST expression into a Go expression string.
func (ec *exprContext) translateExpr(e celast.Expr) string {
	switch e.Kind() {
	case celast.LiteralKind:
		return ec.translateLiteral(e)
	case celast.IdentKind:
		return ec.translateIdent(e)
	case celast.SelectKind:
		return ec.translateSelect(e)
	case celast.CallKind:
		return ec.translateCall(e)
	case celast.ListKind:
		return ec.translateList(e)
	default:
		return fmt.Sprintf("/* unsupported CEL kind %d */nil", e.Kind())
	}
}

func (ec *exprContext) translateLiteral(e celast.Expr) string {
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

func (ec *exprContext) translateIdent(e celast.Expr) string {
	name := e.AsIdent()
	if name == "event" && ec.eventVar != "" {
		return ec.eventVar
	}
	if ec.localVars[name] {
		return name
	}
	if ec.computedFields[name] {
		return "m." + name + "()"
	}
	if ec.modelFields[name] {
		return "m." + exportName(name)
	}
	return name
}

func (ec *exprContext) translateSelect(e celast.Expr) string {
	sel := e.AsSelect()
	operand := ec.translateExpr(sel.Operand())
	field := exportName(sel.FieldName())
	return operand + "." + field
}

func (ec *exprContext) translateCall(e celast.Expr) string {
	call := e.AsCall()
	fn := call.FunctionName()
	args := call.Args()

	// Binary operators
	if goOp, ok := binaryOpMap[fn]; ok && len(args) == 2 {
		left := ec.translateExpr(args[0])
		right := ec.translateExpr(args[1])
		// String concatenation uses + in Go too
		return "(" + left + " " + goOp + " " + right + ")"
	}

	// Conditional
	if fn == operators.Conditional && len(args) == 3 {
		cond := ec.translateExpr(args[0])
		a := ec.translateExpr(args[1])
		b := ec.translateExpr(args[2])
		return "ternary(" + cond + ", " + a + ", " + b + ")"
	}

	// Unary not
	if fn == operators.LogicalNot && len(args) == 1 {
		return "!" + ec.translateExpr(args[0])
	}

	// Unary negate
	if fn == operators.Negate && len(args) == 1 {
		return "-" + ec.translateExpr(args[0])
	}

	// Index operator
	if fn == operators.Index && len(args) == 2 {
		operand := ec.translateExpr(args[0])
		index := ec.translateExpr(args[1])
		return operand + "[" + index + "]"
	}

	// Type conversion: string(x)
	if fn == "string" && len(args) == 1 {
		return "fmt.Sprint(" + ec.translateExpr(args[0]) + ")"
	}

	// size(x) → len(x)
	if fn == "size" && len(args) == 1 {
		return "len(" + ec.translateExpr(args[0]) + ")"
	}

	// int(x)
	if fn == "int" && len(args) == 1 {
		return "int(" + ec.translateExpr(args[0]) + ")"
	}

	// Mutation functions are delegated to translateMutation
	if isMutationFunc(fn) {
		stmts := ec.translateMutation(e)
		return strings.Join(stmts, "\n")
	}

	// Struct constructor call
	if fields, ok := ec.structNames[fn]; ok {
		var parts []string
		for i, f := range fields {
			val := "nil"
			if i < len(args) {
				val = ec.translateExpr(args[i])
			}
			parts = append(parts, exportName(f)+": "+val)
		}
		return exportName(fn) + "{" + strings.Join(parts, ", ") + "}"
	}

	// Member function call
	if call.IsMemberFunction() {
		target := ec.translateExpr(call.Target())
		argStrs := make([]string, len(args))
		for i, a := range args {
			argStrs[i] = ec.translateExpr(a)
		}
		return target + "." + fn + "(" + strings.Join(argStrs, ", ") + ")"
	}

	// Generic function call
	argStrs := make([]string, len(args))
	for i, a := range args {
		argStrs[i] = ec.translateExpr(a)
	}
	return fn + "(" + strings.Join(argStrs, ", ") + ")"
}

func (ec *exprContext) translateList(e celast.Expr) string {
	list := e.AsList()
	elems := list.Elements()
	parts := make([]string, len(elems))
	for i, el := range elems {
		parts[i] = ec.translateExpr(el)
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

func isMutationFunc(name string) bool {
	switch name {
	case "set", "toggle", "push", "remove":
		return true
	}
	return false
}
