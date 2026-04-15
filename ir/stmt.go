package ir

import "git.duckfam.us/jonathan/sngl/ast"

// --- IR statement types ---
//
// These capture type-checked statements so codegen never needs to
// disambiguate ast.VisualNode (component? function? element?) itself.

// Stmt is a type-checked statement node.
type Stmt interface {
	stmtNode()
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
}

func (*NodeInst) stmtNode() {}

// CallStmt is a void function call — definitively not a component.
type CallStmt struct {
	AST  ast.Stmt      // original *ast.CallStmt or *ast.VisualNode
	Call *ast.CallExpr // the call expression (may be nil for bare-ident visual nodes)
	Func *Func         // resolved function, if known
}

func (*CallStmt) stmtNode() {}

// SlotInst is the slot pseudo-element.
type SlotInst struct {
	AST      *ast.VisualNode
	Children []Stmt
}

func (*SlotInst) stmtNode() {}

// Assign wraps a type-checked assignment statement.
type Assign struct {
	AST *ast.AssignStmt
}

func (*Assign) stmtNode() {}

// Toggle wraps a type-checked toggle statement.
type Toggle struct {
	AST *ast.ToggleStmt
}

func (*Toggle) stmtNode() {}

// Emit wraps a type-checked event emission.
type Emit struct {
	AST *ast.EmitStmt
}

func (*Emit) stmtNode() {}

// LocalVar wraps a local variable declaration with its resolved type.
type LocalVar struct {
	AST  *ast.VarStmt
	Type *Type
}

func (*LocalVar) stmtNode() {}

// Return wraps a type-checked return statement.
type Return struct {
	AST *ast.ReturnStmt
}

func (*Return) stmtNode() {}

// If is a type-checked if statement with IR bodies.
type If struct {
	AST  *ast.IfStmt
	Body []Stmt
	Else []Stmt
}

func (*If) stmtNode() {}

// For is a type-checked for statement with IR bodies and resolved element type.
type For struct {
	AST      *ast.ForStmt
	ElemType *Type
	Body     []Stmt
	Else     []Stmt
}

func (*For) stmtNode() {}

// PlatformFilter is a type-checked platform statement with IR body.
type PlatformFilter struct {
	AST  *ast.PlatformStmt
	Body []Stmt
}

func (*PlatformFilter) stmtNode() {}
