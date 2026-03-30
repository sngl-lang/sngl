package android

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
)

func (ec *exprContext) translateMutation(e ast.Node) []string {
	switch n := e.(type) {
	case *ast.StmtBlock:
		var stmts []string
		for _, s := range n.Stmts {
			stmts = append(stmts, ec.translateMutation(s)...)
		}
		return stmts
	case *ast.AssignStmt:
		target := ec.translateMutationTarget(n.Target)
		value := ec.translateExpr(n.Value)
		op := assignOpToKt(n.Op)
		return []string{target + " " + op + " " + value}
	case *ast.ToggleStmt:
		target := ec.translateMutationTarget(n.Target)
		return []string{target + " = !" + target}
	case *ast.MethodExpr:
		target := ec.translateMutationTarget(n.Receiver)
		switch n.Method {
		case "push":
			if len(n.Args) == 1 {
				value := ec.translateExpr(n.Args[0])
				return []string{target + ".add(" + value + ")"}
			}
		case "remove":
			if len(n.Args) == 1 {
				idx := ec.translateExpr(n.Args[0])
				return []string{target + ".removeAt(" + idx + ")"}
			}
		}
		argStrs := make([]string, len(n.Args))
		for i, a := range n.Args {
			argStrs[i] = ec.translateExpr(a)
		}
		return []string{target + "." + n.Method + "(" + strings.Join(argStrs, ", ") + ")"}
	case *ast.EmitStmt:
		argStrs := make([]string, len(n.Args))
		for i, a := range n.Args {
			argStrs[i] = ec.translateExpr(a)
		}
		name := "on" + strings.ToUpper(n.Name[:1]) + n.Name[1:]
		if len(argStrs) > 0 {
			return []string{name + "?.invoke(" + strings.Join(argStrs, ", ") + ")"}
		}
		return []string{name + "?.invoke()"}
	case *ast.CallStmt:
		return []string{ec.translateCall(n.Call)}
	case *ast.CallExpr:
		return []string{ec.translateCall(n)}
	default:
		return []string{"// unsupported mutation: " + fmt.Sprintf("%T", e)}
	}
}

func (ec *exprContext) translateMutationTarget(e ast.Node) string {
	switch n := e.(type) {
	case *ast.IdentExpr:
		return n.Name
	case *ast.SelectExpr:
		operand := ec.translateMutationTarget(n.Operand)
		return operand + "." + n.Field
	case *ast.IndexExpr:
		operand := ec.translateMutationTarget(n.Operand)
		index := ec.translateExpr(n.Index)
		return operand + "[" + index + "]"
	default:
		return ec.translateExpr(e)
	}
}

func assignOpToKt(op ast.AssignOp) string {
	switch op {
	case ast.AssignSet:
		return "="
	case ast.AssignAdd:
		return "+="
	case ast.AssignSub:
		return "-="
	case ast.AssignMul:
		return "*="
	case ast.AssignDiv:
		return "/="
	case ast.AssignMod:
		return "%="
	default:
		return "="
	}
}
