package ir

import (
	"strconv"

	"git.duckfam.us/jonathan/sngl/ast"
)

// --- IR statement types ---
//
// These capture type-checked statements so codegen never needs to
// disambiguate ast.VisualNode (component? function? element?) itself.

// Stmt is a type-checked statement node.
type Stmt interface {
	Node
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
	Bindings  []PropBinding  // first-class bidi prop bindings; consumed by lowering
	Children  []Stmt         // type-checked body
	// Slots is the content supplied per named slot; Children go to the rest
	// slot.
	Slots map[string]*SlotContent `json:",omitempty"`
	ID    string                  // #id binding
	// Handle is the binding that `ID` declared, when a program wrote one.
	//
	// The link exists because `ID` is a *name* and the checker distinguishes
	// handles by *symbol*: `declareNodeIDs` runs per body, so two unrelated
	// components each writing `#bar` declare two vars, while two spliced copies
	// of one body share theirs. Nothing else in the IR can tell those apart,
	// and the difference is what separates an id collision the inliner renames
	// from a read that cannot say which copy it meant.
	//
	// Not serialized: it points back into the symbol graph, and a reparse of a
	// printed tree re-declares the handle from the `#id` it prints.
	Handle *Var `json:"-"`
	Key    Expr // key expression for list diffing (nil → implicit index)
	Ref    Expr // ref binding (nil if none)

	// The three below are a window's and nil on every other node, which is the
	// price of a window being a NodeInst rather than a type of its own. It is
	// three nil fields against the 79 `case *ir.Window:` arms the separate type
	// cost, and none of them is a *body owner* -- Vars, Funcs and Timers stay
	// off NodeInst, which is the distinction PLAN.md's first fork turns on.

	// ErrorHandler is the @error this node declared: the outermost error
	// boundary for the tree it renders. Separate from Handlers because those
	// are the events a platform wires to a widget and nothing wires this one.
	ErrorHandler *EventHandler `json:",omitempty"`
	// Params is the binding a window's scoped rest slot hands its body: one
	// struct value holding what the route knows per request, typed by the
	// `params` prop the call site wrote. Nil where the body wrote no
	// population, and so asked for nothing.
	//
	// The fields are the path's `{name}` placeholders, which is why nothing
	// here reads the href: the struct is the contract, and the path is a plain
	// string html holds to it.
	//
	// The *ir.Param the population declares, like every other population's
	// binding. That a target *stores* it -- one cell filled in before the body
	// renders, a Model field on a target with no request -- is codegen's
	// answer and is written down there (CodegenCtx.ModelState); the checker
	// makes no distinction, having none to make.
	Params *Param `json:"-"`
	// LocalRefs is populated by lower's passNodeEscape (MutationModel platforms
	// only): the set of synthesized widget ref ids (__nN) created in this
	// node's children that do NOT escape to any other scope. A node has one
	// when it is a render scope of its own, which today means a window. See
	// internal/lower/node_escape.go and Component.LocalRefs.
	LocalRefs map[string]bool `json:"-"`
}

// Prop is the value written for name, or nil if the call site did not write
// it. checkAndSplitArgs binds a positional arg to its declared name before the
// slice reaches here, so a lookup by name finds what was written positionally.
//
// Nil-safe on the receiver, and two callers depend on it:
// CodegenCtx.Windows synthesizes a WindowCtx with a nil Window for a
// harness-isolated root component, so html and gtk4 ask a window that is not
// there rather than guarding first.
// VisualNode is the source node n was built from, or nil where it was
// synthesized or came from a call statement.
//
// NodeInst.AST is the interface because `Foo()` parses as an *ast.CallStmt and
// `Foo { }` as an *ast.VisualNode, and both are instantiations. A caller that
// knows it holds the second -- the checker reading a window's block, which the
// parser only ever produces the one way -- asks here rather than repeating the
// assertion.
func (n *NodeInst) VisualNode() *ast.VisualNode {
	if n == nil {
		return nil
	}
	vn, _ := n.AST.(*ast.VisualNode)
	return vn
}

func (n *NodeInst) Prop(name string) Expr {
	if n == nil {
		return nil
	}
	for _, p := range n.Props {
		if p.Name == name {
			return p.Value
		}
	}
	return nil
}

// SlotContent is what a call site supplies for one named slot. Params are the
// caller's own names for the insertion's arguments, matched by position.
type SlotContent struct {
	Params []*Param
	Body   []Stmt
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
	AST  *ast.VisualNode
	Name string
	// Rest mirrors the declaration's: this insertion renders the children a
	// caller wrote bare, which arrive on NodeInst.Children rather than through
	// its Slots map.
	Rest     bool   `json:",omitempty"`
	Args     []Expr `json:",omitempty"` // values passed to a scoped slot
	Children []Stmt // fallback: rendered when the caller supplies nothing
}

func (*SlotInst) stmtNode() {}

// ErrorBoundary is a special-cased component that catches errors raised
// in the event handlers and expressions of its children. Effect analysis
// resolves each fallible call site beneath this node to Handler unless
// an inner boundary or per-call handler takes precedence.
type ErrorBoundary struct {
	AST     *ast.VisualNode
	Handler *EventHandler // the @error handler; nil when only Failed was written
	// Children is what the boundary renders until it catches, and Failed what
	// it renders after -- the `failed` slot's population, held to the same
	// tree as Children.
	//
	// Only the checker and passBoundaryFailed see a non-empty Failed. That
	// pass rewrites the pair into a reactive `if` over a flag its @error
	// handler sets, so by codegen a boundary is the passthrough it has always
	// been and no platform emitter grew a case.
	Children []Stmt
	Failed   []Stmt `json:",omitempty"`
	// FailedSlot is what the declaration calls that slot, kept so Convert can
	// write the population back out under the name the program wrote. The
	// checker finds the slot by shape rather than by name -- it is the one
	// non-rest slot the marked component declares -- so nothing in Go spells
	// `failed`, and renaming it in the library needs no Go edit.
	FailedSlot string `json:",omitempty"`
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

// Emit is a type-checked event emission. It originates from a bare
// `event(args)` call statement whose name resolves to a declared event.
type Emit struct {
	AST  *ast.CallStmt
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
	// Sym is the Var that Idents referring to this local resolve to. The
	// statement and the symbol are separate objects because the checker binds
	// the name in a scope while emitting the statement; an evaluator that
	// keys values by declaration needs the link between the two. Nil for the
	// node handles passDeclarative emits, which are addressed as element refs
	// rather than by symbol.
	Sym *Var
	// CanvasNode is the canvas instantiation this createNode flattened, kept
	// only when it is one.
	//
	// passDeclarative's whole job is to destroy the tree, so a drawing needs
	// something to ride across on -- its shapes are not widgets and are not
	// flattened with it. The node is the smallest such thing and carries
	// everything a platform asks of a canvas: the shapes, and the width,
	// height and scalingMode props. It used to be four fields holding a
	// synthesized draw func and three prop values copied out, which is the
	// same carrier written out longhand.
	CanvasNode *NodeInst
}

func (*LocalVar) stmtNode() {}

// Return is a type-checked return statement.
type Return struct {
	AST   *ast.ReturnStmt
	Value Expr // nil for bare return
}

func (*Return) stmtNode() {}

// Break and Continue are the loop escapes, acting on the innermost enclosing
// loop. There is nothing to type-check about either, so neither carries an
// expression; the AST node is kept for the position a diagnostic needs.
type Break struct {
	AST *ast.BreakStmt
}

func (*Break) stmtNode() {}

type Continue struct {
	AST *ast.ContinueStmt
}

func (*Continue) stmtNode() {}

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
	AST   *ast.ForStmt
	Key   string // iterator variable name
	Value string // optional second variable (empty for single-var form)
	// Iter is what the loop head evaluates to: an iterable to walk, a bool to
	// test before each iteration, or nil for `for { }`. IterKind is the
	// classification of the three.
	Iter     Expr
	ElemType *Type
	Body     []Stmt
	Else     []Stmt
	// KeySym and ValueSym are the symbols Idents in the body resolve to for
	// Key and Value. ValueSym is nil in the single-var form, and both are nil
	// for a name-only loop a codegen backend emits for itself.
	KeySym   *LoopVar
	ValueSym *LoopVar
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
	// (`for var &t = list` / `for var i, &t = list`): the element var has type
	// ref<T> and writes through it must update the original list element.
	// Set by the checker for addressable mutable list iterables. It is a
	// transient marker: the RefLoop lowering pass consumes it — rewriting
	// element uses to indexed list access and desugaring the loop into an
	// ordinary two-var (index, _) loop — then clears it, so codegen never
	// sees a RefElem loop. The headless interpreter (which does not lower)
	// reads it directly to bind the element as a reference.
	RefElem bool
	// Counted is the arithmetic sequence this loop walks when its iterable is
	// a sngl:seq call in a shape a host counting loop can express. Set by the
	// passIterKind lowering pass alongside IterKind; nil for every other loop.
	Counted *Counted `json:"-"`
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
	// IterCounted is iteration over an integer sequence, from the bounds in
	// Counted: the number binds to Key, or to Value with its ordinal in Key
	// when a second variable is written. The sequence is never built.
	IterCounted
	// IterIndexed is two-var iteration over a list/iter<T>: bind (index,
	// element) to (Key, Value).
	IterIndexed
	// IterMapEntries is iteration over a map: bind (key, value) to
	// (Key, Value); Value may be empty (caller substitutes a discard).
	IterMapEntries
	// IterCondition is a loop over a bool head, tested before each iteration:
	// `for x < n { }`. It declares no variable and walks nothing.
	IterCondition
	// IterForever is a loop with no head at all: `for { }`. It ends by a
	// `break` or a `return` in its body.
	IterForever
)

// DeriveIterKind classifies n's iteration shape from whether it has a head at
// all, that head's resolved type, and the loop's variable arity — the single
// decision every language's ForHead used to make independently. No head is
// the forever loop and a bool head is a condition; past those, a map iterable
// yields key/value pairs, otherwise a second variable means indexed iteration
// and a lone variable binds the element.
func DeriveIterKind(n *For) IterKind {
	if n == nil {
		return IterElement
	}
	// No head is the forever loop, and a bool head is a condition -- neither
	// iterates anything, so neither reaches the questions below.
	if n.Iter == nil {
		return IterForever
	}
	if t := n.Iter.ExprType(); t != nil && t.Kind == TypeBool {
		return IterCondition
	}
	if CountedSeq(n) != nil {
		return IterCounted
	}
	if t := n.Iter.ExprType(); t != nil && t.Kind == TypeMap {
		return IterMapEntries
	}
	if n.Value != "" {
		return IterIndexed
	}
	return IterElement
}

// Counted is the sequence an IterCounted loop walks: bounds as expressions,
// step as a constant. End is exclusive, in the direction Step points.
//
// The step is a constant because nothing else can pick the comparison: `i <
// end` and `i > end` are different loop heads, and a step whose sign is only
// known at run time chooses between them at run time. Such a loop keeps the
// materialising form instead, which is correct for either sign.
type Counted struct {
	Start Expr
	End   Expr
	Step  int // never 0; negative counts down
}

// CountedSeq reports the sequence n iterates when its iterable is one of
// sngl:seq's constructors written directly in the loop head, or nil when the
// loop is anything else.
//
// Dispatch is on the #[intrinsic] id, not the function's name: `seq.range` is
// an ordinary package function, so a program's own `range` reaches here too.
// The arguments are read positionally, which the checker has already made
// safe -- it normalises a named-argument call into declaration order.
//
// The two-variable form counts too: the ordinal beside each number is another
// counter, not a reason to build the numbers.
func CountedSeq(n *For) *Counted {
	if n == nil {
		return nil
	}
	call, ok := n.Iter.(*Call)
	if !ok || call.Func == nil {
		return nil
	}
	arg := func(i int) Expr {
		if i >= len(call.Args) {
			return nil
		}
		return call.Args[i].Value
	}
	switch call.Func.Intrinsic {
	case "seq.count":
		// count(n) is range(0, n): the sole argument is the end bound.
		if n := arg(0); n != nil {
			return &Counted{Start: &Literal{Type: TypInt, Value: "0"}, End: n, Step: 1}
		}
	case "seq.range":
		if start, end := arg(0), arg(1); start != nil && end != nil {
			return &Counted{Start: start, End: end, Step: 1}
		}
	case "seq.step":
		start, end, by := arg(0), arg(1), arg(2)
		if start == nil || end == nil {
			return nil
		}
		step, ok := constInt(by)
		if !ok || step == 0 {
			return nil
		}
		return &Counted{Start: start, End: end, Step: step}
	}
	return nil
}

// constInt reads an integer literal's value, and reports false for anything
// else -- including a folded constant that never became a literal.
func constInt(e Expr) (int, bool) {
	lit, ok := e.(*Literal)
	if !ok || lit.Type == nil || lit.Type.Kind != TypeInt {
		return 0, false
	}
	v, err := strconv.Atoi(lit.Value)
	if err != nil {
		return 0, false
	}
	return v, true
}

// CanvasRedrawStmt is injected by passCanvasReactivity into handler/timer
// bodies that mutate state vars read by a canvas draw function. Each platform
// translates this to its native "clear and redraw the canvas" operation.
type CanvasRedrawStmt struct {
	Canvas *NodeInst // the canvas element
}

func (*CanvasRedrawStmt) stmtNode() {}
