package lower

import (
	"slices"
	"strconv"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
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
	// ctor is the #[construct] props this site binds to something other than a
	// literal, in declaration order. Each is a value the live instance was
	// built from and cannot be handed a new one, so a render describing a
	// different value rebuilds rather than patches.
	ctor []*ctorProp
}

// ctorProp is one construct-only prop's memory at an instantiation site: what
// the instance held at each identity was built from, so this render can ask
// whether it still describes the same thing.
//
// live and next mirror the instance registry exactly -- same shape, same
// identity, filled in step -- because the question is per instance and not per
// site. Every construct prop has them: the checker refuses the mark on a type
// the targets do not compare alike (markConstruct), so there is no prop here
// whose value this pass cannot ask its question about.
type ctorProp struct {
	name  string
	value ir.Expr
	live  *ir.Var
	next  *ir.Var
}

// keyed reports whether identity comes from a declared key rather than the
// position.
func (si *slotInstance) keyed() bool { return si.key != nil }

// newSlotInstance allocates the state the instantiation site n needs: its
// `key=` decides how identity is looked up, and the component it instantiates
// decides what the registry holds.
//
// One site instantiates one component, so the registry is typed: a host with
// no runtime types does not care, and Go cannot call a setter through `any`.
func (st *reactivityState) newSlotInstance(node *ir.NodeInst) *slotInstance {
	key, comp := node.Key, node.Component
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
	for _, p := range constructProps(node) {
		t := propValueType(comp, p.Name)
		held := ir.ListOf(t)
		if key != nil {
			held = ir.MapOf(ir.TypString, t)
		}
		cp := &ctorProp{
			name:  p.Name,
			value: p.Value,
			live:  &ir.Var{Name: name("_was_" + p.Name), Type: held, Init: emptyOf(held), Synthesized: true},
			next:  &ir.Var{Name: name("_now_" + p.Name), Type: held, Synthesized: true},
		}
		st.owner.addVar(cp.live)
		inst.ctor = append(inst.ctor, cp)
	}
	if o, ok := st.owner.(compOwner); ok {
		st.held[o.c] = append(st.held[o.c], inst)
	}
	return inst
}

// destroyHeld gives every component that holds a registry a teardown that
// empties it.
//
// DestroyComponent was emitted only where a render stops describing a
// position, which answers for the instances a render owns and for nothing an
// instance owns in turn. So destroying a component that holds a registry of
// its own dropped the registry and left every instance in it mounted -- and
// the registry is a local of that component's own scope, so nothing outside
// could reach them afterwards. The teardown is the only place that can: it
// runs inside the instance, which is where the registry is.
//
// It is prepended to the brackets' teardown, and created where a component has
// none, for the reason that teardown reverses its own order: what an instance
// holds was built after the instance mounted, and may be holding something the
// instance handed it. Recursion falls out -- destroying a held instance runs
// its teardown, which empties its registries in turn.
func (st *reactivityState) destroyHeld() {
	for _, comp := range st.pkg.Components {
		// Only an instance has a teardown to hang this on. A component the
		// build inlined holds nothing: its registries were hoisted into
		// whatever it was inlined into, and live as long as that does.
		if comp == nil || !comp.RuntimeInstance || len(st.held[comp]) == 0 {
			continue
		}
		var body []ir.Stmt
		for _, si := range slices.Backward(st.held[comp]) {
			body = append(body, st.emptyRegistry(si)...)
		}
		if fn := funcNamed(comp.Funcs, TeardownFunc); fn != nil {
			fn.Block = append(body, fn.Block...)
			continue
		}
		comp.Funcs = append(comp.Funcs, &ir.Func{
			Name:   TeardownFunc,
			Return: ir.TypVoid,
			Purity: ir.PurityMutates,
			Block:  body,
		})
	}
}

// emptyRegistry destroys everything one registry holds and leaves it empty, so
// a teardown that runs twice unmounts nothing twice.
//
//	for <k>, <v> = <live> { lower.DestroyComponent(<v>) }
//	<live> = []
//
// A list is walked backwards for the reason closeSlotInstance walks backwards;
// a keyed registry is a map and has no order to reverse.
func (st *reactivityState) emptyRegistry(si *slotInstance) []ir.Stmt {
	destroy := func(inst ir.Expr) ir.Stmt {
		return &ir.CallStmt{Call: &ir.Call{
			Type:     ir.TypVoid,
			Receiver: lowerNSIdent(),
			Func:     st.intrinsics[ir.NodeOpDestroyComponent],
			Args:     []ir.CallArg{{Value: inst}},
		}}
	}
	var loop *ir.For
	if si.keyed() {
		k := &ir.LoopVar{Name: si.live.Name + "_dk", Type: ir.TypString}
		v := &ir.LoopVar{Name: si.live.Name + "_dv", Type: si.elem}
		loop = &ir.For{
			Key: k.Name, KeySym: k, Value: v.Name, ValueSym: v,
			Iter: varRef(si.live), ElemType: si.elem,
			Body: []ir.Stmt{destroy(&ir.Ident{Name: v.Name, Type: si.elem, Sym: v, Synthesized: true})},
		}
	} else {
		i := &ir.LoopVar{Name: si.live.Name + "_di", Type: ir.TypInt}
		j := &ir.Var{Name: si.live.Name + "_dj", Type: ir.TypInt, Synthesized: true}
		loop = &ir.For{
			Key: i.Name, KeySym: i, Value: "_",
			Iter: varRef(si.live), ElemType: si.elem,
			Body: []ir.Stmt{
				&ir.LocalVar{Name: j.Name, Type: ir.TypInt, Sym: j, Init: &ir.Binary{
					Type: ir.TypInt, Op: ast.BinSub,
					Left: &ir.Binary{
						Type: ir.TypInt, Op: ast.BinSub,
						Left:  callListLength(varRef(si.live)),
						Right: intLiteralLit(1),
					},
					Right: &ir.Ident{Name: i.Name, Type: ir.TypInt, Sym: i, Synthesized: true},
				}},
				destroy(&ir.Index{Type: si.elem, Operand: varRef(si.live), Idx: varRef(j)}),
			},
		}
	}
	return []ir.Stmt{
		loop,
		&ir.Assign{Target: varRef(si.live), Op: ast.AssignSet, Value: emptyOf(si.live.Type)},
	}
}

// funcNamed answers with the func of that name, or nil.
func funcNamed(funcs []*ir.Func, name string) *ir.Func {
	for _, f := range funcs {
		if f != nil && f.Name == name {
			return f
		}
	}
	return nil
}

// constructProps is the #[construct]-marked props this site binds to something
// other than a literal, in the order the node wrote them.
//
// A literal is left out because it cannot change: the memory and the
// comparison would both be dead weight, and leaving them out is what keeps a
// site that binds only constants lowering exactly as it did before the mark
// existed.
func constructProps(n *ir.NodeInst) []*ir.Arg {
	if n == nil || n.Component == nil {
		return nil
	}
	var out []*ir.Arg
	for i, p := range n.Props {
		if p.Name == "" || !propIsConstruct(n.Component, p.Name) {
			continue
		}
		if _, ok := p.Value.(*ir.Literal); ok {
			continue
		}
		out = append(out, &n.Props[i])
	}
	return out
}

// propIsConst reports whether comp declared prop const: its value is known at
// build time, so no update ever reaches it and it needs neither a setter nor a
// rebuild.
func propIsConst(comp *ir.Component, prop string) bool {
	if comp == nil {
		return false
	}
	for _, p := range comp.Props {
		if p != nil && p.Name == prop {
			return p.Const
		}
	}
	return false
}

// propIsConstruct reports whether comp declared prop with #[construct].
func propIsConstruct(comp *ir.Component, prop string) bool {
	if comp == nil {
		return false
	}
	for _, p := range comp.Props {
		if p != nil && p.Name == prop {
			return p.Construct
		}
	}
	return false
}

// propValueType is the declared type of one of comp's props.
func propValueType(comp *ir.Component, prop string) *ir.Type {
	if comp == nil {
		return nil
	}
	for _, p := range comp.Props {
		if p != nil && p.Name == prop {
			return p.Type
		}
	}
	return nil
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
		out = append(out, &ir.LocalVar{Name: si.idx.Name, Type: ir.TypInt, Sym: si.idx, Init: intLiteralLit(0)})
	} else {
		out = append(out, &ir.LocalVar{Name: si.seen.Name, Type: si.seen.Type, Sym: si.seen, Init: emptyOf(si.seen.Type)})
	}
	for _, cp := range si.ctor {
		out = append(out, &ir.LocalVar{Name: cp.next.Name, Type: cp.next.Type, Sym: cp.next, Init: emptyOf(cp.next.Type)})
	}
	return out
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
//	if list.length(<live>) > <idx> && <was_p>[<idx>] == <expr> {
//	    <id> = <live>[<idx>]
//	    lower.UpdateComponent(<id>, "<prop>", <expr>)   // one per settable prop
//	} else {
//	    if list.length(<live>) > <idx> { lower.DestroyComponent(<live>[<idx>]) }
//	    var <id>__new <Comp> = lower.CreateComponent(...)
//	    <id> = <id>__new
//	}
//	list.push(<next>, <id>)
//	list.push(<now_p>, <expr>)                          // one per #[construct] prop
//	<idx> = <idx> + 1
//
// The create stays a LocalVar initialised by the call, because that is the
// shape codegen.WalkLowered matches on: an assignment carrying the same call
// reaches no arm and is emitted as a call to a function nothing declares. It
// also stays the *only* create site, which is why a stale instance falls
// through to the else instead of being rebuilt inside the reuse branch: two
// CreateComponent bindings of one id would have a mutation-model platform
// declare the same field twice.
//
// The condition is a length check alone until a #[construct] prop is bound to
// something other than a literal. Then it also asks whether what the instance
// was built from is what this render describes, and a difference takes the
// else: an instance that cannot be handed the new value is destroyed and built
// again. Every construct prop has that question, because the checker refuses
// the mark on a type with no portable equality -- the same question the effect
// settle asks of its `on`, answered by the same walk that emits the comparison.
func (st *reactivityState) reuseOrCreate(si *slotInstance, n *ir.NodeInst, declSt *declarativeState) []ir.Stmt {
	id := n.ID
	cur := &ir.Var{Name: id, Type: si.elem, Synthesized: true}
	curRef := func() *ir.Ident { return varRef(cur) }

	out := []ir.Stmt{&ir.LocalVar{Name: cur.Name, Type: si.elem, Sym: cur, Init: &ir.Literal{Type: ir.TypNull}}}

	// The identity this position is looked up under, and the two questions
	// asked of it: whether one is held, and which one. A registry entry is
	// read the same way whichever identity it is -- so is a construct prop's
	// memory, which is why the registries have the same shape.
	var key *ir.Var
	if si.keyed() {
		k, claim := st.claimKey(si)
		key = k
		out = append(out, claim...)
	}
	has := func() ir.Expr {
		if si.keyed() {
			return callMapContains(varRef(si.live), varRef(key), si.elem)
		}
		return &ir.Binary{
			Type:  ir.TypBool,
			Op:    ast.BinGt,
			Left:  callListLength(varRef(si.live)),
			Right: varRef(si.idx),
		}
	}
	held := func() ir.Expr {
		if si.keyed() {
			return callMapGet(varRef(si.live), varRef(key), &ir.Literal{Type: ir.TypNull}, si.elem)
		}
		return &ir.Index{Type: si.elem, Operand: varRef(si.live), Idx: varRef(si.idx)}
	}
	entry := func(reg *ir.Var) ir.Expr {
		if si.keyed() {
			return &ir.Index{Type: reg.Type.Elems[1], Operand: varRef(reg), Idx: varRef(key)}
		}
		return &ir.Index{Type: reg.Type.Elems[0], Operand: varRef(reg), Idx: varRef(si.idx)}
	}

	// Every prop the instance can absorb is pushed on reuse: the render has no
	// way to know which of them changed, and a setter that writes the value it
	// already held costs an assignment and patches nothing.
	reuse := []ir.Stmt{&ir.Assign{Target: curRef(), Op: ast.AssignSet, Value: held()}}
	for _, p := range n.Props {
		if p.Name == "" {
			continue
		}
		// A prop the instance can neither absorb nor rebuild for is one whose
		// new value goes nowhere, which is the whole bug class this pass grew
		// #[construct] for: the update was dropped and the user saw no error
		// and no effect. The mark is how a declaration opts into the rebuild;
		// anything else arriving here is a routing nothing in the compiler
		// answers, so it is reported rather than built into a program that
		// ignores the write.
		if !componentAbsorbs(n.Component, p.Name) {
			if !propIsConstruct(n.Component, p.Name) && !propIsConst(n.Component, p.Name) {
				st.failf(p.NamePos, "prop %q of component %s can neither be written after construction nor rebuild the instance; mark it #[construct] if it is read only while the instance is built", p.Name, n.Component.Name)
			}
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
	// An instance the position still holds but can no longer describe goes
	// away before its replacement is built. The guard is the same question the
	// reuse branch asked first, so nothing is destroyed at a position that
	// held nothing. close() cannot do this: it destroys what the render
	// described past, and this position is one the render described.
	discard := &ir.If{
		Cond: has(),
		Body: []ir.Stmt{&ir.CallStmt{Call: &ir.Call{
			Type:     ir.TypVoid,
			Receiver: lowerNSIdent(),
			Func:     st.intrinsics[ir.NodeOpDestroyComponent],
			Args:     []ir.CallArg{{Value: held()}},
		}}},
	}

	out = append(out, &ir.If{
		Cond: si.reusable(has, entry),
		Body: reuse,
		Else: append([]ir.Stmt{discard}, create...),
	})

	// What this render leaves for the next one: the instance at this identity,
	// and what each construct prop built it from.
	if si.keyed() {
		out = append(out, &ir.Assign{
			Target: entry(si.next),
			Op:     ast.AssignSet,
			Value:  curRef(),
		})
		for _, cp := range si.ctor {
			out = append(out, &ir.Assign{
				Target: entry(cp.next),
				Op:     ast.AssignSet,
				Value:  deepCloneExpr(cp.value),
			})
		}
		return out
	}
	out = append(out, callListPush(varRef(si.next), curRef(), si.elem))
	for _, cp := range si.ctor {
		out = append(out, callListPush(varRef(cp.next), deepCloneExpr(cp.value), cp.next.Type.Elems[0]))
	}
	return append(out, &ir.Assign{
		Target: varRef(si.idx),
		Op:     ast.AssignSet,
		Value: &ir.Binary{
			Type: ir.TypInt, Op: ast.BinAdd,
			Left:  varRef(si.idx),
			Right: intLiteralLit(1),
		},
	})
}

// reusable is the condition under which the instance a position holds may be
// kept: one is held, and every construct prop it was built from is what this
// render describes.
func (si *slotInstance) reusable(has func() ir.Expr, entry func(*ir.Var) ir.Expr) ir.Expr {
	cond := has()
	for _, cp := range si.ctor {
		cond = &ir.Binary{
			Type:  ir.TypBool,
			Op:    ast.BinAnd,
			Left:  cond,
			Right: rebuildSame(entry(cp.live), cp.value, cp.next.Type.Elems[len(cp.next.Type.Elems)-1]),
		}
	}
	return cond
}

// recreateStatic destroys the instance a fixed position holds and builds a
// fresh one from what the render now describes, putting it back where the old
// one was.
//
//	var <id>__reN <C> = lower.CreateComponent(<C>, {props...})
//	var <id>__reN__el dyn = lower.ComponentRoot(<id>__reN)
//	var <id>__prevN__el dyn = lower.ComponentRoot(<id>)
//	lower.InsertBefore(<id>__pos, <id>__reN__el, <id>__prevN__el)
//	lower.RemoveChild(<id>__pos, <id>__prevN__el)
//	lower.DestroyComponent(<id>)
//	<id> = <id>__reN
//
// This is what #[construct] means at a position no render owns: the prop is
// read while the instance is built and has no setter, so the only way to
// describe a new value is to build a new instance.
//
// The new root goes in before the old one comes out, which is what keeps the
// instance's siblings in order -- InsertBefore is the whole reason this is
// gated on a capability. Destroy runs after the removal because it is the
// instance's teardown and not a detach; every platform's DestroyComponent
// says so.
//
// Both roots are asked for here rather than read off the `<id>__el` binding
// the build path made, because a fixed position need not have emitted one --
// html appends `<id>.__root` inline. Each is its own LocalVar because that is
// the shape ComponentRoot is matched in, and the old one is bound before the
// reassignment that loses the instance it belongs to.
//
// The create stays a LocalVar initialised by the call for the reason
// reuseOrCreate's does: that is the shape codegen.WalkLowered matches on. Its
// name carries a counter because two mutations of one var in one handler body
// splice two updaters into the same scope, and one name for both is a
// redeclaration.
func (st *reactivityState) recreateStatic(n *ir.NodeInst, declSt *declarativeState, rewrite map[ir.Symbol]ir.Expr) []ir.Stmt {
	n1 := strconv.Itoa(st.instCounter)
	st.instCounter++
	elem := instanceTypeOf(n.Component)
	cur := &ir.Var{Name: n.ID, Type: elem, Synthesized: true}
	fresh := &ir.Var{Name: n.ID + "__re" + n1, Type: elem, Synthesized: true}
	freshEl := &ir.Var{Name: fresh.Name + instanceRootSuffix, Type: ir.TypDyn, Synthesized: true}
	prevEl := &ir.Var{Name: n.ID + "__prev" + n1 + instanceRootSuffix, Type: ir.TypDyn, Synthesized: true}

	place := &ir.Ident{
		Name:         ir.InstancePlaceName(n.ID),
		Type:         ir.TypDyn,
		IsElementRef: true,
		Synthesized:  true,
	}
	elRef := func(v *ir.Var) ir.Expr {
		return &ir.Ident{Name: v.Name, Type: ir.TypDyn, IsElementRef: true, Sym: v, Synthesized: true}
	}
	bindRoot := func(el *ir.Var, inst *ir.Var) ir.Stmt {
		return &ir.LocalVar{Name: el.Name, Type: ir.TypDyn, Sym: el, Init: &ir.Call{
			Type:     ir.TypDyn,
			Receiver: lowerNSIdent(),
			Func:     st.intrinsics[ir.NodeOpComponentRoot],
			Args:     []ir.CallArg{{Value: varRef(inst)}},
		}}
	}
	op := func(name string, args ...ir.Expr) ir.Stmt {
		call := &ir.Call{
			Type:     ir.TypVoid,
			Receiver: lowerNSIdent(),
			Func:     st.intrinsics[name],
		}
		for _, a := range args {
			call.Args = append(call.Args, ir.CallArg{Value: a})
		}
		return &ir.CallStmt{Call: call}
	}

	// The props the new instance is built from are this render's, rewritten
	// the way every other updater expression is: inside a lifted body a read
	// of a captured cell goes through the state struct.
	built := &ir.NodeInst{ID: n.ID, Component: n.Component}
	for _, p := range n.Props {
		if p.Name == "" {
			continue
		}
		built.Props = append(built.Props, ir.Arg{
			Name:    p.Name,
			NamePos: p.NamePos,
			Value:   rewriteIdentsToCaptures(p.Value, rewrite),
		})
	}

	return []ir.Stmt{
		&ir.LocalVar{Name: fresh.Name, Type: elem, Sym: fresh, Init: declSt.componentCreateCall(built)},
		bindRoot(freshEl, fresh),
		bindRoot(prevEl, cur),
		op(ir.NodeOpInsertBefore, place, elRef(freshEl), elRef(prevEl)),
		op(ir.NodeOpRemoveChild, cloneIdent(place), elRef(prevEl)),
		op(ir.NodeOpDestroyComponent, varRef(cur)),
		&ir.Assign{Target: varRef(cur), Op: ast.AssignSet, Value: varRef(fresh)},
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

	out := []ir.Stmt{
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
	return append(out, si.retainCtor()...)
}

// retainCtor carries each construct prop's memory across the render, beside
// the registry it mirrors. Written here rather than in reuseOrCreate so the
// two flips are one statement apart and cannot be done for one and not the
// other.
func (si *slotInstance) retainCtor() []ir.Stmt {
	var out []ir.Stmt
	for _, cp := range si.ctor {
		out = append(out, &ir.Assign{Target: varRef(cp.live), Op: ast.AssignSet, Value: varRef(cp.next)})
	}
	return out
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
	out := []ir.Stmt{
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
	return append(out, si.retainCtor()...)
}

// componentAbsorbs reports whether the component carries a setter for a prop,
// which is passComponentProps' answer to whether the prop can be written after
// construction. A prop with none is not routed to UpdateComponent: the call
// would name a function the instance does not have.
//
// #[construct] is what puts a prop in that class deliberately -- the mark is
// why passComponentProps gave it no setter -- and reuseOrCreate answers for it
// by rebuilding the instance. A prop that lands here for any other reason is
// still dropped, which is the diagnostic gap noted in the mark's own docs.
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
