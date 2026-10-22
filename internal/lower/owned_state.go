package lower

import (
	"maps"
	"slices"
	"strconv"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// passOwnedState gives each owned value a component holds its node's
// lifetime: an owned `var` is released when it is overwritten and when the
// node unmounts, an owned derived value becomes a var recomputed when what it
// reads is written, and an allocation an effect's handler hands a
// `#[retains]` parameter is held until the next one replaces it or the
// effect's lifetime ends. Each is an ordinary `effect`, which every target's
// Effect lowering already brackets. design/ownership.md says why it is shaped
// this way.
var passOwnedState = pass{
	name:    "OwnedState",
	enabled: func(Features) bool { return true },
	apply: func(pkg *ir.Package, _ Features, _ Options) error {
		if pkg == nil {
			return nil
		}
		return ownedState(pkg, pkg.Components, []any{pkg})
	},
}

// OwnedState is passOwnedState for the interpreter, which runs a library
// component where it is declared, so every component the program reaches is
// rewritten rather than the package's own.
func OwnedState(pkg *ir.Package) error {
	if pkg == nil {
		return nil
	}
	comps := slices.Clone(pkg.Components)
	roots := []any{pkg}
	for _, p := range importedPackages(pkg) {
		for _, c := range p.Components {
			if !c.Frozen {
				comps = append(comps, c)
				roots = append(roots, c)
			}
		}
		for _, f := range p.Funcs {
			if !f.Frozen {
				roots = append(roots, f)
			}
		}
	}
	return ownedState(pkg, comps, roots)
}

// importedPackages is every package pkg reaches through its imports, pkg
// itself not among them.
func importedPackages(pkg *ir.Package) []*ir.Package {
	var out []*ir.Package
	seen := map[*ir.Package]bool{pkg: true}
	var visit func(p *ir.Package)
	visit = func(p *ir.Package) {
		for _, imp := range p.Imports {
			if imp == nil || imp.Pkg == nil || seen[imp.Pkg] {
				continue
			}
			seen[imp.Pkg] = true
			out = append(out, imp.Pkg)
			visit(imp.Pkg)
		}
	}
	visit(pkg)
	return out
}

type ownedStateRun struct {
	pkg     *ir.Package
	effect  *ir.Component
	counter int
	// cells is each owned var by the component holding it, which a write
	// from outside the component (a test's `c.f = …`) names by field.
	cells map[*ir.Component]map[string]*ir.Var
	owned map[*ir.Var]bool
	// written is each assignment the pass made, which is already the
	// overwrite it would otherwise become.
	written map[*ir.Assign]bool
	// retained is each cell a reporting effect's handler fills, by the
	// component releasing it when the node unmounts.
	retained map[*ir.Component][]*ir.Var
}

func ownedState(pkg *ir.Package, comps []*ir.Component, roots []any) error {
	st := &ownedStateRun{
		pkg:      pkg,
		cells:    map[*ir.Component]map[string]*ir.Var{},
		owned:    map[*ir.Var]bool{},
		written:  map[*ir.Assign]bool{},
		retained: map[*ir.Component][]*ir.Var{},
	}
	derived := map[*ir.Func]*ir.Var{}
	for _, c := range comps {
		maps.Copy(derived, st.derive(c))
		st.retain(c)
	}
	if len(derived) > 0 {
		for _, root := range roots {
			if err := ir.RewriteExprs(root, func(e ir.Expr) (ir.Expr, error) { return readCell(e, derived), nil }); err != nil {
				return err
			}
		}
	}
	for _, c := range comps {
		st.collect(c)
		st.blockVars(&c.Stmts)
	}
	st.blockVars(&pkg.Body)
	if len(st.owned) > 0 {
		for _, root := range roots {
			ir.RewriteBlocks(root, st.overwrites)
		}
	}
	for _, c := range comps {
		st.unmount(c)
	}
	return nil
}

func (st *ownedStateRun) effectComponent() *ir.Component {
	if st.effect == nil {
		st.effect = effectComponent()
	}
	return st.effect
}

// effectComponent is sngl:builtin's `effect`, found by its kind in the
// package that declares the application, which no program imports. A copy:
// a program that writes no effect does not reach the declaration, so the
// build shares the library's own, and a pass flagging the component behind a
// node (InlineComponents' RuntimeInstance) would write the library.
func effectComponent() *ir.Component {
	if t := ir.Published().ApplicationType(); t != nil {
		if app, ok := t.Decl.(*ir.Component); ok && app.Pkg != nil {
			for _, c := range app.Pkg.Components {
				if c.Builtin == ir.BuiltinEffect {
					cp := *c
					cp.Frozen = false
					return &cp
				}
			}
		}
	}
	return nil
}

func (st *ownedStateRun) name(prefix string) string {
	n := prefix + strconv.Itoa(st.counter)
	st.counter++
	return n
}

// derive turns each owned derived value of c into a var of the same name,
// initialized as the value was computed, and an effect keyed on what it reads
// whose `@update` computes it again. The var is then an owned var like any
// other, so the overwrite releases the old value and the unmount the last.
//
// `@update` rather than a bracket's `@mount`: a mount runs after the render
// that first reads the value, and a rekey's `@unmount` before the render that
// precedes the new `@mount`, so a bracket would show that render a value not
// yet made or already released.
func (st *ownedStateRun) derive(c *ir.Component) map[*ir.Func]*ir.Var {
	var fns []*ir.Func
	for _, fn := range c.Funcs {
		if !fn.IsTest && ir.ReleaseOf(fn.Return) != nil && onlyReceiver(fn) && len(fn.Block) > 0 {
			fns = append(fns, fn)
		}
	}
	if len(fns) == 0 {
		return nil
	}
	cells := map[*ir.Func]*ir.Var{}
	for _, fn := range fns {
		cells[fn] = &ir.Var{Name: fn.Name, Pkg: fn.Pkg, Type: fn.Return, Synthesized: true}
	}
	compute := map[*ir.Func]func() ir.Expr{}
	var keep []*ir.Func
	for _, fn := range fns {
		if e := derivedExpr(fn); e != nil {
			compute[fn] = func() ir.Expr { return ir.CloneExprSharingDecls(e) }
			continue
		}
		helper := new(ir.Func)
		*helper = *fn
		helper.Name = "__own_" + fn.Name
		keep = append(keep, helper)
		compute[fn] = func() ir.Expr { return &ir.Call{Type: helper.Return, Func: helper} }
	}
	c.Funcs = append(slices.DeleteFunc(c.Funcs, func(f *ir.Func) bool { return cells[f] != nil }), keep...)
	for _, fn := range fns {
		delete(c.Methods, fn.Name)
	}
	for _, fn := range keep {
		if c.Methods != nil {
			c.Methods[fn.Name] = fn
		}
	}
	var effects []ir.Stmt
	for _, fn := range orderByReads(fns, cells) {
		cell := cells[fn]
		cell.Init = compute[fn]()
		c.Vars = append(c.Vars, cell)
		key := st.key(c, inputs(c, fn, cells))
		if key == nil {
			continue
		}
		update := &ir.Assign{
			Target: &ir.Ident{Name: cell.Name, Sym: cell, Type: cell.Type},
			Value:  compute[fn](),
		}
		effects = append(effects, st.effectNode(key, ir.EventHandler{Name: ir.EffectUpdate, Func: &ir.Func{Pkg: fn.Pkg, Block: []ir.Stmt{update}}}))
	}
	c.Stmts = append(effects, c.Stmts...)
	return cells
}

// onlyReceiver reports whether fn takes nothing but its component's
// receiver: a derived value.
func onlyReceiver(fn *ir.Func) bool {
	for _, p := range fn.Params {
		if !p.Receiver {
			return false
		}
	}
	return true
}

// derivedExpr is what fn computes, where its body is one expression that
// does not name its receiver, which a var's initializer has no binding for.
func derivedExpr(fn *ir.Func) ir.Expr {
	if len(fn.Block) != 1 {
		return nil
	}
	ret, ok := fn.Block[0].(*ir.Return)
	if !ok || ret.Value == nil {
		return nil
	}
	recv := false
	_ = ir.Walk(ret.Value, func(n ir.Node) error {
		if id, ok := n.(*ir.Ident); ok {
			if p, ok := id.Sym.(*ir.Param); ok && p.Receiver {
				recv = true
				return ir.SkipAll
			}
		}
		return nil
	})
	if recv {
		return nil
	}
	return ret.Value
}

// orderByReads is fns with each after the derived values it reads, since a
// var's initializer may only read what is initialized before it.
func orderByReads(fns []*ir.Func, cells map[*ir.Func]*ir.Var) []*ir.Func {
	var out []*ir.Func
	done := map[*ir.Func]bool{}
	var visit func(fn *ir.Func)
	visit = func(fn *ir.Func) {
		if done[fn] {
			return
		}
		done[fn] = true
		_ = ir.Walk(fn.Block, func(n ir.Node) error {
			if call, ok := n.(*ir.Call); ok && cells[call.Func] != nil {
				visit(call.Func)
			}
			return nil
		})
		out = append(out, fn)
	}
	for _, fn := range fns {
		visit(fn)
	}
	return out
}

// readCell is e with a call of a derived value that became a var a read of
// the var: by name in its component, a field of the instance elsewhere.
func readCell(e ir.Expr, cells map[*ir.Func]*ir.Var) ir.Expr {
	call, ok := e.(*ir.Call)
	if !ok || cells[call.Func] == nil {
		return e
	}
	cell := cells[call.Func]
	if call.Receiver == nil || isReceiverIdent(call.Receiver) {
		return &ir.Ident{Name: cell.Name, Sym: cell, Type: cell.Type}
	}
	return &ir.Select{Operand: call.Receiver, Field: cell.Name, Type: cell.Type}
}

func isReceiverIdent(e ir.Expr) bool {
	id, ok := e.(*ir.Ident)
	if !ok {
		return false
	}
	p, ok := id.Sym.(*ir.Param)
	return ok && p.Receiver
}

// inputs is the state fn reads, in the order it first reads it: a prop or a
// var of c, or a package var, directly or through c's own functions. A
// derived value it reads is its var.
func inputs(c *ir.Component, fn *ir.Func, cells map[*ir.Func]*ir.Var) []ir.Symbol {
	var out []ir.Symbol
	seen := map[ir.Symbol]bool{}
	followed := map[*ir.Func]bool{fn: true}
	add := func(s ir.Symbol) {
		if !seen[s] {
			seen[s] = true
			out = append(out, s)
		}
	}
	var visit func(root any)
	visit = func(root any) {
		_ = ir.Walk(root, func(n ir.Node) error {
			switch x := n.(type) {
			case *ir.Ident:
				switch s := x.Sym.(type) {
				case *ir.Param:
					if slices.ContainsFunc(c.Props, func(p *ir.Prop) bool { return p.Sym == s }) {
						add(s)
					}
				case *ir.Var:
					if !s.IsConst && !s.NodeHandle && (slices.Contains(c.Vars, s) || s.Pkg != nil && slices.Contains(s.Pkg.Vars, s)) {
						add(s)
					}
				}
			case *ir.Call:
				switch {
				case cells[x.Func] != nil && x.Func != fn:
					add(cells[x.Func])
				case x.Func != nil && !followed[x.Func] && slices.Contains(c.Funcs, x.Func):
					followed[x.Func] = true
					visit(x.Func.Block)
				}
			}
			return nil
		})
	}
	visit(fn.Block)
	return out
}

// key is what an effect reporting a write to any of reads is keyed on: the
// one read itself, or an anonymous struct of them.
func (st *ownedStateRun) key(c *ir.Component, reads []ir.Symbol) ir.Expr {
	read := func(s ir.Symbol) ir.Expr {
		return &ir.Ident{Name: s.SymName(), Sym: s, Type: s.SymType()}
	}
	switch len(reads) {
	case 0:
		return nil
	case 1:
		return read(reads[0])
	}
	names := map[string]bool{}
	var fields []*ir.StructField
	var inits []ir.FieldInit
	for i, s := range reads {
		name := s.SymName()
		if names[name] {
			name += "_" + strconv.Itoa(i)
		}
		names[name] = true
		fields = append(fields, &ir.StructField{Name: name, Type: s.SymType()})
		inits = append(inits, ir.FieldInit{Name: name, Value: read(s)})
	}
	slices.SortStableFunc(fields, func(a, b *ir.StructField) int { return strings.Compare(a.Name, b.Name) })
	sd := &ir.StructDef{Pkg: c.Pkg, Fields: fields, Anon: true}
	return &ir.StructLit{Type: sd.SymType(), Def: sd, Fields: inits}
}

func (st *ownedStateRun) effectNode(key ir.Expr, handlers ...ir.EventHandler) *ir.NodeInst {
	n := &ir.NodeInst{Name: "effect", Component: st.effectComponent(), Handlers: handlers}
	if key != nil {
		n.Props = []ir.Arg{{Name: ir.EffectOn, Value: key}}
	}
	return n
}

// collect records each owned var c declares.
func (st *ownedStateRun) collect(c *ir.Component) {
	for _, v := range c.Vars {
		if v.IsConst || v.NodeHandle || ir.ReleaseOf(v.Type) == nil {
			continue
		}
		if st.cells[c] == nil {
			st.cells[c] = map[string]*ir.Var{}
		}
		st.cells[c][v.Name] = v
		st.owned[v] = true
	}
}

// blockVars releases each owned var a view block declares when the block
// unmounts, in an effect written after it: the interpreter keeps such a var
// as the block's state, where a target has hoisted it into a component
// (passHoistState) and collect has found it there.
func (st *ownedStateRun) blockVars(body *[]ir.Stmt) {
	var out []ir.Stmt
	for _, s := range *body {
		for _, b := range ir.ViewBlocks(s) {
			st.blockVars(b)
		}
		out = append(out, s)
		lv, ok := s.(*ir.LocalVar)
		if !ok || lv.Sym == nil || ir.ReleaseOf(lv.Type) == nil {
			continue
		}
		st.owned[lv.Sym] = true
		release := ir.EventHandler{Name: ir.EffectUnmount, Func: &ir.Func{Pkg: st.pkg, Block: []ir.Stmt{releaseStmt(identOf(lv.Sym))}}}
		out = append(out, st.effectNode(nil, release))
	}
	*body = out
}

// overwrites is stmts with each assignment to an owned var releasing what the
// var held once the new value is in place: the new value may be computed
// from the old.
func (st *ownedStateRun) overwrites(stmts []ir.Stmt) []ir.Stmt {
	var out []ir.Stmt
	for _, s := range stmts {
		a, ok := s.(*ir.Assign)
		v := st.assigned(a)
		if !ok || v == nil || st.written[a] {
			out = append(out, s)
			continue
		}
		old := &ir.Var{Name: st.name("__old"), Type: v.Type, Synthesized: true}
		st.written[a] = true
		out = append(out,
			&ir.LocalVar{Name: old.Name, Type: v.Type, Init: ir.CloneExprSharingDecls(a.Target), Sym: old},
			a,
			releaseStmt(&ir.Ident{Name: old.Name, Sym: old, Type: v.Type}),
		)
	}
	return out
}

// assigned is the owned var a plainly assigns, nil when it assigns none.
func (st *ownedStateRun) assigned(a *ir.Assign) *ir.Var {
	if a == nil || a.Op != ast.AssignSet {
		return nil
	}
	switch t := a.Target.(type) {
	case *ir.Ident:
		if v, ok := t.Sym.(*ir.Var); ok && st.owned[v] {
			return v
		}
	case *ir.Select:
		if comp, ok := t.Operand.ExprType().Decl.(*ir.Component); ok && t.Operand.ExprType().Kind == ir.TypeComponent {
			return st.cells[comp][t.Field]
		}
	}
	return nil
}

// unmount releases every owned var of c when the node goes, newest first, in
// an effect written ahead of everything else in the body: the first lifetime
// to begin is the last to end, so every other `@unmount` may still read them.
func (st *ownedStateRun) unmount(c *ir.Component) {
	var block []ir.Stmt
	for _, cell := range slices.Backward(st.retained[c]) {
		block = append(block, st.releaseCell(cell)...)
	}
	for _, v := range slices.Backward(c.Vars) {
		if st.owned[v] {
			block = append(block, releaseStmt(&ir.Ident{Name: v.Name, Sym: v, Type: v.Type}))
		}
	}
	if len(block) == 0 {
		return
	}
	h := ir.EventHandler{Name: ir.EffectUnmount, Func: &ir.Func{Pkg: c.Pkg, Block: block}}
	c.Stmts = append([]ir.Stmt{st.effectNode(nil, h)}, c.Stmts...)
}

func releaseStmt(v ir.Expr) ir.Stmt {
	return &ir.CallStmt{Call: &ir.Call{Type: ir.TypVoid, Func: ir.ReleaseOf(v.ExprType()), Args: []ir.CallArg{{Value: v}}}}
}

// retain gives each allocation an effect's handler hands a `#[retains]`
// parameter a cell of c's: the next one the call makes replaces it, and the
// effect's `@unmount` releases it, which is when the native stops holding
// it. An effect that only reports writes brackets nothing, and an `@unmount`
// would make it a bracket ended by every change of its key, releasing what
// the native still holds; its cells are released when the node unmounts.
func (st *ownedStateRun) retain(c *ir.Component) {
	for _, n := range effectNodes(c.Stmts) {
		var cells []*ir.Var
		for _, h := range n.Handlers {
			if h.Name == ir.EffectUnmount || h.Func == nil {
				continue
			}
			h.Func.Block = ir.RewriteBlocks(h.Func.Block, func(stmts []ir.Stmt) []ir.Stmt {
				var out []ir.Stmt
				for _, s := range stmts {
					pre, post, made := st.retainedIn(c, s)
					cells = append(cells, made...)
					out = append(append(append(out, pre...), s), post...)
				}
				return out
			})
		}
		if len(cells) == 0 {
			continue
		}
		if !brackets(n) {
			st.retained[c] = append(st.retained[c], cells...)
			continue
		}
		var release []ir.Stmt
		for _, cell := range slices.Backward(cells) {
			release = append(release, st.releaseCell(cell)...)
		}
		i := slices.IndexFunc(n.Handlers, func(h ir.EventHandler) bool { return h.Name == ir.EffectUnmount })
		if i < 0 {
			n.Handlers = append(n.Handlers, ir.EventHandler{Name: ir.EffectUnmount, Func: &ir.Func{Pkg: c.Pkg}})
			i = len(n.Handlers) - 1
		}
		n.Handlers[i].Func.Block = append(n.Handlers[i].Func.Block, release...)
	}
}

// brackets reports whether n is a lifetime of its own: it declares one, or it
// has no key to report writes to.
func brackets(n *ir.NodeInst) bool {
	return n.EffectKey() == nil || slices.ContainsFunc(n.Handlers, func(h ir.EventHandler) bool {
		return h.Name == ir.EffectMount || h.Name == ir.EffectUnmount
	})
}

// effectNodes is every effect in a view body.
func effectNodes(body []ir.Stmt) []*ir.NodeInst {
	var out []*ir.NodeInst
	var visit func(stmts []ir.Stmt)
	visit = func(stmts []ir.Stmt) {
		for _, s := range stmts {
			if n, ok := s.(*ir.NodeInst); ok && isEffectNode(n) {
				out = append(out, n)
			}
			for _, b := range ir.ViewBlocks(s) {
				visit(*b)
			}
		}
	}
	visit(body)
	return out
}

// retainedIn rewrites each allocation s hands a `#[retains]` parameter to
// pass the cell it is stored in, returning what runs before and after s.
func (st *ownedStateRun) retainedIn(c *ir.Component, s ir.Stmt) (pre, post []ir.Stmt, cells []*ir.Var) {
	_ = ir.Walk(s, func(n ir.Node) error {
		if _, lambda := n.(*ir.Lambda); lambda {
			return ir.SkipDir
		}
		call, ok := n.(*ir.Call)
		if !ok || call.Func == nil {
			return nil
		}
		off := len(call.Args) - len(call.Func.Params)
		for i := range call.Args {
			j := paramIndex(call, i, off)
			arg := call.Args[i].Value
			if j < 0 || j >= len(call.Func.Params) || call.Func.Params[j].Transfer != ir.TransferRetains || !isAllocation(arg) {
				continue
			}
			typ := arg.ExprType()
			cell := &ir.Var{Name: st.name("__ret"), Pkg: c.Pkg, Type: ir.OptionOf(typ), Init: nullLit(), Synthesized: true}
			c.Vars = append(c.Vars, cell)
			cells = append(cells, cell)
			old := &ir.Var{Name: st.name("__old"), Type: cell.Type, Synthesized: true}
			pre = append(pre,
				&ir.LocalVar{Name: old.Name, Type: old.Type, Init: identOf(cell), Sym: old},
				&ir.Assign{Target: identOf(cell), Value: arg},
			)
			call.Args[i].Value = &ir.Conversion{Type: typ, Operand: identOf(cell)}
			post = append(post, releaseIfHeld(identOf(old), typ))
		}
		return nil
	})
	return pre, post, cells
}

// releaseCell is cell emptied and what it held released.
func (st *ownedStateRun) releaseCell(cell *ir.Var) []ir.Stmt {
	held := &ir.Var{Name: st.name("__held"), Type: cell.Type, Synthesized: true}
	return []ir.Stmt{
		&ir.LocalVar{Name: held.Name, Type: held.Type, Init: identOf(cell), Sym: held},
		&ir.Assign{Target: identOf(cell), Value: nullLit()},
		releaseIfHeld(identOf(held), cell.Type.Elems[0]),
	}
}

func releaseIfHeld(opt ir.Expr, typ *ir.Type) ir.Stmt {
	return &ir.If{
		Cond: &ir.Binary{Type: ir.TypBool, Op: ast.BinNeq, Left: opt, Right: nullLit()},
		Body: []ir.Stmt{releaseStmt(&ir.Conversion{Type: typ, Operand: ir.CloneExprSharingDecls(opt)})},
	}
}

func identOf(v *ir.Var) *ir.Ident {
	return &ir.Ident{Name: v.Name, Sym: v, Type: v.Type}
}

func nullLit() ir.Expr {
	return &ir.Literal{Type: ir.TypNull, Value: "null"}
}
