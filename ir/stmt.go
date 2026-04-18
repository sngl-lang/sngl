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
	AST       ast.Stmt       // original *ast.VisualNode (or *ast.CallStmt for Foo() that's a component)
	Name      string         // resolved element/component name
	Component *Component     // non-nil for user component; nil for platform element
	Props     []Arg          // property assignments (positional and named)
	Handlers  []EventHandler // inline event handlers
	Children  []Stmt         // type-checked body
	ID        string         // #id binding
	Key       Expr           // key expression for list diffing (nil → implicit index)
	Ref       Expr           // ref binding (nil if none)
}

// Arg is a property assignment in a node instantiation.
type Arg struct {
	Name  string // empty for positional
	Value Expr   // checked expression
}

func (*NodeInst) stmtNode() {}

// CallStmt is a void function call — definitively not a component.
type CallStmt struct {
	AST  ast.Stmt // original *ast.CallStmt or *ast.VisualNode
	Call *Call     // resolved call expression
}

func (*CallStmt) stmtNode() {}

// SlotInst is the slot pseudo-element.
type SlotInst struct {
	AST      *ast.VisualNode
	Children []Stmt
}

func (*SlotInst) stmtNode() {}

// Assign is a type-checked assignment statement.
type Assign struct {
	AST    *ast.AssignStmt
	Target Expr
	Op     ast.AssignOp
	Value  Expr
}

func (*Assign) stmtNode() {}

// Toggle is a type-checked toggle statement.
type Toggle struct {
	AST    *ast.ToggleStmt
	Target Expr
}

func (*Toggle) stmtNode() {}

// Emit is a type-checked event emission.
type Emit struct {
	AST  *ast.EmitStmt
	Name string
	Args []CallArg
}

func (*Emit) stmtNode() {}

// LocalVar is a local variable declaration with its resolved type.
type LocalVar struct {
	AST  *ast.VarStmt
	Name string
	Type *Type
	Init Expr // resolved initializer (nil if none)
}

func (*LocalVar) stmtNode() {}

// Return is a type-checked return statement.
type Return struct {
	AST   *ast.ReturnStmt
	Value Expr // nil for bare return
}

func (*Return) stmtNode() {}

// If is a type-checked if statement with IR bodies.
type If struct {
	AST  *ast.IfStmt
	Cond Expr
	Body []Stmt
	Else []Stmt
}

func (*If) stmtNode() {}

// For is a type-checked for statement with IR bodies and resolved element type.
type For struct {
	AST      *ast.ForStmt
	Key      string // iterator variable name
	Value    string // optional second variable (empty for single-var form)
	Iter     Expr   // resolved iterator expression
	ElemType *Type
	Body     []Stmt
	Else     []Stmt
}

func (*For) stmtNode() {}

// PlatformFilter is a type-checked platform statement with IR body.
type PlatformFilter struct {
	AST      *ast.PlatformStmt
	Platform string // "html", "bubbletea", etc.
	Body     []Stmt
}

func (*PlatformFilter) stmtNode() {}
