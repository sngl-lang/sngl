package lower

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// passEffect turns each `effect` node into the calls that run its bracket.
//
// Placed before passReactivity, which is the whole reason the box a fetch
// answers into needs no machinery of its own: a mount handler that writes state
// becomes an ordinary assignment in the owner's body, so the ordinary updater
// injection sees it and patches whatever reads it. Lowered after that pass the
// assignment would be invisible and every backend would need telling that the
// value had changed.
//
// Skipped by a platform that emits the node itself. Compose brackets a keyed
// lifetime natively, and DisposableEffect says what an effect means more
// directly than the calls this pass builds; Features.Effects is how it asks.
var passEffect = pass{
	name:    "Effect",
	enabled: func(c Caps) bool { return c.NoEffects },
	apply:   lowerEffects,
}

// TeardownFunc is the name of the handler a platform calls when the program is
// going away. Nothing guarantees it runs -- a killed process and a closed tab
// both skip it -- so what belongs in it is what a healthy exit should release,
// never what correctness depends on.
const TeardownFunc = "__snglTeardown"

// lowerEffects replaces every `effect` node with a settle function.
//
// The node leaves the tree entirely. An effect renders nothing, so the only
// thing its position gave it was a lifetime -- and a lifetime is expressible as
// data: the list of keys the position describes right now. One `effect` written
// at component top level describes one key always; one inside an `if` describes
// one key or none; one inside a `for` describes a key per element. Reconciling
// that list against the list the running brackets hold is mount and unmount,
// and it is the same three statements for all three positions. That is why
// there is no case here for "an effect inside reactive control flow": the
// position is read off the enclosing `if`/`for` when the settle function is
// built, and never asked about again.
func lowerEffects(pkg *ir.Package, _ Caps, _ Options) error {
	if pkg == nil {
		return nil
	}
	st := &effectState{pkg: pkg, reactive: collectReactiveVars(pkg), byVar: map[*ir.Var][]*loweredEffect{}}
	for _, o := range ir.Owners(pkg) {
		if err := st.owner(o); err != nil {
			return err
		}
	}
	st.injectSettles()
	return st.finishTeardown()
}

type effectState struct {
	pkg      *ir.Package
	reactive map[*ir.Var]bool
	counter  int
	// effects is every bracket the program placed, in the order it wrote
	// them. Teardown runs in reverse of it.
	effects []*loweredEffect
	// byVar is the settle each state var triggers: writing it may change the
	// list of keys some position describes.
	byVar map[*ir.Var][]*loweredEffect
}

// loweredEffect is one `effect` node after lowering: the state that says which
// lifetimes are running, the two handler bodies, and the functions that move
// the one to match what the program now describes.
type loweredEffect struct {
	// comp and win name the scope the effect was written in; both nil is the
	// package. Held rather than the *ir.Owner it came from, because Owners
	// hands out a fresh value per call and two of them for one component must
	// compare equal.
	comp *ir.Component
	win  *ir.Window
	// live is `list<T>`: the key each running lifetime is keyed on, in mount
	// order. Its length is how many brackets this position currently holds.
	live *ir.Var
	// keyVar holds the key of the lifetime a handler is being run for, or is
	// nil where the call site wrote no `on`. Set immediately before each call,
	// which is what lets a handler be passed the key ITS OWN lifetime had:
	// teardown needs the outgoing value, and by then `on` reads the next one.
	keyVar *ir.Var
	// elem is live's element type -- the `on` expression's type, or bool where
	// there is no `on` and only presence distinguishes two states.
	elem    *ir.Type
	mount   *ir.Func
	unmount *ir.Func
	settle  *ir.Func
	// tear unmounts everything still running, for the program's way out. Nil
	// when the bracket has no `@unmount` to run.
	tear *ir.Func
}

// effectFrame is one enclosing `if` or `for` between the owner's body and the
// effect. The chain of them is the position, and the settle function rebuilds
// it around a single push to say which keys that position describes.
type effectFrame struct {
	// cond is the condition of an enclosing `if`, with neg set when the effect
	// sits in its `else`.
	cond ir.Expr
	neg  bool
	// loop is the enclosing `for`, or nil when this frame is an `if`.
	loop *ir.For
}

func (st *effectState) owner(o ir.Owner) error {
	body, err := st.stmts(o.Stmts, &o, nil)
	if err != nil {
		return err
	}
	// The first settle runs after the body, not where the node was written.
	// A mount handler writes state, passReactivity turns that into a patch of
	// whatever reads it, and the node that patch names has to have been
	// created -- which at the effect's own position it need not have been.
	for _, fx := range st.effects {
		if fx.comp == o.Comp && fx.win == o.Win {
			body = append(body, callOf(fx.settle))
		}
	}
	switch {
	case o.Comp != nil:
		o.Comp.Body = body
	case o.Win != nil:
		o.Win.Body = body
	default:
		st.pkg.Body = body
	}
	return nil
}

// stmts rewrites one statement list, dropping every effect in it and recording
// the position it was written at.
//
// frames is that position: the `if`/`for` chain from the owner's body down to
// here. An `if` or `for` left with nothing in it is dropped, because it only
// ever held the effect and passReactivity would otherwise synthesize a slot to
// re-render an empty body.
func (st *effectState) stmts(stmts []ir.Stmt, o *ir.Owner, frames []effectFrame) ([]ir.Stmt, error) {
	out := make([]ir.Stmt, 0, len(stmts))
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if isEffectNode(n) {
				if err := st.lowerOne(n, o, frames); err != nil {
					return nil, err
				}
				continue
			}
			kids, err := st.stmts(n.Children, o, frames)
			if err != nil {
				return nil, err
			}
			n.Children = kids
			out = append(out, n)
		case *ir.If:
			b, err := st.stmts(n.Body, o, pushFrame(frames, effectFrame{cond: n.Cond}))
			if err != nil {
				return nil, err
			}
			e, err := st.stmts(n.Else, o, pushFrame(frames, effectFrame{cond: n.Cond, neg: true}))
			if err != nil {
				return nil, err
			}
			n.Body, n.Else = b, e
			if len(n.Body) == 0 && len(n.Else) == 0 {
				continue
			}
			out = append(out, n)
		case *ir.For:
			inner := pushFrame(frames, effectFrame{loop: n})
			b, err := st.stmts(n.Body, o, inner)
			if err != nil {
				return nil, err
			}
			e, err := st.stmts(n.Else, o, inner)
			if err != nil {
				return nil, err
			}
			n.Body, n.Else = b, e
			if len(n.Body) == 0 && len(n.Else) == 0 {
				continue
			}
			out = append(out, n)
		case *ir.SlotInst:
			kids, err := st.stmts(n.Children, o, frames)
			if err != nil {
				return nil, err
			}
			n.Children = kids
			out = append(out, n)
		case *ir.ErrorBoundary:
			kids, err := st.stmts(n.Children, o, frames)
			if err != nil {
				return nil, err
			}
			n.Children = kids
			out = append(out, n)
		case *ir.Window:
			// A window written in a component body is a statement, not one of
			// pkg.Windows, so the owner walking it is that component -- which
			// is also where passReactivity puts the slot state for anything
			// inside it. Without this case an effect in such a window reached
			// no pass at all and every backend emitted `CreateNode("effect")`.
			// It adds no frame: a window's body is not conditional.
			body, err := st.stmts(n.Body, o, frames)
			if err != nil {
				return nil, err
			}
			n.Body = body
			out = append(out, n)
		default:
			out = append(out, s)
		}
	}
	return out, nil
}

// lowerOne replaces one effect with the state and the functions that run it.
//
// The handler bodies become funcs rather than being spliced, because a bracket
// has more than one call site -- the settle, and the program's way out -- and
// inlining would copy the body to each.
func (st *effectState) lowerOne(n *ir.NodeInst, o *ir.Owner, frames []effectFrame) error {
	prefix := "__effect" + strconv.Itoa(st.counter)
	st.counter++

	key := effectKeyExpr(n)
	fx := &loweredEffect{comp: o.Comp, win: o.Win, elem: ir.TypBool}
	if key != nil {
		fx.elem = key.ExprType()
		fx.keyVar = &ir.Var{
			Name:        prefix + "_key",
			Type:        fx.elem,
			Init:        ir.ZeroExpr(fx.elem),
			Synthesized: true,
		}
	}
	if err := st.checkHandlerScope(n, frames); err != nil {
		return err
	}
	fx.mount = st.handlerFunc(n, "mount", prefix+"_mount", o, fx)
	fx.unmount = st.handlerFunc(n, "unmount", prefix+"_unmount", o, fx)
	if fx.mount == nil && fx.unmount == nil {
		return nil
	}
	if fx.keyVar != nil {
		st.addVar(o, fx.keyVar)
	}
	fx.live = &ir.Var{
		Name:        prefix + "_live",
		Type:        ir.ListOf(fx.elem),
		Init:        &ir.ListLit{Type: ir.ListOf(fx.elem)},
		Synthesized: true,
	}
	st.addVar(o, fx.live)

	fx.settle = st.settleFunc(prefix, fx, key, frames)
	st.addFunc(o, fx.settle)
	if fx.unmount != nil {
		fx.tear = st.tearFunc(prefix, fx)
		st.addFunc(o, fx.tear)
	}
	st.effects = append(st.effects, fx)

	// A write to any state the position reads may change the list of keys it
	// describes -- the `on` expression's own dependencies, and those of every
	// condition and iteration between it and the body. A position that reads
	// nothing reactive describes one list forever.
	for _, v := range st.positionVars(key, frames) {
		st.byVar[v] = append(st.byVar[v], fx)
	}
	return nil
}

// settleFunc builds the function that moves the running brackets to the ones
// the position now describes.
//
//	func __effectN_settle() {
//	    __desired list<T> = []
//	    <the if/for chain> { __desired.push(<on>) }
//	    for __i, _ = __effectN_live {                    // teardown, newest first
//	        __j = __effectN_live.length() - 1 - __i
//	        if __j >= __desired.length()          { __effectN_key = __effectN_live[__j]; __effectN_unmount() }
//	        else if __desired[__j] != __effectN_live[__j] { ... same ... }
//	    }
//	    for __i, __k = __desired {                       // setup, in written order
//	        if __i >= __effectN_live.length()     { __effectN_key = __k; __effectN_mount() }
//	        else if __effectN_live[__i] != __k    { ... same ... }
//	    }
//	    __effectN_live = __desired
//	}
//
// The index is the identity: two renders describe the same bracket at the same
// position, and `on` then says whether that bracket's lifetime continues. This
// is the mounted path the interpreter keys on, written as a list because a
// position under a `for` has as many as the iteration has elements.
//
// Teardown comes first, and newest first within it. An effect going away holds
// something the program has to give back, and setting up the next lifetime
// before ending the previous is how a program ends up holding two of whatever
// it was.
func (st *effectState) settleFunc(prefix string, fx *loweredEffect, key ir.Expr, frames []effectFrame) *ir.Func {
	listT := ir.ListOf(fx.elem)
	desiredSym := &ir.Var{Name: prefix + "_desired", Type: listT, Synthesized: true}
	desired := func() *ir.Ident {
		return &ir.Ident{Name: desiredSym.Name, Type: listT, Sym: desiredSym, Synthesized: true}
	}

	// The key this position describes. Without an `on` every bracket carries
	// the same value, so only how many there are can differ -- which is
	// exactly what an effect with no key means.
	push := deepCloneExpr(key)
	if push == nil {
		push = &ir.Literal{Type: ir.TypBool, Value: "true"}
	}

	block := []ir.Stmt{
		&ir.LocalVar{Name: desiredSym.Name, Type: listT, Init: &ir.ListLit{Type: listT}, Sym: desiredSym},
	}
	block = append(block, st.positionBlock(frames, []ir.Stmt{
		callListPush(desired(), push, fx.elem),
	})...)

	if fx.unmount != nil {
		idx := &ir.LoopVar{Name: prefix + "_i", Type: ir.TypInt}
		jSym := &ir.Var{Name: prefix + "_j", Type: ir.TypInt, Synthesized: true}
		j := func() *ir.Ident {
			return &ir.Ident{Name: jSym.Name, Type: ir.TypInt, Sym: jSym, Synthesized: true}
		}
		run := []ir.Stmt{
			&ir.Assign{Target: st.varIdent(fx.keyVar), Op: ast.AssignSet, Value: &ir.Index{Type: fx.elem, Operand: st.varIdent(fx.live), Idx: j()}},
			callOf(fx.unmount),
		}
		if fx.keyVar == nil {
			run = run[1:]
		}
		block = append(block, &ir.For{
			Key:      idx.Name,
			KeySym:   idx,
			Value:    "_",
			Iter:     st.varIdent(fx.live),
			ElemType: fx.elem,
			Body: []ir.Stmt{
				// Mirrored, because the iteration goes forwards and teardown
				// has to go backwards.
				&ir.LocalVar{Name: jSym.Name, Type: ir.TypInt, Sym: jSym, Init: &ir.Binary{
					Type: ir.TypInt, Op: ast.BinSub,
					Left: &ir.Binary{
						Type: ir.TypInt, Op: ast.BinSub,
						Left:  callListLength(st.varIdent(fx.live)),
						Right: intLiteralLit(1),
					},
					Right: &ir.Ident{Name: idx.Name, Type: ir.TypInt, Sym: idx, Synthesized: true},
				}},
				&ir.If{
					Cond: &ir.Binary{Type: ir.TypBool, Op: ast.BinGte, Left: j(), Right: callListLength(desired())},
					Body: run,
					Else: []ir.Stmt{&ir.If{
						Cond: &ir.Binary{
							Type: ir.TypBool, Op: ast.BinNeq,
							Left:  &ir.Index{Type: fx.elem, Operand: desired(), Idx: j()},
							Right: &ir.Index{Type: fx.elem, Operand: st.varIdent(fx.live), Idx: j()},
						},
						Body: cloneStmts(run),
					}},
				},
			},
		})
	}

	if fx.mount != nil {
		idx := &ir.LoopVar{Name: prefix + "_i", Type: ir.TypInt}
		elem := &ir.LoopVar{Name: prefix + "_k", Type: fx.elem}
		elemRef := func() *ir.Ident {
			return &ir.Ident{Name: elem.Name, Type: fx.elem, Sym: elem, Synthesized: true}
		}
		run := []ir.Stmt{
			&ir.Assign{Target: st.varIdent(fx.keyVar), Op: ast.AssignSet, Value: elemRef()},
			callOf(fx.mount),
		}
		if fx.keyVar == nil {
			run = run[1:]
		}
		idxRef := func() *ir.Ident {
			return &ir.Ident{Name: idx.Name, Type: ir.TypInt, Sym: idx, Synthesized: true}
		}
		block = append(block, &ir.For{
			Key:      idx.Name,
			KeySym:   idx,
			Value:    elem.Name,
			ValueSym: elem,
			Iter:     desired(),
			ElemType: fx.elem,
			Body: []ir.Stmt{&ir.If{
				Cond: &ir.Binary{Type: ir.TypBool, Op: ast.BinGte, Left: idxRef(), Right: callListLength(st.varIdent(fx.live))},
				Body: run,
				Else: []ir.Stmt{&ir.If{
					Cond: &ir.Binary{
						Type: ir.TypBool, Op: ast.BinNeq,
						Left:  &ir.Index{Type: fx.elem, Operand: st.varIdent(fx.live), Idx: idxRef()},
						Right: elemRef(),
					},
					Body: cloneStmts(run),
				}},
			}},
		})
	}

	block = append(block, &ir.Assign{Target: st.varIdent(fx.live), Op: ast.AssignSet, Value: desired()})
	return &ir.Func{
		Name:        prefix + "_settle",
		Return:      ir.TypVoid,
		Purity:      ir.PurityMutates,
		Synthesized: true,
		Block:       block,
	}
}

// tearFunc ends every lifetime this position still holds, newest first. Only
// built when there is an `@unmount` to run.
func (st *effectState) tearFunc(prefix string, fx *loweredEffect) *ir.Func {
	idx := &ir.LoopVar{Name: prefix + "_ti", Type: ir.TypInt}
	run := []ir.Stmt{
		&ir.Assign{Target: st.varIdent(fx.keyVar), Op: ast.AssignSet, Value: &ir.Index{
			Type:    fx.elem,
			Operand: st.varIdent(fx.live),
			Idx: &ir.Binary{
				Type: ir.TypInt, Op: ast.BinSub,
				Left: &ir.Binary{
					Type: ir.TypInt, Op: ast.BinSub,
					Left:  callListLength(st.varIdent(fx.live)),
					Right: intLiteralLit(1),
				},
				Right: &ir.Ident{Name: idx.Name, Type: ir.TypInt, Sym: idx, Synthesized: true},
			},
		}},
		callOf(fx.unmount),
	}
	if fx.keyVar == nil {
		run = run[1:]
	}
	return &ir.Func{
		Name:        prefix + "_teardown",
		Return:      ir.TypVoid,
		Purity:      ir.PurityMutates,
		Synthesized: true,
		Block: []ir.Stmt{
			&ir.For{Key: idx.Name, KeySym: idx, Value: "_", Iter: st.varIdent(fx.live), ElemType: fx.elem, Body: run},
			&ir.Assign{Target: st.varIdent(fx.live), Op: ast.AssignSet, Value: &ir.ListLit{Type: ir.ListOf(fx.elem)}},
		},
	}
}

// positionBlock rebuilds the `if`/`for` chain the effect was written under,
// around leaf. What comes out says which keys the position describes, in the
// order the tree writes them.
//
// The loops reuse the original loop's variable symbols rather than fresh ones,
// so the `on` expression cloned into the leaf resolves to the element this
// iteration binds.
func (st *effectState) positionBlock(frames []effectFrame, leaf []ir.Stmt) []ir.Stmt {
	for _, f := range slices.Backward(frames) {

		if f.loop != nil {
			leaf = []ir.Stmt{&ir.For{
				Key:      f.loop.Key,
				Value:    f.loop.Value,
				KeySym:   f.loop.KeySym,
				ValueSym: f.loop.ValueSym,
				Iter:     deepCloneExpr(f.loop.Iter),
				ElemType: f.loop.ElemType,
				AST:      f.loop.AST,
				Body:     leaf,
			}}
			continue
		}
		cond := deepCloneExpr(f.cond)
		if f.neg {
			cond = &ir.Unary{Type: ir.TypBool, Op: ast.UnaryNot, Operand: cond}
		}
		leaf = []ir.Stmt{&ir.If{Cond: cond, Body: leaf}}
	}
	return leaf
}

// checkHandlerScope refuses a handler body that reads a loop variable of an
// enclosing `for`.
//
// The bracket's handlers run from the settle, outside that loop, and the only
// thing carried across is the key -- which is what `on` is for. Reading the
// element directly would compile to a name nothing declares, so it is a
// diagnostic rather than a lowering.
func (st *effectState) checkHandlerScope(n *ir.NodeInst, frames []effectFrame) error {
	loopVars := map[*ir.LoopVar]string{}
	for _, f := range frames {
		if f.loop == nil {
			continue
		}
		if f.loop.KeySym != nil {
			loopVars[f.loop.KeySym] = f.loop.Key
		}
		if f.loop.ValueSym != nil {
			loopVars[f.loop.ValueSym] = f.loop.Value
		}
	}
	if len(loopVars) == 0 {
		return nil
	}
	for _, h := range n.Handlers {
		if h.Func == nil {
			continue
		}
		var found string
		for _, s := range h.Func.Block {
			_ = ir.WalkExprs(s, func(e ir.Expr) error {
				if id, isIdent := e.(*ir.Ident); isIdent {
					if lv, isLoop := id.Sym.(*ir.LoopVar); isLoop && found == "" {
						if name, enclosing := loopVars[lv]; enclosing {
							found = name
						}
					}
				}
				return nil
			})
		}
		if found != "" {
			return fmt.Errorf("%s: an effect's @%s reads the loop variable %q, which its handler does not run inside; pass it through `on` and take it as the handler's parameter", nodePos(n), h.Name, found)
		}
	}
	return nil
}

// handlerFunc lifts one side of the bracket into a func on the owner, or nil
// when the call site wrote none.
//
// The declared parameter is substituted away rather than passed. The key var
// already holds the value each call site wants -- the beginning key at setup
// and the ending one at teardown, because the settle assigns it immediately
// before each call -- so a parameter would carry what is already in scope, and
// every backend would have to agree on how to spell the argument.
func (st *effectState) handlerFunc(n *ir.NodeInst, event, name string, o *ir.Owner, fx *loweredEffect) *ir.Func {
	for _, h := range n.Handlers {
		if h.Name != event || h.Func == nil {
			continue
		}
		block := h.Func.Block
		if len(h.Func.Params) > 0 {
			p := h.Func.Params[0]
			// Without an `on` the parameter can only ever hold the default the
			// declaration gives it, so the value is substituted rather than a
			// cell read.
			replacement := func() ir.Expr { return ir.ZeroExpr(p.Type) }
			if fx.keyVar != nil {
				replacement = func() ir.Expr { return st.varIdent(fx.keyVar) }
			}
			block = newExprWalker(func(e ir.Expr) ir.Expr {
				if id, isIdent := e.(*ir.Ident); isIdent && id.Sym == ir.Symbol(p) {
					return replacement()
				}
				return e
			}).stmts(block)
		}
		fn := &ir.Func{
			Name:   name,
			Block:  block,
			Return: ir.TypVoid,
			Purity: ir.PurityMutates,
		}
		st.addFunc(o, fn)
		return fn
	}
	return nil
}

func (st *effectState) addFunc(o *ir.Owner, fn *ir.Func) {
	switch {
	case o.Comp != nil:
		o.Comp.Funcs = append(o.Comp.Funcs, fn)
	case o.Win != nil:
		o.Win.Funcs = append(o.Win.Funcs, fn)
	default:
		st.pkg.Funcs = append(st.pkg.Funcs, fn)
	}
}

func (st *effectState) addVar(o *ir.Owner, v *ir.Var) {
	switch {
	case o.Comp != nil:
		o.Comp.Vars = append(o.Comp.Vars, v)
	case o.Win != nil:
		o.Win.Vars = append(o.Win.Vars, v)
	default:
		st.pkg.Vars = append(st.pkg.Vars, v)
	}
}

// varIdent is a fresh reference to a var this pass synthesized. Fresh rather
// than shared, so two statements never alias one expression node -- passTernary
// lowers such a node in place and leaves the other site naming an undeclared
// temp.
func (st *effectState) varIdent(v *ir.Var) *ir.Ident {
	if v == nil {
		return nil
	}
	return &ir.Ident{Name: v.Name, Type: v.Type, Sym: v, Synthesized: true}
}

// positionVars is the state the position reads: the `on` expression's
// dependencies plus those of every condition and iteration enclosing it.
// Writing any of them may change the list of keys the position describes.
func (st *effectState) positionVars(key ir.Expr, frames []effectFrame) []*ir.Var {
	var out []*ir.Var
	seen := map[*ir.Var]bool{}
	add := func(e ir.Expr) {
		for _, v := range st.reactiveVarsIn(e) {
			if !seen[v] {
				seen[v] = true
				out = append(out, v)
			}
		}
	}
	add(key)
	for _, f := range frames {
		if f.loop != nil {
			add(f.loop.Iter)
			continue
		}
		add(f.cond)
	}
	return out
}

// reactiveVarsIn is the state an expression reads.
func (st *effectState) reactiveVarsIn(e ir.Expr) []*ir.Var {
	if e == nil {
		return nil
	}
	var out []*ir.Var
	seen := map[*ir.Var]bool{}
	_ = ir.WalkExprs(e, func(x ir.Expr) error {
		id, isIdent := x.(*ir.Ident)
		if !isIdent {
			return nil
		}
		v, isVar := id.Sym.(*ir.Var)
		if !isVar || seen[v] || !st.reactive[v] {
			return nil
		}
		seen[v] = true
		out = append(out, v)
		return nil
	})
	return out
}

// finishTeardown builds the exit handler.
func (st *effectState) finishTeardown() error {
	var tears []*loweredEffect
	for _, fx := range st.effects {
		if fx.tear != nil {
			tears = append(tears, fx)
		}
	}
	if len(tears) == 0 {
		return nil
	}
	// Every unmount has to reach one function a platform can call, so they all
	// have to be in one scope -- and after inlining they are, because a
	// component's effects were spliced into the root. A non-inlined owner,
	// which is a recursive component, would need a teardown of its own wired
	// wherever its instances are torn down: the same seam an instance's prop
	// update needs, and not built.
	first := tears[0]
	for _, fx := range tears[1:] {
		if fx.comp != first.comp || fx.win != first.win {
			return fmt.Errorf("Effect: an @unmount in a component that is not inlined has nowhere to hang its teardown")
		}
	}
	// Reverse the order the program wrote them in: an effect set up later may
	// hold something an earlier one handed it, so releasing in acquisition
	// order can release a thing still in use.
	body := make([]ir.Stmt, 0, len(tears))
	for _, fx := range slices.Backward(tears) {
		body = append(body, callOf(fx.tear))
	}
	fn := &ir.Func{
		Name:   TeardownFunc,
		Return: ir.TypVoid,
		Purity: ir.PurityMutates,
		Block:  body,
	}
	st.addFunc(&ir.Owner{Comp: first.comp, Win: first.win}, fn)
	st.pkg.Teardown = fn
	return nil
}

// injectSettles puts a settle call after every write to a cell some position
// reads.
func (st *effectState) injectSettles() {
	if len(st.byVar) == 0 {
		return
	}
	var rewrite func([]ir.Stmt) []ir.Stmt
	rewrite = func(stmts []ir.Stmt) []ir.Stmt {
		out := make([]ir.Stmt, 0, len(stmts))
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.NodeInst:
				n.Children = rewrite(n.Children)
				for i := range n.Handlers {
					if n.Handlers[i].Func != nil {
						n.Handlers[i].Func.Block = rewrite(n.Handlers[i].Func.Block)
					}
				}
			case *ir.If:
				n.Body, n.Else = rewrite(n.Body), rewrite(n.Else)
			case *ir.For:
				n.Body, n.Else = rewrite(n.Body), rewrite(n.Else)
			case *ir.SlotInst:
				n.Children = rewrite(n.Children)
			case *ir.ErrorBoundary:
				n.Children = rewrite(n.Children)
			case *ir.Window:
				n.Body = rewrite(n.Body)
			}
			out = append(out, s)
			v := writtenVar(s)
			if v == nil {
				continue
			}
			for _, fx := range st.byVar[v] {
				out = append(out, callOf(fx.settle))
			}
		}
		return out
	}
	for _, o := range ir.Owners(st.pkg) {
		switch {
		case o.Comp != nil:
			o.Comp.Body = rewrite(o.Comp.Body)
		case o.Win != nil:
			o.Win.Body = rewrite(o.Win.Body)
		default:
			st.pkg.Body = rewrite(st.pkg.Body)
		}
		for _, f := range o.Funcs {
			// Not into a function this pass synthesized: a mount body that
			// writes the cell its own key reads would re-enter itself.
			if f != nil && !isEffectHelper(f) {
				f.Block = rewrite(f.Block)
			}
		}
	}
}

// isEffectHelper reports whether fn is one this pass synthesized.
func isEffectHelper(fn *ir.Func) bool {
	return fn != nil && (strings.HasPrefix(fn.Name, "__effect") || fn.Name == TeardownFunc)
}

// cloneStmts is a fresh copy of a statement list, so two branches of one `if`
// never share the expression nodes a later pass rewrites in place.
func cloneStmts(stmts []ir.Stmt) []ir.Stmt {
	out := make([]ir.Stmt, 0, len(stmts))
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.Assign:
			clone := *n
			clone.Target = deepCloneExpr(n.Target)
			clone.Value = deepCloneExpr(n.Value)
			out = append(out, &clone)
		case *ir.CallStmt:
			clone := *n
			out = append(out, &clone)
		default:
			out = append(out, s)
		}
	}
	return out
}

// callListPush builds a `list.push(dst, v)` call statement. push mutates its
// receiver and returns nothing, so the append is the call and not an
// assignment back to the list -- see renderSlotBody.
func callListPush(dst ir.Expr, v ir.Expr, elem *ir.Type) ir.Stmt {
	var params []*ir.Param
	var ret *ir.Type
	if def := ir.LookupIntrinsic("list.push"); def != nil {
		params, ret = def.Instantiate(elem)
	}
	return &ir.CallStmt{Call: &ir.Call{
		Type: ir.TypVoid,
		Func: &ir.Func{
			Name:      "push",
			Receiver:  "list",
			Intrinsic: "list.push",
			Params:    params,
			Return:    ret,
		},
		Args: []ir.CallArg{{Value: dst}, {Value: v}},
	}}
}

// writtenVar names the state a statement assigns to, or nil.
func writtenVar(s ir.Stmt) *ir.Var {
	var target ir.Expr
	switch n := s.(type) {
	case *ir.Assign:
		target = n.Target
	case *ir.Toggle:
		target = n.Target
	default:
		return nil
	}
	id, isIdent := target.(*ir.Ident)
	if !isIdent {
		return nil
	}
	v, isVar := id.Sym.(*ir.Var)
	if !isVar {
		return nil
	}
	return v
}

func callOf(fn *ir.Func) ir.Stmt {
	return &ir.CallStmt{Call: &ir.Call{Type: ir.TypVoid, Func: fn}}
}

// isEffectNode reports whether a node is a lifetime bracket, by the kind its
// declaration carries.
func isEffectNode(n *ir.NodeInst) bool {
	return n != nil && n.Component != nil && n.Component.Builtin == ir.BuiltinEffect
}

// effectKeyExpr is the `on` argument, or nil when the call site wrote none.
func effectKeyExpr(n *ir.NodeInst) ir.Expr {
	for _, arg := range n.Props {
		if arg.Name == "on" {
			return arg.Value
		}
	}
	return nil
}

// nodePos names a node for a diagnostic.
func nodePos(n *ir.NodeInst) string {
	if p := ir.StmtPos(n); p.IsValid() {
		return p.String()
	}
	return "effect"
}

// pushFrame is frames with f on the end, in storage of its own. Appending in
// place would have the two branches of one `if` share a slot.
func pushFrame(frames []effectFrame, f effectFrame) []effectFrame {
	out := make([]effectFrame, len(frames), len(frames)+1)
	copy(out, frames)
	return append(out, f)
}
