package lower

import (
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
			case *ir.Select:
				if id, ok := x.Operand.(*ir.Ident); ok && id.Name == comp {
					return p.instanceRead(pkg, x), ir.SkipDir
				}
			}
			return n, nil
		})
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
	if path != nil {
		p.fail("a test's %s[…].%s(), one copy of a node a loop renders, is not placed yet", id, call.Event)
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
	v := packageVar(pkg, id)
	if v == nil {
		p.fail("a test fires %s.%s(), a node the placed program holds in no var", id, call.Event)
		return call
	}
	args := []ir.Expr{&ir.Ident{Name: v.Name, Sym: v, Type: v.Type, IsElementRef: true, Synthesized: true}}
	for _, a := range call.Args {
		args = append(args, a.Value)
	}
	spliced, err := protocolSplice(fn, args)
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
			if id, ok := sel.Operand.(*ir.Ident); ok && id.Name == comp && packageVar(pkg, sel.Field) == nil {
				read[sel.Field] = true
			}
		}
		return nil
	})
	if len(read) == 0 {
		return
	}
	for _, f := range append(append([]*ir.Func(nil), pkg.Funcs...), closureFuncs(pkg)...) {
		if f.IsTest {
			continue
		}
		var held []*ir.Var
		ir.RewriteBlocks(f, func(stmts []ir.Stmt) []ir.Stmt {
			out := stmts[:0:0]
			for _, s := range stmts {
				out = append(out, s)
				lv, ok := s.(*ir.LocalVar)
				if !ok || !read[lv.Name] || !isPlacedNode(lv.Type) || p.held[lv.Name] != nil {
					continue
				}
				v := &ir.Var{Name: lv.Name + "__held", Type: ir.OptionOf(lv.Type), Synthesized: true}
				if p.held == nil {
					p.held = map[string]*ir.Var{}
				}
				p.held[lv.Name] = v
				pkg.Vars = append(pkg.Vars, v)
				held = append(held, v)
				local := &ir.Ident{Name: lv.Name, Type: lv.Type, IsElementRef: true, Synthesized: true}
				if lv.Sym != nil {
					local.Sym = lv.Sym
				}
				out = append(out, &ir.Assign{
					Target: &ir.Ident{Name: v.Name, Sym: v, Type: v.Type, Synthesized: true},
					Op:     ast.AssignSet,
					// Wrapped as the option it is assigned to, which a
					// language holding an option as a struct (C) writes.
					Value: &ir.Conversion{Type: v.Type, Operand: local},
				})
			}
			return out
		})
		var reset []ir.Stmt
		for _, v := range held {
			reset = append(reset, &ir.Assign{
				Target: &ir.Ident{Name: v.Name, Sym: v, Type: v.Type, Synthesized: true},
				Op:     ast.AssignSet,
				Value:  &ir.Literal{Type: ir.TypNull, Value: "null"},
			})
		}
		f.Block = append(reset, f.Block...)
	}
}
