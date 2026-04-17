package android

// compat.go provides bridge helpers for the Android platform. Shared helpers
// live in codegen/compat.go; this file holds Android-specific wrappers and
// the docVar type used only by this platform.

import (
	"fmt"
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

// --- IR bridge helpers ---

// irTypeHint maps an *ir.Type to a string type hint compatible with
// typeHintToKt / zeroValueKt.
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

// irIsLiteral reports whether an IR expression is a literal constant.
func irIsLiteral(e ir.Expr) bool {
	_, ok := e.(*ir.Literal)
	return ok
}

// irLiteralToKt converts an IR literal expression to a Kotlin value string.
func irLiteralToKt(e ir.Expr) string {
	if e == nil {
		return `""`
	}
	lit, ok := e.(*ir.Literal)
	if !ok {
		return `""`
	}
	if lit.Type != nil {
		switch lit.Type.Kind {
		case ir.TypeString:
			return fmt.Sprintf("%q", lit.Raw)
		case ir.TypeInt:
			return lit.Raw
		case ir.TypeFloat:
			s := lit.Raw
			if !strings.Contains(s, ".") {
				s += ".0"
			}
			return s
		case ir.TypeBool:
			return lit.Raw
		case ir.TypeNull:
			return "null"
		}
	}
	return fmt.Sprintf("%q", lit.Raw)
}
