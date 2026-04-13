package html

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

type docConst struct {
	Name string
	Init ast.Expr
}

// docStructs extracts StructDef declarations from a document's Stmts.
func docStructs(doc *ast.Document) []*ast.StructDef {
	var out []*ast.StructDef
	for _, s := range doc.Stmts {
		if sd, ok := s.(*ast.StructDef); ok {
			out = append(out, sd)
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

// docEnums extracts EnumDef from a document's Stmts.
func docEnums(doc *ast.Document) []*ast.EnumDef {
	var out []*ast.EnumDef
	for _, s := range doc.Stmts {
		if e, ok := s.(*ast.EnumDef); ok {
			out = append(out, e)
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

// vnHasEvents returns true if the VisualNode has any event handlers.
func vnHasEvents(vn *ast.VisualNode) bool {
	for _, a := range vn.Args.Args {
		if _, ok := a.(ast.EventHandler); ok {
			return true
		}
	}
	return false
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

// --- Expr helpers ---

// exprIsNonNil returns true if the expression is set (non-nil interface).
func exprIsNonNil(e ast.Expr) bool {
	return e != nil
}

// exprLiteralString extracts a string value from a LiteralExpr, if it is one.
func exprLiteralString(e ast.Expr) (string, bool) {
	if e == nil {
		return "", false
	}
	lit, ok := e.(*ast.LiteralExpr)
	if !ok {
		return "", false
	}
	switch lit.Kind {
	case ast.LiteralStringQuoted:
		// Raw contains the quoted string, unquote it
		if s, err := strconv.Unquote(lit.Raw); err == nil {
			return s, true
		}
		// Strip quotes manually as fallback
		raw := lit.Raw
		if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
			return raw[1 : len(raw)-1], true
		}
		return lit.Raw, true
	case ast.LiteralStringBackticked:
		raw := lit.Raw
		if len(raw) >= 2 && raw[0] == '`' && raw[len(raw)-1] == '`' {
			return raw[1 : len(raw)-1], true
		}
		return lit.Raw, true
	case ast.LiteralStringTrippleQuoted:
		raw := lit.Raw
		if strings.HasPrefix(raw, `"""`) && strings.HasSuffix(raw, `"""`) {
			return raw[3 : len(raw)-3], true
		}
		return lit.Raw, true
	}
	return "", false
}

// exprLiteralBool extracts a bool value from a LiteralExpr, if it is one.
func exprLiteralBool(e ast.Expr) (bool, bool) {
	if e == nil {
		return false, false
	}
	lit, ok := e.(*ast.LiteralExpr)
	if !ok {
		return false, false
	}
	if lit.Kind != ast.LiteralBool {
		return false, false
	}
	return lit.Raw == "true", true
}

// exprLiteralInt extracts an int value from a LiteralExpr, if it is one.
func exprLiteralInt(e ast.Expr) (int, bool) {
	if e == nil {
		return 0, false
	}
	lit, ok := e.(*ast.LiteralExpr)
	if !ok {
		return 0, false
	}
	if lit.Kind != ast.LiteralInt {
		return 0, false
	}
	n, err := strconv.Atoi(lit.Raw)
	if err != nil {
		return 0, false
	}
	return n, true
}

// exprIsLiteral returns true if the expression is a literal value.
func exprIsLiteral(e ast.Expr) bool {
	if e == nil {
		return false
	}
	_, ok := e.(*ast.LiteralExpr)
	return ok
}

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
		if s, ok := exprLiteralString(e); ok {
			return s
		}
		return lit.Raw
	case ast.LiteralStringBackticked, ast.LiteralStringTrippleQuoted:
		if s, ok := exprLiteralString(e); ok {
			return s
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

// exprIsReactive returns true if the expression is not a simple literal.
// In v1, this was `expr.SNGL != nil`.
func exprIsReactive(e ast.Expr) bool {
	if e == nil {
		return false
	}
	_, isLit := e.(*ast.LiteralExpr)
	return !isLit
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

// callArgCount returns the number of positional arguments.
func callArgCount(c *ast.CallExpr) int {
	n := 0
	for _, a := range c.Args.Args {
		if _, ok := a.(ast.Arg); ok {
			n++
		}
	}
	return n
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

// compVars extracts VarDecl specs (state variables) from a component body.
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
