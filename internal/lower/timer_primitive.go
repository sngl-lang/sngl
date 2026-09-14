package lower

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// passTimerPrimitive takes each platform's timer primitive out of the tree and
// records the schedule it describes.
//
// Always on, and not capability-gated, because it answers for every target
// that has such a primitive: `timer` is an ordinary component, and a platform
// overriding it with a node it schedules with needs that node taken out again,
// because a schedule is not a widget. Left standing, a declarative target turns
// it into one -- fyne asked what Fyne constructor to call for it and stopped the
// build, which is the honest version of the same failure gtk4 had silently for
// as long as it had timers at all.
//
// A platform may also contribute none, and finding nothing is an answer rather
// than a failure. Three do: html, fyne and gtk4 each override `timer` with an
// `effect` over a start/stop pair of host natives, which is what a schedule
// already is.
//
// What is left is the two an effect cannot describe. An effect's `@mount` hands
// over a *closure*, and a closure is only a schedule where the host may run it
// against live state. bubbletea is Elm: nothing outside Update may touch the
// model, so the tick has to arrive as a message and its body has to be a case
// Update runs -- the period and that body together, which is what this pass
// records and a closure cannot carry. android's LaunchedEffect *is* the
// schedule rather than something a schedule is built from, so its override
// would be describing a bracket in terms of a bracket.
//
// Placed beside passEffect and for the same reasons: after the inliner, so a
// timer written in a child component has arrived in the tree that gets walked;
// before passReactivity, so a tick body that writes state is still an ordinary
// assignment when the updater injection looks for one; and before
// passDeclarative, which is what would flatten the node into a widget.
var passTimerPrimitive = pass{
	name:    "TimerPrimitive",
	enabled: func(Caps) bool { return true },
	apply:   lowerTimerPrimitives,
}

// timerRole is the second half of a platform's timer primitive id. The id is
// namespaced by the platform that emits it -- `bubbletea:Timer`,
// `android:Timer` -- so only the target being built for can have contributed
// one to this tree, and the role is what they have in common.
const timerRole = "Timer"

// IsTimerPrimitive reports whether a component is some platform's timer
// primitive. Read off the declaration's own mark: a registry would be a second
// place to say it and a second place to forget.
func IsTimerPrimitive(c *ir.Component) bool {
	if c == nil || c.Intrinsic == "" {
		return false
	}
	ns, name, ok := strings.Cut(c.Intrinsic, ":")
	return ok && ns != "" && name == timerRole
}

func lowerTimerPrimitives(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	st := &timerPrimState{pkg: pkg}
	for _, o := range ir.Owners(pkg) {
		st.comp, st.win = o.Comp, o.Win
		*o.Body = st.stmts(o.Stmts(), nil)
	}
	return nil
}

type timerPrimState struct {
	pkg *ir.Package
	// comp and win are the declaration the statements being walked belong to;
	// both nil for the package body. A schedule is recorded on its owner
	// because the passes that walk an owner's imperative bodies -- reactivity's
	// updater injection above all -- descend per owner, and a window is such an
	// owner.
	comp *ir.Component
	win  *ir.Window
}

// stmts rewrites one statement list, dropping every timer primitive in it.
//
// gates is the chain of enclosing `if` conditions, which is how a timer keeps
// the meaning of where it was written on a target that has no tree to read at
// run time: a schedule under a branch is armed only while that branch would
// have rendered, so the conditions are ANDed onto the primitive's own gate. A
// loop is not in the chain -- a timer per iteration is a schedule per
// iteration, which no backend's timer runtime expresses, and one written there
// is left where it is for the tree walk to find nothing to do with.
func (st *timerPrimState) stmts(stmts []ir.Stmt, gates []ir.Expr) []ir.Stmt {
	out := make([]ir.Stmt, 0, len(stmts))
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if IsTimerPrimitive(n.Component) {
				st.record(n, gates)
				continue
			}
			n.Children = st.stmts(n.Children, gates)
		case *ir.If:
			n.Body = st.stmts(n.Body, append(gates[:len(gates):len(gates)], n.Cond))
			n.Else = st.stmts(n.Else, append(gates[:len(gates):len(gates)], negate(n.Cond)))
			if len(n.Body) == 0 && len(n.Else) == 0 {
				continue
			}
		case *ir.SlotInst:
			n.Children = st.stmts(n.Children, gates)
		case *ir.ErrorBoundary:
			n.Children = st.stmts(n.Children, gates)
		case *ir.Window:
			n.Body = st.stmts(n.Body, gates)
		}
		out = append(out, s)
	}
	return out
}

func (st *timerPrimState) record(n *ir.NodeInst, gates []ir.Expr) {
	var tick *ir.Func
	for i := range n.Handlers {
		if n.Handlers[i].Name == "tick" {
			tick = n.Handlers[i].Func
		}
	}
	if tick == nil {
		return
	}
	t := &ir.Timer{
		Interval: propOf(n, "interval"),
		Enabled:  propOf(n, "enabled"),
		Handler:  tick,
	}
	for _, g := range gates {
		t.Enabled = andExpr(t.Enabled, g)
	}
	switch {
	case st.comp != nil:
		st.comp.Timers = append(st.comp.Timers, t)
	case st.win != nil:
		st.win.Timers = append(st.win.Timers, t)
	default:
		st.pkg.Timers = append(st.pkg.Timers, t)
	}
}

func propOf(n *ir.NodeInst, name string) ir.Expr {
	for _, p := range n.Props {
		if p.Name == name {
			return p.Value
		}
	}
	return nil
}

func andExpr(a, b ir.Expr) ir.Expr {
	if a == nil {
		return b
	}
	if b == nil {
		return a
	}
	return &ir.Binary{Type: ir.TypBool, Op: ast.BinAnd, Left: a, Right: b}
}

func negate(e ir.Expr) ir.Expr {
	if e == nil {
		return nil
	}
	return &ir.Unary{Type: ir.TypBool, Op: ast.UnaryNot, Operand: e}
}
