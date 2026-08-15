package ir

import "git.duckfam.us/jonathan/sngl/ast"

// Expr is a type-checked expression node. Every Expr carries its resolved
// type. Concrete types have an AST field for source positions.
type Expr interface {
	exprNode()
	ExprType() *Type
}

// Literal is a constant value: int, float, string, bool, null, color, unit.
type Literal struct {
	AST    *ast.LiteralExpr
	Type   *Type
	Raw    string // source text for codegen
	Suffix string // unit suffix ("px") if unit; empty otherwise
}

// Ident is a resolved identifier reference.
type Ident struct {
	AST          *ast.IdentExpr
	Type         *Type
	Name         string // original identifier name from source
	Sym          Symbol // resolved: *Var, *Func, *Param, *LoopVar, etc.
	Member       string // non-empty for bare enum member ("active" → Status.active)
	IsElementRef bool   // true when from #id syntax (ElementRefExpr)
	// Synthesized marks Idents emitted by a lowering pass for refs to
	// pass-introduced Vars or Funcs (e.g. __slot<N>, __renderSlot<N>,
	// __n<N> widget refs). Codegen consumers use this to distinguish
	// pass-synthesized refs from user-named identifiers without string
	// prefix matching.
	Synthesized bool `json:"-"`
}

// Binary is a binary operation.
type Binary struct {
	AST   *ast.BinaryExpr // nil for synthetic (desugared interpolation)
	Type  *Type
	Op    ast.BinaryOp
	Left  Expr
	Right Expr
}

// Unary is a unary operation.
type Unary struct {
	AST     *ast.UnaryExpr
	Type    *Type
	Op      ast.UnaryOp
	Operand Expr
}

// Ternary is a conditional expression: cond ? then : else.
type Ternary struct {
	AST  *ast.TernaryExpr
	Type *Type
	Cond Expr
	Then Expr
	Else Expr
}

// Call is a resolved function or method call.
//   - Plain function: Func set, Receiver nil
//   - Type-attached method (string.upper, list.push, etc.): Func set with
//     Func.Receiver != "", Receiver nil. The receiver value is Args[0]; both
//     "x.upper()" and "string.upper(x)" normalize to this shape.
//   - Namespace call (ns.foo, html.div): Func optionally set, Receiver set to
//     the namespace ident.
type Call struct {
	AST      *ast.CallExpr
	Type     *Type     // return type
	Func     *Func     // resolved function (nil for unresolved/dynamic)
	Callee   Expr      // callee expression when Func is nil (e.g. func-typed var)
	Receiver Expr      // non-nil only for namespace/component/element-ref calls
	Args     []CallArg // resolved arguments (for type-methods, Args[0] is the receiver)

	// Event is the event name when this call is an element-ref event
	// trigger (`c.inc.click()`), set by the checker after resolving the
	// field to a declared event on the addressed component. Empty for
	// ordinary method/function calls.
	Event string

	// Error handling. These are set by the checker + effect analysis.
	// ErrorHandler is an inline `@error(e) { ... }` attached to this call
	// site. ResolvedHandler is the handler this call's errors route to —
	// either ErrorHandler (per-call), or the nearest enclosing
	// errorBoundary, or the enclosing window's @error. Mode selects the
	// callsite lowering form.
	ErrorHandler    *EventHandler
	ResolvedHandler *EventHandler
	ErrorMode       ErrorMode
}

// ErrorMode describes how a fallible call's error is lowered at the callsite.
type ErrorMode int

const (
	// ErrorNone means the call cannot raise — no wrapping needed.
	ErrorNone ErrorMode = iota
	// ErrorPerCall means an inline @error handler absorbs the error and
	// execution continues after the handler body.
	ErrorPerCall
	// ErrorBubble means the enclosing function is itself fallible; the
	// error bubbles up its return channel unchanged.
	ErrorBubble
	// ErrorInvokeAndTerminate means a boundary or window handler was
	// resolved as the target; codegen invokes that handler and terminates
	// the current event-handler scope.
	ErrorInvokeAndTerminate
	// ErrorPropagateNative means no handler is reachable — codegen emits
	// the target language's native throw (panic / throw).
	ErrorPropagateNative
)

// CallArg is a resolved argument in a function call.
// NamePos records the source position of the Name identifier for named
// args (zero for positional).
type CallArg struct {
	Name    string // empty for positional
	NamePos ast.Pos
	Value   Expr
}

// Conversion is a builtin type coercion: int(x), float(x), string(x), bool(x).
type Conversion struct {
	AST     *ast.CallExpr
	Type    *Type // target type
	Operand Expr
}

// Select is a resolved field or member access.
type Select struct {
	AST     *ast.SelectExpr
	Type    *Type
	Operand Expr
	Field   string
}

// Index is a resolved index operation: operand[index].
type Index struct {
	AST     *ast.IndexExpr
	Type    *Type
	Operand Expr
	Idx     Expr
}

// StructLit is a resolved struct literal.
type StructLit struct {
	AST    *ast.StructExpr
	Type   *Type
	Def    *StructDef // resolved struct definition (nil if anonymous/unresolved)
	Fields []FieldInit
}

// FieldInit is a field in a struct literal. NamePos records the source
// position of the Name identifier (zero for spread or synthesized).
type FieldInit struct {
	Name    string // empty for spread
	NamePos ast.Pos
	Value   Expr
	Spread  bool
}

// ListLit is a resolved list literal.
type ListLit struct {
	AST   *ast.ListExpr
	Type  *Type
	Elems []Expr
}

// MapEntry is one key-value pair in a MapLitIR.
type MapEntry struct {
	Key   Expr
	Value Expr
}

// MapLitIR is a resolved map literal.
type MapLitIR struct {
	AST     *ast.MapLit
	Type    *Type
	Entries []MapEntry
}

// Spread is a spread operation: ...expr.
type Spread struct {
	AST     *ast.SpreadExpr
	Type    *Type
	Operand Expr
}

// Lambda is a resolved function literal / closure.
type Lambda struct {
	AST  *ast.LambdaExpr
	Type *Type
	Func *Func // resolved params, return type, checked body
}

// Closure is a lifted lambda: a top-level Func plus a captured-state struct
// literal. NoLambda emits Closures in place of every Lambda. Codegen for
// closure-supporting target languages never sees a Closure (their cap is
// off); closure-free targets translate Closure into a (state, fn-ref) pair.
type Closure struct {
	AST   *ast.LambdaExpr // original lambda position; nil for synthesized handler lifts
	Type  *Type           // user-visible TypeFunc — without the synthesized leading state param
	Func  *Func           // lifted top-level Func; first Param is the state struct
	State *StructLit      // captured-state struct construction at this site
}

// --- Expr interface implementations ---

func (*Literal) exprNode()    {}
func (*Ident) exprNode()      {}
func (*Binary) exprNode()     {}
func (*Unary) exprNode()      {}
func (*Ternary) exprNode()    {}
func (*Call) exprNode()       {}
func (*Conversion) exprNode() {}
func (*Select) exprNode()     {}
func (*Index) exprNode()      {}
func (*StructLit) exprNode()  {}
func (*ListLit) exprNode()    {}
func (*MapLitIR) exprNode()   {}
func (*Spread) exprNode()     {}
func (*Lambda) exprNode()     {}
func (*Closure) exprNode()    {}

func (x *Literal) ExprType() *Type    { return x.Type }
func (x *Ident) ExprType() *Type      { return x.Type }
func (x *Binary) ExprType() *Type     { return x.Type }
func (x *Unary) ExprType() *Type      { return x.Type }
func (x *Ternary) ExprType() *Type    { return x.Type }
func (x *Call) ExprType() *Type       { return x.Type }
func (x *Conversion) ExprType() *Type { return x.Type }
func (x *Select) ExprType() *Type     { return x.Type }
func (x *Index) ExprType() *Type      { return x.Type }
func (x *StructLit) ExprType() *Type  { return x.Type }
func (x *ListLit) ExprType() *Type    { return x.Type }
func (x *MapLitIR) ExprType() *Type   { return x.Type }
func (x *Spread) ExprType() *Type     { return x.Type }
func (x *Lambda) ExprType() *Type     { return x.Type }
func (x *Closure) ExprType() *Type    { return x.Type }

// IsConst reports whether e can, in principle, be evaluated at compile time.
// This mirrors the optimizer's isConstExpr but without evalCtx: loop vars and
// native-import purity are not considered const here, since those determinations
// belong to later phases. Suitable for check-time assertions like const(expr).
func IsConst(e Expr) bool {
	if e == nil {
		return false
	}
	switch x := e.(type) {
	case *Literal:
		return true
	case *Ident:
		// A bare enum member (e.g. `active` resolving to `Status.active`) is a
		// compile-time constant; it carries Member but no resolved Sym.
		if x.Member != "" {
			return true
		}
		switch x.Name {
		case "PLATFORM", "LANGUAGE", "true", "false", "null":
			return true
		}
		if v, ok := x.Sym.(*Var); ok && v.IsConst {
			return true
		}
		if _, ok := x.Sym.(*Namespace); ok {
			return true
		}
		if _, ok := x.Sym.(*EnumDef); ok {
			return true
		}
		if _, ok := x.Sym.(*StructDef); ok {
			return true
		}
		return false
	case *Binary:
		return IsConst(x.Left) && IsConst(x.Right)
	case *Unary:
		// Reference operations (&x, *p) are never const: storage may be
		// mutated through aliases, and `&` of a literal is not legal.
		if x.Op == ast.UnaryAddr || x.Op == ast.UnaryDeref {
			return false
		}
		return IsConst(x.Operand)
	case *Ternary:
		return IsConst(x.Cond) && IsConst(x.Then) && IsConst(x.Else)
	case *Call:
		for _, a := range x.Args {
			if !IsConst(a.Value) {
				return false
			}
		}
		if x.Receiver != nil && !IsConst(x.Receiver) {
			return false
		}
		return x.Func != nil && x.Func.Purity == PurityPure
	case *Conversion:
		return IsConst(x.Operand)
	case *Select:
		return IsConst(x.Operand)
	case *Index:
		return IsConst(x.Operand) && IsConst(x.Idx)
	case *ListLit:
		for _, el := range x.Elems {
			if !IsConst(el) {
				return false
			}
		}
		return true
	case *StructLit:
		for _, f := range x.Fields {
			if !IsConst(f.Value) {
				return false
			}
		}
		return true
	case *Spread:
		return IsConst(x.Operand)
	case *Closure:
		return false
	}
	return false
}
