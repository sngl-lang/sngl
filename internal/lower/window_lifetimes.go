package lower

import (
	"fmt"
	"strconv"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// Intrinsic ids of the two calls a window's lifetime lowers to, which a
// platform holding Features.WindowLifetimes answers on the window's handle:
// the first says the window now exists, the second that it no longer does.
// Undeclared, like the node ops: no program can name them.
const (
	WindowMountIntrinsic   = "window.mount"
	WindowUnmountIntrinsic = "window.unmount"
)

// passWindowLifetimes makes a window written under an `if` that reads state
// come and go with it.
//
// A window's position says whether it exists, the way an effect's says whether
// its lifetime is running: one written under `if details` describes one window
// or none. That is exactly what passEffect reconciles, so the pass says it in
// passEffect's terms rather than growing a second reconciler. The window moves
// out of the `if` into pkg.Windows, where every target builds a window from,
// and an effect takes its place whose mount and unmount are the two intrinsics
// above: the effect's settle runs where the condition's state is written, and
// its first settle at start, so the window is mounted exactly while the
// condition holds. The body is built once and outlives what the platform
// shows, which is what lets its updaters run while the window is gone.
//
// Presence records the condition for the effects the window's own body
// holds, which live as long as the window does.
//
// Only an `if` at the root of the package body: a window a component renders
// has been spliced there by now, and one under a `for` is a copy per element,
// which a record per window statement cannot hold. A condition that reads no
// state leaves the window alone -- the optimizer has decided it or it never
// changes. A target without Features.WindowLifetimes is refused at the window,
// rather than the window being emitted as if the `if` were not there.
//
// Just before passEffect, which consumes both the effect and Presence, so no
// pass in between rewrites an expression Presence copied.
var passWindowLifetimes = pass{
	name:    "WindowLifetimes",
	enabled: func(Features) bool { return true },
	apply:   lowerWindowLifetimes,
}

func lowerWindowLifetimes(pkg *ir.Package, caps Features, opts Options) error {
	if pkg == nil {
		return nil
	}
	st := &windowLifetimeState{
		pkg:  pkg,
		caps: caps,
		opts: opts,
		fx:   &effectState{pkg: pkg, reactive: collectReactiveVars(pkg)},
	}
	pkg.Body = st.stmts(pkg.Body, nil)
	// Named once every window has moved, by the index codegen.HostWindows
	// names an unnamed window's record by, so the two cannot pick one name
	// for two windows.
	for i, w := range ir.AllWindows(pkg) {
		for _, h := range st.unnamed {
			if h.win == w {
				h.v.Name = "__win" + strconv.Itoa(i)
				w.ID = h.v.Name
				for _, id := range h.refs {
					id.Name = h.v.Name
				}
			}
		}
	}
	return st.err
}

// unnamedWindow is a handle the pass made for a window nobody named, and the
// references to it, named once the windows are where they will stay.
type unnamedWindow struct {
	win  *ir.Window
	v    *ir.Var
	refs []*ir.Ident
}

type windowLifetimeState struct {
	pkg     *ir.Package
	caps    Features
	opts    Options
	fx      *effectState
	unnamed []*unnamedWindow
	err     error
}

// stmts rewrites one statement list. conds is the `if` chain above it, each
// already negated for an `else`.
func (st *windowLifetimeState) stmts(stmts []ir.Stmt, conds []ir.Expr) []ir.Stmt {
	out := stmts[:0:0]
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.If:
			n.Body = st.stmts(n.Body, append(conds[:len(conds):len(conds)], n.Cond))
			n.Else = st.stmts(n.Else, append(conds[:len(conds):len(conds)], negate(n.Cond)))
		case *ir.NodeInst:
			if ir.IsWindowNode(n) && len(conds) > 0 {
				if e := st.lifetime(n, conds); e != nil {
					out = append(out, e)
					continue
				}
			}
		}
		out = append(out, s)
	}
	return out
}

// lifetime moves w out of the conditions it is under and returns the effect
// that mounts it, or nil when the conditions read no state.
func (st *windowLifetimeState) lifetime(w *ir.Window, conds []ir.Expr) ir.Stmt {
	presence := conds[0]
	for _, c := range conds[1:] {
		presence = &ir.Binary{Type: ir.TypBool, Op: ast.BinAnd, Left: presence, Right: c}
	}
	if len(st.fx.reactiveVarsIn(presence)) == 0 {
		return nil
	}
	if !st.caps.WindowLifetimes {
		if st.err == nil {
			platform := st.opts.Platform
			if platform == "" {
				platform = "this target"
			}
			st.err = fmt.Errorf("%s: a window under an `if` that reads state is created and destroyed with it, which %s cannot do", windowPos(w), platform)
		}
		return nil
	}
	handle := w.Handle
	var unnamed *unnamedWindow
	if handle == nil {
		// Nothing names the window, so it gets a handle only the effect reads.
		handle = &ir.Var{NodeHandle: true, Synthesized: true}
		w.Handle = handle
		unnamed = &unnamedWindow{win: w, v: handle}
		st.unnamed = append(st.unnamed, unnamed)
	}
	w.Presence = deepCloneExpr(presence)
	st.pkg.Windows = append(st.pkg.Windows, w)

	call := func(id string) []ir.Stmt {
		recv := &ir.Ident{Name: handle.Name, Sym: handle, Synthesized: handle.Synthesized}
		if unnamed != nil {
			unnamed.refs = append(unnamed.refs, recv)
		}
		return []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
			Type:     ir.TypVoid,
			Receiver: recv,
			Func:     &ir.Func{Name: id, Intrinsic: id, Return: ir.TypVoid, Purity: ir.PurityMutates, Synthesized: true},
		}}}
	}
	return &ir.NodeInst{
		Name:      "effect",
		Component: windowLifetimeEffect,
		Handlers: []ir.EventHandler{
			{Name: "mount", Func: &ir.Func{Return: ir.TypVoid, Purity: ir.PurityMutates, Block: call(WindowMountIntrinsic)}},
			{Name: "unmount", Func: &ir.Func{Return: ir.TypVoid, Purity: ir.PurityMutates, Block: call(WindowUnmountIntrinsic)}},
		},
	}
}

// windowLifetimeEffect stands for sngl:builtin's `effect` declaration, which
// is all passEffect asks of the node it lowers: the builtin kind.
var windowLifetimeEffect = &ir.Component{Name: "effect", Builtin: ir.BuiltinEffect}

func negate(e ir.Expr) ir.Expr {
	return &ir.Unary{Type: ir.TypBool, Op: ast.UnaryNot, Operand: deepCloneExpr(e)}
}
