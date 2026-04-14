package android

// compat.go provides bridge helpers for the Android platform. Shared helpers
// live in codegen/compat.go; this file holds Android-specific wrappers and
// the docVar type used only by this platform.

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

// --- Document helpers ---

// docVar is a flattened variable binding from VarDecl specs.
type docVar struct {
	Name     string
	Init     ast.Expr
	Type     ast.TypeExpr
	Handlers []ast.EventHandler
}

// docVars extracts VarDecl specs (state variables) from a document's Stmts.
func docVars(doc *ast.Document) []docVar {
	var out []docVar
	for _, s := range doc.Stmts {
		if vd, ok := s.(*ast.VarDecl); ok {
			for _, spec := range vd.Specs {
				for _, name := range spec.Names {
					out = append(out, docVar{
						Name:     name,
						Init:     spec.Default,
						Type:     spec.Type,
						Handlers: spec.Handlers,
					})
				}
			}
		}
	}
	return out
}

func docFuncs(doc *ast.Document) []*ast.FuncDef            { return codegen.DocFuncDefs(doc) }
func docComponents(doc *ast.Document) []*ast.ComponentDecl { return codegen.DocComponents(doc) }
func docBodyStmts(doc *ast.Document) []ast.Stmt            { return codegen.DocBodyStmts(doc) }

// --- VisualNode helpers (delegate to codegen) ---

func vnName(vn *ast.VisualNode) string                          { return codegen.VisualNodeName(vn) }
func vnProps(vn *ast.VisualNode) map[string]ast.Expr            { return codegen.VnProps(vn) }
func vnProp(vn *ast.VisualNode, name string) ast.Expr           { return codegen.VnProp(vn, name) }
func vnEvents(vn *ast.VisualNode) map[string]*ast.EventHandler  { return codegen.VnEvents(vn) }
func vnEvent(vn *ast.VisualNode, name string) *ast.EventHandler { return codegen.VnEvent(vn, name) }
func vnChildren(vn *ast.VisualNode) []ast.Stmt                  { return codegen.VnChildren(vn) }
func vnChildNodes(vn *ast.VisualNode) []*ast.VisualNode         { return codegen.VnChildNodes(vn) }
func vnStyleFields(vn *ast.VisualNode) map[string]ast.Expr      { return codegen.VnStyleFields(vn) }

// --- Expr helpers (delegate to codegen) ---

func exprLiteralAny(e ast.Expr) any      { return codegen.ExprLiteralAny(e) }
func exprIsLiteral(e ast.Expr) bool      { return codegen.ExprIsLiteral(e) }
func exprIsNonNil(e ast.Expr) bool       { return e != nil }
func exprTypeHint(t ast.TypeExpr) string { return codegen.ExprTypeHint(t) }

// --- CallExpr helpers (delegate to codegen) ---

func callFuncName(c *ast.CallExpr) string { return codegen.CallFuncName(c) }
func callArgs(c *ast.CallExpr) []ast.Expr { return codegen.CallArgs(c) }

// --- ComponentDecl helpers (delegate to codegen) ---

func compParams(comp *ast.ComponentDecl) []ast.Param   { return codegen.CompParams(comp) }
func compHasChildren(comp *ast.ComponentDecl) bool     { return codegen.CompHasChildren(comp) }
func compBodyStmts(comp *ast.ComponentDecl) []ast.Stmt { return codegen.CompBodyStmts(comp) }
