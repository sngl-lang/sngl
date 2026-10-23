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
	apply:   func(pkg *ir.Package, _ Features, _ Options) error { return OwnedState(pkg) },
}

// OwnedState rewrites every component the program reaches, the package's own
// and each reached library one: a build's copy of a library component is its
// own, and the interpreter runs one where it is declared. A library
// component the program does not reach is the shared, frozen one, and is
// left alone.
func OwnedState(pkg *ir.Package) error {
	if pkg == nil {
		return nil
	}
	comps := slices.Clone(pkg.Components)
	roots := []any{pkg}
	// A user package's component is already listed on pkg (lower.Lower), and
	// rewritten twice it would release its vars twice.
	seen := map[*ir.Component]bool{}
	for _, c := range comps {
		seen[c] = true
	}
	for _, p := range importedPackages(pkg) {
		for _, c := range p.Components {
			if !c.Frozen && !seen[c] {
				seen[c] = true
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
	owned   map[*ir.Var]bool
	// written is each assignment the pass made or rewrote, which is already
	// the overwrite it would otherwise become; taken each `remove` or
	// `clear` it rewrote, since a walk reaches a lambda's block twice.
	written map[*ir.Assign]bool
	taken   map[*ir.CallStmt]bool
	// retained is each cell a reporting effect's handler fills, by the
	// component releasing it when the node unmounts.
	retained map[*ir.Component][]*ir.Var
	// cells is every `__retN` cell: an option of an owned type, which is
	// owned by what it holds, but released by retain's own statements.
	cells map[*ir.Var]bool
	// locals is each var a block declares, which names a value rather than
	// holding it, and aliases those among them initialized from what a holder
	// holds, which a store of retains as a store of the holder's own would.
	locals  map[*ir.Var]bool
	aliases map[*ir.Var]bool
}

func ownedState(pkg *ir.Package, comps []*ir.Component, roots []any) error {
	st := &ownedStateRun{
		pkg:      pkg,
		owned:    map[*ir.Var]bool{},
		written:  map[*ir.Assign]bool{},
		taken:    map[*ir.CallStmt]bool{},
		retained: map[*ir.Component][]*ir.Var{},
		cells:    map[*ir.Var]bool{},
		locals:   map[*ir.Var]bool{},
		aliases:  map[*ir.Var]bool{},
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
	// After the derived values are reads of their cells: a call of one in an
	// initializer is not a temp, and bound to a var of its own was released
	// twice (`reads` in testdata/owned_state.sngl).
	for _, c := range comps {
		st.initTemps(&c.Vars, c.Pkg)
	}
	st.initTemps(&pkg.Vars, pkg)
	for _, p := range importedPackages(pkg) {
		if !p.Library {
			st.initTemps(&p.Vars, p)
		}
	}
	for _, c := range comps {
		st.collect(c.Vars)
		st.blockVars(&c.Stmts)
	}
	st.collect(pkg.Vars)
	for _, p := range importedPackages(pkg) {
		if !p.Library {
			st.collect(p.Vars)
		}
	}
	st.blockVars(&pkg.Body)
	for _, root := range roots {
		st.findAliases(root)
	}
	for _, c := range comps {
		st.storedInits(c.Vars)
	}
	st.storedInits(pkg.Vars)
	for _, p := range importedPackages(pkg) {
		if !p.Library {
			st.storedInits(p.Vars)
		}
	}
	for _, root := range roots {
		ir.RewriteBlocks(root, st.overwrites)
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
		if ir.IsOwnedDerived(fn) {
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
		reads := inputs(c, fn, cells)
		c.Vars = slices.Insert(c.Vars, cellIndex(c, fn, reads), cell)
		key := st.key(c, reads)
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

// cellIndex is where fn's cell goes among c's vars: before the first var
// whose initializer reads it, which otherwise read the cell's zero value,
// and after every var the cell reads itself. A reader declared before one of
// those is a cycle, and the cell goes last as it did.
func cellIndex(c *ir.Component, fn *ir.Func, reads []ir.Symbol) int {
	at := len(c.Vars)
	for i, v := range c.Vars {
		if v.Init == nil {
			continue
		}
		calls := false
		_ = ir.Walk(v.Init, func(n ir.Node) error {
			if call, ok := n.(*ir.Call); ok && call.Func == fn {
				calls = true
				return ir.SkipAll
			}
			return nil
		})
		if calls {
			at = i
			break
		}
	}
	for _, v := range c.Vars[at:] {
		if slices.Contains(reads, ir.Symbol(v)) {
			return len(c.Vars)
		}
	}
	return at
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

// collect records each owned var among vars, a component's or the package's.
// A package var's overwrite releases as a component's does; its last value is
// ReleaseTemps's to release at teardown, which the effect lowering writes
// afresh after this pass.
func (st *ownedStateRun) collect(vars []*ir.Var) {
	for _, v := range vars {
		if v.IsConst || v.NodeHandle || st.cells[v] || !ir.Owned(v.Type) {
			continue
		}
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
		if !ok || lv.Sym == nil || !ir.Owned(lv.Type) {
			continue
		}
		st.owned[lv.Sym] = true
		lv.Init = st.stored(lv.Init)
		release := ir.EventHandler{Name: ir.EffectUnmount, Func: &ir.Func{Pkg: st.pkg, Block: []ir.Stmt{st.release(identOf(lv.Sym))}}}
		out = append(out, st.effectNode(nil, release))
	}
	*body = out
}

// overwrites is stmts with each write of an owned value releasing what it
// replaces or takes out, once the new value is in place: an assignment to an
// owned var, to an element of a list or a map, or to a field; and a `remove`
// or `clear` of a list whose elements are owned.
func (st *ownedStateRun) overwrites(stmts []ir.Stmt) []ir.Stmt {
	var out []ir.Stmt
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.Assign:
			if !st.written[n] && n.Op == ast.AssignSet {
				if rewritten := st.overwrite(n); rewritten != nil {
					out = append(out, rewritten...)
					continue
				}
			}
		case *ir.CallStmt:
			st.putIn(n)
			if !st.taken[n] {
				if rewritten := st.takeOut(n); rewritten != nil {
					st.taken[n] = true
					out = append(out, rewritten...)
					continue
				}
			}
		}
		out = append(out, s)
	}
	return out
}

// putIn retains what a `push` or an `insert` hands the list, where it is
// another holder's: the list holds its own from here, as a var assigned one
// does.
func (st *ownedStateRun) putIn(s *ir.CallStmt) {
	call := s.Call
	if call == nil || call.Func == nil {
		return
	}
	switch call.Func.Intrinsic {
	case "list.push", "list.insert":
		last := len(call.Args) - 1
		if last > 0 {
			call.Args[last].Value = st.stored(call.Args[last].Value)
		}
	}
}

func (st *ownedStateRun) overwrite(a *ir.Assign) []ir.Stmt {
	if v := st.assigned(a); v != nil {
		return st.replacing(a, v.Type, ir.CloneExprSharingDecls(a.Target))
	}
	t := a.Target.ExprType()
	if t == nil || !ir.Owned(t) {
		return nil
	}
	switch target := a.Target.(type) {
	case *ir.Select:
		if ot := target.Operand.ExprType(); ot != nil && ot.Kind == ir.TypeStruct {
			return st.replacing(a, t, ir.CloneExprSharingDecls(a.Target))
		}
	case *ir.Index:
		ct := target.Operand.ExprType()
		if ct == nil {
			return nil
		}
		switch ct.Kind {
		case ir.TypeList:
			idx := st.bindIndex(target)
			return append([]ir.Stmt{idx}, st.replacing(a, t, ir.CloneExprSharingDecls(a.Target))...)
		case ir.TypeMap:
			idx := st.bindIndex(target)
			old := &ir.Var{Name: st.name("__old"), Type: ir.OptionOf(t), Synthesized: true}
			take := &ir.Assign{Target: identOf(old), Value: &ir.Conversion{Type: old.Type, Operand: ir.CloneExprSharingDecls(a.Target)}}
			st.written[take] = true
			had := &ir.If{
				Cond: &ir.Call{Type: ir.TypBool, Func: ir.IntrinsicMethod("contains", "map.contains", ct, ir.TypBool), Args: []ir.CallArg{{Value: ir.CloneExprSharingDecls(target.Operand)}, {Value: ir.CloneExprSharingDecls(target.Idx)}}},
				Body: []ir.Stmt{take},
			}
			val := st.bindNew(a)
			return []ir.Stmt{idx, val, &ir.LocalVar{Name: old.Name, Type: old.Type, Init: nullLit(), Sym: old}, had, a, st.releaseIfHeld(identOf(old), t)}
		}
	}
	return nil
}

// replacing is a with what the target held released once the new value is
// in place. The new value is bound first, then the old: an assignment whose
// value waits is split by AsyncOffload at the wait, and the old value read
// ahead of it was the one before the wait, not the one at the write -- two
// clicks in flight released it twice.
func (st *ownedStateRun) replacing(a *ir.Assign, t *ir.Type, read ir.Expr) []ir.Stmt {
	val := st.bindNew(a)
	old := &ir.Var{Name: st.name("__old"), Type: t, Synthesized: true}
	return []ir.Stmt{
		val,
		&ir.LocalVar{Name: old.Name, Type: t, Init: read, Sym: old},
		a,
		st.release(identOf(old)),
	}
}

// bindNew binds a's value to a local a reads instead, and marks a as the
// overwrite it has become.
func (st *ownedStateRun) bindNew(a *ir.Assign) ir.Stmt {
	t := a.Target.ExprType()
	val := &ir.Var{Name: st.name("__new"), Type: t, Synthesized: true}
	decl := &ir.LocalVar{Name: val.Name, Type: t, Init: st.stored(a.Value), Sym: val}
	a.Value = identOf(val)
	st.written[a] = true
	return decl
}

// bindIndex binds the index an element write names to a local the write and
// the read of the old element share, so it is evaluated once.
func (st *ownedStateRun) bindIndex(target *ir.Index) ir.Stmt {
	t := target.Idx.ExprType()
	idx := &ir.Var{Name: st.name("__idx"), Type: t, Synthesized: true}
	decl := &ir.LocalVar{Name: idx.Name, Type: t, Init: target.Idx, Sym: idx}
	target.Idx = identOf(idx)
	return decl
}

// takeOut is s with what a list's `remove` takes out released once it is
// out, and what `clear` empties released before it is: `remove` of an index
// outside the list takes nothing out.
func (st *ownedStateRun) takeOut(s *ir.CallStmt) []ir.Stmt {
	call := s.Call
	if call == nil || call.Func == nil || len(call.Args) == 0 {
		return nil
	}
	list := call.Args[0].Value
	lt := list.ExprType()
	if lt == nil || lt.Kind != ir.TypeList || !ir.Owned(lt) {
		return nil
	}
	elem := lt.Elems[0]
	switch call.Func.Intrinsic {
	case "list.clear":
		return []ir.Stmt{st.release(ir.CloneExprSharingDecls(list)), s}
	case "list.remove":
		if len(call.Args) != 2 {
			return nil
		}
		idx := &ir.Var{Name: st.name("__idx"), Type: ir.TypInt, Synthesized: true}
		bound := &ir.LocalVar{Name: idx.Name, Type: ir.TypInt, Init: call.Args[1].Value, Sym: idx}
		call.Args[1].Value = identOf(idx)
		old := &ir.Var{Name: st.name("__old"), Type: ir.OptionOf(elem), Synthesized: true}
		length := &ir.Call{Type: ir.TypInt, Func: ir.IntrinsicMethod("length", "list.length", lt, ir.TypInt), Args: []ir.CallArg{{Value: ir.CloneExprSharingDecls(list)}}}
		inRange := &ir.Binary{Type: ir.TypBool, Op: ast.BinAnd,
			Left:  &ir.Binary{Type: ir.TypBool, Op: ast.BinGte, Left: identOf(idx), Right: &ir.Literal{Type: ir.TypInt, Value: "0"}},
			Right: &ir.Binary{Type: ir.TypBool, Op: ast.BinLt, Left: identOf(idx), Right: length},
		}
		take := &ir.Assign{Target: identOf(old), Value: &ir.Conversion{Type: old.Type, Operand: &ir.Index{Type: elem, Operand: ir.CloneExprSharingDecls(list), Idx: identOf(idx)}}}
		st.written[take] = true
		return []ir.Stmt{
			bound,
			&ir.LocalVar{Name: old.Name, Type: old.Type, Init: nullLit(), Sym: old},
			&ir.If{Cond: inRange, Body: []ir.Stmt{take}},
			s,
			st.releaseIfHeld(identOf(old), elem),
		}
	}
	return nil
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
			if i := slices.IndexFunc(comp.Vars, func(v *ir.Var) bool { return v.Name == t.Field }); i >= 0 && st.owned[comp.Vars[i]] {
				return comp.Vars[i]
			}
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
			block = append(block, st.release(identOf(v)))
		}
	}
	if len(block) == 0 {
		return
	}
	h := ir.EventHandler{Name: ir.EffectUnmount, Func: &ir.Func{Pkg: c.Pkg, Block: block}}
	c.Stmts = append([]ir.Stmt{st.effectNode(nil, h)}, c.Stmts...)
}

func (st *ownedStateRun) release(v ir.Expr) ir.Stmt {
	return releaseWith(st.pkg.ReleaseFor(v.ExprType()), v)
}

func releaseWith(release *ir.Func, v ir.Expr) ir.Stmt {
	return &ir.CallStmt{Call: &ir.Call{Type: ir.TypVoid, Func: release, Args: []ir.CallArg{{Value: v}}}}
}

// initTemps gives each allocation a var's initializer borrows -- `open(fd)`
// in `var n = size(open(fd))` -- a var of its own declared ahead of it, so
// it is held with what holds the var and released with it: a component's
// when the node unmounts, as every owned var of the component is; a
// package's at teardown (ReleaseTemps' packageVars). An initializer has no
// statement around it to release a temp after. A method of the component
// computing the initializer in place would make the temp a statement's, and
// is the shape to move to once two backends emit it: Go drops a synthesized
// method's result type, and Kotlin declares a component's functions after
// the vars, where an initializer cannot reach one.
func (st *ownedStateRun) initTemps(vars *[]*ir.Var, pkg *ir.Package) {
	var out []*ir.Var
	for _, v := range *vars {
		if !v.IsConst && v.Init != nil {
			var c tempCollector
			c.visit(&v.Init, false)
			for _, f := range c.found {
				held := &ir.Var{Name: st.name("__init"), Pkg: pkg, Type: f.expr.ExprType(), Init: f.expr}
				*f.slot = identOf(held)
				out = append(out, held)
			}
		}
		out = append(out, v)
	}
	*vars = out
}

// storedInits passes each var's initializer through stored.
func (st *ownedStateRun) storedInits(vars []*ir.Var) {
	for _, v := range vars {
		if st.owned[v] {
			v.Init = st.stored(v.Init)
		}
	}
}

// findAliases records every var a block under root declares, and which of
// them alias what a holder holds. A walk meets a declaration before any
// read of it, so an alias of an alias is one too.
func (st *ownedStateRun) findAliases(root any) {
	_ = ir.Walk(root, func(n ir.Node) error {
		if lv, ok := n.(*ir.LocalVar); ok && lv.Sym != nil && !st.owned[lv.Sym] {
			st.locals[lv.Sym] = true
			if ir.RetainOf(lv.Type) != nil && st.holderRead(lv.Init) {
				st.aliases[lv.Sym] = true
			}
		}
		return nil
	})
}

// stored is e as a holder stores it: a read of what another holder holds, of
// a type that declares a retain, is passed through the retain, so the holder
// has its own to release; an allocation is the holder's already, and a local
// made one by a store moves it in. The literals and conversions a holder
// stores hold what is in them, so each is reached inside; a ternary's or a
// match's arm is stored by the arm taken.
func (st *ownedStateRun) stored(e ir.Expr) ir.Expr {
	switch x := e.(type) {
	case nil:
		return nil
	case *ir.Ternary:
		x.Then, x.Else = st.stored(x.Then), st.stored(x.Else)
		return x
	case *ir.MatchExpr:
		for _, arm := range x.Arms {
			arm.Value = st.stored(arm.Value)
		}
		return x
	case *ir.ListLit:
		for i := range x.Elems {
			x.Elems[i] = st.stored(x.Elems[i])
		}
		return x
	case *ir.MapLitIR:
		for i := range x.Entries {
			x.Entries[i].Value = st.stored(x.Entries[i].Value)
		}
		return x
	case *ir.StructLit:
		for i := range x.Fields {
			x.Fields[i].Value = st.stored(x.Fields[i].Value)
		}
		return x
	case *ir.Conversion:
		if ir.RetainOf(x.Type) == nil {
			x.Operand = st.stored(x.Operand)
			return x
		}
	}
	rt := ir.RetainOf(e.ExprType())
	if rt == nil || !st.holderRead(e) {
		return e
	}
	return &ir.Call{Type: e.ExprType(), Func: rt, Args: []ir.CallArg{{Value: e}}}
}

// holderRead reports whether e reads a value some holder holds: a var other
// than a local, a local aliasing one, a parameter -- the caller's -- a loop
// variable, a field, an element, or what an intrinsic reads out of a
// collection, through the copy the checker wraps a struct read in. An
// allocation is nobody's yet, and a local made one names it.
func (st *ownedStateRun) holderRead(e ir.Expr) bool {
	switch x := e.(type) {
	case *ir.Ident:
		switch s := x.Sym.(type) {
		case *ir.Var:
			return !st.locals[s] || st.aliases[s]
		case *ir.Param, *ir.LoopVar:
			return true
		}
	case *ir.Select, *ir.Index:
		return true
	case *ir.Call:
		if ir.IsValueCopy(x) {
			return st.holderRead(x.Args[0].Value)
		}
		return x.Func != nil && x.Func.Intrinsic != ""
	case *ir.Conversion:
		return st.holderRead(x.Operand)
	case *ir.Ternary:
		return st.holderRead(x.Then) && st.holderRead(x.Else)
	}
	return false
}

// retain gives each allocation a component hands a `#[retains]` parameter a
// cell of c's: the next one the call makes replaces it, released once the
// native has the new one, and the lifetime that made the call releases the
// last, which is when the native stops holding it. For a bracketing effect's
// handler that is the effect's own `@unmount`. An effect that only reports
// writes brackets nothing, and an `@unmount` would make it a bracket ended by
// every change of its key, releasing what the native still holds; its cells,
// and those of every other call a node encloses -- an event handler, a
// timer's `@tick`, a component function -- are released when the node
// unmounts. A call no component encloses is the checker's to refuse.
func (st *ownedStateRun) retain(c *ir.Component) {
	for _, n := range effectNodes(c.Stmts) {
		var cells []*ir.Var
		for _, h := range n.Handlers {
			if h.Name == ir.EffectUnmount || h.Func == nil {
				continue
			}
			h.Func.Block = ir.RewriteBlocks(h.Func.Block, st.retaining(c, &cells))
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
	// An effect's handler is reached again here and rewritten already: its
	// argument reads a cell now and allocates nothing.
	var cells []*ir.Var
	for _, fn := range c.Funcs {
		fn.Block = ir.RewriteBlocks(fn.Block, st.retaining(c, &cells))
	}
	c.Stmts = ir.RewriteBlocks(c.Stmts, st.retaining(c, &cells))
	st.retained[c] = append(st.retained[c], cells...)
}

// retaining is the block rewrite that gives each statement's retained
// allocations their cells, appending the cells it makes to cells.
func (st *ownedStateRun) retaining(c *ir.Component, cells *[]*ir.Var) func([]ir.Stmt) []ir.Stmt {
	return func(stmts []ir.Stmt) []ir.Stmt {
		var out []ir.Stmt
		for _, s := range stmts {
			pre, post, made := st.retainedIn(c, s)
			*cells = append(*cells, made...)
			out = append(append(append(out, pre...), s), post...)
		}
		return out
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
// pass the cell it is stored in, returning what runs before and after s. A
// statement s encloses -- a handler's, a lambda's, a child's -- is its own
// block's, and is rewritten where it stands.
func (st *ownedStateRun) retainedIn(c *ir.Component, s ir.Stmt) (pre, post []ir.Stmt, cells []*ir.Var) {
	_ = ir.Walk(s, func(n ir.Node) error {
		if _, lambda := n.(*ir.Lambda); lambda {
			return ir.SkipDir
		}
		if nested, ok := n.(ir.Stmt); ok && nested != s {
			return ir.SkipDir
		}
		call, ok := n.(*ir.Call)
		if !ok || call.Func == nil {
			return nil
		}
		for i := range call.Args {
			p := call.ParamFor(i)
			arg := call.Args[i].Value
			if p == nil || p.Transfer != ir.TransferRetains || !isAllocation(arg) {
				continue
			}
			typ := arg.ExprType()
			cell := &ir.Var{Name: st.name("__ret"), Pkg: c.Pkg, Type: ir.OptionOf(typ), Init: nullLit(), Synthesized: true}
			c.Vars = append(c.Vars, cell)
			cells = append(cells, cell)
			st.cells[cell] = true
			old := &ir.Var{Name: st.name("__old"), Type: cell.Type, Synthesized: true}
			pre = append(pre,
				&ir.LocalVar{Name: old.Name, Type: old.Type, Init: identOf(cell), Sym: old},
				&ir.Assign{Target: identOf(cell), Value: arg},
			)
			call.Args[i].Value = &ir.Conversion{Type: typ, Operand: identOf(cell)}
			post = append(post, st.releaseIfHeld(identOf(old), typ))
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
		st.releaseIfHeld(identOf(held), cell.Type.Elems[0]),
	}
}

func (st *ownedStateRun) releaseIfHeld(opt ir.Expr, typ *ir.Type) ir.Stmt {
	return &ir.If{
		Cond: &ir.Binary{Type: ir.TypBool, Op: ast.BinNeq, Left: opt, Right: nullLit()},
		Body: []ir.Stmt{st.release(&ir.Conversion{Type: typ, Operand: ir.CloneExprSharingDecls(opt)})},
	}
}

func identOf(v *ir.Var) *ir.Ident {
	return &ir.Ident{Name: v.Name, Sym: v, Type: v.Type}
}

func nullLit() ir.Expr {
	return &ir.Literal{Type: ir.TypNull, Value: "null"}
}
