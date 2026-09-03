package lower

import (
	"strconv"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// A component instantiated inside a reactive slot survives the slot's
// re-render.
//
// The slot rebuilds its body wholesale, which is right for the nodes it draws
// and wrong for an instance: the instance is where a component's own `var`s
// live, and rebuilding it throws away the state the program is keeping there.
// So the instances are held apart from the nodes, in a list the render
// reconciles rather than rebuilds.
//
// Identity is the position, and the list is what makes that cheap: the
// instance an iteration reuses is the one at the same index, so the lookup is
// a length check and an index -- no hashing, no key to stringify, and the
// shape every target renders as an array access. It is the same reconcile the
// effect settle performs over its list of keys, for the same reason: what a
// position describes now, against what it is already holding.
//
// The consequence of positional identity is the reason `key=` exists: inserting
// in the middle shifts every instance after it, so each keeps the previous
// element's state. A keyed registry answers that and costs a map; this is the
// default because most lists are appended to.
type slotInstance struct {
	// live is what this occurrence currently holds, across renders. An owner
	// var, because it is the one thing about a render that must not be
	// recomputed.
	live *ir.Var
	// next and idx are the render's own bookkeeping: what this pass has
	// described so far, and how many. Locals of the slot function.
	next *ir.Var
	idx  *ir.Var
}

// newSlotInstance allocates the state one instantiation site needs.
func (st *reactivityState) newSlotInstance() *slotInstance {
	n := st.instCounter
	st.instCounter++
	name := func(suffix string) string { return "__inst" + strconv.Itoa(n) + suffix }
	listOfDyn := ir.ListOf(ir.TypDyn)

	inst := &slotInstance{
		live: &ir.Var{Name: name("_live"), Type: listOfDyn, Init: &ir.ListLit{Type: listOfDyn}, Synthesized: true},
		next: &ir.Var{Name: name("_next"), Type: listOfDyn, Synthesized: true},
		idx:  &ir.Var{Name: name("_i"), Type: ir.TypInt, Synthesized: true},
	}
	st.owner.addVar(inst.live)
	return inst
}

func varRef(v *ir.Var) *ir.Ident {
	return &ir.Ident{Name: v.Name, Type: v.Type, Sym: v, Synthesized: true}
}

// open is the bookkeeping a render starts with: an empty list to describe into
// and a cursor at zero.
func (si *slotInstance) open() []ir.Stmt {
	listOfDyn := ir.ListOf(ir.TypDyn)
	return []ir.Stmt{
		&ir.LocalVar{Name: si.next.Name, Type: listOfDyn, Sym: si.next, Init: &ir.ListLit{Type: listOfDyn}},
		&ir.LocalVar{Name: si.idx.Name, Type: ir.TypInt, Sym: si.idx, Init: intLiteralLit(0)},
	}
}

// reuseOrCreate binds id to the instance for the position the cursor is at:
// the one already held, or a new one.
//
//	var <id> dyn = null
//	if list.length(<live>) > <idx> {
//	    <id> = <live>[<idx>]
//	    lower.UpdateComponent(<id>, "<prop>", <expr>)   // one per settable prop
//	} else {
//	    var <id>__new dyn = lower.CreateComponent(...)
//	    <id> = <id>__new
//	}
//	list.push(<next>, <id>)
//	<idx> = <idx> + 1
//
// The create stays a LocalVar initialised by the call, because that is the
// shape codegen.WalkLowered matches on: an assignment carrying the same call
// reaches no arm and is emitted as a call to a function nothing declares.
func (st *reactivityState) reuseOrCreate(si *slotInstance, n *ir.NodeInst, declSt *declarativeState) []ir.Stmt {
	id := n.ID
	cur := &ir.Var{Name: id, Type: ir.TypDyn, Synthesized: true}
	curRef := func() *ir.Ident { return varRef(cur) }

	reuse := []ir.Stmt{&ir.Assign{
		Target: curRef(),
		Op:     ast.AssignSet,
		Value:  &ir.Index{Type: ir.TypDyn, Operand: varRef(si.live), Idx: varRef(si.idx)},
	}}
	// Every prop the instance can absorb is pushed on reuse: the render has no
	// way to know which of them changed, and a setter that writes the value it
	// already held costs an assignment and patches nothing.
	for _, p := range n.Props {
		if p.Name == "" || !componentAbsorbs(n.Component, p.Name) {
			continue
		}
		reuse = append(reuse, &ir.CallStmt{Call: &ir.Call{
			Type:     ir.TypVoid,
			Receiver: lowerNSIdent(),
			Func:     st.intrinsics[ir.NodeOpUpdateComponent],
			Args: []ir.CallArg{
				{Value: curRef()},
				{Value: &ir.Literal{Type: ir.TypString, Value: p.Name}},
				{Value: deepCloneExpr(p.Value)},
			},
		}})
	}

	fresh := &ir.Var{Name: id + "__new", Type: ir.TypDyn, Synthesized: true}
	create := []ir.Stmt{
		&ir.LocalVar{
			Name: fresh.Name,
			Type: &ir.Type{Kind: ir.TypeComponent, Decl: n.Component},
			Sym:  fresh,
			Init: declSt.componentCreateCall(n),
		},
		&ir.Assign{Target: curRef(), Op: ast.AssignSet, Value: varRef(fresh)},
	}

	return []ir.Stmt{
		&ir.LocalVar{Name: cur.Name, Type: ir.TypDyn, Sym: cur, Init: &ir.Literal{Type: ir.TypNull}},
		&ir.If{
			Cond: &ir.Binary{
				Type:  ir.TypBool,
				Op:    ast.BinGt,
				Left:  callListLength(varRef(si.live)),
				Right: varRef(si.idx),
			},
			Body: reuse,
			Else: create,
		},
		callListPush(varRef(si.next), curRef(), ir.TypDyn),
		&ir.Assign{
			Target: varRef(si.idx),
			Op:     ast.AssignSet,
			Value: &ir.Binary{
				Type: ir.TypInt, Op: ast.BinAdd,
				Left:  varRef(si.idx),
				Right: intLiteralLit(1),
			},
		},
	}
}

// close ends the render: everything the position no longer describes is
// destroyed, newest first, and what it does describe becomes what is held.
//
//	for <i>, _ = <live> {
//	    <j> = list.length(<live>) - 1 - <i>
//	    if <j> >= <idx> { lower.DestroyComponent(<live>[<j>]) }
//	}
//	<live> = <next>
//
// Backwards for the reason the effect teardown is: an instance built later may
// hold something an earlier one handed it.
func (st *reactivityState) closeSlotInstance(si *slotInstance) []ir.Stmt {
	loop := &ir.LoopVar{Name: si.idx.Name + "_d", Type: ir.TypInt}
	jSym := &ir.Var{Name: si.idx.Name + "_j", Type: ir.TypInt, Synthesized: true}

	return []ir.Stmt{
		&ir.For{
			Key:      loop.Name,
			KeySym:   loop,
			Value:    "_",
			Iter:     varRef(si.live),
			ElemType: ir.TypDyn,
			Body: []ir.Stmt{
				&ir.LocalVar{Name: jSym.Name, Type: ir.TypInt, Sym: jSym, Init: &ir.Binary{
					Type: ir.TypInt, Op: ast.BinSub,
					Left: &ir.Binary{
						Type: ir.TypInt, Op: ast.BinSub,
						Left:  callListLength(varRef(si.live)),
						Right: intLiteralLit(1),
					},
					Right: &ir.Ident{Name: loop.Name, Type: ir.TypInt, Sym: loop, Synthesized: true},
				}},
				&ir.If{
					Cond: &ir.Binary{Type: ir.TypBool, Op: ast.BinGte, Left: varRef(jSym), Right: varRef(si.idx)},
					Body: []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
						Type:     ir.TypVoid,
						Receiver: lowerNSIdent(),
						Func:     st.intrinsics[ir.NodeOpDestroyComponent],
						Args: []ir.CallArg{{Value: &ir.Index{
							Type:    ir.TypDyn,
							Operand: varRef(si.live),
							Idx:     varRef(jSym),
						}}},
					}}},
				},
			},
		},
		&ir.Assign{Target: varRef(si.live), Op: ast.AssignSet, Value: varRef(si.next)},
	}
}

// componentAbsorbs reports whether the component carries a setter for a prop,
// which is passComponentProps' answer to whether the prop can be written after
// construction. A prop with none is not routed to UpdateComponent: the call
// would name a function the instance does not have.
func componentAbsorbs(comp *ir.Component, prop string) bool {
	if comp == nil {
		return false
	}
	want := ir.ComponentSetter(prop)
	for _, f := range comp.Funcs {
		if f != nil && f.Name == want {
			return true
		}
	}
	return false
}

// isInstanceNode reports whether a node in a slot body is a component
// instantiation the build renders at run time.
func isInstanceNode(n *ir.NodeInst) bool {
	return n != nil && n.Component != nil && hasRealComponentBody(n.Component)
}
