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
		// Detect push(list, val) and remove(list, idx) patterns on the
		// assignment target and translate to direct list mutations.
		if n.Op == ast.AssignSet {
			if call, ok := n.Value.(*ast.CallExpr); ok {
				if targetIdent, ok := n.Target.(*ast.IdentExpr); ok {
					if call.Func == "push" && len(call.Args) == 2 {
						if src, ok := call.Args[0].(*ast.IdentExpr); ok && src.Name == targetIdent.Name {
							value := ec.translateExpr(call.Args[1])
							return []string{ec.translateMutationTarget(n.Target) + ".add(" + value + ")"}
						}
					}
					if call.Func == "remove" && len(call.Args) == 2 {
						if src, ok := call.Args[0].(*ast.IdentExpr); ok && src.Name == targetIdent.Name {
							idx := ec.translateExpr(call.Args[1])
							return []string{ec.translateMutationTarget(n.Target) + ".removeAt(" + idx + ")"}
						}
					}
				}
			}
		}
		target := ec.translateMutationTarget(n.Target)
		value := ec.translateExpr(n.Value)
		op := assignOpToKt(n.Op)
		return []string{target + " " + op + " " + value}
	case *ast.ToggleStmt:
		// For list[i].field!! on data classes, use .copy() since fields are val.
		if sel, ok := n.Target.(*ast.SelectExpr); ok {
			if idx, ok := sel.Operand.(*ast.IndexExpr); ok {
				list := ec.translateMutationTarget(idx.Operand)
				index := ec.translateExpr(idx.Index)
				field := sel.Field
				item := list + "[" + index + "]"
				return []string{list + "[" + index + "] = " + item + ".copy(" + field + " = !" + item + "." + field + ")"}
			}
		}
		target := ec.translateMutationTarget(n.Target)
		return []string{target + " = !" + target}
	case *ast.MethodExpr:
		// Alert namespace — translate to Android Toast/AlertDialog
		if ident, ok := n.Receiver.(*ast.IdentExpr); ok && ident.Name == "Alert" {
			return ec.translateAlert(n)
		}
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

// translateAlert translates Alert.toast/info/warn/error to Android API calls.
func (ec *exprContext) translateAlert(n *ast.MethodExpr) []string {
	switch n.Method {
	case "toast":
		msg := ec.translateExpr(n.Args[0])
		return []string{fmt.Sprintf("Toast.makeText(context, %s, Toast.LENGTH_SHORT).show()", msg)}
	case "info":
		msg := ec.translateExpr(n.Args[0])
		return []string{fmt.Sprintf(`android.app.AlertDialog.Builder(context).setMessage(%s).setPositiveButton("OK", null).show()`, msg)}
	case "warn":
		msg := ec.translateExpr(n.Args[0])
		return []string{fmt.Sprintf(`android.app.AlertDialog.Builder(context).setTitle("Warning").setMessage(%s).setPositiveButton("OK", null).show()`, msg)}
	case "error":
		msg := ec.translateExpr(n.Args[0])
		return []string{fmt.Sprintf(`android.app.AlertDialog.Builder(context).setTitle("Error").setMessage(%s).setPositiveButton("OK", null).show()`, msg)}
	case "confirm":
		return []string{"// Alert.confirm requires async dialog — not yet supported"}
	}
	return []string{ec.translateExpr(n)}
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
