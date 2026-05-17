package ir

import "git.duckfam.us/jonathan/sngl/ast"

// Context represents a top-level `context #name(default)` declaration.
// Default is the checked default-value expression; Typ is its inferred
// type. The lowering pass NoContext threads its value through every
// reachable component as a hidden parameter.
//
// At source level, `context #name(default)` without a trailing body parses
// as *ast.CallStmt (not *ast.VisualNode) — see Phase 1 parser note.
type Context struct {
	AST     *ast.CallStmt
	Name    string
	Typ     *Type
	Default Expr
}

func (c *Context) SymName() string { return c.Name }
func (c *Context) SymType() *Type  { return c.Typ }

// ContextProvider is the IR form of `name(value) { children }` inside a
// window or component body. The lowering pass NoContext rewrites it into
// hidden-prop assignments on every component call reachable inside Children.
type ContextProvider struct {
	AST      *ast.VisualNode
	Ref      *Context
	Value    Expr
	Children []Stmt
}

func (p *ContextProvider) stmtNode() {}

// ContextRead is the IR form of a bare reference to a context name in
// expression position. Lowering rewrites these to reads of the synthesized
// hidden parameter `__ctx_<Name>`.
type ContextRead struct {
	AST *ast.IdentExpr
	Ref *Context
	Typ *Type
}

func (r *ContextRead) exprNode()       {}
func (r *ContextRead) ExprType() *Type { return r.Typ }

var (
	_ Symbol = (*Context)(nil)
	_ Stmt   = (*ContextProvider)(nil)
	_ Expr   = (*ContextRead)(nil)
)
