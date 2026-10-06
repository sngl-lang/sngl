package lower

import (
	"fmt"
	"slices"
	"strconv"

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
	enabled: func(c Features) bool { return !c.Effects },
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
// one key or none; one inside a `for` describes a key per element; one in that
// `for`'s `else` describes one key when the iterable is empty and none
// otherwise, since that is what `else` means. Reconciling that list against the
// list the running brackets hold is mount and unmount, and it is the same three
// statements for all four positions. That is why
// there is no case here for "an effect inside reactive control flow": the
// position is read off the enclosing `if`/`for` when the settle function is
// built, and never asked about again.
func lowerEffects(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	st := &effectState{pkg: pkg, reactive: collectReactiveVars(pkg), byVar: map[*ir.Var][]*loweredEffect{}}
	for _, o := range ir.Owners(pkg) {
		if err := st.owner(o); err != nil {
			return err
		}
	}
	st.buildGroups()
	st.injectSettles()
	st.colorAsync()
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
	// groups is one settle entry point per scope holding effects, in the
	// order the scopes were walked.
	groups []*effectGroup
}

// effectGroup is every bracket one scope holds, and the single function that
// settles all of them.
//
// One function rather than one per bracket because the order across brackets
// is part of the contract: the interpreter ends every dying lifetime before it
// begins any new one, so calling two per-bracket settles in a row -- which is
// what this pass used to emit -- gives -A,+A,-B,+B where the language says
// -B,-A,+A,+B. A program that hands a resource from one bracket to the next
// held two of it for the length of that window.
type effectGroup struct {
	comp *ir.Component
	fxs  []*loweredEffect
	// settling, pending and steps are the re-entrancy guard: a settle reached
	// from inside a running one records that another pass is owed instead of
	// starting one, and the running pass takes it. The test this replaces was
	// on the callee's *name*, so it saw only the immediate call and one hop
	// through an ordinary helper recursed until the stack gave out.
	settling *ir.Var
	pending  *ir.Var
	steps    *ir.Var
	settle   *ir.Func
}

// maxEffectSettleSteps bounds the settle loop: an effect that rekeys itself
// describes a different tree every pass and never settles.
//
// A pass here is the whole group -- every bracket's down and up -- so the
// number bounds how many times the handlers may make more work for themselves,
// and not how many brackets a scope may hold. That distinction is the one this
// used to get wrong on the other side: interp counted handlers run, so a scope
// with more brackets than the bound was told an effect was rekeying itself.
// interp's maxEffectRestarts is the same question asked per bracket.
//
// Reaching the bound raises, in buildGroups. It used to stop silently, which
// left a program running with brackets that did not describe its tree while the
// interpreter refused that same program.
const maxEffectSettleSteps = 512

// loweredEffect is one `effect` node after lowering: the state that says which
// lifetimes are running, the two handler bodies, and the functions that move
// the one to match what the program now describes.
type loweredEffect struct {
	// comp names the scope the effect was written in; nil is the package. Held rather than the *ir.Owner it came from, because Owners
	// hands out a fresh value per call and two of them for one component must
	// compare equal.
	comp *ir.Component
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
	elem *ir.Type
	// desired is the key list the position describes right now. A scope var
	// rather than a local, because the two halves of a settle are two
	// functions: down computes it and ends every lifetime it no longer names,
	// up begins every one it newly names.
	desired *ir.Var
	mount   *ir.Func
	unmount *ir.Func
	down    *ir.Func
	up      *ir.Func
	// group is the scope's settle entry point, which calls this bracket's two
	// halves in their place among the others.
	group *effectGroup
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
	// loopElse marks the frame as that `for`'s `else` rather than its body.
	// The two are opposite positions over the same iterable -- the body
	// describes a key per element, the else exactly one when there are none --
	// so a frame that recorded only the loop rebuilt the else as the loop and
	// mounted the bracket once per element.
	loopElse bool
}

func (st *effectState) owner(o ir.Owner) error {
	body, err := st.stmts(o.Stmts(), &o, nil)
	if err != nil {
		return err
	}
	*o.Body = body
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
			b, err := st.stmts(n.Body, o, pushFrame(frames, effectFrame{loop: n}))
			if err != nil {
				return nil, err
			}
			e, err := st.stmts(n.Else, o, pushFrame(frames, effectFrame{loop: n, loopElse: true}))
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
	fx := &loweredEffect{comp: o.Comp, elem: ir.TypBool}
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
	fx.desired = &ir.Var{
		Name:        prefix + "_desired",
		Type:        ir.ListOf(fx.elem),
		Init:        &ir.ListLit{Type: ir.ListOf(fx.elem)},
		Synthesized: true,
	}
	st.addVar(o, fx.desired)

	fx.down = st.downFunc(prefix, fx, key, frames)
	st.addFunc(o, fx.down)
	fx.up = st.upFunc(prefix, fx)
	st.addFunc(o, fx.up)
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

// downFunc builds the first half of a settle: what the position describes now,
// and the end of every lifetime that description no longer names.
//
//	func __effectN_down() {
//	    __effectN_desired = []
//	    <the if/for chain> { __effectN_desired.push(<on>) }
//	    for __i, _ = __effectN_live {                    // teardown, newest first
//	        __j = __effectN_live.length() - 1 - __i
//	        if __j >= __effectN_desired.length()               { __effectN_key = __effectN_live[__j]; __effectN_unmount() }
//	        else if __effectN_desired[__j] != __effectN_live[__j] { ... same ... }
//	    }
//	}
//
// The index is the identity: two renders describe the same bracket at the same
// position, and `on` then says whether that bracket's lifetime continues. This
// is the mounted path the interpreter keys on, written as a list because a
// position under a `for` has as many as the iteration has elements.
//
// The comparison is rebuildDiffers', not a bare `!=`: a struct key compares
// field by field so that it means the same thing on every target. The checker
// has already refused an `on` no walk can compare.
func (st *effectState) downFunc(prefix string, fx *loweredEffect, key ir.Expr, frames []effectFrame) *ir.Func {
	listT := ir.ListOf(fx.elem)
	desired := func() *ir.Ident { return st.varIdent(fx.desired) }

	// The key this position describes. Without an `on` every bracket carries
	// the same value, so only how many there are can differ -- which is
	// exactly what an effect with no key means.
	push := deepCloneExpr(key)
	if push == nil {
		push = &ir.Literal{Type: ir.TypBool, Value: "true"}
	}

	block := []ir.Stmt{
		&ir.Assign{Target: desired(), Op: ast.AssignSet, Value: &ir.ListLit{Type: listT}},
	}
	block = append(block, st.positionBlock(prefix, frames, []ir.Stmt{
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
		decide := []ir.Stmt{&ir.If{
			Cond: &ir.Binary{Type: ir.TypBool, Op: ast.BinGte, Left: j(), Right: callListLength(desired())},
			Body: run,
			Else: []ir.Stmt{&ir.If{
				Cond: rebuildDiffers(
					&ir.Index{Type: fx.elem, Operand: desired(), Idx: j()},
					&ir.Index{Type: fx.elem, Operand: st.varIdent(fx.live), Idx: j()},
					fx.elem,
				),
				Body: cloneStmts(run),
			}},
		}}
		block = append(block, &ir.For{
			Key:      idx.Name,
			KeySym:   idx,
			Value:    "_",
			Iter:     st.varIdent(fx.live),
			ElemType: fx.elem,
			Body: append([]ir.Stmt{
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
			}, decide...),
		})
	}

	return &ir.Func{
		Name:        prefix + "_down",
		Return:      ir.TypVoid,
		Purity:      ir.PurityMutates,
		Synthesized: true,
		Block:       block,
	}
}

// upFunc builds the second half: the beginning of every lifetime the position
// newly names, in the order the tree wrote them, and the record of what is now
// running.
//
//	func __effectN_up() {
//	    for __i, __k = __effectN_desired {
//	        if __i >= __effectN_live.length()  { __effectN_key = __k; __effectN_mount() }
//	        else if __effectN_live[__i] != __k { ... same ... }
//	    }
//	    __effectN_live = __effectN_desired
//	}
func (st *effectState) upFunc(prefix string, fx *loweredEffect) *ir.Func {
	var block []ir.Stmt
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
		decide := []ir.Stmt{&ir.If{
			Cond: &ir.Binary{Type: ir.TypBool, Op: ast.BinGte, Left: idxRef(), Right: callListLength(st.varIdent(fx.live))},
			Body: run,
			Else: []ir.Stmt{&ir.If{
				Cond: rebuildDiffers(
					&ir.Index{Type: fx.elem, Operand: st.varIdent(fx.live), Idx: idxRef()},
					elemRef(),
					fx.elem,
				),
				Body: cloneStmts(run),
			}},
		}}
		block = append(block, &ir.For{
			Key:      idx.Name,
			KeySym:   idx,
			Value:    elem.Name,
			ValueSym: elem,
			Iter:     st.varIdent(fx.desired),
			ElemType: fx.elem,
			Body:     decide,
		})
	}
	block = append(block, &ir.Assign{Target: st.varIdent(fx.live), Op: ast.AssignSet, Value: st.varIdent(fx.desired)})
	return &ir.Func{
		Name:        prefix + "_up",
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
	loop := &ir.For{Key: idx.Name, KeySym: idx, Value: "_", Iter: st.varIdent(fx.live), ElemType: fx.elem}
	if fx.keyVar == nil {
		// The ordinal exists only to index the key out of the live list, so a
		// position with no key has nothing to read it. The loop has to stop
		// binding it too, not just stop using it: a host that declares an
		// index no statement mentions is a compile error in Go.
		run = run[1:]
		loop.Key, loop.KeySym, loop.Value = "", nil, ""
	}
	loop.Body = run
	return &ir.Func{
		Name:        prefix + "_teardown",
		Return:      ir.TypVoid,
		Purity:      ir.PurityMutates,
		Synthesized: true,
		Block: []ir.Stmt{
			loop,
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
func (st *effectState) positionBlock(prefix string, frames []effectFrame, leaf []ir.Stmt) []ir.Stmt {
	for i, f := range slices.Backward(frames) {
		if f.loop != nil && f.loopElse {
			leaf = st.emptyLoopBlock(prefix+"_ran"+strconv.Itoa(i), f.loop, leaf)
			continue
		}
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

// emptyLoopBlock runs leaf exactly when loop's iterable yields nothing, which
// is what an effect in a `for … else` is placed under.
//
//	__effectN_ranK := false
//	for <the loop's iterable> { __effectN_ranK = true }
//	if !__effectN_ranK { <leaf> }
//
// The same three statements passForElse writes, and deliberately spelled the
// same way, because they answer the same question about the same loop.
//
// A walk rather than a length: the iterable may be a map or a pull sequence,
// neither of which has one, and `iter<T>` hands out no count at all. The walk
// binds nothing, so the element does not have to be nameable here either --
// which matters, since an `else` body is the one place a loop's variables are
// out of scope. It carries no `break`, because passForElse has not run yet and
// the escape would be the only one in a block this pass builds.
func (st *effectState) emptyLoopBlock(name string, loop *ir.For, leaf []ir.Stmt) []ir.Stmt {
	sym := &ir.Var{Name: name, Type: ir.TypBool, Synthesized: true}
	ref := func() *ir.Ident {
		return &ir.Ident{Name: name, Type: ir.TypBool, Sym: sym, Synthesized: true}
	}
	return []ir.Stmt{
		&ir.LocalVar{Name: name, Type: ir.TypBool, Sym: sym, Init: &ir.Literal{Type: ir.TypBool, Value: "false"}},
		&ir.For{
			Iter:     deepCloneExpr(loop.Iter),
			ElemType: loop.ElemType,
			AST:      loop.AST,
			Body: []ir.Stmt{&ir.Assign{
				Target: ref(), Op: ast.AssignSet,
				Value: &ir.Literal{Type: ir.TypBool, Value: "true"},
			}},
		},
		&ir.If{Cond: &ir.Unary{Type: ir.TypBool, Op: ast.UnaryNot, Operand: ref()}, Body: leaf},
	}
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
		// An `else` body is outside its own loop's bindings already, so a
		// frame for one contributes none: naming the element there is an
		// unresolved name in the checker, not this diagnostic.
		if f.loop == nil || f.loopElse {
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

// colorAsync re-runs the async fixpoint over the functions this pass built.
//
// The checker coloured the program it was given; the settle chain did not exist
// then. A handler that awaits is lifted into __effectN_mount, which
// __effectN_up calls, which the group settle calls, which the body and every
// injected settle call -- and one link left uncoloured is an `await` in a
// function nothing marked async, which is text no bundler accepts.
func (st *effectState) colorAsync() {
	funcs := st.allOwnerFuncs()
	for {
		changed := false
		for _, fn := range funcs {
			if fn.IsAsync {
				continue
			}
			if ir.BlockHasFuncvarAsyncCall(fn.Block, st.pkg.PointsTo) {
				fn.IsAsync = true
				changed = true
			}
		}
		if !changed {
			return
		}
	}
}

func (st *effectState) allOwnerFuncs() []*ir.Func {
	funcs := append([]*ir.Func(nil), st.pkg.Funcs...)
	for _, c := range st.pkg.Components {
		funcs = append(funcs, c.Funcs...)
	}
	// Plus the func behind every lambda they hold: html's `timer` is
	// `setInterval(func() { tick() }, d)`, so an awaiting tick is spliced into
	// that closure and the arrow itself is what needs `async`.
	seen := make(map[*ir.Func]bool, len(funcs))
	for _, fn := range funcs {
		seen[fn] = true
	}
	roots := make([]any, 0, len(funcs)+len(st.pkg.Components))
	for _, fn := range funcs {
		roots = append(roots, fn.Block)
	}
	for _, o := range ir.Owners(st.pkg) {
		roots = append(roots, o.Stmts())
	}
	for _, root := range roots {
		_ = ir.Walk(root, func(node ir.Node) error {
			var fn *ir.Func
			switch x := node.(type) {
			case *ir.Lambda:
				fn = x.Func
			case *ir.Closure:
				fn = x.Func
			}
			if fn != nil && !seen[fn] {
				seen[fn] = true
				funcs = append(funcs, fn)
			}
			return nil
		})
	}
	return funcs
}

// addFunc and addVar exist only to supply the package to an ir.Owner this
// pass built by hand -- one from Owners already carries it.
func (st *effectState) addFunc(o *ir.Owner, fn *ir.Func) {
	o.Pkg = st.pkg
	o.AddFuncs(fn)
}

func (st *effectState) addVar(o *ir.Owner, v *ir.Var) {
	o.Pkg = st.pkg
	o.AddVars(v)
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

// reactiveVarsIn is the state an expression reads, directly or through what it
// calls. The base walk does not follow Call.Func -- the callee is owned by
// pkg.Funcs -- so the call graph is followed by gatherFuncReads, the same
// answer passReactivity gives a slot whose condition is a derived func.
func (st *effectState) reactiveVarsIn(e ir.Expr) []*ir.Var {
	if e == nil {
		return nil
	}
	var out []*ir.Var
	seen := map[*ir.Var]bool{}
	take := func(v *ir.Var) {
		if !seen[v] && st.reactive[v] {
			seen[v] = true
			out = append(out, v)
		}
	}
	_ = ir.WalkExprs(e, func(x ir.Expr) error {
		switch n := x.(type) {
		case *ir.Ident:
			if v, isVar := n.Sym.(*ir.Var); isVar {
				take(v)
			}
		case *ir.Call:
			reads := map[*ir.Var]bool{}
			gatherFuncReads(n.Func, st.reactive, reads, nil)
			for v := range reads {
				take(v)
			}
		}
		return nil
	})
	return out
}

// finishTeardown builds the exit handlers: one per owner holding an @unmount.
//
// A component built at run time releases its own, from the destroy each target
// calls wherever it tears the instance down -- the teardown destroyHeld also
// hangs a registry's instances on. Everything else was spliced into the page,
// and the page's is the one a platform calls at exit.
func (st *effectState) finishTeardown() error {
	type owner struct {
		comp *ir.Component
	}
	var order []owner
	tears := map[owner][]*loweredEffect{}
	for _, fx := range st.effects {
		if fx.tear == nil {
			continue
		}
		o := owner{fx.comp}
		if _, ok := tears[o]; !ok {
			order = append(order, o)
		}
		tears[o] = append(tears[o], fx)
	}
	// Every page scope -- the package and each window -- shares the one exit
	// handler the platform calls, since a window owns nothing and what it
	// holds is its container's. Reversed across scopes as within one, so the
	// scope set up last is released first.
	var page []ir.Stmt
	// Where the exit handler lives: the page scope's own owner when there is
	// one -- a harness root component's teardown is one of its methods -- and
	// the package, which every window's declarations belong to, when there
	// are several.
	var pageOwner *owner
	for _, o := range slices.Backward(order) {
		// Reverse the order the program wrote them in: an effect set up later
		// may hold something an earlier one handed it, so releasing in
		// acquisition order can release a thing still in use.
		body := make([]ir.Stmt, 0, len(tears[o]))
		for _, fx := range slices.Backward(tears[o]) {
			body = append(body, callOf(fx.tear))
		}
		if o.comp != nil && o.comp.RuntimeInstance {
			if fn := funcNamed(o.comp.Funcs, TeardownFunc); fn != nil {
				fn.Block = append(fn.Block, body...)
				continue
			}
			st.addFunc(&ir.Owner{Comp: o.comp}, &ir.Func{
				Name:   TeardownFunc,
				Return: ir.TypVoid,
				Purity: ir.PurityMutates,
				Block:  body,
			})
			continue
		}
		page = append(page, body...)
		if pageOwner == nil {
			pageOwner = &o
		} else if *pageOwner != o {
			pageOwner = &owner{}
		}
	}
	if page != nil {
		fn := &ir.Func{
			Name:   TeardownFunc,
			Return: ir.TypVoid,
			Purity: ir.PurityMutates,
			Block:  page,
		}
		st.addFunc(&ir.Owner{Comp: pageOwner.comp}, fn)
		st.pkg.Teardown = fn
	}
	return nil
}

// buildGroups gives every scope holding brackets its one settle entry point,
// calls it once at the end of that scope's body, and hands it to the platform
// as a mount.
func (st *effectState) buildGroups() {
	for _, fx := range st.effects {
		g := st.groupFor(fx.comp)
		g.fxs = append(g.fxs, fx)
		fx.group = g
	}
	for i, g := range st.groups {
		prefix := "__effects" + strconv.Itoa(i)
		o := &ir.Owner{Comp: g.comp}
		g.settling = st.flagVar(o, prefix+"_settling")
		g.pending = st.flagVar(o, prefix+"_pending")
		g.steps = &ir.Var{
			Name:        prefix + "_steps",
			Type:        ir.TypInt,
			Init:        intLiteralLit(0),
			Synthesized: true,
		}
		st.addVar(o, g.steps)

		// Every ending lifetime before any beginning one, and the endings
		// newest first: the brackets a scope holds were begun in the order it
		// wrote them, so reverse of that is reverse of when they began. This is
		// what interp's Reconcile does across the whole running set, and what a
		// settle per bracket could not express.
		var pass []ir.Stmt
		pass = append(pass, st.setFlag(g.pending, false))
		pass = append(pass, &ir.Assign{
			Target: st.varIdent(g.steps), Op: ast.AssignSet,
			Value: &ir.Binary{Type: ir.TypInt, Op: ast.BinAdd, Left: st.varIdent(g.steps), Right: intLiteralLit(1)},
		})
		for _, fx := range slices.Backward(g.fxs) {
			pass = append(pass, callOf(fx.down))
		}
		for _, fx := range g.fxs {
			pass = append(pass, callOf(fx.up))
		}
		g.settle = &ir.Func{
			Name:        prefix + "_settle",
			Return:      ir.TypVoid,
			Purity:      ir.PurityMutates,
			Synthesized: true,
			Block: []ir.Stmt{&ir.If{
				Cond: st.varIdent(g.settling),
				Body: []ir.Stmt{st.setFlag(g.pending, true)},
				Else: []ir.Stmt{
					st.setFlag(g.settling, true),
					st.setFlag(g.pending, true),
					&ir.Assign{Target: st.varIdent(g.steps), Op: ast.AssignSet, Value: intLiteralLit(0)},
					&ir.For{
						Iter: &ir.Binary{
							Type: ir.TypBool, Op: ast.BinAnd,
							Left: st.varIdent(g.pending),
							Right: &ir.Binary{
								Type: ir.TypBool, Op: ast.BinLt,
								Left:  st.varIdent(g.steps),
								Right: intLiteralLit(maxEffectSettleSteps),
							},
						},
						Body: pass,
					},
					st.setFlag(g.settling, false),
					// The loop's condition is `pending && steps < bound`, so
					// leaving it with `pending` still set is the bound
					// stopping it rather than the settle finishing. Reported,
					// because the alternative -- what this used to do -- is a
					// program that quietly runs on with brackets that do not
					// describe its tree, while the interpreter refuses the
					// same program outright.
					//
					// No handler scope: a settle is called from the body, from
					// every event handler that writes a key it reads, and from
					// the platform's mount, so there is no one errorBoundary it
					// sits inside the way a recursive instantiation sits inside
					// the body that wrote it. Native is the honest answer, and
					// reporting nowhere is louder than settling nowhere.
					&ir.If{
						Cond: st.varIdent(g.pending),
						Body: []ir.Stmt{raiseStmt(
							fmt.Sprintf("effects did not settle in %d passes; an effect is rekeying itself", maxEffectSettleSteps),
							"effect",
							nil,
						)},
					},
				},
			}},
		}
		st.addFunc(o, g.settle)

		// The first settle runs after the body, not where the node was written.
		// A mount handler writes state, passReactivity turns that into a patch
		// of whatever reads it, and the node that patch names has to have been
		// created -- which at the effect's own position it need not have been.
		switch {
		case g.comp != nil:
			g.comp.Body = append(g.comp.Body, callOf(g.settle))
		default:
			st.pkg.Body = append(st.pkg.Body, callOf(g.settle))
		}
		st.pkg.Mounts = append(st.pkg.Mounts, g.settle)
	}
}

func (st *effectState) flagVar(o *ir.Owner, name string) *ir.Var {
	v := &ir.Var{
		Name:        name,
		Type:        ir.TypBool,
		Init:        &ir.Literal{Type: ir.TypBool, Value: "false"},
		Synthesized: true,
	}
	st.addVar(o, v)
	return v
}

func (st *effectState) setFlag(v *ir.Var, to bool) ir.Stmt {
	return &ir.Assign{
		Target: st.varIdent(v), Op: ast.AssignSet,
		Value: &ir.Literal{Type: ir.TypBool, Value: strconv.FormatBool(to)},
	}
}

func (st *effectState) groupFor(comp *ir.Component) *effectGroup {
	for _, g := range st.groups {
		if g.comp == comp {
			return g
		}
	}
	g := &effectGroup{comp: comp}
	st.groups = append(st.groups, g)
	return g
}

// injectSettles puts a settle call after every write to a cell some position
// reads.
//
// allBlocks rather than a descent of its own: the walk this used to do reached
// an owner's funcs and its view body and nothing else, so a write in a timer
// handler, a var handler, a lifted lambda or a window declared inside a
// component settled nothing at all.
//
// Every block, this pass's own synthesized funcs included. Those used to be
// skipped, on the argument that a mount writing the cell its own key reads
// would re-enter itself -- but the settling/pending flags are what stop
// re-entrancy, and the skip stopped something else: a mount writing ANOTHER
// bracket's key, which is legal and which the interpreter settles to a
// fixpoint. Two effects rekeying each other ran one pass compiled and looped to
// the bound interpreted. The self-rekey the comment described is now a check
// error, so the skip guarded nothing it claimed to and broke what it did not
// mention.
func (st *effectState) injectSettles() {
	if len(st.byVar) == 0 {
		return
	}
	blocks := allBlocks(st.pkg)
	// A nested `if`/`for` body inside an *imperative* block is not one of
	// these: allBlocks descends control flow in a view body and adds each
	// branch, but an imperative block arrives as one pointer. So the recursion
	// below skips a body that is already on the list, and reaches the ones that
	// never were -- `@click { if n > 0 { running = !running } }` settled
	// nothing at all, one brace deeper than the fixture that covers this.
	own := make(map[*[]ir.Stmt]bool, len(blocks))
	for _, b := range blocks {
		own[b] = true
	}
	for _, block := range blocks {
		*block = st.injectInto(*block, own)
	}
}

func (st *effectState) injectInto(stmts []ir.Stmt, own map[*[]ir.Stmt]bool) []ir.Stmt {
	out := make([]ir.Stmt, 0, len(stmts))
	for _, s := range stmts {
		st.injectNested(s, own)
		out = append(out, s)
		for _, g := range st.settledBy(s) {
			out = append(out, callOf(g.settle))
		}
	}
	return out
}

// injectNested descends the control flow a statement holds. A lambda is not
// descended: allBlocks lists every lambda body in the package, so one is
// reached on its own iteration.
func (st *effectState) injectNested(s ir.Stmt, own map[*[]ir.Stmt]bool) {
	var bodies []*[]ir.Stmt
	switch n := s.(type) {
	case *ir.If:
		bodies = []*[]ir.Stmt{&n.Body, &n.Else}
	case *ir.For:
		bodies = []*[]ir.Stmt{&n.Body, &n.Else}
	default:
		return
	}
	for _, b := range bodies {
		if own[b] {
			continue
		}
		*b = st.injectInto(*b, own)
	}
}

// settledBy is the scopes whose brackets a statement may have moved, in a
// stable order.
func (st *effectState) settledBy(s ir.Stmt) []*effectGroup {
	var hit map[*effectGroup]bool
	for _, v := range st.writtenVars(s) {
		for _, fx := range st.byVar[v] {
			if hit == nil {
				hit = map[*effectGroup]bool{}
			}
			hit[fx.group] = true
		}
	}
	if len(hit) == 0 {
		return nil
	}
	var out []*effectGroup
	for _, g := range st.groups {
		if hit[g] {
			out = append(out, g)
		}
	}
	return out
}

// writtenVars is the state a statement mutates.
//
// mutatedVar and mutatingCallReceiver are passReactivity's, which is the point:
// the two passes have to agree on what a write is, and this one used to insist
// on a bare `*ir.Ident` -- so `items.push(x)`, the only form push has, and
// `obj.f = x` and `xs[i] = x` produced brackets that never ran again.
//
// A call to an ordinary function needs nothing here. Its body is a block of its
// own, so the settle is injected at the write inside it and has already run by
// the time the call returns.
func (st *effectState) writtenVars(s ir.Stmt) []*ir.Var {
	var out []*ir.Var
	add := func(e ir.Expr) {
		if v, _ := mutatedVar(st.pkg, st.reactive, e); v != nil {
			out = append(out, v)
		}
	}
	switch n := s.(type) {
	case *ir.Assign:
		add(n.Target)
	case *ir.Toggle:
		add(n.Target)
	case *ir.CallStmt:
		add(mutatingCallReceiver(n.Call))
	}
	return out
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
			if n.Call != nil {
				// The *ir.Call too. A shallow copy shared it, which was latent
				// only because the calls this pass clones take no arguments --
				// the promise the copy exists to keep is that no later pass
				// rewriting one branch in place reaches the other.
				call := *n.Call
				call.Receiver = deepCloneExpr(n.Call.Receiver)
				call.Args = make([]ir.CallArg, len(n.Call.Args))
				for i, a := range n.Call.Args {
					a.Value = deepCloneExpr(a.Value)
					call.Args[i] = a
				}
				clone.Call = &call
			}
			out = append(out, &clone)
		default:
			out = append(out, s)
		}
	}
	return out
}

// callListPush builds a `list.push(dst, v)` call statement. push mutates its
// receiver and returns nothing, so the append is the call and not an assignment
// back to the list. Shared with passSlotInstances, which describes what a
// render holds the same way.
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
