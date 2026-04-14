package bubbletea

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

// v2 compat helpers — bridge v1-style access patterns to v2 AST.

// --- Document variable helpers ---

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

// --- VisualNode helpers ---

func vnName(vn *ast.VisualNode) string                          { return codegen.VisualNodeName(vn) }
func vnProps(vn *ast.VisualNode) map[string]ast.Expr            { return codegen.VnProps(vn) }
func vnProp(vn *ast.VisualNode, name string) ast.Expr           { return codegen.VnProp(vn, name) }
func vnEvents(vn *ast.VisualNode) map[string]*ast.EventHandler  { return codegen.VnEvents(vn) }
func vnEvent(vn *ast.VisualNode, name string) *ast.EventHandler { return codegen.VnEvent(vn, name) }
func vnChildren(vn *ast.VisualNode) []ast.Stmt                  { return codegen.VnChildren(vn) }
func vnChildNodes(vn *ast.VisualNode) []*ast.VisualNode         { return codegen.VnChildNodes(vn) }
func vnStyleFields(vn *ast.VisualNode) map[string]ast.Expr      { return codegen.VnStyleFields(vn) }

// --- Document-level accessors ---

func docFuncDefs(doc *ast.Document) []*ast.FuncDef         { return codegen.DocFuncDefs(doc) }
func docStructDefs(doc *ast.Document) []*ast.StructDef     { return codegen.DocStructDefs(doc) }
func docComponents(doc *ast.Document) []*ast.ComponentDecl { return codegen.DocComponents(doc) }
func docBodyStmts(doc *ast.Document) []ast.Stmt            { return codegen.DocBodyStmts(doc) }

// --- ComponentDecl helpers ---

func compParams(comp *ast.ComponentDecl) []ast.Param   { return codegen.CompParams(comp) }
func compHasChildren(comp *ast.ComponentDecl) bool     { return codegen.CompHasChildren(comp) }
func compBodyStmts(comp *ast.ComponentDecl) []ast.Stmt { return codegen.CompBodyStmts(comp) }

// findComponentInDoc finds a ComponentDecl by name in the document.
func findComponentInDoc(doc *ast.Document, name string) *ast.ComponentDecl {
	return codegen.FindComponent(doc, name)
}

// --- Expr helpers ---

func exprTypeHint(t ast.TypeExpr) string { return codegen.ExprTypeHint(t) }

// --- FuncDef helpers ---

// isComputed returns true if a FuncDef is a computed (expression-form, zero-arg).
func isComputed(fn *ast.FuncDef) bool {
	return fn.Body != nil && len(fn.Params.Params) == 0
}

// funcReturnType returns the string type hint for a FuncDef's return type.
func funcReturnType(fn *ast.FuncDef) string {
	return codegen.ExprTypeHint(fn.ReturnType)
}

// --- CallExpr helpers ---

func callFuncName(c *ast.CallExpr) string { return codegen.CallFuncName(c) }
func callArgs(c *ast.CallExpr) []ast.Expr { return codegen.CallArgs(c) }
