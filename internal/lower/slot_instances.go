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
	// elem is the type of the handle this site holds: an instance of the one
	// component it instantiates.
	elem *ir.Type
	// key is the `key=` the instantiation declared, or nil for positional
	// identity. With one, live and next are maps and idx is unused; seen
	// counts how many times each key has been claimed this render.
	key  ir.Expr
	seen *ir.Var
}

// keyed reports whether identity comes from a declared key rather than the
// position.
func (si *slotInstance) keyed() bool { return si.key != nil }

// newSlotInstance allocates the state one instantiation site needs. key is the
// `key=` the node declared, or nil for positional identity, and comp is the
// component being instantiated.
//
// One site instantiates one component, so the registry is typed: a host with
// no runtime types does not care, and Go cannot call a setter through `any`.
func (st *reactivityState) newSlotInstance(key ir.Expr, comp *ir.Component) *slotInstance {
	n := st.instCounter
	st.instCounter++
	name := func(suffix string) string { return "__inst" + strconv.Itoa(n) + suffix }

	elem := instanceTypeOf(comp)
	held := ir.ListOf(elem)
	if key != nil {
		held = ir.MapOf(ir.TypString, elem)
	}
	inst := &slotInstance{
		elem: elem,
		key:  key,
		live: &ir.Var{Name: name("_live"), Type: held, Init: emptyOf(held), Synthesized: true},
		next: &ir.Var{Name: name("_next"), Type: held, Synthesized: true},
		idx:  &ir.Var{Name: name("_i"), Type: ir.TypInt, Synthesized: true},
	}
	if key != nil {
		counts := ir.MapOf(ir.TypString, ir.TypInt)
		inst.seen = &ir.Var{Name: name("_seen"), Type: counts, Synthesized: true}
	}
	st.owner.addVar(inst.live)
	return inst
}

// instanceTypeOf is the handle type an instantiation of comp is held as.
func instanceTypeOf(comp *ir.Component) *ir.Type {
	if comp == nil {
		return ir.TypDyn
	}
	return &ir.Type{Kind: ir.TypeInstance, Decl: comp}
}

// emptyOf is the empty value a registry starts each render from.
func emptyOf(t *ir.Type) ir.Expr {
	if t.Kind == ir.TypeMap {
		return &ir.MapLitIR{Type: t}
	}
	return &ir.ListLit{Type: t}
}

func varRef(v *ir.Var) *ir.Ident {
	return &ir.Ident{Name: v.Name, Type: v.Type, Sym: v, Synthesized: true}
}

// open is the bookkeeping a render starts with: somewhere to describe into,
// and the cursor or the claim counts that go with it.
func (si *slotInstance) open() []ir.Stmt {
	out := []ir.Stmt{
		&ir.LocalVar{Name: si.next.Name, Type: si.next.Type, Sym: si.next, Init: emptyOf(si.next.Type)},
	}
	if !si.keyed() {
		return append(out, &ir.LocalVar{Name: si.idx.Name, Type: ir.TypInt, Sym: si.idx, Init: intLiteralLit(0)})
	}
	return append(out, &ir.LocalVar{Name: si.seen.Name, Type: si.seen.Type, Sym: si.seen, Init: emptyOf(si.seen.Type)})
}

// claimKey is the identity one iteration claims: the declared key as a string,
// and a suffix when this render has already claimed it.
//
//	var <k> string = string(<key>)
//	var <n> int = <seen>.get(<k>, 0)
//	<seen>[<k>] = <n> + 1
//	if <n> > 0 { <k> = <k> + "#" + string(<n>) }
//
// Two iterations claiming one identity is the author's bug, but collapsing
// them is not the way to report it: the second would reuse the first's
// instance, so one instance would render at two positions in the tree. The
// interpreter suffixes for the same reason -- see iterationID -- and the two
// agreeing is what keeps a keyed list meaning the same thing on every target.
func (st *reactivityState) claimKey(si *slotInstance) (*ir.Var, []ir.Stmt) {
	n := st.instCounter
	k := &ir.Var{Name: "__key" + strconv.Itoa(n), Type: ir.TypString, Synthesized: true}
	count := &ir.Var{Name: "__key" + strconv.Itoa(n) + "_n", Type: ir.TypInt, Synthesized: true}

	return k, []ir.Stmt{
		&ir.LocalVar{Name: k.Name, Type: ir.TypString, Sym: k, Init: &ir.Conversion{
			Type:    ir.TypString,
			Operand: deepCloneExpr(si.key),
		}},
		&ir.LocalVar{Name: count.Name, Type: ir.TypInt, Sym: count, Init: callMapGet(varRef(si.seen), varRef(k), intLiteralLit(0), ir.TypInt)},
		&ir.Assign{
			Target: &ir.Index{Type: ir.TypInt, Operand: varRef(si.seen), Idx: varRef(k)},
			Op:     ast.AssignSet,
			Value:  &ir.Binary{Type: ir.TypInt, Op: ast.BinAdd, Left: varRef(count), Right: intLiteralLit(1)},
		},
		&ir.If{
			Cond: &ir.Binary{Type: ir.TypBool, Op: ast.BinGt, Left: varRef(count), Right: intLiteralLit(0)},
			Body: []ir.Stmt{&ir.Assign{
				Target: varRef(k),
				Op:     ast.AssignSet,
				Value: &ir.Binary{
					Type: ir.TypString, Op: ast.BinAdd,
					Left: &ir.Binary{
						Type: ir.TypString, Op: ast.BinAdd,
						Left:  varRef(k),
						Right: &ir.Literal{Type: ir.TypString, Value: "#"},
					},
					Right: &ir.Conversion{Type: ir.TypString, Operand: varRef(count)},
				},
			}},
		},
	}
}

// callMapGet builds `m.get(key, default)`.
func callMapGet(m, key, def ir.Expr, valueType *ir.Type) *ir.Call {
	var params []*ir.Param
	var ret *ir.Type
	if d := ir.LookupIntrinsic("map.get"); d != nil {
		params, ret = d.Instantiate(ir.TypString, valueType)
	}
	if ret == nil {
		ret = valueType
	}
	return &ir.Call{
		Type:      valueType,
		Func:      &ir.Func{Name: "get", Receiver: "map", Intrinsic: "map.get", Params: params, Return: ret},
		Args:      []ir.CallArg{{Value: m}, {Value: key}, {Value: def}},
		ErrorMode: ir.ErrorNone,
	}
}

// callMapContains builds `m.contains(key)`.
func callMapContains(m, key ir.Expr, valueType *ir.Type) *ir.Call {
	var params []*ir.Param
	if d := ir.LookupIntrinsic("map.contains"); d != nil {
		params, _ = d.Instantiate(ir.TypString, valueType)
	}
	return &ir.Call{
		Type: ir.TypBool,
		Func: &ir.Func{Name: "contains", Receiver: "map", Intrinsic: "map.contains", Params: params, Return: ir.TypBool},
		Args: []ir.CallArg{{Value: m}, {Value: key}},
	}
}

// reuseOrCreate binds id to the instance for the position the cursor is at:
// the one already held, or a new one.
//
//	var <id> <Comp> = null
//	if list.length(<live>) > <idx> {
//	    <id> = <live>[<idx>]
//	    lower.UpdateComponent(<id>, "<prop>", <expr>)   // one per settable prop
//	} else {
//	    var <id>__new <Comp> = lower.CreateComponent(...)
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
	cur := &ir.Var{Name: id, Type: si.elem, Synthesized: true}
	curRef := func() *ir.Ident { return varRef(cur) }

	reuse := []ir.Stmt{&ir.Assign{
		Target: curRef(),
		Op:     ast.AssignSet,
		Value:  &ir.Index{Type: si.elem, Operand: varRef(si.live), Idx: varRef(si.idx)},
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

	fresh := &ir.Var{Name: id + "__new", Type: si.elem, Synthesized: true}
	create := []ir.Stmt{
		&ir.LocalVar{
			Name: fresh.Name,
			Type: si.elem,
			Sym:  fresh,
			Init: declSt.componentCreateCall(n),
		},
		&ir.Assign{Target: curRef(), Op: ast.AssignSet, Value: varRef(fresh)},
	}

	if si.keyed() {
		k, claim := st.claimKey(si)
		reuse = append([]ir.Stmt{&ir.Assign{
			Target: curRef(),
			Op:     ast.AssignSet,
			Value:  callMapGet(varRef(si.live), varRef(k), &ir.Literal{Type: ir.TypNull}, si.elem),
		}}, reuse[1:]...)
		out := append([]ir.Stmt{
			&ir.LocalVar{Name: cur.Name, Type: si.elem, Sym: cur, Init: &ir.Literal{Type: ir.TypNull}},
		}, claim...)
		return append(out,
			&ir.If{
				Cond: callMapContains(varRef(si.live), varRef(k), si.elem),
				Body: reuse,
				Else: create,
			},
			&ir.Assign{
				Target: &ir.Index{Type: si.elem, Operand: varRef(si.next), Idx: varRef(k)},
				Op:     ast.AssignSet,
				Value:  curRef(),
			},
		)
	}

	return []ir.Stmt{
		&ir.LocalVar{Name: cur.Name, Type: si.elem, Sym: cur, Init: &ir.Literal{Type: ir.TypNull}},
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
		callListPush(varRef(si.next), curRef(), si.elem),
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
	if si.keyed() {
		return st.closeKeyedSlotInstance(si)
	}
	loop := &ir.LoopVar{Name: si.idx.Name + "_d", Type: ir.TypInt}
	jSym := &ir.Var{Name: si.idx.Name + "_j", Type: ir.TypInt, Synthesized: true}

	return []ir.Stmt{
		&ir.For{
			Key:      loop.Name,
			KeySym:   loop,
			Value:    "_",
			Iter:     varRef(si.live),
			ElemType: si.elem,
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
							Type:    si.elem,
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

// closeKeyedSlotInstance is close for a keyed registry: an identity the render
// did not claim is one whose instance is going away.
//
//	for <k>, <v> = <live> {
//	    if !<next>.contains(<k>) { lower.DestroyComponent(<v>) }
//	}
//	<live> = <next>
//
// No order to reverse here. A list has one -- the order the positions were
// described in -- and a map does not, so nothing about a keyed registry says
// which of two departing instances was built first. Where that matters, it is
// the list the program should be using.
func (st *reactivityState) closeKeyedSlotInstance(si *slotInstance) []ir.Stmt {
	k := &ir.LoopVar{Name: si.live.Name + "_k", Type: ir.TypString}
	v := &ir.LoopVar{Name: si.live.Name + "_v", Type: si.elem}
	return []ir.Stmt{
		&ir.For{
			Key:      k.Name,
			KeySym:   k,
			Value:    v.Name,
			ValueSym: v,
			Iter:     varRef(si.live),
			ElemType: si.elem,
			Body: []ir.Stmt{&ir.If{
				Cond: &ir.Unary{
					Type:    ir.TypBool,
					Op:      ast.UnaryNot,
					Operand: callMapContains(varRef(si.next), &ir.Ident{Name: k.Name, Type: ir.TypString, Sym: k, Synthesized: true}, si.elem),
				},
				Body: []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
					Type:     ir.TypVoid,
					Receiver: lowerNSIdent(),
					Func:     st.intrinsics[ir.NodeOpDestroyComponent],
					Args:     []ir.CallArg{{Value: &ir.Ident{Name: v.Name, Type: si.elem, Sym: v, Synthesized: true}}},
				}}},
			}},
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
