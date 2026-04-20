package html

// compat.go holds the handful of AST-oriented helpers still needed by
// AST-only subsystems (cdprunner and promote). The main html.go path is
// IR-native and no longer uses anything from this file.

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
)

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

func docFuncs(doc *ast.Document) []*ast.FuncDef      { return codegen.DocFuncDefs(doc) }
func callFuncName(c *ast.CallExpr) string            { return codegen.CallFuncName(c) }
func callArgs(c *ast.CallExpr) []ast.Expr            { return codegen.CallArgs(c) }
func compParams(comp *ast.ComponentDecl) []ast.Param { return codegen.CompParams(comp) }
