package lower

import (
	"fmt"
	"slices"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// perCopyCells gives each var of a component spliced under loops one cell per
// copy of those loops, for a target that keeps no state of an instance's own
// (Features.InstanceState). Hoisted as it is, a var is one cell every copy
// writes: two rows of a list share one counter.
//
// The var becomes a map from the copy's key to its value -- the loops' indices,
// joined -- and every use in the body goes through it: a read is
// `cell.get(key, init)`, so a copy nothing has written yet reads the value the
// var was declared with, and a write goes through a temporary it is stored back
// from, which covers an assignment, a toggle and a mutating method alike. The
// initializer is read where the copy is, so one naming a prop reads that copy's
// argument. A func of the component's that touches a cell is handed the key as
// a trailing parameter.
//
// Keyed by position, as a composable's state is by default: a list the program
// reorders keeps each cell with the position rather than the element.
func perCopyCells(at string, body []ir.Stmt, vars []*ir.Var, funcs []*ir.Func, loops []*ir.For) ([]ir.Stmt, error) {
	cells := map[*ir.Var]ir.Expr{}
	for _, v := range vars {
		init := v.Init
		if init == nil {
			init = ir.DeclaredDefault(v.Type)
		}
		cells[v] = init
	}
	for _, v := range vars {
		v.Type = ir.MapOf(ir.TypString, v.Type)
		v.Init = &ir.MapLitIR{Type: v.Type}
	}

	keyed := cellFuncs(funcs, cells)
	temps := new(int)
	for fn, param := range keyed {
		fn.Params = append(fn.Params, param)
		fn.Block = rewriteCells(fn.Block, cells, keyed, temps, func() ir.Expr { return paramIdent(param) })
	}
	restore, err := keyEffectsByCopy(at, body, cells, keyed, temps, loops)
	if err != nil {
		return nil, err
	}
	body = rewriteCells(body, cells, keyed, temps, func() ir.Expr { return CopyKey(loops) })
	restore()
	passLoopVars(body, funcs, loops)
	seen := map[string]bool{}
	uniqueCellTemps(body, seen)
	for _, fn := range funcs {
		uniqueCellTemps(fn.Block, seen)
	}
	return body, nil
}

// uniqueCellTemps gives each cell write's temporary a name of its own. A
// write of an enclosing component's cell reaches a body spliced inside it
// through a two-way binding, as a copy of the write already rewritten --
// async.query's `pending = true` and `pending = false`, writing the cell of
// the wrapper holding its unbound props -- so two copies declared one name,
// which the fold splicing each `if true` into its list made one scope.
func uniqueCellTemps(stmts []ir.Stmt, seen map[string]bool) {
	_ = ir.Walk(stmts, func(nd ir.Node) error {
		w, ok := nd.(*ir.If)
		if !ok || len(w.Body) == 0 {
			return nil
		}
		lit, ok := w.Cond.(*ir.Literal)
		if !ok || lit.Value != "true" {
			return nil
		}
		lv, ok := w.Body[0].(*ir.LocalVar)
		if !ok || lv.Sym == nil || !lv.Sym.Synthesized || !strings.HasPrefix(lv.Name, "__cell") {
			return nil
		}
		if !seen[lv.Name] {
			seen[lv.Name] = true
			return nil
		}
		name := lv.Name
		for k := 2; seen[name]; k++ {
			name = fmt.Sprintf("%s_%d", lv.Name, k)
		}
		seen[name] = true
		old := lv.Sym
		fresh := &ir.Var{Name: name, Type: old.Type, Synthesized: true}
		lv.Name, lv.Sym = name, fresh
		_ = ir.Rewrite(w.Body[1:], func(n ir.Node) (ir.Node, error) {
			if id, ok := n.(*ir.Ident); ok && id.Sym == ir.Symbol(old) {
				return &ir.Ident{Name: name, Type: id.Type, Sym: fresh, Synthesized: true}, nil
			}
			return n, nil
		})
		return nil
	})
}

// keyEffectsByCopy routes the cells an effect's handlers touch through the
// copy's key, which the handlers are handed rather than read: passEffect runs
// them from the settle, outside the loops the key is built from. So the
// effect is keyed `on` the copy's key -- which the settle evaluates where the
// copy is, and hands each handler -- and each handler reads its cells through
// the parameter that receives it. An effect keyed `on` something already --
// async.query's, in a card per row -- is keyed on the pair, `{copy, on}`,
// and its handlers read each half off what they are handed: a change of
// either is a change of key, as it was of the one.
//
// The handlers are rewritten here and held out of the body's rewrite, which
// would otherwise route the cells it already routed a second time; restore
// puts them back.
func keyEffectsByCopy(at string, body []ir.Stmt, cells map[*ir.Var]ir.Expr, keyed map[*ir.Func]*ir.Param, temps *int, loops []*ir.For) (restore func(), err error) {
	var held []func()
	restore = func() {
		for _, f := range held {
			f()
		}
	}
	_ = ir.WalkStmts(body, func(s ir.Stmt) error {
		n, ok := s.(*ir.NodeInst)
		if !ok || !isEffectNode(n) || !handlersTouchCells(n, cells, keyed) {
			return nil
		}
		// Keyed by the copy already, by the splice of a component inside this
		// one -- async.query inside the wrapper holding its unbound props --
		// whose loops are these: each handler reads the key it was handed
		// into its local, which routes these cells too.
		if keyedByCopy(n) {
			for i := range n.Handlers {
				fn := n.Handlers[i].Func
				if fn == nil {
					continue
				}
				local := copyKeyLocal(fn)
				block := rewriteCells(fn.Block, cells, keyed, temps, func() ir.Expr { return CopyKey(loops) })
				block = keyPartsFrom(block, loops, func() ir.Expr {
					return &ir.Ident{Name: local.Name, Type: local.Type, Sym: local, Synthesized: true}
				})
				fn.Block = nil
				held = append(held, func() { fn.Block = block })
			}
			return nil
		}
		on := effectKeyExpr(n)
		if on == nil {
			for _, h := range n.Handlers {
				if h.Func != nil && len(h.Func.Params) > 0 {
					err = fmt.Errorf("%s: an effect in a component built once per copy of a loop reads that component's state, and its @%s takes a parameter with no `on` to fill it; drop the parameter", at, h.Name)
					return ir.SkipAll
				}
			}
		}
		keyType := ir.TypString
		key := ir.Expr(CopyKey(loops))
		if on != nil {
			pair := &ir.StructDef{Anon: true, Fields: []*ir.StructField{
				{Name: "copy", Type: ir.TypString},
				{Name: "on", Type: on.ExprType()},
			}}
			keyType = pair.SymType()
			key = &ir.StructLit{Type: keyType, Def: pair, Fields: []ir.FieldInit{
				{Name: "copy", Value: key},
				{Name: "on", Value: on},
			}}
			n.Props = slices.DeleteFunc(n.Props, func(a ir.Arg) bool { return a.Name == ir.EffectOn })
			if len(n.TypeArgs) > 0 {
				n.TypeArgs = slices.Clone(n.TypeArgs)
				n.TypeArgs[0] = keyType
			}
		}
		n.Props = append(n.Props, ir.Arg{Name: ir.EffectOn, Value: key})
		for i := range n.Handlers {
			h := &n.Handlers[i]
			if h.Func == nil {
				continue
			}
			param := &ir.Param{Name: "__copy", Type: keyType}
			// The halves are read into locals where the handler starts: the
			// key is what the settle hands it now, and a mount that waits
			// writes its cells after the wait, when a later copy's mount may
			// have been handed another -- a local is what that tail captures.
			var entry []ir.Stmt
			local := func(name string, t *ir.Type, init ir.Expr) func() ir.Expr {
				v := &ir.Var{Name: name, Type: t, Synthesized: true}
				entry = append(entry, &ir.LocalVar{Name: name, Type: t, Init: init, Sym: v})
				return func() ir.Expr { return &ir.Ident{Name: name, Type: t, Sym: v, Synthesized: true} }
			}
			var copyInit ir.Expr = paramIdent(param)
			if on != nil {
				copyInit = &ir.Select{Type: ir.TypString, Operand: paramIdent(param), Field: "copy"}
			}
			copyOf := local(copyKeyName, ir.TypString, copyInit)
			var was *ir.Param
			var onOf func() ir.Expr
			if on != nil {
				if len(h.Func.Params) > 0 {
					was = h.Func.Params[0]
					onOf = local(was.Name, was.Type, &ir.Select{Type: was.Type, Operand: paramIdent(param), Field: "on"})
				}
			}
			h.Func.Params = []*ir.Param{param}
			block := rewriteCells(h.Func.Block, cells, keyed, temps, func() ir.Expr { return CopyKey(loops) })
			block = keyPartsFrom(block, loops, copyOf)
			if was != nil {
				block = newExprWalker(func(e ir.Expr) ir.Expr {
					if id, ok := e.(*ir.Ident); ok && id.Sym == ir.Symbol(was) {
						return onOf()
					}
					return e
				}).stmts(block)
			}
			block = append(entry, block...)
			fn := h.Func
			fn.Block = nil
			held = append(held, func() { fn.Block = block })
		}
		return nil
	})
	if err != nil {
		restore()
	}
	return restore, err
}

// copyKeyName is the local an effect's handler reads its copy's key into
// where it starts (keyEffectsByCopy).
const copyKeyName = "__copyKey"

// copyKeyLocal is the local fn reads its copy's key into, nil for a handler
// no splice keyed by its copy.
func copyKeyLocal(fn *ir.Func) *ir.Var {
	for _, st := range fn.Block {
		if lv, ok := st.(*ir.LocalVar); ok && lv.Name == copyKeyName && lv.Sym != nil && lv.Sym.Synthesized {
			return lv.Sym
		}
	}
	return nil
}

// keyedByCopy reports whether a splice already keyed n's handlers by the copy.
func keyedByCopy(n *ir.NodeInst) bool {
	for _, h := range n.Handlers {
		if h.Func != nil && copyKeyLocal(h.Func) != nil {
			return true
		}
	}
	return false
}

// keyPartsFrom reads each loop index a copy key is built from out of param,
// which holds the whole key (CopyKey): `string(__copy0)` is its first part.
// That covers every key a splice wrote into the handlers, an enclosing
// component's included -- a handler its call site wrote reads that
// component's cells by the loops around it, which are the outer of these.
// An index read any other way is the program's, and passEffect says where.
func keyPartsFrom(block []ir.Stmt, loops []*ir.For, key func() ir.Expr) []ir.Stmt {
	part := map[*ir.LoopVar]int{}
	for k, fs := range loops {
		part[loopIndex(fs, k)] = k
	}
	_ = ir.Rewrite(block, func(nd ir.Node) (ir.Node, error) {
		c, ok := nd.(*ir.Conversion)
		if !ok || c.Type.Kind != ir.TypeString {
			return nd, nil
		}
		id, ok := c.Operand.(*ir.Ident)
		if !ok {
			return nd, nil
		}
		lv, ok := id.Sym.(*ir.LoopVar)
		if !ok {
			return nd, nil
		}
		k, ok := part[lv]
		if !ok {
			return nd, nil
		}
		if len(loops) == 1 {
			return key(), nil
		}
		parts := ir.ListOf(ir.TypString)
		split := &ir.Call{
			Type: parts,
			Func: ir.IntrinsicMethod("split", "string.split", ir.TypString, parts),
			Args: []ir.CallArg{{Value: key()}, {Value: stringLit("/")}},
		}
		return &ir.Index{Type: ir.TypString, Operand: split, Idx: intLiteralLit(k)}, nil
	})
	return block
}

// handlersTouchCells reports whether any of n's handlers reads or writes a
// cell, directly or through a func handed the key.
func handlersTouchCells(n *ir.NodeInst, cells map[*ir.Var]ir.Expr, keyed map[*ir.Func]*ir.Param) bool {
	touches := false
	for _, h := range n.Handlers {
		if h.Func == nil {
			continue
		}
		_ = ir.Walk(h.Func.Block, func(nd ir.Node) error {
			switch x := nd.(type) {
			case *ir.Ident:
				if v, ok := x.Sym.(*ir.Var); ok && cells[v] != nil {
					touches = true
				}
			case *ir.Call:
				if keyed[x.Func] != nil {
					touches = true
				}
			}
			if touches {
				return ir.SkipAll
			}
			return nil
		})
	}
	return touches
}

// passLoopVars hands each func the loop variables it reads as parameters.
// The funcs are hoisted to the owner, outside the loops, while a prop the
// instance was given -- `row(label = it)` -- was substituted into them as the
// loop variable it names, which is in scope only where the copy is.
func passLoopVars(body []ir.Stmt, funcs []*ir.Func, loops []*ir.For) {
	loopVars := map[*ir.LoopVar]bool{}
	for _, fs := range loops {
		for _, lv := range []*ir.LoopVar{fs.KeySym, fs.ValueSym} {
			if lv != nil {
				loopVars[lv] = true
			}
		}
	}
	if len(loopVars) == 0 {
		return
	}
	reads := map[*ir.Func][]*ir.LoopVar{}
	has := func(fn *ir.Func, lv *ir.LoopVar) bool { return slices.Contains(reads[fn], lv) }
	for changed := true; changed; {
		changed = false
		for _, fn := range funcs {
			if fn == nil {
				continue
			}
			_ = ir.Walk(fn.Block, func(nd ir.Node) error {
				var need []*ir.LoopVar
				switch x := nd.(type) {
				case *ir.Ident:
					if lv, ok := x.Sym.(*ir.LoopVar); ok && loopVars[lv] {
						need = []*ir.LoopVar{lv}
					}
					if f, ok := x.Sym.(*ir.Func); ok {
						need = reads[f]
					}
				case *ir.Call:
					need = reads[x.Func]
				}
				for _, lv := range need {
					if !has(fn, lv) {
						reads[fn] = append(reads[fn], lv)
						changed = true
					}
				}
				return nil
			})
		}
	}
	params := map[*ir.Func]map[*ir.LoopVar]*ir.Param{}
	arity := map[*ir.Func]int{}
	for fn, lvs := range reads {
		arity[fn] = len(fn.Params)
		params[fn] = map[*ir.LoopVar]*ir.Param{}
		for _, lv := range lvs {
			p := &ir.Param{Name: lv.Name, Type: lv.Type}
			fn.Params = append(fn.Params, p)
			params[fn][lv] = p
		}
	}
	// Inside a func a loop variable is its parameter; in the body it is itself.
	route := func(stmts []ir.Stmt, own map[*ir.LoopVar]*ir.Param) {
		arg := func(lv *ir.LoopVar) ir.Expr {
			if p := own[lv]; p != nil {
				return paramIdent(p)
			}
			return &ir.Ident{Name: lv.Name, Type: lv.Type, Sym: lv}
		}
		_ = ir.Rewrite(stmts, func(nd ir.Node) (ir.Node, error) {
			switch x := nd.(type) {
			case *ir.Ident:
				if lv, ok := x.Sym.(*ir.LoopVar); ok && own[lv] != nil {
					return paramIdent(own[lv]), nil
				}
				// A func taken as a value is bound to the loop variables
				// where it is taken, as a call passes them where it is made.
				if f, ok := x.Sym.(*ir.Func); ok && len(reads[f]) > 0 {
					return loopBoundValue(x, f, arity[f], reads[f], arg), ir.SkipDir
				}
			case *ir.Call:
				for _, lv := range reads[x.Func] {
					x.Args = append(x.Args, ir.CallArg{Value: arg(lv)})
				}
			}
			return nd, nil
		})
	}
	for _, fn := range funcs {
		if fn != nil {
			route(fn.Block, params[fn])
		}
	}
	route(body, nil)
}

// loopBoundValue is a lambda standing for fn taken as a value: it takes fn's
// own arity parameters and passes them on, followed by the loop variables fn
// was given (passLoopVars).
func loopBoundValue(id *ir.Ident, fn *ir.Func, arity int, lvs []*ir.LoopVar, arg func(*ir.LoopVar) ir.Expr) ir.Expr {
	wrapper := &ir.Func{Return: fn.Return, Purity: fn.Purity, CanError: fn.CanError, IsAsync: fn.IsAsync}
	call := &ir.Call{Type: fn.Return, Func: fn}
	for _, p := range fn.Params[:arity] {
		if p.Receiver {
			continue
		}
		np := &ir.Param{Name: p.Name, Type: p.Type}
		wrapper.Params = append(wrapper.Params, np)
		call.Args = append(call.Args, ir.CallArg{Value: paramIdent(np)})
	}
	for _, lv := range lvs {
		call.Args = append(call.Args, ir.CallArg{Value: arg(lv)})
	}
	if fn.Return == nil || fn.Return.Kind == ir.TypeVoid {
		wrapper.Block = []ir.Stmt{&ir.CallStmt{Call: call}}
	} else {
		wrapper.Block = []ir.Stmt{&ir.Return{Value: call}}
	}
	return &ir.Lambda{Type: id.Type, Func: wrapper}
}

// cellFuncs is the funcs that read or write a cell, directly or through one
// another, each with the parameter its key arrives in.
func cellFuncs(funcs []*ir.Func, cells map[*ir.Var]ir.Expr) map[*ir.Func]*ir.Param {
	out := map[*ir.Func]*ir.Param{}
	for changed := true; changed; {
		changed = false
		for _, fn := range funcs {
			if fn == nil || out[fn] != nil {
				continue
			}
			touches := false
			_ = ir.Walk(fn.Block, func(nd ir.Node) error {
				switch x := nd.(type) {
				case *ir.Ident:
					if v, ok := x.Sym.(*ir.Var); ok && cells[v] != nil {
						touches = true
					}
				case *ir.Call:
					if out[x.Func] != nil {
						touches = true
					}
				}
				return nil
			})
			if touches {
				out[fn] = &ir.Param{Name: "__copy", Type: ir.TypString}
				changed = true
			}
		}
	}
	return out
}

func paramIdent(p *ir.Param) ir.Expr {
	return &ir.Ident{Name: p.Name, Type: p.Type, Sym: p, Synthesized: true}
}

// CopyKey is the key of the copy the enclosing loops are on: each loop's index,
// outermost first. A loop that binds only its element is given an index, the
// way the focus lowering gives one a key, so a copy's position does not depend
// on its elements being distinct.
func CopyKey(loops []*ir.For) ir.Expr {
	indices := make([]ir.Expr, len(loops))
	for i, fs := range loops {
		idx := loopIndex(fs, i)
		indices[i] = &ir.Ident{Name: idx.Name, Type: idx.Type, Sym: idx, Synthesized: true}
	}
	return ir.CopyKey(indices)
}

// BlockLoops is the loops that make copies of what block b of s holds, given
// loops, those around s: a `for`'s body is one more, and the content of a
// record in its container's loop over the records one fewer
// (ir.RecordBranchOf).
func BlockLoops(s ir.Stmt, b *[]ir.Stmt, loops []*ir.For) []*ir.For {
	switch x := s.(type) {
	case *ir.For:
		if b == &x.Body {
			return append(slices.Clip(loops), x)
		}
	case *ir.If:
		if f := ir.RecordBranchOf(x.Cond, loops); f != nil && b == &x.Body {
			return slices.DeleteFunc(slices.Clone(loops), func(l *ir.For) bool { return l == f })
		}
	}
	return loops
}

// loopIndex is the loop's index variable, rewriting a single-variable loop
// into the two-variable form -- index in Key, element in Value -- that every
// consumer downstream already reads Key off.
func loopIndex(fs *ir.For, depth int) *ir.LoopVar {
	if fs.Value == "" {
		// A list the checker wrapped as a sequence -- a counted one folded to
		// its elements -- is walked as the list, which hands out an index; a
		// pull sequence would be given a counter beside the loop, which a
		// view that unrolls the loop cannot read.
		if conv, ok := fs.Iter.(*ir.Conversion); ok && conv.Type != nil && conv.Type.Kind == ir.TypeIter {
			if t := conv.Operand.ExprType(); t != nil && t.Kind == ir.TypeList {
				fs.Iter = conv.Operand
			}
		}
		fs.Value, fs.ValueSym = fs.Key, fs.KeySym
		// A loop binding nothing is given an element of its own: a counted
		// loop counts on its element, and `_` is no counter.
		if fs.Value == "" {
			fs.Value = fmt.Sprintf("__copyElem%d", depth)
			fs.ValueSym = &ir.LoopVar{Name: fs.Value, Type: fs.ElemType}
		}
		fs.Key = fmt.Sprintf("__copy%d", depth)
		fs.KeySym = &ir.LoopVar{Name: fs.Key, Type: ir.TypInt}
		// passIterKind classified the single-variable loop; a head written
		// from the old kind binds the element to the index's name.
		fs.Counted = ir.CountedSeq(fs)
		fs.IterKind = ir.DeriveIterKind(fs)
	}
	if fs.KeySym == nil {
		fs.KeySym = &ir.LoopVar{Name: fs.Key, Type: ir.TypInt}
	}
	return fs.KeySym
}

func stringLit(s string) *ir.Literal {
	return &ir.Literal{Type: ir.TypString, Value: s}
}

// rewriteCells routes every use of a cell in stmts through its map.
func rewriteCells(stmts []ir.Stmt, cells map[*ir.Var]ir.Expr, keyed map[*ir.Func]*ir.Param, temps *int, key func() ir.Expr) []ir.Stmt {
	var visit func(ir.Node) (ir.Node, error)
	rewrite := func(stmts []ir.Stmt) []ir.Stmt {
		_ = ir.Rewrite(stmts, visit)
		return stmts
	}
	visit = func(nd ir.Node) (ir.Node, error) {
		switch x := nd.(type) {
		case *ir.Ident:
			if v, ok := x.Sym.(*ir.Var); ok && cells[v] != nil {
				return cellGet(v, rewriteCellExpr(deepCloneExpr(cells[v]), cells, keyed, temps, key), key()), ir.SkipDir
			}
		case *ir.Call:
			if p := keyed[x.Func]; p != nil {
				x.Args = append(x.Args, ir.CallArg{Value: key()})
			}
		case ir.Stmt:
			v := writtenCell(x, cells)
			if v == nil {
				return nd, nil
			}
			// The statement against a temporary holding the copy's value, then
			// stored back: the three reach the fold after lowering as an `if
			// true`, which it splices into the list the write was in -- so each
			// temporary needs a name of its own there.
			*temps++
			tmp := &ir.Var{Name: fmt.Sprintf("__cell%d_%s", *temps, v.Name), Type: v.Type.Elems[1], Synthesized: true}
			retargetCell(x, v, tmp)
			inner := rewrite([]ir.Stmt{x})
			block := []ir.Stmt{
				&ir.LocalVar{Name: tmp.Name, Type: tmp.Type, Init: cellGet(v, rewriteCellExpr(deepCloneExpr(cells[v]), cells, keyed, temps, key), key()), Sym: tmp},
			}
			block = append(block, inner...)
			block = append(block, &ir.Assign{
				Target: &ir.Index{Type: tmp.Type, Operand: cellIdent(v), Idx: key()},
				Op:     ast.AssignSet,
				Value:  &ir.Ident{Name: tmp.Name, Type: tmp.Type, Sym: tmp, Synthesized: true},
			})
			return &ir.If{Cond: &ir.Literal{Type: ir.TypBool, Value: "true"}, Body: block}, ir.SkipDir
		}
		return nd, nil
	}
	return rewrite(stmts)
}

// rewriteCellExpr is rewriteCells for a lone expression: an initializer, which
// may read another of the component's vars and is read where its copy is.
func rewriteCellExpr(e ir.Expr, cells map[*ir.Var]ir.Expr, keyed map[*ir.Func]*ir.Param, temps *int, key func() ir.Expr) ir.Expr {
	if e == nil {
		return nil
	}
	holder := []ir.Stmt{&ir.Return{Value: e}}
	holder = rewriteCells(holder, cells, keyed, temps, key)
	return holder[0].(*ir.Return).Value
}

func cellIdent(v *ir.Var) *ir.Ident {
	return &ir.Ident{Name: v.Name, Type: v.Type, Sym: v}
}

func cellGet(v *ir.Var, init, key ir.Expr) ir.Expr {
	elem := v.Type.Elems[1]
	return &ir.Call{
		Type: elem,
		Func: ir.IntrinsicMethod("get", "map.get", v.Type, elem),
		Args: []ir.CallArg{{Value: cellIdent(v)}, {Value: key}, {Value: init}},
	}
}

// writtenCell is the cell a statement writes, if it writes one: the root of an
// assignment's or a toggle's target, or the receiver of a method that mutates
// it.
func writtenCell(s ir.Stmt, cells map[*ir.Var]ir.Expr) *ir.Var {
	var target ir.Expr
	switch n := s.(type) {
	case *ir.Assign:
		target = n.Target
	case *ir.Toggle:
		target = n.Target
	case *ir.CallStmt:
		if n.Call != nil && len(n.Call.Args) > 0 && mutatesReceiver(n.Call.Func) {
			target = n.Call.Args[0].Value
		}
	}
	if v, ok := exprRoot(target).(*ir.Var); ok && cells[v] != nil {
		return v
	}
	return nil
}

func mutatesReceiver(fn *ir.Func) bool {
	if fn == nil {
		return false
	}
	if fn.Intrinsic != "" {
		def, ok := ir.IntrinsicByName(fn.Intrinsic)
		return ok && def.MutatesReceiver
	}
	return fn.IsMethod() && fn.Purity == ir.PurityMutates
}

// exprRoot is the symbol an lvalue is rooted at: `x`, `x.f` and `x[i]` all
// write x.
func exprRoot(e ir.Expr) ir.Symbol {
	for {
		switch x := e.(type) {
		case *ir.Ident:
			return x.Sym
		case *ir.Select:
			e = x.Operand
		case *ir.Index:
			e = x.Operand
		default:
			return nil
		}
	}
}

// retargetCell points the root of the write s makes at tmp instead of v.
func retargetCell(s ir.Stmt, v *ir.Var, tmp *ir.Var) {
	var slot *ir.Expr
	switch n := s.(type) {
	case *ir.Assign:
		slot = &n.Target
	case *ir.Toggle:
		slot = &n.Target
	case *ir.CallStmt:
		slot = &n.Call.Args[0].Value
	}
	for slot != nil {
		switch x := (*slot).(type) {
		case *ir.Ident:
			if x.Sym == v {
				*slot = &ir.Ident{Name: tmp.Name, Type: tmp.Type, Sym: tmp, Synthesized: true}
			}
			return
		case *ir.Select:
			slot = &x.Operand
		case *ir.Index:
			slot = &x.Operand
		default:
			return
		}
	}
}

// holdsLifetime reports whether stmts place a timer or an effect: something a
// render function, which is what a component built at run time is without
// InstanceState, has nowhere to keep.
func holdsLifetime(stmts []ir.Stmt) bool {
	return places(stmts, func(n *ir.NodeInst) bool { return ir.IsTimerPrimitive(n.Component) || isEffectNode(n) })
}

// placesTimer reports whether stmts place a timer primitive.
func placesTimer(stmts []ir.Stmt) bool {
	return places(stmts, func(n *ir.NodeInst) bool { return ir.IsTimerPrimitive(n.Component) })
}

func places(stmts []ir.Stmt, match func(*ir.NodeInst) bool) bool {
	found := false
	_ = ir.WalkStmts(stmts, func(s ir.Stmt) error {
		n, ok := s.(*ir.NodeInst)
		switch {
		case !ok:
		case match(n):
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}
