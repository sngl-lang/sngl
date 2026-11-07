package lower

import (
	"strconv"

	"duckfam.us/sngl/ir"
)

// SnapshotOffloadedReads reads, before each `async.start`, the state its
// work reads -- a component's var or prop, or the package's -- into a local
// the work reads instead, so the goroutine reads no cell the drawing thread
// writes. What the work hands back through `async.do` runs on the drawing
// thread, and reads the state there.
//
// It runs after the build's second optimize, which is where it has to: the
// work passAsyncOffload moves is a call through a query's `fetch`, and the
// optimizer inlines the adapter's lambda into it only then, bringing the
// context it reads -- a fetch on gtk4 and fyne read the instance's
// `__ctx_server` while a render rewrote it. passAsyncOffload's own bindArgs
// copies the arguments it can see, which that read was not yet among.
//
// A cell the work writes is left alone: copying it would lose the write. So
// is one whose copy is not its value -- a list's copy shares what it holds,
// and bubbletea runs its work on a deep snapshot of the model for exactly
// that -- so only a scalar, a string, an enum or an option of one is copied.
func SnapshotOffloadedReads(pkg *ir.Package) {
	locals := map[ir.Symbol]bool{}
	props := map[ir.Symbol]bool{}
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if lv, ok := n.(*ir.LocalVar); ok && lv.Sym != nil {
			locals[lv.Sym] = true
		}
		return nil
	})
	for _, c := range pkg.Components {
		for _, p := range c.Props {
			if p.Sym != nil {
				props[p.Sym] = true
			}
		}
	}
	shared := func(sym ir.Symbol) bool {
		if !copiedWhole(sym.SymType()) {
			return false
		}
		switch s := sym.(type) {
		case *ir.Var:
			return !locals[s]
		case *ir.Param:
			return props[s]
		}
		return false
	}
	n := 0
	ir.RewriteBlocks(pkg, func(stmts []ir.Stmt) []ir.Stmt {
		var out []ir.Stmt
		for _, s := range stmts {
			work := startWork(s)
			if work == nil {
				out = append(out, s)
				continue
			}
			reads, written := offloadedReads(work.Block, shared)
			copies := map[ir.Symbol]*ir.Var{}
			for _, sym := range reads {
				if written[sym] {
					continue
				}
				name := "__async_read" + strconv.Itoa(n)
				n++
				v := &ir.Var{Name: name, Type: sym.SymType(), Synthesized: true}
				out = append(out,
					&ir.LocalVar{Name: name, Type: sym.SymType(), Init: &ir.Ident{Name: sym.SymName(), Type: sym.SymType(), Sym: sym}, Sym: v},
					&ir.Assign{Target: &ir.Ident{Name: "_", Type: sym.SymType(), Synthesized: true}, Value: &ir.Ident{Name: name, Type: sym.SymType(), Sym: v, Synthesized: true}},
				)
				copies[sym] = v
			}
			if len(copies) > 0 {
				work.Block = readCopies(work.Block, copies)
			}
			out = append(out, s)
		}
		return out
	})
}

// startWork is the function an `async.start` statement runs, or nil.
func startWork(s ir.Stmt) *ir.Func {
	cs, ok := s.(*ir.CallStmt)
	if !ok || cs.Call == nil || cs.Call.Func == nil || cs.Call.Func.Intrinsic != AsyncStartIntrinsic || len(cs.Call.Args) != 1 {
		return nil
	}
	lam, ok := cs.Call.Args[0].Value.(*ir.Lambda)
	if !ok || lam.Func == nil {
		return nil
	}
	return lam.Func
}

// postedBack reports whether n is an `async.do`, whose function runs on the
// drawing thread.
func postedBack(n ir.Node) bool {
	c, ok := n.(*ir.Call)
	return ok && c.Func != nil && c.Func.Intrinsic == AsyncDoIntrinsic
}

// offloadedReads is the shared cells block reads off the drawing thread, in
// the order it first reads them, and those it writes.
func offloadedReads(block []ir.Stmt, shared func(ir.Symbol) bool) ([]ir.Symbol, map[ir.Symbol]bool) {
	var reads []ir.Symbol
	seen := map[ir.Symbol]bool{}
	written := map[ir.Symbol]bool{}
	_ = ir.Walk(block, func(n ir.Node) error {
		if postedBack(n) {
			return ir.SkipDir
		}
		switch x := n.(type) {
		case *ir.Assign:
			if sym := exprRoot(x.Target); sym != nil {
				written[sym] = true
			}
		case *ir.Ident:
			if x.Sym != nil && shared(x.Sym) && !seen[x.Sym] {
				seen[x.Sym] = true
				reads = append(reads, x.Sym)
			}
		}
		return nil
	})
	return reads, written
}

// readCopies points block's reads of each copied cell at its copy, leaving
// what an `async.do` runs on the drawing thread reading the cell.
func readCopies(block []ir.Stmt, copies map[ir.Symbol]*ir.Var) []ir.Stmt {
	_ = ir.Walk(block, func(n ir.Node) error {
		if postedBack(n) {
			return ir.SkipDir
		}
		if id, ok := n.(*ir.Ident); ok {
			if v, ok := copies[id.Sym]; ok {
				id.Name, id.Sym = v.Name, v
			}
		}
		return nil
	})
	return block
}

// copiedWhole reports whether a value of t is copied whole by the
// assignment a target writes for it.
func copiedWhole(t *ir.Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case ir.TypeString, ir.TypeInt, ir.TypeFloat, ir.TypeBool, ir.TypeUnit:
		return true
	case ir.TypeEnum:
		return ir.PayloadEnum(t) == nil
	case ir.TypeOption:
		return len(t.Elems) == 1 && copiedWhole(t.Elems[0])
	}
	return false
}
