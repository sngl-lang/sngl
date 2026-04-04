package android

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// exprContext holds state needed to translate SNGL expression nodes into Kotlin expressions.
type exprContext struct {
	modelFields    map[string]bool
	computedFields map[string]bool
	localVars      map[string]bool
	structNames    map[string][]string // struct name → ordered field names
	eventVar       string
}

func (ec *exprContext) translateExpr(e ast.Node) string {
	if e == nil {
		return "null"
	}
	switch n := e.(type) {
	case *ast.LiteralExpr:
		return ec.translateLiteral(n)
	case *ast.IdentExpr:
		return ec.translateIdent(n)
	case *ast.SelectExpr:
		operand := ec.translateExpr(n.Operand)
		return operand + "." + n.Field
	case *ast.BinaryExpr:
		left := ec.translateExpr(n.Left)
		right := ec.translateExpr(n.Right)
		return "(" + left + " " + binaryOpToKt(n.Op) + " " + right + ")"
	case *ast.UnaryExpr:
		operand := ec.translateExpr(n.Operand)
		if n.Op == ast.UnaryNot {
			return "!" + operand
		}
		return "-" + operand
	case *ast.TernaryExpr:
		cond := ec.translateExpr(n.Cond)
		a := ec.translateExpr(n.Then)
		b := ec.translateExpr(n.Else)
		// Kotlin if-expressions require matching branch types.
		// Promote int literals to double when the other branch is float-typed.
		if isIntLiteral(n.Then) && isFloatExpr(n.Else) {
			a += ".0"
		} else if isIntLiteral(n.Else) && isFloatExpr(n.Then) {
			b += ".0"
		}
		return "(if (" + cond + ") " + a + " else " + b + ")"
	case *ast.IndexExpr:
		operand := ec.translateExpr(n.Operand)
		index := ec.translateExpr(n.Index)
		return operand + "[" + index + "]"
	case *ast.CallExpr:
		return ec.translateCall(n)
	case *ast.MethodExpr:
		target := ec.translateExpr(n.Receiver)
		argStrs := make([]string, len(n.Args))
		for i, a := range n.Args {
			argStrs[i] = ec.translateExpr(a)
		}
		return target + "." + n.Method + "(" + strings.Join(argStrs, ", ") + ")"
	case *ast.StructExpr:
		var parts []string
		for _, f := range n.Fields {
			parts = append(parts, f.Name+" = "+ec.translateExpr(f.Value))
		}
		return exportName(n.Name) + "(" + strings.Join(parts, ", ") + ")"
	case *ast.ListExpr:
		parts := make([]string, len(n.Elements))
		for i, el := range n.Elements {
			parts[i] = ec.translateExpr(el)
		}
		return "listOf(" + strings.Join(parts, ", ") + ")"
	case *ast.SpreadExpr:
		return "*" + ec.translateExpr(n.Operand)
	case *ast.InterpolationExpr:
		var sb strings.Builder
		sb.WriteByte('"')
		for _, p := range n.Parts {
			if lit, ok := p.(*ast.LiteralExpr); ok && lit.Kind == ast.LiteralString {
				s := fmt.Sprintf("%v", lit.Value)
				s = strings.ReplaceAll(s, "\\", "\\\\")
				s = strings.ReplaceAll(s, "\"", "\\\"")
				s = strings.ReplaceAll(s, "$", "\\$")
				sb.WriteString(s)
			} else {
				sb.WriteString("${")
				sb.WriteString(ec.translateExpr(p))
				sb.WriteByte('}')
			}
		}
		sb.WriteByte('"')
		return sb.String()
	case *ast.StmtBlock:
		stmts := ec.translateMutation(n)
		return strings.Join(stmts, "\n")
	case *ast.AssignStmt:
		stmts := ec.translateMutation(n)
		return strings.Join(stmts, "\n")
	case *ast.ToggleStmt:
		stmts := ec.translateMutation(n)
		return strings.Join(stmts, "\n")
	case *ast.CallStmt:
		return ec.translateCall(n.Call)
	case *ast.ParenExpr:
		return "(" + ec.translateExpr(n.Inner) + ")"
	default:
		return fmt.Sprintf("/* unsupported node %T */null", e)
	}
}

func (ec *exprContext) translateLiteral(n *ast.LiteralExpr) string {
	switch n.Kind {
	case ast.LiteralString:
		return fmt.Sprintf("%q", n.Value)
	case ast.LiteralInt:
		return fmt.Sprintf("%d", n.Value)
	case ast.LiteralFloat:
		s := fmt.Sprintf("%v", n.Value)
		if !strings.Contains(s, ".") {
			s += ".0"
		}
		return s
	case ast.LiteralBool:
		if n.Value.(bool) {
			return "true"
		}
		return "false"
	case ast.LiteralNull:
		return "null"
	case ast.LiteralColor:
		return fmt.Sprintf("%q", n.Value)
	case ast.LiteralUnit:
		return fmt.Sprintf("%q", n.Value)
	default:
		return fmt.Sprintf("%v", n.Value)
	}
}

func (ec *exprContext) translateIdent(n *ast.IdentExpr) string {
	name := n.Name
	if name == "event" && ec.eventVar != "" {
		return ec.eventVar
	}
	if ec.localVars[name] {
		return name
	}
	if ec.computedFields[name] {
		return name
	}
	if ec.modelFields[name] {
		return name
	}
	return name
}

func (ec *exprContext) translateCall(n *ast.CallExpr) string {
	fn := n.Func
	args := n.Args

	if fn == "string" && len(args) == 1 {
		return ec.translateExpr(args[0]) + ".toString()"
	}
	if fn == "int" && len(args) == 1 {
		return ec.translateExpr(args[0]) + ".toInt()"
	}
	if fn == "float" && len(args) == 1 {
		return ec.translateExpr(args[0]) + ".toDouble()"
	}
	if fn == "size" && len(args) == 1 {
		return ec.translateExpr(args[0]) + ".size"
	}

	// Struct constructor call
	if fields, ok := ec.structNames[fn]; ok {
		var parts []string
		for i, f := range fields {
			val := "null"
			if i < len(args) {
				val = ec.translateExpr(args[i])
			}
			parts = append(parts, f+" = "+val)
		}
		return exportName(fn) + "(" + strings.Join(parts, ", ") + ")"
	}

	argStrs := make([]string, len(args))
	for i, a := range args {
		argStrs[i] = ec.translateExpr(a)
	}
	return fn + "(" + strings.Join(argStrs, ", ") + ")"
}

func isIntLiteral(n ast.Node) bool {
	if lit, ok := n.(*ast.LiteralExpr); ok {
		return lit.Kind == ast.LiteralInt
	}
	return false
}

func isFloatExpr(n ast.Node) bool {
	switch e := n.(type) {
	case *ast.LiteralExpr:
		return e.Kind == ast.LiteralFloat
	case *ast.BinaryExpr:
		return isFloatExpr(e.Left) || isFloatExpr(e.Right)
	case *ast.IdentExpr:
		// Heuristic: if it's used with a float literal in an arithmetic expr, likely float
		return false
	}
	return false
}

func binaryOpToKt(op ast.BinaryOp) string {
	switch op {
	case ast.BinAdd:
		return "+"
	case ast.BinSub:
		return "-"
	case ast.BinMul:
		return "*"
	case ast.BinDiv:
		return "/"
	case ast.BinMod:
		return "%"
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
	case ast.BinAnd:
		return "&&"
	case ast.BinOr:
		return "||"
	default:
		return "?"
	}
}
