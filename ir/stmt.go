package ir

import "git.duckfam.us/jonathan/sngl/ast"

// --- IR statement types ---
//
// These capture type-checked statements so codegen never needs to
// disambiguate ast.VisualNode (component? function? element?) itself.

// Stmt is a type-checked statement node.
type Stmt interface {
	stmtNode()
	StmtPos() ast.Pos
}

// NodeInst is a resolved component or platform-element instantiation.
// Component is non-nil when instantiating a user-defined component.
type NodeInst struct {
	AST       ast.Stmt    // original *ast.VisualNode (or *ast.CallStmt for Foo() that's a component)
	Name      string      // resolved element/component name
	Component *Component  // non-nil for user component; nil for platform element
	Args      ast.ArgList // props and event handlers
	Children  []Stmt      // type-checked body
	ID        string      // #id binding
	Ref       *ast.Expr   // ref binding
	Pos       ast.Pos
}

func (*NodeInst) stmtNode()          {}
func (n *NodeInst) StmtPos() ast.Pos { return n.Pos }

// CallStmt is a void function call — definitively not a component.
type CallStmt struct {
	AST  ast.Stmt      // original *ast.CallStmt or *ast.VisualNode
	Call *ast.CallExpr // the call expression (may be nil for bare-ident visual nodes)
	Func *Func         // resolved function, if known
	Pos  ast.Pos
}

func (*CallStmt) stmtNode()          {}
func (n *CallStmt) StmtPos() ast.Pos { return n.Pos }

// SlotInst is the slot pseudo-element.
type SlotInst struct {
	AST      *ast.VisualNode
	Children []Stmt
	Pos      ast.Pos
}

func (*SlotInst) stmtNode()          {}
func (n *SlotInst) StmtPos() ast.Pos { return n.Pos }

// Assign wraps a type-checked assignment statement.
type Assign struct {
	AST *ast.AssignStmt
	Pos ast.Pos
}

func (*Assign) stmtNode()          {}
func (n *Assign) StmtPos() ast.Pos { return n.Pos }

// Toggle wraps a type-checked toggle statement.
type Toggle struct {
	AST *ast.ToggleStmt
	Pos ast.Pos
}

func (*Toggle) stmtNode()          {}
func (n *Toggle) StmtPos() ast.Pos { return n.Pos }

// Emit wraps a type-checked event emission.
type Emit struct {
	AST *ast.EmitStmt
	Pos ast.Pos
}

func (*Emit) stmtNode()          {}
func (n *Emit) StmtPos() ast.Pos { return n.Pos }

// LocalVar wraps a local variable declaration with its resolved type.
type LocalVar struct {
	AST  *ast.VarStmt
	Type *Type
	Pos  ast.Pos
}

func (*LocalVar) stmtNode()          {}
func (n *LocalVar) StmtPos() ast.Pos { return n.Pos }

// Return wraps a type-checked return statement.
type Return struct {
	AST *ast.ReturnStmt
	Pos ast.Pos
}

func (*Return) stmtNode()          {}
func (n *Return) StmtPos() ast.Pos { return n.Pos }

// If is a type-checked if statement with IR bodies.
type If struct {
	AST  *ast.IfStmt
	Body []Stmt
	Else []Stmt
	Pos  ast.Pos
}

func (*If) stmtNode()          {}
func (n *If) StmtPos() ast.Pos { return n.Pos }

// For is a type-checked for statement with IR bodies and resolved element type.
type For struct {
	AST      *ast.ForStmt
	ElemType *Type
	Body     []Stmt
	Else     []Stmt
	Pos      ast.Pos
}

func (*For) stmtNode()          {}
func (n *For) StmtPos() ast.Pos { return n.Pos }

// PlatformFilter is a type-checked platform statement with IR body.
type PlatformFilter struct {
	AST  *ast.PlatformStmt
	Body []Stmt
	Pos  ast.Pos
}

func (*PlatformFilter) stmtNode()          {}
func (n *PlatformFilter) StmtPos() ast.Pos { return n.Pos }
