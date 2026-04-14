package android

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

func (ec *exprContext) translateMutation(e any) []string {
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
					funcName := codegen.CallFuncName(call)
					callArgList := codegen.CallArgs(call)
					if funcName == "push" && len(callArgList) == 2 {
						if src, ok := callArgList[0].(*ast.IdentExpr); ok && src.Name == targetIdent.Name {
							value := ec.translateExpr(callArgList[1])
							return []string{ec.translateMutationTarget(n.Target) + ".add(" + value + ")"}
						}
					}
					if funcName == "remove" && len(callArgList) == 2 {
						if src, ok := callArgList[0].(*ast.IdentExpr); ok && src.Name == targetIdent.Name {
							idx := ec.translateExpr(callArgList[1])
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
	case *ast.CallStmt:
		return ec.translateCallStmt(n)
	case *ast.CallExpr:
		return []string{ec.translateCall(n)}
	case *ast.EmitStmt:
		args := codegen.CallArgs(&ast.CallExpr{Args: n.Args})
		argStrs := make([]string, len(args))
		for i, a := range args {
			argStrs[i] = ec.translateExpr(a)
		}
		name := "on" + strings.ToUpper(n.Name[:1]) + n.Name[1:]
		if len(argStrs) > 0 {
			return []string{name + "?.invoke(" + strings.Join(argStrs, ", ") + ")"}
		}
		return []string{name + "?.invoke()"}
	default:
		return []string{"// unsupported mutation: " + fmt.Sprintf("%T", e)}
	}
}

// translateCallStmt handles a CallStmt, including method calls that were
// previously ast.MethodExpr in v1. In v2, method calls are CallExpr with
// a SelectExpr as Func.
func (ec *exprContext) translateCallStmt(n *ast.CallStmt) []string {
	call := n.Call
	sel, isMethod := call.Func.(*ast.SelectExpr)
	if !isMethod {
		return []string{ec.translateCall(call)}
	}

	// Alert namespace — translate to Android Toast/AlertDialog
	if ident, ok := sel.Operand.(*ast.IdentExpr); ok && ident.Name == "Alert" {
		return ec.translateAlertCall(sel.Field, call)
	}

	target := ec.translateMutationTarget(sel.Operand)
	method := sel.Field
	args := codegen.CallArgs(call)

	switch method {
	case "push":
		if len(args) == 1 {
			value := ec.translateExpr(args[0])
			return []string{target + ".add(" + value + ")"}
		}
	case "remove":
		if len(args) == 1 {
			idx := ec.translateExpr(args[0])
			return []string{target + ".removeAt(" + idx + ")"}
		}
	}

	argStrs := make([]string, len(args))
	for i, a := range args {
		argStrs[i] = ec.translateExpr(a)
	}
	return []string{target + "." + method + "(" + strings.Join(argStrs, ", ") + ")"}
}

func (ec *exprContext) translateMutationTarget(e any) string {
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
		if expr, ok := e.(ast.Expr); ok {
			return ec.translateExpr(expr)
		}
		return fmt.Sprintf("/* unsupported target %T */", e)
	}
}

// translateAlertCall translates Alert.toast/info/warn/error to Android API calls.
func (ec *exprContext) translateAlertCall(method string, call *ast.CallExpr) []string {
	args := codegen.CallArgs(call)
	switch method {
	case "toast":
		if len(args) > 0 {
			msg := ec.translateExpr(args[0])
			return []string{fmt.Sprintf("Toast.makeText(context, %s, Toast.LENGTH_SHORT).show()", msg)}
		}
	case "info":
		if len(args) > 0 {
			msg := ec.translateExpr(args[0])
			return []string{fmt.Sprintf(`android.app.AlertDialog.Builder(context).setMessage(%s).setPositiveButton("OK", null).show()`, msg)}
		}
	case "warn":
		if len(args) > 0 {
			msg := ec.translateExpr(args[0])
			return []string{fmt.Sprintf(`android.app.AlertDialog.Builder(context).setTitle("Warning").setMessage(%s).setPositiveButton("OK", null).show()`, msg)}
		}
	case "error":
		if len(args) > 0 {
			msg := ec.translateExpr(args[0])
			return []string{fmt.Sprintf(`android.app.AlertDialog.Builder(context).setTitle("Error").setMessage(%s).setPositiveButton("OK", null).show()`, msg)}
		}
	case "confirm":
		return []string{"// Alert.confirm requires async dialog — not yet supported"}
	}
	return []string{ec.translateCall(call)}
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
