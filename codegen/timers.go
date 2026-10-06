package codegen

import (
	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// ScheduledTimer is one placed timer primitive: the schedule it describes,
// read off the node where it was written.
//
// Collected here rather than lowered into a side list because a schedule is
// only special to the three platforms that emit one. html, fyne and gtk4
// override `timer` with an `effect` and never see a ScheduledTimer at all,
// which is the shape the rest of the compiler should have: a family is
// codegen's to make special.
type ScheduledTimer struct {
	Interval ir.Expr
	// Enabled is the primitive's own gate ANDed with the conditions of every
	// `if` it was written inside.
	Enabled ir.Expr
	Handler *ir.Func
}

// CollectTimers is every schedule a statement list describes, in the order
// the tree holds them.
//
// The gate chain is how a timer keeps the meaning of where it was written on
// a target that has no tree to read at run time: bubbletea arms its schedules
// from Init(), outside the view entirely, so a timer under a branch has to
// carry that branch as an expression. A loop body is not walked: a
// timer there is a schedule per iteration, which stands in the tree as the node
// it is for a target that runs its loops, and is CollectLoopTimers' for one
// that arms schedules outside the tree.
//
// A primitive with no @tick handler describes no schedule and is skipped: the
// interval would arm a deadline with nothing to run.
//
// A window is not descended into. ir.Owners reports one as an owner in its own
// right, so every caller reaches a window's body on its own turn, and
// descending here would collect its schedules a second time.
func CollectTimers(stmts []ir.Stmt) []ScheduledTimer {
	var out []ScheduledTimer
	collectTimers(stmts, nil, &out)
	return out
}

func collectTimers(stmts []ir.Stmt, gates []ir.Expr, out *[]ScheduledTimer) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if ir.IsTimerPrimitive(n.Component) {
				if t, ok := timerOf(n, gates); ok {
					*out = append(*out, t)
				}
				continue
			}
			collectTimers(n.Children, gates, out)
		case *ir.If:
			collectTimers(n.Body, append(gates[:len(gates):len(gates)], n.Cond), out)
			collectTimers(n.Else, append(gates[:len(gates):len(gates)], notExpr(n.Cond)), out)
		case *ir.SlotInst:
			collectTimers(n.Children, gates, out)
		case *ir.ErrorBoundary:
			collectTimers(n.Children, gates, out)
		}
	}
}

// LoopTimer is a schedule written under loops: one per copy of them, which
// CollectTimers leaves standing in the tree for a target that runs its loops.
// Loops are the enclosing view loops, outermost first.
type LoopTimer struct {
	ScheduledTimer
	Loops []*ir.For
}

// CollectLoopTimers is every schedule stmts describe under a loop, for a target
// that arms its schedules outside the tree and so has to keep one per copy of
// the loops around it. The gates are the conditions around it, inside the
// loops and out.
func CollectLoopTimers(stmts []ir.Stmt) []LoopTimer {
	var out []LoopTimer
	collectLoopTimers(stmts, nil, nil, &out)
	return out
}

func collectLoopTimers(stmts []ir.Stmt, gates []ir.Expr, loops []*ir.For, out *[]LoopTimer) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if ir.IsTimerPrimitive(n.Component) {
				if t, ok := timerOf(n, gates); ok && len(loops) > 0 {
					*out = append(*out, LoopTimer{ScheduledTimer: t, Loops: loops})
				}
				continue
			}
			collectLoopTimers(n.Children, gates, loops, out)
		case *ir.For:
			collectLoopTimers(n.Body, gates, append(loops[:len(loops):len(loops)], n), out)
		case *ir.If:
			collectLoopTimers(n.Body, append(gates[:len(gates):len(gates)], n.Cond), loops, out)
			collectLoopTimers(n.Else, append(gates[:len(gates):len(gates)], notExpr(n.Cond)), loops, out)
		case *ir.SlotInst:
			collectLoopTimers(n.Children, gates, loops, out)
		case *ir.ErrorBoundary:
			collectLoopTimers(n.Children, gates, loops, out)
		}
	}
}

func timerOf(n *ir.NodeInst, gates []ir.Expr) (ScheduledTimer, bool) {
	var tick *ir.Func
	for i := range n.Handlers {
		if n.Handlers[i].Name == ir.TimerTickEvent {
			tick = n.Handlers[i].Func
		}
	}
	if tick == nil {
		return ScheduledTimer{}, false
	}
	t := ScheduledTimer{Handler: tick}
	for _, p := range n.Props {
		switch p.Name {
		case ir.TimerIntervalProp:
			t.Interval = p.Value
		case ir.TimerEnabledProp:
			t.Enabled = p.Value
		}
	}
	for _, g := range gates {
		t.Enabled = andExpr(t.Enabled, g)
	}
	return t, true
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

func notExpr(e ir.Expr) ir.Expr {
	if e == nil {
		return nil
	}
	return &ir.Unary{Type: ir.TypBool, Op: ast.UnaryNot, Operand: e}
}

// WithoutSchedules is stmts with the timer primitives dropped: what a platform
// renders, once CollectTimers has read what it schedules.
//
// Shallow, and that is the whole of it -- a nested one is skipped where the
// emitter meets it. What a *list* has to be free of is the ones that would
// otherwise be counted: a container decides between one child and several by
// the length of this slice, and a schedule placed beside a widget is not a
// second widget.
//
// The slice is returned unchanged when it holds none, which is every list in
// every program that never wrote a timer.
func WithoutSchedules(stmts []ir.Stmt) []ir.Stmt {
	keep := true
	for _, s := range stmts {
		if n, ok := s.(*ir.NodeInst); ok && ir.IsTimerPrimitive(n.Component) {
			keep = false
			break
		}
	}
	if keep {
		return stmts
	}
	out := make([]ir.Stmt, 0, len(stmts))
	for _, s := range stmts {
		if n, ok := s.(*ir.NodeInst); ok && ir.IsTimerPrimitive(n.Component) {
			continue
		}
		out = append(out, s)
	}
	return out
}
