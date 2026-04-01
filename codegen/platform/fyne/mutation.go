package fyne

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
)

// translateMutation converts a SNGL statement node into Go assignment statements.
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
		switch n.Op {
		case ast.AssignAdd:
			return []string{target + " = " + target + " + " + value}
		case ast.AssignSub:
			return []string{target + " = " + target + " - " + value}
		case ast.AssignMul:
			return []string{target + " = " + target + " * " + value}
		case ast.AssignDiv:
			return []string{target + " = " + target + " / " + value}
		case ast.AssignMod:
			return []string{target + " = " + target + " % " + value}
		default:
			return []string{target + " = " + value}
		}
	case *ast.ToggleStmt:
		target := ec.translateMutationTarget(n.Target)
		return []string{target + " = !" + target}
	case *ast.MethodExpr:
		// Alert namespace — translate to toast queue
		if ident, ok := n.Receiver.(*ast.IdentExpr); ok && ident.Name == "Alert" {
			return ec.translateAlert(n)
		}
		target := ec.translateMutationTarget(n.Receiver)
		switch n.Method {
		case "push":
			if len(n.Args) == 1 {
				value := ec.translateExpr(n.Args[0])
				return []string{target + " = append(" + target + ", " + value + ")"}
			}
		case "remove":
			if len(n.Args) == 1 {
				idx := ec.translateExpr(n.Args[0])
				return []string{target + " = append(" + target + "[:" + idx + "], " + target + "[" + idx + "+1:]...)"}
			}
		}
		return []string{ec.translateExpr(n)}
	case *ast.CallStmt:
		return []string{ec.translateExpr(n.Call)}
	case *ast.CallExpr:
		return []string{ec.translateExpr(n)}
	default:
		return []string{fmt.Sprintf("// unsupported mutation: %T", e)}
	}
}

// translateMutationTarget translates a SNGL expression used as a mutation target.
func (ec *exprContext) translateMutationTarget(e ast.Node) string {
	switch n := e.(type) {
	case *ast.IdentExpr:
		if ec.modelFields[n.Name] {
			return "m." + n.Name
		}
		return n.Name
	case *ast.SelectExpr:
		operand := ec.translateMutationTarget(n.Operand)
		return operand + "." + exportName(n.Field)
	case *ast.IndexExpr:
		operand := ec.translateMutationTarget(n.Operand)
		index := ec.translateExpr(n.Index)
		return operand + "[" + index + "]"
	default:
		return ec.translateExpr(e)
	}
}

// translateAlert translates Alert.toast/info/warn/error calls to toast queue operations.
func (ec *exprContext) translateAlert(n *ast.MethodExpr) []string {
	switch n.Method {
	case "toast":
		msg := ec.translateExpr(n.Args[0])
		variant := `"info"`
		if len(n.Args) > 1 {
			variant = ec.translateExpr(n.Args[1])
		}
		return []string{fmt.Sprintf("m.showToast(%s, %s)", msg, variant)}
	case "info", "warn", "error":
		msg := ec.translateExpr(n.Args[0])
		return []string{fmt.Sprintf("m.showToast(%s, %q)", msg, n.Method)}
	case "confirm":
		return []string{"// Alert.confirm not supported in TUI"}
	}
	return []string{ec.translateExpr(n)}
}
