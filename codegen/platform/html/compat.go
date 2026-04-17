package html

// compat.go provides bridge helpers for the HTML platform. Shared helpers
// live in codegen/compat.go; this file holds HTML-specific wrappers and
// the docVar/docConst types used only by this platform.

import (
	"maps"

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

// --- AST-based dependency helpers ---
// These mirror the IR-based functions in codegen/deps.go but operate on
// AST types. The HTML platform still walks AST visual nodes and event
// handlers internally; these bridge until the platform is fully IR-native.

// findRootIdentAST extracts the root identifier name from an AST expression.
func findRootIdentAST(e ast.Expr) string {
	if e == nil {
		return ""
	}
	switch n := e.(type) {
	case *ast.IdentExpr:
		return n.Name
	case *ast.SelectExpr:
		return findRootIdentAST(n.Operand)
	case *ast.IndexExpr:
		return findRootIdentAST(n.Operand)
	}
	return ""
}

// mutatedFieldsAST returns the set of field names mutated by an AST statement.
func mutatedFieldsAST(s ast.Stmt) map[string]bool {
	fields := make(map[string]bool)
	if s == nil {
		return fields
	}
	switch n := s.(type) {
	case *ast.AssignStmt:
		if root := findRootIdentAST(n.Target); root != "" {
			fields[root] = true
		}
	case *ast.ToggleStmt:
		if root := findRootIdentAST(n.Target); root != "" {
			fields[root] = true
		}
	case *ast.CallStmt:
		if n.Call != nil {
			maps.Copy(fields, mutatedFieldsExprAST(n.Call))
		}
	}
	return fields
}

// mutatedFieldsExprAST returns mutated fields from an AST call expression.
func mutatedFieldsExprAST(e ast.Expr) map[string]bool {
	fields := make(map[string]bool)
	if e == nil {
		return fields
	}
	if call, ok := e.(*ast.CallExpr); ok {
		// Method call — the receiver (Func as SelectExpr) may reference a model field.
		if sel, ok := call.Func.(*ast.SelectExpr); ok {
			if root := findRootIdentAST(sel.Operand); root != "" {
				fields[root] = true
			}
		}
	}
	return fields
}

// exprDepsAST walks an AST expression and returns model field dependencies,
// expanding through computed fields.
func exprDepsAST(e ast.Expr, modelFields, computedFields map[string]bool, computedDeps map[string]map[string]bool) map[string]bool {
	if e == nil {
		return nil
	}
	deps := make(map[string]bool)
	walkExprDepsAST(e, modelFields, deps)
	// Expand through computed fields.
	result := make(map[string]bool)
	for d := range deps {
		result[d] = true
		if computedFields[d] {
			if cd, ok := computedDeps[d]; ok {
				maps.Copy(result, cd)
			}
		}
	}
	return result
}

// walkExprDepsAST recursively finds model field references in an AST expression.
func walkExprDepsAST(e ast.Expr, modelFields map[string]bool, deps map[string]bool) {
	if e == nil {
		return
	}
	switch n := e.(type) {
	case *ast.IdentExpr:
		if modelFields[n.Name] {
			deps[n.Name] = true
		}
	case *ast.SelectExpr:
		if root := findRootIdentAST(n.Operand); root != "" && modelFields[root] {
			deps[root] = true
		}
	case *ast.BinaryExpr:
		walkExprDepsAST(n.Left, modelFields, deps)
		walkExprDepsAST(n.Right, modelFields, deps)
	case *ast.UnaryExpr:
		walkExprDepsAST(n.Operand, modelFields, deps)
	case *ast.TernaryExpr:
		walkExprDepsAST(n.Cond, modelFields, deps)
		walkExprDepsAST(n.Then, modelFields, deps)
		walkExprDepsAST(n.Else, modelFields, deps)
	case *ast.CallExpr:
		walkExprDepsAST(n.Func, modelFields, deps)
		for _, a := range n.Args.Args {
			if arg, ok := a.(ast.Arg); ok {
				walkExprDepsAST(arg.Value, modelFields, deps)
			}
		}
	case *ast.IndexExpr:
		walkExprDepsAST(n.Operand, modelFields, deps)
		walkExprDepsAST(n.Index, modelFields, deps)
	case *ast.ListExpr:
		for _, el := range n.Elements {
			walkExprDepsAST(el, modelFields, deps)
		}
	case *ast.SpreadExpr:
		walkExprDepsAST(n.Operand, modelFields, deps)
	}
}
