package lower

import (
	"fmt"
	"slices"

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
		if len(v.Handlers) > 0 {
			return nil, fmt.Errorf("%s: %q has a handler, and this target keeps a var of a component under a `for` as one cell per copy, which a handler has no copy to run for", at, v.Name)
		}
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
	body = rewriteCells(body, cells, keyed, temps, func() ir.Expr { return CopyKey(loops) })
	passLoopVars(body, funcs, loops)
	return body, nil
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
	for fn, lvs := range reads {
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
	var key ir.Expr
	for i, fs := range loops {
		idx := loopIndex(fs, i)
		part := ir.Expr(&ir.Conversion{Type: ir.TypString, Operand: &ir.Ident{Name: idx.Name, Type: idx.Type, Sym: idx, Synthesized: true}})
		if key == nil {
			key = part
			continue
		}
		key = &ir.Binary{Type: ir.TypString, Op: ast.BinAdd, Left: &ir.Binary{Type: ir.TypString, Op: ast.BinAdd, Left: key, Right: stringLit("/")}, Right: part}
	}
	return key
}

// loopIndex is the loop's index variable, rewriting a single-variable loop
// into the two-variable form -- index in Key, element in Value -- that every
// consumer downstream already reads Key off.
func loopIndex(fs *ir.For, depth int) *ir.LoopVar {
	if fs.Value == "" {
		fs.Value, fs.ValueSym = fs.Key, fs.KeySym
		if fs.Value == "" {
			fs.Value = "_"
		}
		fs.Key = fmt.Sprintf("__copy%d", depth)
		fs.KeySym = &ir.LoopVar{Name: fs.Key, Type: ir.TypInt}
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
	var params []*ir.Param
	if def := ir.LookupIntrinsic("map.get"); def != nil {
		params, _ = def.Instantiate(ir.TypString, elem)
	}
	return &ir.Call{
		Type: elem,
		Func: &ir.Func{Name: "get", Receiver: "map", Intrinsic: "map.get", Return: elem, Params: params},
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
	return fn.Receiver != "" && fn.Purity == ir.PurityMutates
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
