package lower

import (
	"fmt"
	"slices"
	"strconv"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// A bracket keyed on a cell -- a var, or a field of one -- is reported writes by
// where they landed, which the compiled targets know statically: the write's
// target is a path from a var through fields and indices, and so is the key.
// Comparing the two says what the interpreter's write log says at run time
// (internal/interp/writelog.go's relate): below the key, at it, above it, or
// beside it in the same var, which reports nothing.
//
// A list's change events are a queue per bracket -- three parallel lists of a
// kind, an index and an item -- which each write the bracket handles appends
// to through a note function, and the settle drains before `@update`. A note
// is a call rather than statements at the site so that a test body, which
// spells the instance's functions through the instance it holds, notes the way
// a handler does.

// Change kinds, as the queue spells them.
const (
	changeInsert = iota
	changeSet
	changeRemove
	changeClear
)

var changeKinds = map[string]int{
	ir.EffectInsert: changeInsert,
	ir.EffectSet:    changeSet,
	ir.EffectRemove: changeRemove,
	ir.EffectClear:  changeClear,
}

// effectCell is the cell a key names: a var and the steps below it, each a
// field or -- once -- the element an enclosing loop's index names, which is
// what `for var i, &x = xs` keyed `on = x` becomes once passRefLoop has made
// the element `xs[i]`.
type effectCell struct {
	root  *ir.Var
	steps []cellStep
	// loop is the index the loop step reads, nil for a cell outside a loop.
	// Every copy the loop makes is a cell of its own, and a write names the
	// one it reached by the index it wrote at.
	loop *ir.LoopVar
	// iterable is the list the loop step indexes -- the key up to that step.
	iterable ir.Expr
}

type cellStep struct {
	field string
	loop  *ir.LoopVar
}

// cellOfKey is the cell `on` names, or nil where it names none -- a computed
// key, or a prop the inliner left standing, which is written by whoever bound
// it and so at no write site this program shows. frames are the enclosing
// position, whose loops' indices may step into a list.
func cellOfKey(key ir.Expr, frames []effectFrame) *effectCell {
	loopIndex := func(e ir.Expr) *ir.LoopVar {
		id, isIdent := e.(*ir.Ident)
		if !isIdent {
			return nil
		}
		for _, f := range frames {
			if f.loop != nil && !f.loopElse && f.loop.KeySym != nil && ir.Symbol(f.loop.KeySym) == id.Sym {
				return f.loop.KeySym
			}
		}
		return nil
	}
	if on := copyPairOn(key); on != nil {
		if c := cellOfKey(on, frames); c != nil && c.loop != nil {
			return c
		}
		return nil
	}
	c := &effectCell{}
	for {
		switch n := key.(type) {
		case *ir.Select:
			c.steps = append([]cellStep{{field: n.Field}}, c.steps...)
			key = n.Operand
		case *ir.Index:
			lv := loopIndex(n.Idx)
			if lv == nil || c.loop != nil {
				return nil
			}
			c.loop, c.iterable = lv, n.Operand
			c.steps = append([]cellStep{{loop: lv}}, c.steps...)
			key = n.Operand
		case *ir.Ident:
			if v, isVar := n.Sym.(*ir.Var); isVar {
				c.root = v
				return c
			}
			return nil
		default:
			return nil
		}
	}
}

// pathStep is one step of a write's target below its var: a field, or an
// index, with the slot holding the index expression so the site can bind it
// to a temp.
type pathStep struct {
	field string
	idx   *ir.Expr
}

// writePath is the var a write target lands in and the steps below it, or nil
// where the target is no such path. A test's `c.xs` reaches the instance's var
// through the instance it was handed, as testMemberVar answers.
func writePath(target ir.Expr) (*ir.Var, []pathStep) {
	var steps []pathStep
	for {
		switch n := target.(type) {
		case *ir.Select:
			if id, isIdent := n.Operand.(*ir.Ident); isIdent {
				if _, isParam := id.Sym.(*ir.Param); isParam {
					return testMemberVar(n), steps
				}
			}
			steps = append([]pathStep{{field: n.Field}}, steps...)
			target = n.Operand
		case *ir.Index:
			steps = append([]pathStep{{idx: &n.Idx}}, steps...)
			target = n.Operand
		case *ir.Ident:
			if v, isVar := n.Sym.(*ir.Var); isVar {
				return v, steps
			}
			return nil, nil
		default:
			return nil, nil
		}
	}
}

// relation is how a write stands to a cell.
type relation int

const (
	// relApart is a write that names neither the cell nor what holds it.
	relApart relation = iota
	// relAbove replaced what holds the cell, or the cell itself.
	relAbove
	// relAt is a write to the cell by one of a list's own mutations.
	relAt
	// relElem is a write inside one element of the list in the cell.
	relElem
)

// relate is how steps, below the cell's var, stand to the cell's own, and for
// relElem the slot holding the element's index. copy is the slot holding the
// index the write took at the cell's loop step, which says which copy's cell
// it reached; atIterable says a write above the cell landed on the list that
// step indexes.
func (c *effectCell) relate(steps []pathStep) (rel relation, idx, copy *ir.Expr, atIterable bool) {
	for i, cs := range c.steps {
		if i == len(steps) {
			return relAbove, nil, nil, cs.loop != nil
		}
		if cs.loop != nil {
			if steps[i].idx == nil {
				return relApart, nil, nil, false
			}
			copy = steps[i].idx
			continue
		}
		if steps[i].idx != nil || steps[i].field != cs.field {
			return relApart, nil, nil, false
		}
	}
	if len(steps) == len(c.steps) {
		return relAt, nil, copy, false
	}
	if s := steps[len(c.steps)]; s.idx != nil {
		return relElem, s.idx, copy, false
	}
	return relAbove, nil, copy, false
}

// notesFor is what one statement owes the cell-keyed brackets: the statements
// before it and after it, and the brackets whose `@update` it marks.
//
// A write the bracket's handlers describe is noted; any other write that
// reaches the key marks `@update`, which reports it once after the changes.
// The statement may be rewritten in place to read an index from a temp, since
// a note evaluates the index again.
func (st *effectState) notesFor(s ir.Stmt) (before, after []ir.Stmt, dirty []*loweredEffect) {
	var target ir.Expr
	var method string
	switch n := s.(type) {
	case *ir.Assign:
		target = n.Target
	case *ir.Toggle:
		target = n.Target
	case *ir.CallStmt:
		target = mutatingCallReceiver(n.Call)
		if target != nil {
			method = n.Call.Func.Intrinsic
		}
	}
	if target == nil {
		return nil, nil, nil
	}
	root, steps := writePath(target)
	if root == nil {
		// A write the path does not spell -- a lifted closure's state, say --
		// still reaches a bracket whose var it wrote.
		if v, _ := mutatedVar(st.pkg, st.reactive, target); v != nil {
			dirty = append(dirty, st.cellBy[v]...)
		}
		return nil, nil, dirty
	}
	for _, fx := range st.cellBy[root] {
		rel, idx, copySlot, atIterable := fx.cell.relate(steps)
		perCopy := fx.cell.loop != nil
		if perCopy && rel == relAbove {
			// A list's own mutation of the list the loop walks moves the
			// elements from where it inserts or removes, so those copies'
			// cells hold another value; a push only adds a copy, which is
			// reported nothing, and a clear leaves none. Anything else above
			// the cell writes every copy.
			if atIterable {
				switch method {
				case "list.push", "list.clear":
					continue
				case "list.insert", "list.remove":
					call := s.(*ir.CallStmt).Call
					before = append(before, st.callNote(fx, "shift", st.siteIndex(&call.Args[1].Value, &before)))
					continue
				}
			}
			dirty = append(dirty, fx)
			continue
		}
		var copyArg []ir.Expr
		if perCopy && rel != relApart {
			copyArg = []ir.Expr{st.siteIndex(copySlot, &before)}
		}
		note := func(which string, args ...ir.Expr) ir.Stmt {
			return st.callNote(fx, which, append(slices.Clone(copyArg), args...)...)
		}
		kind := ""
		switch {
		case rel == relApart:
			continue
		case rel == relElem:
			kind = ir.EffectSet
		case rel == relAt && method != "":
			switch method {
			case "list.push", "list.insert":
				kind = ir.EffectInsert
			case "list.remove":
				kind = ir.EffectRemove
			case "list.clear":
				kind = ir.EffectClear
			}
		}
		if kind == "" || fx.changes[kind] == nil {
			if perCopy {
				// Owed to the one copy the write reached.
				after = append(after, note("mark"))
				continue
			}
			dirty = append(dirty, fx)
			continue
		}
		switch kind {
		case ir.EffectSet:
			after = append(after, note("set", st.siteIndex(idx, &before)))
		case ir.EffectClear:
			after = append(after, note("clear"))
		case ir.EffectRemove:
			call := s.(*ir.CallStmt).Call
			before = append(before, note("remove", st.siteIndex(&call.Args[1].Value, &before)))
		case ir.EffectInsert:
			if method == "list.push" {
				after = append(after, note("append"))
				continue
			}
			call := s.(*ir.CallStmt).Call
			at := st.siteIndex(&call.Args[1].Value, &before)
			before = append(before, note("length"))
			after = append(after, note("insert", at))
		}
	}
	return before, after, dirty
}

// siteIndex is an index the site evaluates once: the expression itself where
// reading it again reads the same value, or a temp bound ahead of the
// statement, which the statement then reads in its place.
func (st *effectState) siteIndex(slot *ir.Expr, before *[]ir.Stmt) ir.Expr {
	switch n := (*slot).(type) {
	case *ir.Literal:
		return deepCloneExpr(n)
	case *ir.Ident:
		switch n.Sym.(type) {
		case *ir.LoopVar, *ir.Param:
			return deepCloneExpr(n)
		}
	}
	sym := &ir.Var{Name: "__effect_at" + strconv.Itoa(st.temps), Type: ir.TypInt, Synthesized: true}
	st.temps++
	ref := func() ir.Expr { return &ir.Ident{Name: sym.Name, Type: ir.TypInt, Sym: sym, Synthesized: true} }
	*before = append(*before, &ir.LocalVar{Name: sym.Name, Type: ir.TypInt, Sym: sym, Init: *slot})
	*slot = ref()
	return ref()
}

func (st *effectState) callNote(fx *loweredEffect, which string, args ...ir.Expr) ir.Stmt {
	fn := st.noteFunc(fx, which)
	call := &ir.Call{Type: ir.TypVoid, Func: fn}
	for _, a := range args {
		call.Args = append(call.Args, ir.CallArg{Value: a})
	}
	return &ir.CallStmt{Call: call}
}

// noteFunc is fx's note of one kind of write, built on first use. Each reads
// what it records off the cell itself:
//
//	func __effectN_note_set(at int)    { <record set, at, cell[at]> }
//	func __effectN_note_append()       { <record insert, len-1, cell[len-1]> }
//	func __effectN_note_length()       { __effectN_len = cell.length() }
//	func __effectN_note_insert(at int) { if cell.length() > __effectN_len { <record insert, at, cell[at]> } }
//	func __effectN_note_remove(at int) { if at >= 0 && at < cell.length() { <record remove, at, cell[at]> } }
//	func __effectN_note_clear()        { <record clear, 0, zero> }
//
// A remove is noted before the write, while the item is still there; an
// insert or remove out of range leaves the list as it was, and reports
// nothing, so the insert's note asks whether the list grew.
func (st *effectState) noteFunc(fx *loweredEffect, which string) *ir.Func {
	if fn := fx.notes[which]; fn != nil {
		return fn
	}
	var params []*ir.Param
	param := func(name string) func() ir.Expr {
		p := &ir.Param{Name: name, Type: ir.TypInt}
		params = append(params, p)
		return func() ir.Expr { return &ir.Ident{Name: p.Name, Type: ir.TypInt, Sym: p, Synthesized: true} }
	}
	// A copy's cell is the key with the loop's index read from the note's
	// `copy`, since the note runs outside the loop.
	var copyRef func() ir.Expr
	cell := func() ir.Expr { return deepCloneExpr(fx.key) }
	if loop := fx.cell.loop; loop != nil && which != "shift" {
		copyRef = param("copy")
		cell = func() ir.Expr {
			return newExprWalker(func(e ir.Expr) ir.Expr {
				if id, isIdent := e.(*ir.Ident); isIdent && id.Sym == ir.Symbol(loop) {
					return copyRef()
				}
				return e
			}).expr(deepCloneExpr(fx.key))
		}
	}
	length := func() ir.Expr { return callListLength(cell()) }
	var at func() ir.Expr
	switch which {
	case "set", "insert", "remove":
		at = param("at")
	case "shift":
		at = param("from")
	}
	last := func() ir.Expr {
		return &ir.Binary{Type: ir.TypInt, Op: ast.BinSub, Left: length(), Right: intLiteralLit(1)}
	}
	elem := func(i ir.Expr) ir.Expr {
		return copiedValue(func() ir.Expr { return &ir.Index{Type: fx.itemVar.Type, Operand: cell(), Idx: i} }, fx.itemVar.Type)
	}
	record := func(kind int, i, item ir.Expr) []ir.Stmt {
		out := []ir.Stmt{
			callListPush(st.varIdent(fx.kinds), intLiteralLit(kind), ir.TypInt),
			callListPush(st.varIdent(fx.ats), i, ir.TypInt),
			callListPush(st.varIdent(fx.items), item, fx.itemVar.Type),
		}
		if copyRef != nil {
			out = append(out, callListPush(st.varIdent(fx.copies), copyRef(), ir.TypInt))
		}
		return out
	}
	var block []ir.Stmt
	switch which {
	case "mark":
		block = []ir.Stmt{callListPush(st.varIdent(fx.dirtyAt), copyRef(), ir.TypInt)}
	case "shift":
		// Every copy from `from` on holds another element after the write.
		pos := &ir.LoopVar{Name: fx.prefix + "_sp", Type: ir.TypInt}
		posRef := func() ir.Expr { return &ir.Ident{Name: pos.Name, Type: ir.TypInt, Sym: pos, Synthesized: true} }
		elemT := listElem(fx.cell.iterable)
		block = []ir.Stmt{&ir.For{
			Key:      pos.Name,
			KeySym:   pos,
			Value:    "_",
			Iter:     deepCloneExpr(fx.cell.iterable),
			ElemType: elemT,
			Body: []ir.Stmt{&ir.If{
				Cond: &ir.Binary{Type: ir.TypBool, Op: ast.BinGte, Left: posRef(), Right: at()},
				Body: []ir.Stmt{callListPush(st.varIdent(fx.dirtyAt), posRef(), ir.TypInt)},
			}},
		}}
	case "set":
		block = record(changeSet, at(), elem(at()))
	case "append":
		block = record(changeInsert, last(), elem(last()))
	case "length":
		block = []ir.Stmt{&ir.Assign{Target: st.varIdent(fx.length), Op: ast.AssignSet, Value: length()}}
	case "insert":
		block = []ir.Stmt{&ir.If{
			Cond: &ir.Binary{Type: ir.TypBool, Op: ast.BinGt, Left: length(), Right: st.varIdent(fx.length)},
			Body: record(changeInsert, at(), elem(at())),
		}}
	case "remove":
		block = []ir.Stmt{&ir.If{
			Cond: &ir.Binary{
				Type: ir.TypBool, Op: ast.BinAnd,
				Left:  &ir.Binary{Type: ir.TypBool, Op: ast.BinGte, Left: at(), Right: intLiteralLit(0)},
				Right: &ir.Binary{Type: ir.TypBool, Op: ast.BinLt, Left: at(), Right: length()},
			},
			Body: record(changeRemove, at(), elem(at())),
		}}
	case "clear":
		block = record(changeClear, intLiteralLit(0), ir.ZeroExpr(fx.itemVar.Type))
	default:
		panic(fmt.Sprintf("noteFunc: unknown note %q", which))
	}
	fn := &ir.Func{
		Name:        fx.prefix + "_note_" + which,
		Params:      params,
		Return:      ir.TypVoid,
		Purity:      ir.PurityMutates,
		Synthesized: true,
		Block:       block,
	}
	if fx.notes == nil {
		fx.notes = map[string]*ir.Func{}
	}
	fx.notes[which] = fn
	st.addFunc(&ir.Owner{Comp: fx.comp}, fn)
	return fn
}

// drainBlock runs what one bracket's queue holds, in the order the writes were
// made, while the position still describes the bracket, and empties it either
// way: a bracket the tree no longer holds is reported nothing.
//
//	<the if chain> {
//	    for __i, __k = __effectN_kinds {
//	        __effectN_at = __effectN_ats[__i]; __effectN_item = __effectN_items[__i]
//	        if __k == 0 { __effectN_insert() } else if …
//	    }
//	}
//	__effectN_kinds = []; __effectN_ats = []; __effectN_items = []
//
// Walked where it stands rather than taken into locals first: Kotlin assigns
// a list in place, so a local would be the queue the next statement empties.
// Nothing a handler runs can extend it -- no handler may write what `on`
// reads -- and one writing another bracket's key extends that bracket's.
func (st *effectState) drainBlock(fx *loweredEffect) []ir.Stmt {
	return append(st.positionBlock(fx.prefix, fx.frames, []ir.Stmt{st.drainLoop(fx)}), st.clearQueue(fx)...)
}

// drainLoop runs the queue's records for the bracket the position describes
// where it stands -- under a loop stepping into the key, the one copy that
// loop's index names.
func (st *effectState) drainLoop(fx *loweredEffect) ir.Stmt {
	idx := &ir.LoopVar{Name: fx.prefix + "_ci", Type: ir.TypInt}
	kind := &ir.LoopVar{Name: fx.prefix + "_ck", Type: ir.TypInt}
	idxRef := func() ir.Expr { return &ir.Ident{Name: idx.Name, Type: ir.TypInt, Sym: idx, Synthesized: true} }
	kindRef := func() ir.Expr { return &ir.Ident{Name: kind.Name, Type: ir.TypInt, Sym: kind, Synthesized: true} }
	var dispatch []ir.Stmt
	for _, ev := range slices.Backward(ir.EffectDeltas) {
		h := fx.changes[ev]
		if h == nil {
			continue
		}
		branch := &ir.If{
			Cond: &ir.Binary{Type: ir.TypBool, Op: ast.BinEq, Left: kindRef(), Right: intLiteralLit(changeKinds[ev])},
			Body: callHandler(h),
			Else: dispatch,
		}
		dispatch = []ir.Stmt{branch}
	}
	body := []ir.Stmt{
		&ir.Assign{Target: st.varIdent(fx.atVar), Op: ast.AssignSet, Value: &ir.Index{Type: ir.TypInt, Operand: st.varIdent(fx.ats), Idx: idxRef()}},
		&ir.Assign{Target: st.varIdent(fx.itemVar), Op: ast.AssignSet, Value: &ir.Index{Type: fx.itemVar.Type, Operand: st.varIdent(fx.items), Idx: idxRef()}},
	}
	body = append(body, dispatch...)
	if fx.copies != nil {
		// Only this copy's records: the loop the position rebuilds binds the
		// index the key steps through.
		body = []ir.Stmt{&ir.If{
			Cond: &ir.Binary{
				Type: ir.TypBool, Op: ast.BinEq,
				Left:  &ir.Index{Type: ir.TypInt, Operand: st.varIdent(fx.copies), Idx: idxRef()},
				Right: &ir.Ident{Name: fx.cell.loop.Name, Type: ir.TypInt, Sym: fx.cell.loop, Synthesized: true},
			},
			Body: body,
		}}
	}
	loop := &ir.For{
		Key:      idx.Name,
		KeySym:   idx,
		Value:    kind.Name,
		ValueSym: kind,
		Iter:     st.varIdent(fx.kinds),
		ElemType: ir.TypInt,
		Body:     body,
	}
	return loop
}

// clearQueue empties a bracket's queue.
func (st *effectState) clearQueue(fx *loweredEffect) []ir.Stmt {
	var out []ir.Stmt
	for _, q := range []*ir.Var{fx.kinds, fx.ats, fx.items, fx.copies} {
		if q != nil {
			out = append(out, &ir.Assign{Target: st.varIdent(q), Op: ast.AssignSet, Value: &ir.ListLit{Type: q.Type}})
		}
	}
	return out
}

// copyReportBlock is reportBlock for a bracket under a loop that steps into
// its key, where each copy is a cell of its own: in the position the loop
// rebuilds, a copy runs the records of the writes that reached it, then its
// `@update` if one was owed to it or to every copy.
//
//	<the position> {
//	    <this copy's records>
//	    __effectN_hit := __effectN_dirty
//	    for _, __p = __effectN_dirtyAt { if __p == <index> { __effectN_hit = true } }
//	    if __effectN_hit { __effectN_key = <on>; __effectN_update() }
//	}
//	__effectN_dirty = false; __effectN_dirtyAt = []; <the queue emptied>
func (st *effectState) copyReportBlock(prefix string, fx *loweredEffect) []ir.Stmt {
	var leaf []ir.Stmt
	if fx.kinds != nil {
		leaf = append(leaf, st.drainLoop(fx))
	}
	hit := &ir.Var{Name: fx.prefix + "_hit", Type: ir.TypBool, Synthesized: true}
	hitRef := func() ir.Expr { return &ir.Ident{Name: hit.Name, Type: ir.TypBool, Sym: hit} }
	p := &ir.LoopVar{Name: fx.prefix + "_p", Type: ir.TypInt}
	index := func() ir.Expr {
		return &ir.Ident{Name: fx.cell.loop.Name, Type: ir.TypInt, Sym: fx.cell.loop, Synthesized: true}
	}
	run := []ir.Stmt{
		&ir.Assign{Target: st.varIdent(fx.keyVar), Op: ast.AssignSet, Value: deepCloneExpr(fx.key)},
	}
	run = append(run, callHandler(fx.update)...)
	leaf = append(leaf,
		&ir.LocalVar{Name: hit.Name, Type: ir.TypBool, Sym: hit, Init: st.varIdent(fx.dirty)},
		&ir.For{
			Key:      p.Name,
			KeySym:   p,
			Iter:     st.varIdent(fx.dirtyAt),
			ElemType: ir.TypInt,
			Body: []ir.Stmt{&ir.If{
				Cond: &ir.Binary{Type: ir.TypBool, Op: ast.BinEq, Left: &ir.Ident{Name: p.Name, Type: ir.TypInt, Sym: p, Synthesized: true}, Right: index()},
				Body: []ir.Stmt{&ir.Assign{Target: hitRef(), Op: ast.AssignSet, Value: &ir.Literal{Type: ir.TypBool, Value: "true"}}},
			}},
		},
		&ir.If{Cond: hitRef(), Body: run},
	)
	out := st.positionBlock(prefix, fx.frames, leaf)
	out = append(out,
		st.setFlag(fx.dirty, false),
		&ir.Assign{Target: st.varIdent(fx.dirtyAt), Op: ast.AssignSet, Value: &ir.ListLit{Type: fx.dirtyAt.Type}},
	)
	return append(out, st.clearQueue(fx)...)
}

// lowerChanges lifts a bracket's list change handlers and gives it the queue
// they are run from. Its handlers' `at` and `item` are substituted with two
// scope vars the drain sets before each call, as the key is for the others.
func (st *effectState) lowerChanges(n *ir.NodeInst, o *ir.Owner, fx *loweredEffect, key ir.Expr) error {
	var handlers []*ir.EventHandler
	for i := range n.Handlers {
		if _, isChange := changeKinds[n.Handlers[i].Name]; isChange && n.Handlers[i].Func != nil {
			handlers = append(handlers, &n.Handlers[i])
		}
	}
	if len(handlers) == 0 {
		return nil
	}
	pos := ir.NodePos(n)
	if handlers[0].AST != nil {
		pos = handlers[0].AST.Pos
	}
	if fx.cell == nil {
		return fmt.Errorf("%s: an effect's @%s needs `on` to name a var or a field of one on a compiled target; this one is written where the program cannot see the write", pos, handlers[0].Name)
	}
	elem := listElem(key)
	if elem == nil {
		return fmt.Errorf("%s: an effect's @%s needs `on` to be a list", pos, handlers[0].Name)
	}
	newVar := func(suffix string, t *ir.Type, init ir.Expr) *ir.Var {
		v := &ir.Var{Name: fx.prefix + suffix, Type: t, Init: init, Synthesized: true}
		st.addVar(o, v)
		return v
	}
	fx.atVar = newVar("_at", ir.TypInt, intLiteralLit(0))
	fx.itemVar = newVar("_item", elem, ir.ZeroExpr(elem))
	fx.kinds = newVar("_kinds", ir.ListOf(ir.TypInt), &ir.ListLit{Type: ir.ListOf(ir.TypInt)})
	fx.ats = newVar("_ats", ir.ListOf(ir.TypInt), &ir.ListLit{Type: ir.ListOf(ir.TypInt)})
	fx.items = newVar("_items", ir.ListOf(elem), &ir.ListLit{Type: ir.ListOf(elem)})
	if fx.cell.loop != nil {
		fx.copies = newVar("_copies", ir.ListOf(ir.TypInt), &ir.ListLit{Type: ir.ListOf(ir.TypInt)})
	}
	fx.changes = map[string]*ir.Func{}
	for _, h := range handlers {
		subst := map[ir.Symbol]*ir.Var{}
		for i, p := range h.Func.Params {
			switch i {
			case 0:
				subst[p] = fx.atVar
			case 1:
				subst[p] = fx.itemVar
			}
		}
		block := newExprWalker(func(e ir.Expr) ir.Expr {
			if id, isIdent := e.(*ir.Ident); isIdent {
				if v := subst[id.Sym]; v != nil {
					return st.varIdent(v)
				}
			}
			return e
		}).stmts(h.Func.Block)
		fn := &ir.Func{Name: fx.prefix + "_" + h.Name, Block: block, Return: ir.TypVoid, Purity: ir.PurityMutates}
		st.addFunc(o, fn)
		fx.changes[h.Name] = fn
		if h.Name == ir.EffectInsert {
			fx.length = newVar("_len", ir.TypInt, intLiteralLit(0))
		}
	}
	return nil
}

// copiedValue is the value read now, held apart from where it was read: the
// item a change event is handed is the element as the write left it, and a
// later write in the same handler must not move it. A struct is rebuilt field
// by field, since JavaScript holds one by reference; a struct nested in one is
// rebuilt too, and anything else is the value read. read is called once per
// field, so each reads the element afresh.
//
// A generic or host struct is read as it is: neither has a literal every
// target can write.
func copiedValue(read func() ir.Expr, t *ir.Type) ir.Expr {
	def, isStruct := structDefOfType(t)
	if !isStruct || len(def.TypeParams) > 0 || def.Foreign.Name != "" || def.RecordOf != nil {
		return read()
	}
	lit := &ir.StructLit{Type: t, Def: def}
	for _, f := range def.Fields {
		lit.Fields = append(lit.Fields, ir.FieldInit{
			Name: f.Name,
			Value: copiedValue(func() ir.Expr {
				return &ir.Select{Type: f.Type, Operand: read(), Field: f.Name}
			}, f.Type),
		})
	}
	return lit
}

func structDefOfType(t *ir.Type) (*ir.StructDef, bool) {
	if t == nil || t.Kind != ir.TypeStruct {
		return nil, false
	}
	def, ok := t.Decl.(*ir.StructDef)
	return def, ok && def != nil
}
