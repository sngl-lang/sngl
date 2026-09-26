package optimize

import (
	"fmt"
	"math"
	"strconv"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// foldExpr attempts to evaluate an expression as a constant, falling back
// to recursive sub-expression folding.
func foldExpr(e ir.Expr, ctx *evalCtx) ir.Expr {
	if e == nil {
		return nil
	}
	if ctx != nil && ctx.copyShared && isScalar(e.ExprType()) {
		ctx.copyShared = false
		defer func() { ctx.copyShared = true }()
	}

	if val, ok := evalExpr(e, ctx); ok && !ctx.keepsReference(e, val) {
		if expr := irFromValue(val, e.ExprType()); expr != nil {
			return expr
		}
	}

	// Try function inlining.
	if call, ok := e.(*ir.Call); ok {
		// An argument bound to a const parameter is held to a literal before
		// the call is inlined away: the contract is the call site's, and once
		// the body is spliced in there is no parameter left to hold it to.
		for i := range call.Args {
			if constParamOf(call, i) != nil {
				call.Args[i].Value = foldCallArg(call, i, ctx)
			}
		}
		if inlined := inlineCall(call, ctx); inlined != nil {
			if ctx != nil && call.Func != nil {
				ctx.inliningFuncs[call.Func] = true
				result := foldExpr(inlined, ctx)
				delete(ctx.inliningFuncs, call.Func)
				return result
			}
			return foldExpr(inlined, ctx)
		}
	}

	// Recursive sub-expression folding.
	switch x := e.(type) {
	case *ir.Binary:
		x.Left = foldExpr(x.Left, ctx)
		x.Right = foldExpr(x.Right, ctx)
		if lit := scaledUnitLiteral(x); lit != nil {
			return lit
		}
		if lit := combinedUnitLiteral(x); lit != nil {
			return lit
		}
		if lit := settledNullTest(x); lit != nil {
			return lit
		}
	case *ir.Unary:
		// Reference operations (&x, *p) are never const — the underlying
		// storage may be mutated through aliases, and `&literal` is not a
		// valid emission. Leave the operand untouched so the codegen still
		// sees the original lvalue identifier.
		if x.Op == ast.UnaryDeref && !isRef(x.Operand) {
			// A plain T that stood in for a ref<T> parameter: the coercion
			// leaves the value as it is, so what the body wrote as `*p` is now
			// a deref of something that was never a reference. Reading it is
			// the value itself.
			return foldExpr(x.Operand, ctx)
		}
		if x.Op == ast.UnaryAddr || x.Op == ast.UnaryDeref {
			return e
		}
		x.Operand = foldExpr(x.Operand, ctx)
	case *ir.Ternary:
		x.Cond = foldExpr(x.Cond, ctx)
		// Short-circuit if cond folded to literal bool.
		if lit, ok := x.Cond.(*ir.Literal); ok && lit.Type != nil && lit.Type.Kind == ir.TypeBool {
			if lit.Value == "true" {
				return foldExpr(x.Then, ctx)
			}
			return foldExpr(x.Else, ctx)
		}
		x.Then = foldExpr(x.Then, ctx)
		x.Else = foldExpr(x.Else, ctx)
	case *ir.Call:
		if x.Receiver != nil {
			x.Receiver = foldExpr(x.Receiver, ctx)
		}
		for i := range x.Args {
			x.Args[i].Value = foldCallArg(x, i, ctx)
		}
	case *ir.Conversion:
		x.Operand = foldExpr(x.Operand, ctx)
		// An unwrap of a value put into an option is that value. The pair
		// appears where a call site supplied a plain T for a declared
		// option<T> and the body read it in a branch a null test had settled
		// -- two authors' halves, meeting only after inlining. Left standing,
		// Go boxes and immediately dereferences, Kotlin asserts `2.0!!`, and
		// the static html renderer cannot see a constant where there is one.
		if inner := unwrappedOptionOperand(x); inner != nil {
			return foldExpr(inner, ctx)
		}
	case *ir.Select:
		x.Operand = foldExpr(x.Operand, ctx)
		if id, ok := x.Operand.(*ir.Ident); ok {
			// A window's `#id` binds a handle var like every other node's, so
			// the window is reached through it rather than off the symbol.
			if v, ok := id.Sym.(*ir.Var); ok && v.NodeHandle {
				// Every prop of the #[builtin("window")] component is
				// readable off the id, so each must fold here -- a Select left
				// standing reaches codegen as a dangling reference. Keep in
				// step with windowStructValue (expand.go), which does the same
				// for the unrolled-list case.
				if win := ctx.windowForHandle(v); win != nil {
					if val := win.Prop(x.Field); val != nil {
						// Folded again, and against *this* context: the read
						// may sit in an unrolled loop body where the loop
						// variable the prop names is bound, and a prop
						// returned as written came out as the pre-unroll
						// `it.title` and rendered empty.
						//
						// That is safe only because ctx.values is keyed by
						// ir.Symbol *pointer*. A `const greet` read by this
						// prop and a `for var greet` shadowing the name around
						// the read are two symbols and two keys, so the prop
						// still folds to the const. Resolve anything in here
						// by name and this becomes a wrong value rather than a
						// missing one.
						//
						// cloneExpr and not ir.CloneExpr: the result is a
						// second occurrence of the expression rather than the
						// prop itself, and this one shallow-copies, leaving
						// Ident.Sym pointing at the same symbols. ir.CloneExpr
						// repoints them, which is exactly the lookup above.
						//
						// Guarded because `window #h(title = h.title)` reads
						// the prop it is. Left standing there, which is what a
						// prop with no answer already reached codegen as.
						key := windowProp{win: win, field: x.Field}
						if !ctx.foldingProp[key] {
							// foldPkg builds the map so that every child ctx
							// shares one; this is for the contexts assembled
							// by hand, which today fold nothing (nativescan)
							// but would write into a nil map if one ever did.
							if ctx.foldingProp == nil {
								ctx.foldingProp = map[windowProp]bool{}
							}
							ctx.foldingProp[key] = true
							out := foldExpr(cloneExpr(val), ctx)
							delete(ctx.foldingProp, key)
							return out
						}
					}
				}
			}
		}
	case *ir.Index:
		x.Operand = foldExpr(x.Operand, ctx)
		x.Idx = foldExpr(x.Idx, ctx)
	case *ir.ListLit:
		elems := x.Elems[:0:0]
		for _, el := range x.Elems {
			el = foldExpr(el, ctx)
			if sp, ok := el.(*ir.Spread); ok {
				if inner, ok := sp.Operand.(*ir.ListLit); ok {
					elems = append(elems, inner.Elems...)
					continue
				}
			}
			elems = append(elems, el)
		}
		x.Elems = elems
	case *ir.StructLit:
		for i := range x.Fields {
			if x.Fields[i].Value != nil {
				x.Fields[i].Value = foldExpr(x.Fields[i].Value, ctx)
			}
		}
	case *ir.Spread:
		x.Operand = foldExpr(x.Operand, ctx)
	case *ir.MapLitIR:
		for i := range x.Entries {
			x.Entries[i].Key = foldExpr(x.Entries[i].Key, ctx)
			x.Entries[i].Value = foldExpr(x.Entries[i].Value, ctx)
		}
	case *ir.Literal, *ir.Ident:
		// No subexpressions to fold.
	case *ir.Lambda:
		// A lambda's body is statements, folded as any other block is. It used
		// to be skipped here on the grounds that the enclosing func would fold
		// it, which holds for a lambda the program wrote and not for one a
		// lowering pass synthesized: passQuery builds the thunk after the
		// optimizer has walked every declaration, so the calls inside it are
		// calls nothing has looked at.
		if x.Func != nil {
			x.Func.Block = foldStmts(x.Func.Block, ctx)
		}
	case *ir.Closure:
		if x.Func != nil {
			x.Func.Block = foldStmts(x.Func.Block, ctx)
		}
	case *ir.ContextRead:
		// No subexpressions.
	default:
		panic(fmt.Sprintf("foldExpr: unhandled expr %T", x))
	}
	// Asked a second time, because a sub-expression may have become constant
	// only during the walk above and the evaluation at the top saw the shape
	// before that. An option unwrap is what makes this necessary rather than
	// merely tidy: evalConversion refuses an option *wrap* outright, so
	// `float(option<float>(2.0)) / 10.0` evaluated to nothing whole while both
	// its operands folded -- and gtk4 emitted the division for cgo to do.
	if val, ok := evalExpr(e, ctx); ok && !ctx.keepsReference(e, val) {
		if expr := irFromValue(val, e.ExprType()); expr != nil {
			return expr
		}
	}
	return e
}

// foldStmts folds a slice of statements, removing nil results.
// Under Documents, for loops whose iterator evaluates to a const list are
// unrolled in place so downstream codegen sees static statements instead of
// runtime iteration.
// If statements whose condition evaluates to a const bool are inlined
// (true branch) or dropped (false branch) so downstream codegen sees static
// structure instead of runtime conditionals.
func foldStmts(stmts []ir.Stmt, ctx *evalCtx) []ir.Stmt {
	var out []ir.Stmt
	for _, s := range stmts {
		if ni, ok := s.(*ir.NodeInst); ok && ni.Component != nil {
			if inlined := inlineComponentCall(ni, ctx); inlined != nil {
				out = append(out, inlined...)
				continue
			}
		}
		// Only under Documents; see evalCtx.unroll.
		if fs, ok := s.(*ir.For); ok && ctx.unroll {
			if expanded := expandForStmt(fs, ctx); expanded != nil {
				out = append(out, expanded...)
				continue
			}
		}
		// A catch block's condition is the literal true by construction, and
		// flattening it would drop the catch.
		if ifs, ok := s.(*ir.If); ok && ifs.Catch == nil {
			cond := foldExpr(ifs.Cond, ctx)
			if lit, ok := cond.(*ir.Literal); ok && lit.Type != nil && lit.Type.Kind == ir.TypeBool {
				branch := ifs.Else
				if lit.Value == "true" {
					branch = ifs.Body
				}
				branch = foldStmts(branch, ctx)
				if ifs.FromTernary {
					out = initTernaryTemp(out, branch)
				} else {
					out = append(out, branch...)
				}
				continue
			}
			ifs.Cond = cond
		}
		result := foldStmt(s, ctx)
		if result != nil {
			out = append(out, result)
		}
	}
	return out
}

// initTernaryTemp appends the surviving branch of a folded NoTernary `if`,
// turning its final assignment into the initializer of the `var __ltN` that
// passTernary declared immediately before the `if`.
//
// Left as a bare assignment it is a statement in a view body, which a
// RenderModel emitter draws nothing for: bubbletea skipped it and rendered the
// temp's zero, so a markup bullet came out empty. A declaration with its value
// is something every emitter already writes.
func initTernaryTemp(out, branch []ir.Stmt) []ir.Stmt {
	if len(out) == 0 || len(branch) == 0 {
		return append(out, branch...)
	}
	decl, ok := out[len(out)-1].(*ir.LocalVar)
	set, ok2 := branch[len(branch)-1].(*ir.Assign)
	if !ok || !ok2 || decl.Init != nil || set.Op != ast.AssignSet {
		return append(out, branch...)
	}
	if id, ok := set.Target.(*ir.Ident); !ok || id.Sym == nil || id.Sym != decl.Sym {
		return append(out, branch...)
	}
	// The branch's own hoists compute the value, so the declaration moves
	// below them; nothing between it and the `if` could have read the temp.
	out = append(out[:len(out)-1], branch[:len(branch)-1]...)
	decl.Init = set.Value
	return append(out, decl)
}

// foldStmt folds constants and eliminates dead branches in a statement.
// Returns nil to remove the statement.
func foldStmt(s ir.Stmt, ctx *evalCtx) ir.Stmt {
	switch n := s.(type) {
	case *ir.If:
		return foldIfStmt(n, ctx)
	case *ir.NodeInst:
		return foldNodeInst(n, ctx)
	case *ir.For:
		n.Iter = foldExpr(n.Iter, ctx)
		n.Body = foldStmts(n.Body, ctx)
		n.Else = foldStmts(n.Else, ctx)
	case *ir.Assign:
		n.Value = foldOwned(n.Value, ctx)
	case *ir.CallStmt:
		if n.Call != nil {
			if n.Call.Receiver != nil {
				n.Call.Receiver = foldExpr(n.Call.Receiver, ctx)
			}
			for i := range n.Call.Args {
				n.Call.Args[i].Value = foldCallArg(n.Call, i, ctx)
			}
		}
	case *ir.LocalVar:
		if n.Init != nil {
			n.Init = foldOwned(n.Init, ctx)
		}
		// The statements that paint a flattened canvas, which are ordinary
		// statements with ordinary constants in them: a shape's style test
		// against a colour nobody set folds away here or the target draws it.
		if n.CanvasNode != nil {
			n.CanvasNode.Children = foldStmts(n.CanvasNode.Children, ctx)
		}
	case *ir.Return:
		if n.Value != nil {
			n.Value = foldOwned(n.Value, ctx)
		}
	case *ir.Emit:
		for i := range n.Args {
			n.Args[i].Value = foldOwned(n.Args[i].Value, ctx)
		}
	case *ir.SlotInst:
		n.Children = foldStmts(n.Children, ctx)
		for _, name := range ir.SlotNames(n.Slots) {
			n.Slots[name].Body = foldStmts(n.Slots[name].Body, ctx)
		}
	case *ir.ContextProvider:
		if n.Value != nil {
			n.Value = foldOwned(n.Value, ctx)
		}
		n.Children = foldStmts(n.Children, ctx)
	case *ir.Toggle:
		n.Target = foldExpr(n.Target, ctx)
	case *ir.ErrorBoundary:
		n.Children = foldStmts(n.Children, ctx)
	case *ir.CanvasRedrawStmt:
		// Canvas redraw stmts carry only NodeInst/Func pointers; no expressions to fold.
	case *ir.Break, *ir.Continue:
		// A loop escape carries no expression.
	default:
		panic(fmt.Sprintf("foldStmt: unhandled stmt %T", n))
	}
	return s
}

func foldIfStmt(s *ir.If, ctx *evalCtx) ir.Stmt {
	if s.Catch != nil {
		s.Body = foldStmts(s.Body, ctx)
		return s
	}
	s.Cond = foldExpr(s.Cond, ctx)

	if lit, ok := s.Cond.(*ir.Literal); ok && lit.Type != nil && lit.Type.Kind == ir.TypeBool {
		if lit.Value == "true" {
			// Always true — inline body.
			s.Cond = nil
		} else {
			// Always false — dead branch.
			return nil
		}
	}

	s.Body = foldStmts(s.Body, ctx)
	s.Else = foldStmts(s.Else, ctx)
	return s
}

func foldNodeInst(n *ir.NodeInst, ctx *evalCtx) ir.Stmt {
	for i := range n.Props {
		// A func-typed prop's lambda is a handler written as an argument, so
		// its body folds the way a handler's does. foldExpr leaves a lambda
		// alone — a lambda elsewhere is folded when its enclosing func is —
		// and this one has no enclosing func to be reached from, so an
		// unrolled loop would leave its own variable behind in the body.
		if lam, ok := n.Props[i].Value.(*ir.Lambda); ok && lam.Func != nil {
			lam.Func.Block = foldStmts(lam.Func.Block, ctx)
			continue
		}
		n.Props[i].Value = foldPropArg(n, n.Props[i].Name, n.Props[i].Value, ctx)
	}
	for i := range n.Handlers {
		n.Handlers[i].Func.Block = foldStmts(n.Handlers[i].Func.Block, ctx)
	}
	// A window's @error is a handler like the rest, and reached from nowhere
	// else: it hung off ir.Window, whose own fold arm walked the props and the
	// body and not this.
	if n.ErrorHandler != nil && n.ErrorHandler.Func != nil {
		n.ErrorHandler.Func.Block = foldStmts(n.ErrorHandler.Func.Block, ctx)
	}
	// A named slot's population is a body like the children are. Visited by
	// name because Slots is a map: folding itself does not care, but a pass
	// sharing this walk's order should not depend on Go's.
	for _, name := range ir.SlotNames(n.Slots) {
		if sc := n.Slots[name]; sc != nil {
			sc.Body = foldStmts(sc.Body, ctx)
		}
	}
	n.Children = foldStmts(n.Children, ctx)
	return n
}

// putsValueInOption reports whether conv is a value being placed into an
// option -- which is to say that what comes out of it is not null.
//
// Deliberately wider than ir.IsOptionWrap, which is the promotion of a bare T
// into option<T> and nothing else: an argument crossing two coercions at once
// arrives as the single conversion `option<float>(2)`, an int operand under a
// float option, and that is a value in an option too.
func putsValueInOption(conv *ir.Conversion) bool {
	if conv == nil || conv.Type == nil || conv.Type.Kind != ir.TypeOption || conv.Operand == nil {
		return false
	}
	src := conv.Operand.ExprType()
	if src == nil {
		return false
	}
	return src.Kind != ir.TypeNull && src.Kind != ir.TypeOption && src.Kind != ir.TypeDyn
}

// unwrappedOptionOperand answers `float(option<float>(2))` with `float(2)`,
// and nil for anything that is not an unwrap of a value known to be there.
//
// The conversion is rebuilt rather than dropped because the inner operand may
// carry a different type from the position it now stands in -- the `2` above
// is an int where a float is wanted.
func unwrappedOptionOperand(x *ir.Conversion) ir.Expr {
	if !ir.IsOptionUnwrap(x) {
		return nil
	}
	inner, ok := x.Operand.(*ir.Conversion)
	if !ok || !putsValueInOption(inner) {
		return nil
	}
	if inner.Operand.ExprType().Equal(x.Type) {
		return inner.Operand
	}
	return &ir.Conversion{AST: x.AST, Type: x.Type, Operand: inner.Operand}
}

// isNullOperand reports whether e is the `null` literal, which carries a type
// of its own rather than any option's.
func isNullOperand(e ir.Expr) bool {
	t := e.ExprType()
	return t != nil && t.Kind == ir.TypeNull
}

// settledNullTest folds `x != null` and `x == null` where the type of x says
// the answer: everything in SNGL is non-nullable but `option<T>`, the `null`
// literal itself, and `dyn`, which says nothing about what it holds.
//
// The test is written by whoever declared the option and answered by whoever
// filled it in, and for a component prop those are two different authors. A
// platform override asks `if value != null`; a call site that supplied a value
// has had its argument wrapped by the checker, so after inlining what is left
// is a float compared against null. Unfolded, every determinate `progress` on
// android emitted a dead branch around the live one, a `!!` on a literal, and
// an unreachable arm whose type the host then had to agree with.
func settledNullTest(x *ir.Binary) *ir.Literal {
	if x.Op != ast.BinEq && x.Op != ast.BinNeq {
		return nil
	}
	var other ir.Expr
	switch {
	case isNullOperand(x.Left) && isNullOperand(x.Right):
		// null against null: no operand type to read, the literals decide.
		return &ir.Literal{Type: ir.TypBool, Value: strconv.FormatBool(x.Op == ast.BinEq)}
	case isNullOperand(x.Right):
		other = x.Left
	case isNullOperand(x.Left):
		other = x.Right
	default:
		return nil
	}
	// A value written where an option was declared reaches here as the
	// conversion the checker inserted, whose *type* is the option. What it
	// holds is the operand, and that is the half the test is about.
	for {
		conv, ok := other.(*ir.Conversion)
		if !ok || !putsValueInOption(conv) {
			break
		}
		other = conv.Operand
	}
	if !alwaysPresent(other.ExprType()) || !droppable(other) {
		return nil
	}
	return &ir.Literal{Type: ir.TypBool, Value: strconv.FormatBool(x.Op == ast.BinNeq)}
}

// alwaysPresent reports whether a value of t is necessarily there, so that
// comparing one against null has an answer the type already holds.
//
// An allowlist, and that direction is the point: the kinds that *can* be
// absent are not a closed set -- `option`, `dyn` and a func null converts to,
// but also an unbound type parameter, a foreign handle that is a host pointer,
// and a `remote` still in flight. Listed the other way round, each new kind
// would join the fold by default and be wrong there in silence.
func alwaysPresent(t *ir.Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case ir.TypeBool, ir.TypeInt, ir.TypeFloat, ir.TypeString,
		ir.TypeUnit, ir.TypeEnum, ir.TypeStruct, ir.TypeList, ir.TypeMap:
		return true
	}
	return false
}

// droppable reports whether evaluating e can be skipped without losing
// anything. Every other fold in this file replaces an expression with what
// evaluating it produced; this one replaces it with an answer read off its
// *type*, so the operand goes unevaluated and a call inside it would never
// run.
func droppable(e ir.Expr) bool {
	ok := true
	_ = ir.WalkExprs(e, func(sub ir.Expr) error {
		call, isCall := sub.(*ir.Call)
		if !isCall {
			return nil
		}
		if call.Func == nil || call.Func.Purity != ir.PurityPure || call.ErrorMode != ir.ErrorNone {
			ok = false
		}
		return nil
	})
	return ok
}

// scaledUnitLiteral folds a unit literal scaled by a number -- `400 * 1px`,
// `1px * 400`, `100px / 2` -- into the single literal `400px`.
//
// It works on the expressions rather than through evalExpr because the
// folder's value model has no unit: parseLiteral answers nil for one, so a
// measurement written as arithmetic never folded at all. That is not cosmetic.
// A canvas reads its pixel size off a literal prop, and a canvas sized by a
// const expression was falling back to the 300x150 default on every platform.
//
// Scaling is the case that needs no conversion table: the suffix is the one
// the unit literal was written with. Adding two needs one, which is
// combinedUnitLiteral below.
func scaledUnitLiteral(x *ir.Binary) *ir.Literal {
	if x.Op != ast.BinMul && x.Op != ast.BinDiv {
		return nil
	}
	unit, _ := x.Left.(*ir.Literal)
	num, _ := x.Right.(*ir.Literal)
	if unit == nil || num == nil {
		return nil
	}
	if unit.Suffix == "" {
		if x.Op == ast.BinDiv {
			// A number over a unit is not that unit.
			return nil
		}
		unit, num = num, unit
	}
	if unit.Suffix == "" || num.Suffix != "" {
		return nil
	}
	amount, err := strconv.ParseFloat(unit.Value, 64)
	if err != nil {
		return nil
	}
	factor, err := strconv.ParseFloat(num.Value, 64)
	if err != nil {
		return nil
	}
	if x.Op == ast.BinDiv {
		if factor == 0 {
			return nil
		}
		amount /= factor
	} else {
		amount *= factor
	}
	text := strconv.FormatFloat(amount, 'g', -1, 64)
	if amount == math.Trunc(amount) && !math.IsInf(amount, 0) {
		text = strconv.FormatInt(int64(amount), 10)
	}
	return &ir.Literal{Type: unit.Type, Value: text, Suffix: unit.Suffix}
}

// combinedUnitLiteral folds `3px + 4px` into `7px` and `1rem + 2em` into
// `18em`, using ir.UnitMagnitude to reduce each side into the base it belongs
// to. This is the design's "prefer a plain number when the terms share a
// base": once folded, every backend emits one magnitude instead of a
// per-base record built out of two.
//
// Two literals in *different* bases -- `1px + 2pct` -- have no single number
// and are deliberately left alone, for the backend's multi-base
// representation to carry. That is the only case that reaches it.
func combinedUnitLiteral(x *ir.Binary) *ir.Literal {
	if x.Op != ast.BinAdd && x.Op != ast.BinSub {
		return nil
	}
	left, _ := x.Left.(*ir.Literal)
	right, _ := x.Right.(*ir.Literal)
	if left == nil || right == nil {
		return nil
	}
	lm, lbase, ok := ir.UnitMagnitude(left)
	if !ok {
		return nil
	}
	rm, rbase, ok := ir.UnitMagnitude(right)
	if !ok || lbase != rbase || !left.Type.SameUnitType(right.Type) {
		return nil
	}
	sum := lm + rm
	if x.Op == ast.BinSub {
		sum = lm - rm
	}
	// The result is stated in the shared base, not in either operand's
	// suffix: `1rem + 2em` is 18em, and there is no rem count that says so.
	return &ir.Literal{Type: left.Type, Value: ir.FormatUnitMagnitude(sum), Suffix: lbase}
}

// isRef reports whether e denotes a reference. An expression with no type is
// not judgeable and counts as one, so an unrelated gap in type information
// cannot make a real deref disappear.
func isRef(e ir.Expr) bool {
	if e == nil {
		return true
	}
	t := e.ExprType()
	return t == nil || t.Kind == ir.TypeRef
}
