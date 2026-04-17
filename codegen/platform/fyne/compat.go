package fyne

// compat.go provides bridge helpers for the Fyne platform. Shared helpers
// live in codegen/compat.go; this file holds Fyne-specific wrappers and
// the docVar type used only by this platform.

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/codegen"
	"git.duckfam.us/jonathan/sngl/ir"
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

func docFuncDefs(doc *ast.Document) []*ast.FuncDef         { return codegen.DocFuncDefs(doc) }
func docStructDefs(doc *ast.Document) []*ast.StructDef     { return codegen.DocStructDefs(doc) }
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

func exprLiteralString(e ast.Expr) (string, bool) { return codegen.ExprLiteralString(e) }
func exprLiteralBool(e ast.Expr) (bool, bool)     { return codegen.ExprLiteralBool(e) }
func exprLiteralInt(e ast.Expr) (int, bool)       { return codegen.ExprLiteralInt(e) }
func exprLiteralAny(e ast.Expr) any               { return codegen.ExprLiteralAny(e) }
func exprIsLiteral(e ast.Expr) bool               { return codegen.ExprIsLiteral(e) }
func exprTypeHint(t ast.TypeExpr) string          { return codegen.ExprTypeHint(t) }

// --- CallExpr helpers (delegate to codegen) ---

func callFuncName(c *ast.CallExpr) string { return codegen.CallFuncName(c) }
func callArgs(c *ast.CallExpr) []ast.Expr { return codegen.CallArgs(c) }

// --- ComponentDecl helpers (delegate to codegen) ---

func compParams(comp *ast.ComponentDecl) []ast.Param   { return codegen.CompParams(comp) }
func compHasChildren(comp *ast.ComponentDecl) bool     { return codegen.CompHasChildren(comp) }
func compBodyStmts(comp *ast.ComponentDecl) []ast.Stmt { return codegen.CompBodyStmts(comp) }

// --- FuncDef helpers ---

// isStdlibFunc returns true if a function is a stdlib method (has "." in name).
func isStdlibFunc(fn *ast.FuncDef) bool {
	for _, c := range fn.Name {
		if c == '.' {
			return true
		}
	}
	return false
}

// isComputed returns true if a FuncDef is a computed (expression-form, zero-arg).
func isComputed(fn *ast.FuncDef) bool {
	return fn.Body != nil && len(fn.Params.Params) == 0
}

// funcReturnType returns the string type hint for a FuncDef's return type.
func funcReturnType(fn *ast.FuncDef) string {
	return codegen.ExprTypeHint(fn.ReturnType)
}

// findComponentInDoc finds a ComponentDecl by name in the document.
func findComponentInDoc(doc *ast.Document, name string) *ast.ComponentDecl {
	return codegen.FindComponent(doc, name)
}

// --- AST-based dependency helpers ---
// These mirror the IR-based functions in codegen/deps.go but operate on
// AST types. The Fyne platform still walks AST visual nodes and event
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
			if sel, ok := n.Call.Func.(*ast.SelectExpr); ok {
				if root := findRootIdentAST(sel.Operand); root != "" {
					fields[root] = true
				}
			}
		}
	}
	return fields
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

// extractDepsAST returns all model field references from an AST expression.
func extractDepsAST(e ast.Expr, modelFields map[string]bool) map[string]bool {
	deps := make(map[string]bool)
	walkExprDepsAST(e, modelFields, deps)
	return deps
}

// --- IR bridge helpers ---

// irStmtToAST extracts the AST statement backpointer from an IR statement.
// Returns nil if no backpointer exists.
func irStmtToAST(s ir.Stmt) ast.Stmt {
	switch n := s.(type) {
	case *ir.Assign:
		return n.AST
	case *ir.Toggle:
		return n.AST
	case *ir.CallStmt:
		return n.AST
	case *ir.Emit:
		return n.AST
	case *ir.LocalVar:
		return n.AST
	case *ir.Return:
		return n.AST
	case *ir.If:
		return n.AST
	case *ir.For:
		return n.AST
	case *ir.NodeInst:
		return n.AST
	case *ir.PlatformFilter:
		return n.AST
	}
	return nil
}

// irTypeHint maps an *ir.Type to a string type hint compatible with
// typeHintToGo / zeroValueGo.
func irTypeHint(t *ir.Type) string {
	if t == nil {
		return ""
	}
	switch t.Kind {
	case ir.TypeBool:
		return "bool"
	case ir.TypeInt:
		return "int"
	case ir.TypeFloat:
		return "float"
	case ir.TypeString:
		return "string"
	case ir.TypeColor:
		return "color"
	case ir.TypeDate:
		return "date"
	case ir.TypeTime:
		return "time"
	case ir.TypeDateTime:
		return "dateTime"
	case ir.TypeDuration:
		return "duration"
	case ir.TypeURL:
		return "url"
	case ir.TypeList:
		if len(t.Elems) > 0 {
			return "list:" + irTypeHint(t.Elems[0])
		}
		return "list"
	case ir.TypeOption:
		if len(t.Elems) > 0 {
			return "option:" + irTypeHint(t.Elems[0])
		}
		return "option"
	case ir.TypeStruct:
		if t.Decl != nil {
			return t.Decl.SymName()
		}
		return "struct"
	case ir.TypeEnum:
		if t.Decl != nil {
			return "enum:" + t.Decl.SymName()
		}
		return "enum"
	default:
		return strings.ToLower(t.Kind.String())
	}
}
