package fyne

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

// exprContext holds state needed to translate SNGL expression nodes into Go expressions.
type exprContext struct {
	modelFields    map[string]bool     // bind/computed names → prefix with "m."
	computedFields map[string]bool     // computed names → call as methods m.name()
	localVars      map[string]bool     // for-loop vars, component params → no prefix
	structNames    map[string][]string // struct name → ordered field names
	eventVar       string              // what "event" maps to
}

// translateExpr converts a SNGL expression Node into a Go expression string.
func (ec *exprContext) translateExpr(e ast.Node) string {
	if e == nil {
		return "nil"
	}
	switch n := e.(type) {
	case *ast.LiteralExpr:
		return ec.translateLiteral(n)
	case *ast.IdentExpr:
		return ec.translateIdent(n)
	case *ast.SelectExpr:
		operand := ec.translateExpr(n.Operand)
		field := exportName(n.Field)
		return operand + "." + field
	case *ast.BinaryExpr:
		left := ec.translateExpr(n.Left)
		right := ec.translateExpr(n.Right)
		if n.Op == ast.BinDiv {
			return "(float64(" + left + ") / float64(" + right + "))"
		}
		return "(" + left + " " + binaryOpToGo(n.Op) + " " + right + ")"
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
		return "ternary(" + cond + ", " + a + ", " + b + ")"
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
		if fields, ok := ec.structNames[n.Name]; ok {
			var parts []string
			for i, f := range fields {
				val := "nil"
				if i < len(n.Fields) {
					val = ec.translateExpr(n.Fields[i].Value)
				}
				parts = append(parts, exportName(f)+": "+val)
			}
			return exportName(n.Name) + "{" + strings.Join(parts, ", ") + "}"
		}
		var parts []string
		for _, f := range n.Fields {
			parts = append(parts, exportName(f.Name)+": "+ec.translateExpr(f.Value))
		}
		return exportName(n.Name) + "{" + strings.Join(parts, ", ") + "}"
	case *ast.ListExpr:
		parts := make([]string, len(n.Elements))
		for i, el := range n.Elements {
			parts[i] = ec.translateExpr(el)
		}
		return "[]any{" + strings.Join(parts, ", ") + "}"
	case *ast.SpreadExpr:
		return ec.translateExpr(n.Operand) + "..."
	case *ast.InterpolationExpr:
		var fmtParts []string
		var args []string
		for _, part := range n.Parts {
			if lit, ok := part.(*ast.LiteralExpr); ok && lit.Kind == ast.LiteralString {
				fmtParts = append(fmtParts, strings.ReplaceAll(fmt.Sprintf("%v", lit.Value), "%", "%%"))
			} else {
				fmtParts = append(fmtParts, "%v")
				args = append(args, ec.translateExpr(part))
			}
		}
		if len(args) == 0 {
			return fmt.Sprintf("%q", strings.Join(fmtParts, ""))
		}
		return "fmt.Sprintf(" + fmt.Sprintf("%q", strings.Join(fmtParts, "")) + ", " + strings.Join(args, ", ") + ")"
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
		return ec.translateExpr(n.Call)
	case *ast.LambdaExpr:
		for _, param := range n.Params {
			ec.localVars[param] = true
		}
		body := ec.translateExpr(n.Body)
		for _, param := range n.Params {
			delete(ec.localVars, param)
		}
		params := make([]string, len(n.Params))
		for i, param := range n.Params {
			params[i] = param + " any"
		}
		return "func(" + strings.Join(params, ", ") + ") any { return " + body + " }"
	default:
		return fmt.Sprintf("/* unsupported node %T */nil", e)
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
		return "nil"
	case ast.LiteralColor:
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
		return "m." + name + "()"
	}
	if ec.modelFields[name] {
		return "m." + name
	}
	return name
}

func (ec *exprContext) translateCall(n *ast.CallExpr) string {
	fn := n.Func
	args := n.Args

	if fn == "string" && len(args) == 1 {
		return "fmt.Sprint(" + ec.translateExpr(args[0]) + ")"
	}
	if fn == "size" && len(args) == 1 {
		return "len(" + ec.translateExpr(args[0]) + ")"
	}
	if fn == "int" && len(args) == 1 {
		return "int(" + ec.translateExpr(args[0]) + ")"
	}
	if fn == "float" && len(args) == 1 {
		return "float64(" + ec.translateExpr(args[0]) + ")"
	}
	if fn == "push" && len(args) == 2 {
		return "append(" + ec.translateExpr(args[0]) + ", " + ec.translateExpr(args[1]) + ")"
	}
	if fn == "remove" && len(args) == 2 {
		list := ec.translateExpr(args[0])
		idx := ec.translateExpr(args[1])
		return "append(" + list + "[:" + idx + "], " + list + "[" + idx + "+1:]...)"
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

	argStrs := make([]string, len(args))
	for i, a := range args {
		argStrs[i] = ec.translateExpr(a)
	}
	// Extern functions are model fields — prefix with m.
	if ec.modelFields[fn] {
		fn = "m." + exportName(fn)
	}
	return fn + "(" + strings.Join(argStrs, ", ") + ")"
}

func binaryOpToGo(op ast.BinaryOp) string {
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

// exprToGoValue converts an ast.Expr to a Go value string.
func exprToGoValue(expr ast.Expr, ec *exprContext) string {
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
		default:
			return fmt.Sprintf("%v", v)
		}
	}
	if expr.SNGL != nil && ec != nil {
		return ec.translateExpr(expr.SNGL)
	}
	return `""`
}

// exprToGoCond converts an ast.Expr to a Go boolean expression string.
func exprToGoCond(expr ast.Expr, ec *exprContext) string {
	if expr.SNGL != nil {
		return ec.translateExpr(expr.SNGL)
	}
	if expr.Literal != nil {
		if v, ok := expr.Literal.(bool); ok {
			if v {
				return "true"
			}
			return "false"
		}
	}
	return "true"
}

// exprToGoStringList converts an ast.Expr that should be a list of strings
// into a Go []string expression.
func exprToGoStringList(expr ast.Expr, ec *exprContext) string {
	if expr.SNGL != nil {
		if list, ok := expr.SNGL.(*ast.ListExpr); ok {
			parts := make([]string, len(list.Elements))
			for i, el := range list.Elements {
				parts[i] = ec.translateExpr(el)
			}
			return "[]string{" + strings.Join(parts, ", ") + "}"
		}
		return ec.translateExpr(expr.SNGL)
	}
	return "nil"
}
