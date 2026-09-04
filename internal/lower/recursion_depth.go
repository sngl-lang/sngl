package lower

import (
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// MaxRecursionDepth counts nested instances of one recursive component: the
// number of times a component in a call cycle may be built from inside another
// instance of that same cycle before the next one raises instead.
//
// 512 because the bound is a backstop against a program whose base case never
// arrives, not a budget a real tree spends: every target builds these nested
// instances on its own call stack -- a JS factory calling a JS factory, a Go
// constructor calling a Go constructor -- and a browser gives that stack a few
// thousand frames, several of which each level costs. A tree deeper than 512 is
// a tree no host will render; a tree shallower than 512 was never at risk.
const MaxRecursionDepth = 512

// recursionDepthProp is the prop each cycle member carries its own nesting
// depth in. A prop rather than a counter the target holds somewhere, because a
// prop is the one channel every platform already routes into an instance --
// the bound then costs no backend a case, and needs nothing global to reset.
const recursionDepthProp = "__depth"

// passRecursionDepth bounds a component that instantiates itself.
//
// A recursive component is the one thing the inliner cannot flatten, so its
// instantiation happens while the program runs and its base case is the
// program's to get right. Without a bound, one that never arrives is not a
// wrong picture but a dead host: each level is a real frame on the target's
// own call stack, so the page blows the JS stack or the binary overflows Go's.
//
// The bound is emitted here, as ordinary IR, precisely so no backend sees it:
// a prop carries the depth in, an `if` at each recursive instantiation site
// asks whether the next level is one too many, and the raise is the same
// `error.raise` a program writes for itself.
//
// Always on, and not a capability: it answers for every target, the way
// IndexedIter, ForElse and CSE do. A package with no cycle in it is left
// exactly as it was.
var passRecursionDepth = pass{
	name:    "RecursionDepth",
	enabled: func(Caps) bool { return true },
	apply:   lowerRecursionDepth,
}

func lowerRecursionDepth(pkg *ir.Package, _ Caps, opts Options) error {
	if pkg == nil {
		return nil
	}
	cycles := findRecursiveCycles(pkg, opts)
	if len(cycles) == 0 {
		return nil
	}
	// Iterated off pkg.Components rather than the cycle set, because a map's
	// order is not one and the props a component grows would land in it.
	depth := map[*ir.Component]*ir.Param{}
	for _, c := range pkg.Components {
		if cycles[c] {
			depth[c] = addDepthProp(c)
		}
	}
	for _, c := range pkg.Components {
		if !cycles[c] {
			continue
		}
		st := &recursionState{cycles: cycles, depth: depth, self: depth[c], bounded: map[*ir.NodeInst]bool{}}
		st.guard(c, nil)
	}
	return nil
}

// addDepthProp gives c the prop its own nesting depth arrives in.
//
// An ordinary prop, deliberately, and not #[construct]: the mark would be the
// truer description -- a depth is read while the instance is built and never
// changes -- but it also means no cell, and the guard is read from a *slot
// render*, which on a struct-shaped target is a method. A parameter of the
// constructor is not in scope there, so the check compiled to `undefined:
// __depth` on fyne. passComponentProps promotes this to a field like any other
// prop, which is what puts it where the guard can read it.
func addDepthProp(c *ir.Component) *ir.Param {
	sym := &ir.Param{Name: recursionDepthProp, Type: ir.TypInt}
	c.Props = append(c.Props, &ir.Prop{
		Name:    recursionDepthProp,
		Type:    ir.TypInt,
		Default: intLiteralLit(0),
		Sym:     sym,
	})
	return sym
}

type recursionState struct {
	cycles map[*ir.Component]bool
	depth  map[*ir.Component]*ir.Param
	// self is the depth parameter of the component whose body is being walked:
	// what this level is, and so what the next one would be.
	self *ir.Param
	// bounded is the sites already wrapped. The walk meets each one twice,
	// because the `if` that replaces a node keeps that node in its else and
	// the walk descends into the replacement it was handed.
	bounded map[*ir.NodeInst]bool
}

// guard rewrites every instantiation of a cycle member found under root into
// the bounded form:
//
//	if __depth >= MaxRecursionDepth {
//	    error.raise("…", "recursion")
//	} else {
//	    <node>(…, __depth = __depth + 1)
//	}
//
// scope is the error handlers the site sits inside, innermost first -- the same
// stack the checker's own walk keeps, so a boundary written in the component's
// body catches the bound exactly as it catches a raise the program wrote there.
// With none in scope the raise is native, which is a recursive component with
// no boundary above it taking the window down: the bound reports, and reporting
// nowhere is louder than rendering a truncated tree nobody asked for.
//
// A boundary is the one thing this needs a nesting stack for, and ir.Rewrite
// is pre-order with no exit hook to pop one -- so a boundary's children are a
// walk of their own with the extended scope and the outer walk stops there.
// That is the only reason the recursion here is not ir.Rewrite's own; every
// other body is reached by it, which is what fixes a recursive instantiation
// written inside slot content the call site supplies. The hand-written descent
// this replaces stopped at NodeInst.Slots, so such a site got no bound at all
// while findRecursiveCycles -- also blind to slots at the time -- reported no
// cycle to bound.
func (st *recursionState) guard(root any, scope []*ir.EventHandler) {
	_ = ir.Rewrite(root, func(node ir.Node) (ir.Node, error) {
		switch n := node.(type) {
		case *ir.ErrorBoundary:
			inner := scope
			if n.Handler != nil {
				// The boundary's own @error handler keeps the outer scope: a
				// raise inside it is not something the boundary it belongs to
				// catches.
				st.guard(n.Handler.Func, scope)
				inner = append([]*ir.EventHandler{n.Handler}, scope...)
			}
			st.guard(n.Children, inner)
			return node, ir.SkipDir
		case *ir.NodeInst:
			if st.bounded[n] || st.depth[n.Component] == nil {
				return node, nil
			}
			st.bounded[n] = true
			return st.bound(n, scope), nil
		}
		return node, nil
	})
}

// bound is the if/else one instantiation becomes. The node keeps its place in
// the else, so a build that never reaches the bound renders exactly the tree it
// rendered before this pass existed.
func (st *recursionState) bound(n *ir.NodeInst, scope []*ir.EventHandler) ir.Stmt {
	n.Props = append(n.Props, ir.Arg{
		Name:  recursionDepthProp,
		Value: &ir.Binary{Type: ir.TypInt, Op: ast.BinAdd, Left: st.here(), Right: intLiteralLit(1)},
	})
	return &ir.If{
		Cond: &ir.Binary{
			Type:  ir.TypBool,
			Op:    ast.BinGte,
			Left:  st.here(),
			Right: intLiteralLit(MaxRecursionDepth),
		},
		Body: []ir.Stmt{raiseStmt(
			fmt.Sprintf("%s: recursion exceeded %d nested instances", n.Name, MaxRecursionDepth),
			scope,
		)},
		Else: []ir.Stmt{n},
	}
}

// here reads the depth of the instance whose body this is. A fresh Ident per
// use: two sites sharing one would have any later pass that rewrites an
// expression in place rewrite both.
func (st *recursionState) here() ir.Expr {
	return &ir.Ident{Name: recursionDepthProp, Type: ir.TypInt, Sym: st.self, Synthesized: true}
}

// raiseStmt is `error.raise(msg, "recursion")`, resolved against scope.
//
// The func is synthesized rather than looked up: `error.raise` is declared in
// sngl:app, which a program bounded by this pass need not have imported, and
// ir.IsErrorRaiseFunc identifies it by the receiver+name pair for exactly that
// reason.
func raiseStmt(msg string, scope []*ir.EventHandler) ir.Stmt {
	call := &ir.Call{
		Type: ir.TypVoid,
		Func: &ir.Func{
			Name:     "raise",
			Pkg:      "sngl:app",
			Receiver: "error",
			Params: []*ir.Param{
				{Name: "message", Type: ir.TypString},
				{Name: "kind", Type: ir.TypString},
			},
			Intrinsic: "error.raise",
			Purity:    ir.PurityPure,
			Stdlib:    true,
		},
		Args: []ir.CallArg{
			{Name: "message", Value: &ir.Literal{Type: ir.TypString, Value: msg}},
			{Name: "kind", Value: &ir.Literal{Type: ir.TypString, Value: "recursion"}},
		},
		ErrorMode: ir.ErrorPropagateNative,
	}
	if len(scope) > 0 {
		call.ErrorMode = ir.ErrorInvokeAndTerminate
		call.ResolvedHandler = scope[0]
	}
	return &ir.CallStmt{Call: call}
}
