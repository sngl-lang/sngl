package fyne

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen/lang/golang"
)

// exprContext is a type alias for golang.GoContext. All expression and
// mutation translation is delegated to the shared Go language package.
type exprContext = golang.GoContext

// Bridge functions that map platform-local names to golang package exports.

func exportName(s string) string   { return golang.ExportName(s) }
func unexportName(s string) string { return golang.UnexportName(s) }

func inferGoType(expr ast.Expr) string { return golang.InferGoType(expr) }
func typeHintToGo(hint string) string  { return golang.TypeHintToGo(hint) }
func literalToGo(expr ast.Expr) string { return golang.LiteralToGo(expr) }
func externFuncGoType(paramTypes []string, returnType string) string {
	return golang.ExternFuncGoType(paramTypes, returnType)
}
func snglNodeGoType(e ast.Node) string                    { return golang.SnglNodeGoType(e) }
func needsTimeType(hint string) bool                      { return golang.NeedsTimeType(hint) }
func exprToGoValue(expr ast.Expr, ec *exprContext) string { return golang.ExprToGoValue(expr, ec) }
func exprToGoCond(expr ast.Expr, ec *exprContext) string  { return golang.ExprToGoCond(expr, ec) }
func exprToGoStringList(expr ast.Expr, ec *exprContext) string {
	return golang.ExprToGoStringList(expr, ec)
}

// fyneAlertFunc translates Alert calls for Fyne's toast method.
func fyneAlertFunc(ec *golang.GoContext, n *ast.MethodExpr) []string {
	switch n.Method {
	case "toast":
		msg := ec.TranslateExpr(n.Args[0])
		variant := `"info"`
		if len(n.Args) > 1 {
			variant = ec.TranslateExpr(n.Args[1])
		}
		return []string{fmt.Sprintf("m.showToast(%s, %s)", msg, variant)}
	case "info", "warn", "error":
		msg := ec.TranslateExpr(n.Args[0])
		return []string{fmt.Sprintf("m.showToast(%s, %q)", msg, n.Method)}
	case "confirm":
		return []string{"// Alert.confirm not supported in Fyne"}
	}
	return []string{ec.TranslateExpr(n)}
}
