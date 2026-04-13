package android

// compat.go provides bridge helpers to extract v1-style AST accessors from
// the v2 AST structures. This allows the bulk of the codegen code to remain
// readable while working with the new Document/VisualNode/Expr types.

import (
	"strconv"
	"strings"

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

// docFuncs extracts FuncDef declarations from a document's Stmts.
func docFuncs(doc *ast.Document) []*ast.FuncDef {
	var out []*ast.FuncDef
	for _, s := range doc.Stmts {
		if fn, ok := s.(*ast.FuncDef); ok {
			out = append(out, fn)
		}
	}
	return out
}

// docComponents extracts ComponentDecl from a document's Stmts.
func docComponents(doc *ast.Document) []*ast.ComponentDecl {
	var out []*ast.ComponentDecl
	for _, s := range doc.Stmts {
		if c, ok := s.(*ast.ComponentDecl); ok {
			out = append(out, c)
		}
	}
	return out
}

// docBodyStmts returns top-level visual statements (VisualNode, IfStmt, ForStmt)
// from a document's Stmts, which represent the "app body" in v2.
func docBodyStmts(doc *ast.Document) []ast.Stmt {
	var out []ast.Stmt
	for _, s := range doc.Stmts {
		switch s.(type) {
		case *ast.VisualNode, *ast.IfStmt, *ast.ForStmt:
			out = append(out, s)
		}
	}
	return out
}

// --- VisualNode helpers ---

// vnName returns the component/element name of a VisualNode.
func vnName(vn *ast.VisualNode) string {
	return codegen.VisualNodeName(vn)
}

// vnProps extracts named arguments (props) from a VisualNode's ArgList.
func vnProps(vn *ast.VisualNode) map[string]ast.Expr {
	props := make(map[string]ast.Expr)
	for _, a := range vn.Args.Args {
		if arg, ok := a.(ast.Arg); ok && arg.Name != "" {
			props[arg.Name] = arg.Value
		}
	}
	return props
}

// vnProp returns a single named prop value, or nil if not found.
func vnProp(vn *ast.VisualNode, name string) ast.Expr {
	for _, a := range vn.Args.Args {
		if arg, ok := a.(ast.Arg); ok && arg.Name == name {
			return arg.Value
		}
	}
	return nil
}

// vnEvents extracts event handlers from a VisualNode's ArgList.
func vnEvents(vn *ast.VisualNode) map[string]*ast.EventHandler {
	events := make(map[string]*ast.EventHandler)
	for i := range vn.Args.Args {
		if eh, ok := vn.Args.Args[i].(ast.EventHandler); ok {
			events[eh.Name] = &eh
		}
	}
	return events
}

// vnEvent returns a single event handler by name, or nil if not found.
func vnEvent(vn *ast.VisualNode, name string) *ast.EventHandler {
	for i := range vn.Args.Args {
		if eh, ok := vn.Args.Args[i].(ast.EventHandler); ok && eh.Name == name {
			return &eh
		}
	}
	return nil
}

// vnChildren returns child statements from a VisualNode's Block.
func vnChildren(vn *ast.VisualNode) []ast.Stmt {
	return vn.Block.Stmts
}

// vnChildNodes returns child VisualNodes from a VisualNode's Block.
func vnChildNodes(vn *ast.VisualNode) []*ast.VisualNode {
	var out []*ast.VisualNode
	for _, s := range vn.Block.Stmts {
		if child, ok := s.(*ast.VisualNode); ok {
			out = append(out, child)
		}
	}
	return out
}

// vnStyleFields extracts "style" sub-properties from a VisualNode's props.
// In v1 this was a method on VisualNode; in v2, style is a named arg whose
// value is a StructExpr with fields like padding, width, etc.
func vnStyleFields(vn *ast.VisualNode) map[string]ast.Expr {
	style := vnProp(vn, "style")
	if style == nil {
		return nil
	}
	se, ok := style.(*ast.StructExpr)
	if !ok {
		return nil
	}
	fields := make(map[string]ast.Expr, len(se.Fields))
	for _, f := range se.Fields {
		fields[f.Name] = f.Value
	}
	return fields
}

// --- ComponentDecl helpers ---

// compParams extracts Param entries from a ComponentDecl's Props.
func compParams(comp *ast.ComponentDecl) []ast.Param {
	var out []ast.Param
	for _, p := range comp.Props.Props {
		if param, ok := p.(ast.Param); ok {
			out = append(out, param)
		}
	}
	return out
}

// compHasChildren returns true if the component accepts children.
func compHasChildren(comp *ast.ComponentDecl) bool {
	return comp.ChildrenType != nil
}

// compBodyStmts returns the visual/control-flow statements in a component body.
func compBodyStmts(comp *ast.ComponentDecl) []ast.Stmt {
	var out []ast.Stmt
	for _, s := range comp.Body.Stmts {
		switch s.(type) {
		case *ast.VisualNode, *ast.IfStmt, *ast.ForStmt:
			out = append(out, s)
		}
	}
	return out
}

// --- Expr helpers ---

// exprLiteralAny extracts the literal value as any type for backwards compat.
func exprLiteralAny(e ast.Expr) any {
	if e == nil {
		return nil
	}
	lit, ok := e.(*ast.LiteralExpr)
	if !ok {
		return nil
	}
	switch lit.Kind {
	case ast.LiteralStringQuoted:
		if s, err := strconv.Unquote(lit.Raw); err == nil {
			return s
		}
		raw := lit.Raw
		if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
			return raw[1 : len(raw)-1]
		}
		return lit.Raw
	case ast.LiteralStringBackticked, ast.LiteralStringTrippleQuoted:
		raw := lit.Raw
		if strings.HasPrefix(raw, "`") && strings.HasSuffix(raw, "`") {
			return raw[1 : len(raw)-1]
		}
		if strings.HasPrefix(raw, `"""`) && strings.HasSuffix(raw, `"""`) {
			return raw[3 : len(raw)-3]
		}
		return lit.Raw
	case ast.LiteralBool:
		return lit.Raw == "true"
	case ast.LiteralInt:
		n, _ := strconv.Atoi(lit.Raw)
		return n
	case ast.LiteralFloat:
		f, _ := strconv.ParseFloat(lit.Raw, 64)
		return f
	case ast.LiteralNull:
		return nil
	default:
		return lit.Raw
	}
}

// exprIsLiteral returns true if the expression is a literal value.
func exprIsLiteral(e ast.Expr) bool {
	if e == nil {
		return false
	}
	_, ok := e.(*ast.LiteralExpr)
	return ok
}

// exprIsNonNil returns true if the expression is set (non-nil interface).
func exprIsNonNil(e ast.Expr) bool {
	return e != nil
}

// exprTypeHint returns the type hint string from a TypeExpr if it's a named type.
func exprTypeHint(t ast.TypeExpr) string {
	if t == nil {
		return ""
	}
	if nt, ok := t.(*ast.NamedType); ok {
		return nt.Name
	}
	return ""
}

// --- CallExpr helpers ---

// callFuncName extracts the function name from a CallExpr.Func.
func callFuncName(c *ast.CallExpr) string {
	switch f := c.Func.(type) {
	case *ast.IdentExpr:
		return f.Name
	case *ast.SelectExpr:
		if ident, ok := f.Operand.(*ast.IdentExpr); ok {
			return ident.Name + "." + f.Field
		}
		return f.Field
	}
	return ""
}

// callArgs extracts argument expressions from a CallExpr.Args.
func callArgs(c *ast.CallExpr) []ast.Expr {
	var out []ast.Expr
	for _, a := range c.Args.Args {
		if arg, ok := a.(ast.Arg); ok {
			out = append(out, arg.Value)
		}
	}
	return out
}
