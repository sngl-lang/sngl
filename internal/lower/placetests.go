package lower

import (
	"slices"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// A test of a placed program reaches its instance as the program's own
// state: a build rooted at the component a test takes renders it as the
// package body would (liftRoot), so each var, node and function of it is
// the package's, and a test's read of one through its instance is a read of
// that (placeTests). Each event a test fires is the platform's
// #[gen.fires] for the event the program's handler was connected to.

// fireAt is where a test fires an event: the node's component and the event
// the program's handler was connected to on it, which an override may have
// connected under a name of its own.
type fireAt struct {
	comp  *ir.Component
	event string
}

// recordFire notes that handler, connected to event on node, is what a
// test's trigger of the event the program wrote reaches.
func (p *placement) recordFire(node ir.Expr, event string, handler ir.Expr) {
	id, ok := node.(*ir.Ident)
	if !ok {
		return
	}
	comp, _ := id.Type.Decl.(*ir.Component)
	if comp == nil {
		return
	}
	if p.fires == nil {
		p.fires = map[string]fireAt{}
	}
	p.fires[id.Name+"."+programEvent(handler, event)] = fireAt{comp: comp, event: event}
}

// programEvent is the event the program wrote a handler for: the one an
// override re-raised, which the inliner recorded on the handler, or the
// event it is connected to (codegen.TriggerEventName).
func programEvent(handler ir.Expr, event string) string {
	var fn *ir.Func
	switch h := handler.(type) {
	case *ir.Ident:
		fn, _ = h.Sym.(*ir.Func)
	case *ir.Lambda:
		fn = h.Func
	case *ir.Closure:
		fn = h.Func
	}
	if fn == nil || fn.Lowered.ComponentEvent == "" {
		return event
	}
	return fn.Lowered.ComponentEvent
}

// liftRoot makes a build rooted at a component render it as the package body
// would: its vars, functions and body the package's, so a language writes
// the one program it writes for any build.
func liftRoot(pkg *ir.Package) {
	root := pkg.RootDecl()
	if root == nil {
		return
	}
	pkg.Vars = append(pkg.Vars, root.Vars...)
	pkg.Funcs = append(pkg.Funcs, root.Funcs...)
	pkg.Body = append(pkg.Body, root.Stmts...)
	// The root's methods are functions of the package's now, as they are
	// where the build is rooted at the package body: the root's state is the
	// package's, and a method reads none of it through its receiver. Left
	// with one, `b.reset()` from a handler passed the component `main` itself,
	// which no language writes as a value. passNoImplicitRecv has made every
	// call's receiver its first argument.
	demoted := map[*ir.Func]bool{}
	for _, f := range root.Funcs {
		if len(f.Params) > 0 && f.Params[0].Receiver {
			dropReceiver(f)
			demoted[f] = true
		}
	}
	if len(demoted) > 0 {
		_ = ir.Walk(pkg, func(n ir.Node) error {
			if call, ok := n.(*ir.Call); ok && demoted[call.Func] {
				call.Receiver = nil
				if len(call.Args) == len(call.Func.Params)+1 {
					call.Args = call.Args[1:]
				}
			}
			return nil
		})
	}
	root.Vars, root.Funcs, root.Stmts = nil, nil, nil
}

// placeTests rewrites each test's reach through the instance it takes:
// `c.n`, the program's var, `c.save`, its node, and `c.save.click()`, the
// #[gen.fires] of the event the program's handler is connected to.
func (p *placement) placeTests(pkg *ir.Package) {
	for _, fn := range pkg.Funcs {
		if !fn.IsTest {
			continue
		}
		comp := instanceParamName(fn)
		if comp == "" {
			continue
		}
		p.holdRead(pkg, fn, comp)
		_ = ir.Rewrite(fn.Block, func(n ir.Node) (ir.Node, error) {
			switch x := n.(type) {
			case *ir.Call:
				if _, id, path, ok := ir.TriggerTarget(x); ok && x.Receiver != nil {
					return p.fire(pkg, x, comp, id, path), ir.SkipDir
				}
				if r, ok := x.Receiver.(*ir.Ident); ok && r.Name == comp {
					cp := *x
					cp.Receiver = nil
					return &cp, nil
				}
			case *ir.Ident:
				// The instance itself, handed a method lowering wrote for it
				// as its receiver: its state is the program's, so the method
				// reads none of it through the receiver.
				if _, param := x.Sym.(*ir.Param); param && x.Name == comp {
					return &ir.Literal{Type: ir.TypNull, Value: "null"}, ir.SkipDir
				}
			case *ir.Binary:
				// A copy null-tested is whether its key holds one.
				if held, ok := p.copyNullTest(x, comp); ok {
					return held, ir.SkipDir
				}
			case *ir.Index:
				if read, ok := p.copyRead(x, comp); ok {
					return read, ir.SkipDir
				}
			case *ir.Select:
				if id, ok := x.Operand.(*ir.Ident); ok && id.Name == comp {
					return p.instanceRead(pkg, x), ir.SkipDir
				}
			case *ir.Conversion:
				// An instance a record is made of, as the component it
				// is of: the record its var holds, whose reads recordReads
				// finishes.
				if sel, ok := x.Operand.(*ir.Select); ok {
					if id, ok := sel.Operand.(*ir.Ident); ok && id.Name == comp {
						read := p.instanceRead(pkg, sel)
						if t := read.ExprType(); t != nil && t.Kind == ir.TypeOption && p.recordOf(t.Elems[0]) != nil {
							return &ir.Conversion{Type: t.Elems[0], Operand: read}, ir.SkipDir
						}
						cp := *x
						cp.Operand = read
						return &cp, ir.SkipDir
					}
				}
			}
			return n, nil
		})
		p.recordReads(fn)
	}
}

// instanceParamName is the name of the instance fn takes, or "".
func instanceParamName(fn *ir.Func) string {
	for _, p := range fn.Params {
		if p.Type != nil && p.Type.Kind == ir.TypeComponent {
			return p.Name
		}
	}
	return ""
}

// packageVar is the package var named name, or nil.
func packageVar(pkg *ir.Package, name string) *ir.Var {
	for _, v := range pkg.Vars {
		if v.Name == name {
			return v
		}
	}
	return nil
}

// instanceRead is sel, a read through a test's instance, as the package's:
// a var, the box NoRef made of one a closure writes, or a node.
func (p *placement) instanceRead(pkg *ir.Package, sel *ir.Select) ir.Expr {
	v := p.held[sel.Field]
	if v == nil {
		v = packageVar(pkg, sel.Field)
	}
	if v == nil {
		p.fail("a test reading %s, which the placed program holds in no var", sel.Field)
		return sel
	}
	ref := &ir.Ident{Name: v.Name, Sym: v, Type: v.Type, Synthesized: true}
	if sd, ok := v.Type.Decl.(*ir.StructDef); ok && v.Type.Kind == ir.TypeStruct && sd.RefBox {
		return &ir.Select{Operand: ref, Field: "value", Type: sd.Fields[0].Type}
	}
	return ref
}

// fire is a test's trigger of event on the node id, the platform's
// #[gen.fires] for the event the program's handler was connected to.
func (p *placement) fire(pkg *ir.Package, call *ir.Call, comp, id string, path []ir.Expr) ir.Expr {
	recv, _, _, _ := ir.TriggerTarget(call)
	if recv != comp {
		p.fail("a test's trigger through %s, not the instance it takes", recv)
		return call
	}
	at, ok := p.fires[id+"."+call.Event]
	if !ok {
		p.fail("a test's %s.%s(), which no handler of the program is connected to", id, call.Event)
		return call
	}
	fn := at.comp.Protocol.Firer(at.event)
	if fn == nil {
		p.fail("a test fires @%s of %s, which declares no #[gen.fires(%q)]", at.event, at.comp.Name, at.event)
		return call
	}
	if len(fn.Params) != 1+len(call.Args) {
		p.fail("a test's %s.%s() hands %d values, and %s takes %d", id, call.Event, len(call.Args), fn.Name, len(fn.Params)-1)
		return call
	}
	var node ir.Expr
	if path != nil {
		// One copy of a node loops render: the one its key holds.
		copies := p.copies[id]
		if copies == nil {
			p.fail("a test fires %s[…].%s(), and no copy of %s is held by its key", id, call.Event, id)
			return call
		}
		node = &ir.Index{Operand: &ir.Ident{Name: copies.Name, Sym: copies, Type: copies.Type, Synthesized: true}, Idx: ir.CopyKey(path), Type: copies.Type.Elems[1]}
	} else if v := packageVar(pkg, id); v != nil {
		node = &ir.Ident{Name: v.Name, Sym: v, Type: v.Type, IsElementRef: true, Synthesized: true}
	} else if v := p.held[id]; v != nil {
		// A node a function other than the package body makes, which the
		// test reads where it is rendered (holdRead).
		node = &ir.Conversion{Type: v.Type.Elems[0], Operand: &ir.Ident{Name: v.Name, Sym: v, Type: v.Type, Synthesized: true}}
	} else {
		p.fail("a test fires %s.%s(), a node the placed program holds in no var", id, call.Event)
		return call
	}
	args := []ir.Expr{node}
	for _, a := range call.Args {
		args = append(args, a.Value)
	}
	spliced, err := protocolSplice(fn, args, p.language)
	if err != nil {
		p.fail("%v", err)
		return call
	}
	return spliced
}

// holdRead gives each node fn reads through its instance that a function
// other than the package body makes -- a render slot's, made and taken away
// as the slot renders -- a package var holding it while it is rendered and
// null otherwise: each render of the function starts with none made.
func (p *placement) holdRead(pkg *ir.Package, fn *ir.Func, comp string) {
	read := map[string]bool{}
	_ = ir.Walk(fn.Block, func(n ir.Node) error {
		if sel, ok := n.(*ir.Select); ok {
			// One hoistShared made a package var is still made as its
			// function renders, and gone where it no longer does.
			if id, ok := sel.Operand.(*ir.Ident); ok && id.Name == comp && (packageVar(pkg, sel.Field) == nil || p.shared[sel.Field]) {
				read[sel.Field] = true
			}
		}
		return nil
	})
	if len(read) == 0 {
		return
	}
	// hold is block with each node read made in it held as it is made, and
	// the resets that start the function rendering it. An instance a record
	// is made of is declared first and set after, from the instances kept
	// or a new one, so it is held as it is set, and let go as it is
	// destroyed, unless another took its place.
	hold := func(block []ir.Stmt) ([]ir.Stmt, []ir.Stmt) {
		var held []*ir.Var
		instances := map[ir.Symbol]*ir.Var{}
		_ = ir.Walk(block, func(n ir.Node) error {
			lv, ok := n.(*ir.LocalVar)
			// One a test reaches a copy of is in the package's map from
			// each copy's key instead (copyWrite).
			if !ok || lv.Sym == nil || !read[lv.Name] || p.held[lv.Name] != nil || p.copies[lv.Name] != nil {
				return nil
			}
			if r := p.recordOf(lv.Type); r != nil {
				v := p.heldVar(pkg, lv.Name, lv.Type)
				held = append(held, v)
				instances[lv.Sym] = v
				p.letGo(r, v, lv.Type, func(self ir.Expr) ir.Expr { return self })
			}
			return nil
		})
		block = ir.RewriteBlocks(block, func(stmts []ir.Stmt) []ir.Stmt {
			out := stmts[:0:0]
			for _, s := range stmts {
				out = append(out, s)
				switch x := s.(type) {
				case *ir.LocalVar:
					if v := instances[x.Sym]; v != nil && x.Sym != nil && x.Init != nil {
						out = append(out, holdAssign(v, &ir.Ident{Name: x.Name, Sym: x.Sym, Type: x.Type, Synthesized: true}))
					}
				case *ir.Assign:
					if id, ok := x.Target.(*ir.Ident); ok && id.Sym != nil && instances[id.Sym] != nil {
						out = append(out, holdAssign(instances[id.Sym], ir.CloneExprSharingDecls(id)))
					}
				}
				lv, ok := s.(*ir.LocalVar)
				if !ok || !read[lv.Name] || !isPlacedNode(lv.Type) || p.held[lv.Name] != nil {
					continue
				}
				v := p.heldVar(pkg, lv.Name, lv.Type)
				held = append(held, v)
				local := &ir.Ident{Name: lv.Name, Type: lv.Type, IsElementRef: true, Synthesized: true}
				if lv.Sym != nil {
					local.Sym = lv.Sym
				}
				out = append(out, holdAssign(v, local))
			}
			return out
		})
		var reset []ir.Stmt
		for _, v := range held {
			reset = append(reset, holdAssign(v, &ir.Literal{Type: ir.TypNull, Value: "null"}))
		}
		return block, reset
	}
	for _, f := range append(append([]*ir.Func(nil), pkg.Funcs...), closureFuncs(pkg)...) {
		if f.IsTest {
			continue
		}
		block, reset := hold(f.Block)
		f.Block = append(reset, block...)
	}
	// The package body's, under a branch it renders once.
	body, reset := hold(pkg.Body)
	pkg.Body = append(reset, body...)
	// A record's, held as its constructor makes it and let go as it is
	// destroyed, unless another record's took its place. In order, since
	// each makes the package var it holds in.
	for _, r := range p.recordList() {
		for _, f := range r.sd.Fields {
			if !read[f.Name] || p.held[f.Name] != nil || !isPlacedNode(f.Type) {
				continue
			}
			v := p.heldVar(pkg, f.Name, f.Type)
			r.new.Block = ir.RewriteBlocks(r.new.Block, func(stmts []ir.Stmt) []ir.Stmt {
				out := stmts[:0:0]
				for _, s := range stmts {
					out = append(out, s)
					if a, ok := s.(*ir.Assign); ok {
						if sel, ok := a.Target.(*ir.Select); ok && sel.Field == f.Name {
							out = append(out, holdAssign(v, ir.CloneExprSharingDecls(sel)))
						}
					}
				}
				return out
			})
			p.letGo(r, v, f.Type, func(self ir.Expr) ir.Expr { return &ir.Select{Operand: self, Field: f.Name, Type: f.Type} })
		}
	}
}

// recordReads finishes fn's reads of a record a test holds an instance of:
// a var the record holds boxed, read as its value as a package var's is
// (instanceRead), and a function of the component the instance is a copy
// of, which is the record's own copy of it.
func (p *placement) recordReads(fn *ir.Func) {
	_ = ir.Rewrite(fn.Block, func(n ir.Node) (ir.Node, error) {
		switch x := n.(type) {
		case *ir.Select:
			r := p.recordOf(x.Operand.ExprType())
			if r == nil {
				return n, nil
			}
			for _, f := range r.sd.Fields {
				if f.Name != x.Field || f.Type == nil || f.Type.Equal(x.Type) {
					continue
				}
				if sd, ok := f.Type.Decl.(*ir.StructDef); ok && f.Type.Kind == ir.TypeStruct && sd.RefBox {
					return &ir.Select{Operand: &ir.Select{Operand: x.Operand, Field: x.Field, Type: f.Type}, Field: "value", Type: sd.Fields[0].Type}, ir.SkipDir
				}
			}
		case *ir.Call:
			if x.Func == nil || len(x.Args) == 0 {
				return n, nil
			}
			r := p.recordOf(x.Args[0].Value.ExprType())
			if r == nil || slices.Contains(r.owned, x.Func) {
				return n, nil
			}
			if own := r.named[x.Func.Name]; own != nil {
				cp := *x
				cp.Func = own
				return &cp, nil
			}
		}
		return n, nil
	})
}

// letGo has r's destroy null v, a held var of t, while it holds what of
// reads of the record destroyed: a keyed render builds the record taking
// one's place before it destroys the one it replaces.
func (p *placement) letGo(r *record, v *ir.Var, t *ir.Type, of func(self ir.Expr) ir.Expr) {
	self := r.destroy.Params[0]
	selfRef := &ir.Ident{Name: self.Name, Sym: self, Type: self.Type, Synthesized: true}
	ref := func() ir.Expr { return &ir.Ident{Name: v.Name, Sym: v, Type: v.Type, Synthesized: true} }
	mine := &ir.Binary{Type: ir.TypBool, Op: ast.BinAnd,
		Left:  &ir.Binary{Type: ir.TypBool, Op: ast.BinNeq, Left: ref(), Right: &ir.Literal{Type: ir.TypNull, Value: "null"}},
		Right: &ir.Binary{Type: ir.TypBool, Op: ast.BinEq, Left: &ir.Conversion{Type: t, Operand: ref()}, Right: of(selfRef)},
	}
	clear := &ir.If{Cond: mine, Body: []ir.Stmt{holdAssign(v, &ir.Literal{Type: ir.TypNull, Value: "null"})}}
	r.destroy.Block = append([]ir.Stmt{clear}, r.destroy.Block...)
}

// heldVar is the package var holding the node a test reads named name,
// made the first time one is asked for.
func (p *placement) heldVar(pkg *ir.Package, name string, t *ir.Type) *ir.Var {
	if v := p.held[name]; v != nil {
		return v
	}
	v := &ir.Var{Name: name + "__held", Type: ir.OptionOf(t), Synthesized: true}
	if p.held == nil {
		p.held = map[string]*ir.Var{}
	}
	p.held[name] = v
	pkg.Vars = append(pkg.Vars, v)
	return v
}

// holdAssign is the write of value to v, a held node's var.
func holdAssign(v *ir.Var, value ir.Expr) ir.Stmt {
	if !ir.IsNullLiteral(value) {
		// Wrapped as the option it is assigned to, which a language
		// holding an option as a struct (C) writes.
		value = &ir.Conversion{Type: v.Type, Operand: value}
	}
	return &ir.Assign{Target: &ir.Ident{Name: v.Name, Sym: v, Type: v.Type, Synthesized: true}, Op: ast.AssignSet, Value: value}
}

// copyWrite is the write of a copy's key onto node, a node loops render that
// a test reaches one copy of: the node goes in the package's map from each
// copy's key, which the test's trigger reads it out of.
func (p *placement) copyWrite(node *ir.Ident, key ir.Expr) ir.Stmt {
	if p.copies == nil {
		p.copies = map[string]*ir.Var{}
	}
	v := p.copies[node.Name]
	if v == nil {
		t := ir.MapOf(ir.TypString, node.Type)
		v = &ir.Var{Name: node.Name + "__copies", Type: t, Init: &ir.MapLitIR{Type: t}, Synthesized: true}
		p.copies[node.Name] = v
		p.pkg.Vars = append(p.pkg.Vars, v)
	}
	return &ir.Assign{
		Target: &ir.Index{Operand: &ir.Ident{Name: v.Name, Sym: v, Type: v.Type, Synthesized: true}, Idx: key, Type: node.Type},
		Op:     ast.AssignSet,
		Value:  node,
	}
}

// clearCopies has each render that stamps copies start their map afresh,
// so a copy the render no longer makes is held by no key: the function
// writing a key, or, where a record's constructor or setter writes it, each
// function building that record, whose render updates every prop of each
// instance it keeps -- the key among them.
func (p *placement) clearCopies(pkg *ir.Package) {
	if len(p.copies) == 0 {
		return
	}
	held := map[*ir.Var]bool{}
	for _, v := range p.copies {
		held[v] = true
	}
	stamps := func(fn *ir.Func) []*ir.Var {
		var out []*ir.Var
		_ = ir.Walk(fn.Block, func(n ir.Node) error {
			if a, ok := n.(*ir.Assign); ok {
				if ix, ok := a.Target.(*ir.Index); ok {
					if id, ok := ix.Operand.(*ir.Ident); ok {
						if v, ok := id.Sym.(*ir.Var); ok && held[v] && !slices.Contains(out, v) {
							out = append(out, v)
						}
					}
				}
			}
			return nil
		})
		return out
	}
	// What a record's functions stamp, its builders clear.
	byRecord := map[*ir.Func][]*ir.Var{}
	inRecord := map[*ir.Func]bool{}
	for _, r := range p.records {
		var vs []*ir.Var
		for _, fn := range r.funcs() {
			inRecord[fn] = true
			for _, v := range stamps(fn) {
				if !slices.Contains(vs, v) {
					vs = append(vs, v)
				}
			}
		}
		if len(vs) > 0 {
			byRecord[r.new] = vs
		}
	}
	for _, fn := range append(slices.Clone(pkg.Funcs), closureFuncs(pkg)...) {
		if inRecord[fn] || fn.IsTest {
			continue
		}
		clear := stamps(fn)
		_ = ir.Walk(fn.Block, func(n ir.Node) error {
			if c, ok := n.(*ir.Call); ok {
				for _, v := range byRecord[c.Func] {
					if !slices.Contains(clear, v) {
						clear = append(clear, v)
					}
				}
			}
			return nil
		})
		var reset []ir.Stmt
		for _, v := range clear {
			reset = append(reset, &ir.Assign{
				Target: &ir.Ident{Name: v.Name, Sym: v, Type: v.Type, Synthesized: true},
				Op:     ast.AssignSet,
				Value:  &ir.MapLitIR{Type: v.Type},
			})
		}
		fn.Block = append(reset, fn.Block...)
	}
}

// copyRead is ix, a test's read of one copy of a node loops render,
// `c.row[1]`, as the copy its key holds, or null where the render made none
// there. ok is false where ix reads no copy.
func (p *placement) copyRead(ix *ir.Index, comp string) (ir.Expr, bool) {
	copies, key := p.copyOf(ix, comp)
	if copies == nil {
		return nil, false
	}
	ref := func() ir.Expr { return &ir.Ident{Name: copies.Name, Sym: copies, Type: copies.Type, Synthesized: true} }
	at := &ir.Index{Operand: ref(), Idx: key, Type: copies.Type.Elems[1]}
	if ix.Type == nil || ix.Type.Kind != ir.TypeOption {
		return at, true
	}
	return &ir.Ternary{Type: ix.Type, Cond: copyHeld(copies, ir.CloneExprSharingDecls(key)), Then: &ir.Conversion{Type: ix.Type, Operand: at}, Else: &ir.Literal{Type: ir.TypNull, Value: "null"}}, true
}

// copyNullTest is b, a test's `c.row[1] == null` or `!= null`, as whether
// the copy's key holds one; ok is false where b tests no copy.
func (p *placement) copyNullTest(b *ir.Binary, comp string) (ir.Expr, bool) {
	if b.Op != ast.BinEq && b.Op != ast.BinNeq {
		return nil, false
	}
	side := b.Left
	if !ir.IsNullLiteral(b.Right) {
		if !ir.IsNullLiteral(b.Left) {
			return nil, false
		}
		side = b.Right
	}
	ix, ok := side.(*ir.Index)
	if !ok {
		return nil, false
	}
	copies, key := p.copyOf(ix, comp)
	if copies == nil {
		return nil, false
	}
	var held ir.Expr = copyHeld(copies, key)
	if b.Op == ast.BinEq {
		held = &ir.Unary{Type: ir.TypBool, Op: ast.UnaryNot, Operand: held}
	}
	return held, true
}

// copyHeld is whether copies holds a copy at key.
func copyHeld(copies *ir.Var, key ir.Expr) ir.Expr {
	ref := &ir.Ident{Name: copies.Name, Sym: copies, Type: copies.Type, Synthesized: true}
	return &ir.Call{Type: ir.TypBool, Func: ir.IntrinsicMethod("contains", "map.contains", copies.Type, ir.TypBool), Args: []ir.CallArg{{Value: ref}, {Value: key}}}
}

// copyOf is the map holding the copies ix reads one of off the instance
// comp, and the copy's key, or nil where ix reads no copy.
func (p *placement) copyOf(ix *ir.Index, comp string) (*ir.Var, ir.Expr) {
	var path []ir.Expr
	var x ir.Expr = ix
	for {
		in, isIx := x.(*ir.Index)
		if !isIx {
			break
		}
		path = append([]ir.Expr{in.Idx}, path...)
		x = in.Operand
	}
	sel, ok := x.(*ir.Select)
	if !ok {
		return nil, nil
	}
	if id, ok := sel.Operand.(*ir.Ident); !ok || id.Name != comp {
		return nil, nil
	}
	copies := p.copies[sel.Field]
	if copies == nil {
		return nil, nil
	}
	return copies, ir.CopyKey(path)
}
