package html

// compat.go provides bridge helpers for the HTML platform. Shared helpers
// live in codegen/compat.go; this file holds HTML-specific wrappers and
// the docVar/docConst types used only by this platform.

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

// --- Document helpers ---

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
						Handlers: spec.Handlers,
					})
				}
			}
		}
	}
	return out
}

// docVar is a flattened variable binding from VarDecl specs.
type docVar struct {
	Name     string
	Init     ast.Expr
	Handlers []ast.EventHandler
}

// docConst is a flattened constant binding from ConstDecl specs.
type docConst struct {
	Name string
	Init ast.Expr
}

func docFuncs(doc *ast.Document) []*ast.FuncDef              { return codegen.DocFuncDefs(doc) }
func docStructs(doc *ast.Document) []*ast.StructDef          { return codegen.DocStructDefs(doc) }
func docComponents(doc *ast.Document) []*ast.ComponentDecl   { return codegen.DocComponents(doc) }
func docEnums(doc *ast.Document) []*ast.EnumDef              { return codegen.DocEnumDefs(doc) }
func docBodyStmts(doc *ast.Document) []ast.Stmt              { return codegen.DocBodyStmts(doc) }
func findMainComponent(doc *ast.Document) *ast.ComponentDecl { return codegen.FindMainComponent(doc) }

// docConsts extracts ConstDecl specs from a document's Stmts.
func docConsts(doc *ast.Document) []docConst {
	var out []docConst
	for _, s := range doc.Stmts {
		if cd, ok := s.(*ast.ConstDecl); ok {
			for _, spec := range cd.Specs {
				for _, name := range spec.Names {
					out = append(out, docConst{
						Name: name,
						Init: spec.Default,
					})
				}
			}
		}
	}
	return out
}

// --- VisualNode helpers (delegate to codegen) ---

func vnName(vn *ast.VisualNode) string                          { return codegen.VisualNodeName(vn) }
func vnProps(vn *ast.VisualNode) map[string]ast.Expr            { return codegen.VnProps(vn) }
func vnProp(vn *ast.VisualNode, name string) ast.Expr           { return codegen.VnProp(vn, name) }
func vnEvents(vn *ast.VisualNode) map[string]*ast.EventHandler  { return codegen.VnEvents(vn) }
func vnEvent(vn *ast.VisualNode, name string) *ast.EventHandler { return codegen.VnEvent(vn, name) }
func vnHasEvents(vn *ast.VisualNode) bool                       { return codegen.VnHasEvents(vn) }
func vnChildren(vn *ast.VisualNode) []ast.Stmt                  { return codegen.VnChildren(vn) }
func vnChildNodes(vn *ast.VisualNode) []*ast.VisualNode         { return codegen.VnChildNodes(vn) }

// --- Expr helpers (delegate to codegen) ---

func exprIsNonNil(e ast.Expr) bool                { return e != nil }
func exprLiteralString(e ast.Expr) (string, bool) { return codegen.ExprLiteralString(e) }
func exprLiteralBool(e ast.Expr) (bool, bool)     { return codegen.ExprLiteralBool(e) }
func exprLiteralInt(e ast.Expr) (int, bool)       { return codegen.ExprLiteralInt(e) }
func exprIsLiteral(e ast.Expr) bool               { return codegen.ExprIsLiteral(e) }
func exprLiteralAny(e ast.Expr) any               { return codegen.ExprLiteralAny(e) }
func exprIsReactive(e ast.Expr) bool              { return codegen.ExprIsReactive(e) }

// --- CallExpr helpers (delegate to codegen) ---

func callFuncName(c *ast.CallExpr) string { return codegen.CallFuncName(c) }
func callArgs(c *ast.CallExpr) []ast.Expr { return codegen.CallArgs(c) }
func callArgCount(c *ast.CallExpr) int    { return codegen.CallArgCount(c) }

// --- ComponentDecl helpers ---

func compParams(comp *ast.ComponentDecl) []ast.Param   { return codegen.CompParams(comp) }
func compBodyStmts(comp *ast.ComponentDecl) []ast.Stmt { return codegen.CompBodyStmts(comp) }

// compVars extracts VarDecl specs from a component body.
func compVars(comp *ast.ComponentDecl) []docVar {
	var out []docVar
	for _, s := range comp.Body.Stmts {
		if vd, ok := s.(*ast.VarDecl); ok {
			for _, spec := range vd.Specs {
				for _, name := range spec.Names {
					out = append(out, docVar{
						Name:     name,
						Init:     spec.Default,
						Handlers: spec.Handlers,
					})
				}
			}
		}
	}
	return out
}

// compFuncs extracts FuncDef declarations from a component body.
func compFuncs(comp *ast.ComponentDecl) []*ast.FuncDef {
	var out []*ast.FuncDef
	for _, s := range comp.Body.Stmts {
		if fn, ok := s.(*ast.FuncDef); ok {
			out = append(out, fn)
		}
	}
	return out
}

// compConsts extracts ConstDecl specs from a component body.
func compConsts(comp *ast.ComponentDecl) []docConst {
	var out []docConst
	for _, s := range comp.Body.Stmts {
		if cd, ok := s.(*ast.ConstDecl); ok {
			for _, spec := range cd.Specs {
				for _, name := range spec.Names {
					out = append(out, docConst{
						Name: name,
						Init: spec.Default,
					})
				}
			}
		}
	}
	return out
}
