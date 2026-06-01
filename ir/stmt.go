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
// NamePos is the source position of the Name identifier for named props
// (zero Pos for positional or when the ast.Arg lacked NamePos).
type Arg struct {
	Name    string // empty for positional
	NamePos ast.Pos
	Value   Expr // checked expression
}

func (*NodeInst) stmtNode() {}

// CallStmt is a void function call — definitively not a component.
type CallStmt struct {
	AST  ast.Stmt // original *ast.CallStmt or *ast.VisualNode
	Call *Call    // resolved call expression
}

func (*CallStmt) stmtNode() {}

// SlotInst is the slot pseudo-element.
type SlotInst struct {
	AST      *ast.VisualNode
	Children []Stmt
}

func (*SlotInst) stmtNode() {}

// ErrorBoundary is a special-cased component that catches errors raised
// in the event handlers and expressions of its children. Effect analysis
// resolves each fallible call site beneath this node to Handler unless
// an inner boundary or per-call handler takes precedence.
type ErrorBoundary struct {
	AST      *ast.VisualNode
	Handler  *EventHandler // the @error handler; required
	Children []Stmt
}

func (*ErrorBoundary) stmtNode() {}

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
	// LoweredSlotID is set by passReactivity to the slot ID assigned when
	// the If's Cond depends on a reactive Var. "" when the construct is not
	// reactive. Pass-2 of passReactivity rewrites these into CallStmt
	// __renderSlot<N>() invocations.
	LoweredSlotID string `json:"-"`
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
	// HoistedWindowIDs holds list<Window> symbols hoisted from window #ids
	// declared inside this loop's body. After optimizer expansion, the
	// optimizer binds each symbol's value to the unrolled list of windows.
	HoistedWindowIDs []*Var
	// LoweredSlotID is set by passReactivity to the slot ID assigned when
	// the For's Iter depends on a reactive Var. "" when the construct is not
	// reactive. Pass-2 of passReactivity rewrites these into CallStmt
	// __renderSlot<N>() invocations.
	LoweredSlotID string `json:"-"`
	// RefElem is true when the element loop variable was &-bound
	// (`for &t = list` / `for i, &t = list`): the element var has type
	// ref<T> and writes through it must update the original list element.
	// Set by the checker for addressable mutable list iterables. It is a
	// transient marker: the RefLoop lowering pass consumes it — rewriting
	// element uses to indexed list access and desugaring the loop into an
	// ordinary two-var (index, _) loop — then clears it, so codegen never
	// sees a RefElem loop. The headless interpreter (which does not lower)
	// reads it directly to bind the element as a reference.
	RefElem bool
}

func (*For) stmtNode() {}

// PlatformFilter is a type-checked platform statement with IR body.
type PlatformFilter struct {
	AST      *ast.PlatformStmt
	Platform string // "html", "bubbletea", etc.
	Body     []Stmt
}

func (*PlatformFilter) stmtNode() {}
