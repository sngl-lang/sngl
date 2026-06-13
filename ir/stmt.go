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
	AST        ast.Stmt       // original *ast.VisualNode (or *ast.CallStmt for Foo() that's a component)
	Name       string         // resolved element/component name
	Component  *Component     // non-nil for user component; nil for platform element
	Props      []Arg          // property assignments (positional and named)
	Handlers   []EventHandler // inline event handlers
	Bindings   []PropBinding  // first-class bidi prop bindings; consumed by lowering
	Children   []Stmt         // type-checked body
	ID         string         // #id binding
	Key        Expr           // key expression for list diffing (nil → implicit index)
	Ref        Expr           // ref binding (nil if none)
	CanvasDraw *Func          // non-nil for canvas containers after passCanvas
}

// Arg is a property assignment in a node instantiation.
// NamePos is the source position of the Name identifier for named props
// (zero Pos for positional or when the ast.Arg lacked NamePos).
type Arg struct {
	Name    string // empty for positional
	NamePos ast.Pos
	Value   Expr // checked expression
}

// PropBinding is a first-class bidirectional prop binding at a NodeInst call
// site. PropName names the component's declared :prop; Target is the lvalue
// in the parent scope to which child mutations hoist. The lowering phase
// transforms PropBindings into @event+handler pairs so codegen sees normal IR.
type PropBinding struct {
	PropName string
	NamePos  ast.Pos
	Target   Expr // must satisfy isAssignableTarget in checker
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
	// CanvasDraw is set by passDeclarative when flattening a canvas
	// NodeInst (whose own CanvasDraw was set by passCanvas) into a
	// `lower.CreateNode("canvas")` LocalVar. It carries the synthesized
	// draw func through flattening so widget-emitting platforms (fyne,
	// gtk4) can wire a raster-backed canvas widget. CanvasWidth/Height
	// carry the canvas's pixel dimensions (from its width/height props).
	CanvasDraw   *Func
	CanvasWidth  int
	CanvasHeight int
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
	// FromTernary marks an If synthesized by NoTernary lowering to implement a
	// `cond ? a : b` expression: its branch bodies are plain Assigns into a
	// sibling `var __ltN` temp, not visual NodeInsts. RenderModel view emitters
	// (which otherwise treat an If in the view body as a structural conditional
	// and render only its NodeInst children) must emit it as imperative control
	// flow so the temp is assigned in scope for the consuming widget.
	FromTernary bool `json:"-"`
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
	// IterKind records how this loop iterates — element, (index, element),
	// or (key, value) — so each language's ForHead emits a pure template
	// instead of re-deriving the map-vs-list choice from Iter's type. Stamped
	// by the passIterKind lowering pass (always-on, late, so it observes the
	// final post-RefLoop shape). Zero value (IterElement) holds until lowering
	// runs; the headless interpreter ignores it.
	IterKind IterKind
}

func (*For) stmtNode() {}

// IterKind classifies a For loop's iteration shape. DeriveIterKind computes it
// from the loop's resolved Iter type and variable arity.
type IterKind int

const (
	// IterElement is single-var iteration over a list/iter<T>: bind each
	// element to Key.
	IterElement IterKind = iota
	// IterIndexed is two-var iteration over a list/iter<T>: bind (index,
	// element) to (Key, Value).
	IterIndexed
	// IterMapEntries is iteration over a map: bind (key, value) to
	// (Key, Value); Value may be empty (caller substitutes a discard).
	IterMapEntries
)

// DeriveIterKind classifies n's iteration shape from its resolved Iter type
// and variable arity — the single decision every language's ForHead used to
// make independently. A map iterable yields key/value pairs; otherwise a
// second variable means indexed iteration, and a lone variable binds the
// element.
func DeriveIterKind(n *For) IterKind {
	if n == nil || n.Iter == nil {
		return IterElement
	}
	if t := n.Iter.ExprType(); t != nil && t.Kind == TypeMap {
		return IterMapEntries
	}
	if n.Value != "" {
		return IterIndexed
	}
	return IterElement
}

// PlatformFilter is a type-checked platform statement with IR body.
type PlatformFilter struct {
	AST      *ast.PlatformStmt
	Platform string // "html", "bubbletea", etc.
	Body     []Stmt
}

func (*PlatformFilter) stmtNode() {}

// CanvasRedrawStmt is injected by passCanvasReactivity into handler/timer
// bodies that mutate state vars read by a canvas draw function. Each platform
// translates this to its native "clear and redraw the canvas" operation.
type CanvasRedrawStmt struct {
	Canvas   *NodeInst // the canvas element (has CanvasDraw set)
	DrawFunc *Func     // the synthesized draw function
}

func (*CanvasRedrawStmt) stmtNode() {}
