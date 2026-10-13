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

// effectCell is the cell a key names: a var and the fields below it.
type effectCell struct {
	root   *ir.Var
	fields []string
}

// cellOfKey is the cell `on` names, or nil where it names none -- a computed
// key, or a prop the inliner left standing, which is written by whoever bound
// it and so at no write site this program shows.
func cellOfKey(key ir.Expr) *effectCell {
	var fields []string
	for {
		switch n := key.(type) {
		case *ir.Select:
			fields = append([]string{n.Field}, fields...)
			key = n.Operand
		case *ir.Ident:
			if v, isVar := n.Sym.(*ir.Var); isVar {
				return &effectCell{root: v, fields: fields}
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

// relate is how steps, below the cell's var, stand to the cell's fields, and
// for relElem the slot holding the element's index.
func (c *effectCell) relate(steps []pathStep) (relation, *ir.Expr) {
	for i, f := range c.fields {
		if i == len(steps) {
			return relAbove, nil
		}
		if steps[i].idx != nil || steps[i].field != f {
			return relApart, nil
		}
	}
	if len(steps) == len(c.fields) {
		return relAt, nil
	}
	if s := steps[len(c.fields)]; s.idx != nil {
		return relElem, s.idx
	}
	return relAbove, nil
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
		rel, idx := fx.cell.relate(steps)
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
			dirty = append(dirty, fx)
			continue
		}
		switch kind {
		case ir.EffectSet:
			after = append(after, st.callNote(fx, "set", st.siteIndex(idx, &before)))
		case ir.EffectClear:
			after = append(after, st.callNote(fx, "clear"))
		case ir.EffectRemove:
			call := s.(*ir.CallStmt).Call
			before = append(before, st.callNote(fx, "remove", st.siteIndex(&call.Args[1].Value, &before)))
		case ir.EffectInsert:
			if method == "list.push" {
				after = append(after, st.callNote(fx, "append"))
				continue
			}
			call := s.(*ir.CallStmt).Call
			at := st.siteIndex(&call.Args[1].Value, &before)
			before = append(before, st.callNote(fx, "length"))
			after = append(after, st.callNote(fx, "insert", at))
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
	cell := func() ir.Expr { return deepCloneExpr(fx.key) }
	length := func() ir.Expr { return callListLength(cell()) }
	var params []*ir.Param
	var at func() ir.Expr
	if which == "set" || which == "insert" || which == "remove" {
		p := &ir.Param{Name: "at", Type: ir.TypInt}
		params = []*ir.Param{p}
		at = func() ir.Expr { return &ir.Ident{Name: p.Name, Type: ir.TypInt, Sym: p, Synthesized: true} }
	}
	last := func() ir.Expr {
		return &ir.Binary{Type: ir.TypInt, Op: ast.BinSub, Left: length(), Right: intLiteralLit(1)}
	}
	elem := func(i ir.Expr) ir.Expr {
		return copiedValue(func() ir.Expr { return &ir.Index{Type: fx.itemVar.Type, Operand: cell(), Idx: i} }, fx.itemVar.Type)
	}
	record := func(kind int, i, item ir.Expr) []ir.Stmt {
		return []ir.Stmt{
			callListPush(st.varIdent(fx.kinds), intLiteralLit(kind), ir.TypInt),
			callListPush(st.varIdent(fx.ats), i, ir.TypInt),
			callListPush(st.varIdent(fx.items), item, fx.itemVar.Type),
		}
	}
	var block []ir.Stmt
	switch which {
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
	loop := &ir.For{
		Key:      idx.Name,
		KeySym:   idx,
		Value:    kind.Name,
		ValueSym: kind,
		Iter:     st.varIdent(fx.kinds),
		ElemType: ir.TypInt,
		Body:     body,
	}
	out := st.positionBlock(fx.prefix, fx.frames, []ir.Stmt{loop})
	for _, q := range []*ir.Var{fx.kinds, fx.ats, fx.items} {
		out = append(out, &ir.Assign{Target: st.varIdent(q), Op: ast.AssignSet, Value: &ir.ListLit{Type: q.Type}})
	}
	return out
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
