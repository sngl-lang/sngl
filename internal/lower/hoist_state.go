package lower

import (
	"fmt"
	"slices"
	"strconv"

	"duckfam.us/sngl/ir"
)

// passHoistState makes a `var` written in a block of a view state of that
// block, as a component's `var` is state of the component.
//
// The checker collects a component's own `var` into comp.Vars and the
// package's into pkg.Vars; one written anywhere else in a view -- a node's
// children, a loop's body, a branch, a page, a window's body -- it leaves as
// an *ir.LocalVar statement where it was written. No target implements that
// shape: bubbletea and gtk4 emitted it as a local of the render function,
// reset on every frame, with the handler writing a name the Model does not
// have, and android and html declared it nowhere at all.
//
// What the block is decides where the state lives, and nothing about a
// window does:
//
//   - A block rendered once -- a window's top level, a vbox outside any loop
//     or branch over state -- is an instance spliced where it is written, so
//     its vars join its owner (the component it is in, or the package) under
//     their own names, renamed only where the owner already has the name.
//   - A block under a `for`, an `if` over state, a page or a slot's population
//     is wrapped in a component synthesized for it, `__block_state<N>`, whose
//     vars are the block's, and the inliner does with the instance what it
//     does with any component with state: one per copy under the loop, built
//     again when the branch or the page comes back. What the block reads of
//     the scope it is written in arrives as props -- a loop's variable, a
//     slot's parameter, the owner's props -- and what it reads of the owner's
//     state as a two-way prop bound to it, so a write in a copy is a write of
//     the owner's.
//
// First of the lowering, so every pass after it sees state where state is.
var passHoistState = pass{
	name:    "HoistState",
	enabled: func(Features) bool { return true },
	apply:   applyHoistState,
}

func applyHoistState(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	st := &blockState{pkg: pkg, renamed: map[*ir.Var]string{}}
	// The package's own top level is its state, as a component's is.
	pkg.Body, pkg.Vars = promoteLocalVarsToVars(pkg.Body, pkg.Vars)
	po := st.packageOwner()
	pkg.Body = st.walk(po, pkg.Body, false, true)
	for _, comp := range pkg.Components {
		if comp != nil {
			comp.Body = st.walk(st.componentOwner(comp), comp.Body, false, true)
		}
	}
	pkg.Components = append(pkg.Components, st.made...)
	if st.err != nil {
		return st.err
	}
	if len(st.renamed) == 0 {
		return nil
	}
	return ir.Rewrite(pkg, func(n ir.Node) (ir.Node, error) {
		if id, ok := n.(*ir.Ident); ok {
			if v, ok := id.Sym.(*ir.Var); ok {
				if name, ok := st.renamed[v]; ok {
					id.Name = name
				}
			}
		}
		return n, nil
	})
}

type blockState struct {
	pkg     *ir.Package
	made    []*ir.Component
	renamed map[*ir.Var]string
	err     error
}

// stateOwner is what a block's vars join when the block is rendered once.
type stateOwner struct {
	comp  *ir.Component // nil for the package
	vars  *[]*ir.Var
	names map[string]bool
}

func (st *blockState) packageOwner() *stateOwner {
	o := &stateOwner{vars: &st.pkg.Vars, names: map[string]bool{}}
	for _, v := range st.pkg.Vars {
		o.names[v.Name] = true
	}
	for _, v := range st.pkg.Consts {
		o.names[v.Name] = true
	}
	for _, f := range st.pkg.Funcs {
		o.names[f.Name] = true
	}
	return o
}

func (st *blockState) componentOwner(c *ir.Component) *stateOwner {
	o := &stateOwner{comp: c, vars: &c.Vars, names: map[string]bool{}}
	for _, v := range c.Vars {
		o.names[v.Name] = true
	}
	for _, p := range c.Props {
		o.names[p.Name] = true
	}
	for _, f := range c.Funcs {
		o.names[f.Name] = true
	}
	return o
}

// walk lowers the blocks of a view. dyn says the statements are rendered
// other than once -- under a loop, a branch over state, a page; top that they
// are the owner's own body, whose vars the checker already made its state.
func (st *blockState) walk(o *stateOwner, stmts []ir.Stmt, dyn, top bool) []ir.Stmt {
	if !top && hasLocalVar(stmts) {
		if !dyn {
			stmts = st.promote(o, stmts)
		} else {
			return []ir.Stmt{st.wrap(o, stmts)}
		}
	}
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			d := dyn
			if n.Component != nil && n.Component.Builtin == ir.BuiltinNavPage {
				// A page is mounted while it shows and gone when it does not.
				d = true
			}
			n.Children = st.walk(o, n.Children, d, false)
			for _, name := range ir.SlotNames(n.Slots) {
				// A population renders where the component inserts it, which
				// may be any number of times.
				n.Slots[name].Body = st.walk(o, n.Slots[name].Body, true, false)
			}
		case *ir.If:
			d := dyn || !ir.IsConst(n.Cond)
			n.Body = st.walk(o, n.Body, d, false)
			n.Else = st.walk(o, n.Else, d, false)
		case *ir.For:
			n.Body = st.walk(o, n.Body, true, false)
			n.Else = st.walk(o, n.Else, dyn, false)
		case *ir.ErrorBoundary:
			n.Children = st.walk(o, n.Children, dyn, false)
			n.Failed = st.walk(o, n.Failed, dyn, false)
		case *ir.ContextProvider:
			// passContext splices a provider's children into the body around it
			// and promotes the vars it held there, with the provided value in
			// scope for their initializers; rendered once, they are its to
			// promote. Rendered other than once, the block is an instance like
			// any other, and the provided value reaches it as a context does.
			n.Children = st.walk(o, n.Children, dyn, !dyn)
		case *ir.SlotInst:
			n.Children = st.walk(o, n.Children, dyn, false)
			for _, name := range ir.SlotNames(n.Slots) {
				// An entry's population renders wherever the population it
				// was handed inserts the entry -- any number of times, as a
				// node's population does.
				n.Slots[name].Body = st.walk(o, n.Slots[name].Body, true, false)
			}
		}
	}
	return stmts
}

func hasLocalVar(stmts []ir.Stmt) bool {
	for _, s := range stmts {
		if _, ok := s.(*ir.LocalVar); ok {
			return true
		}
	}
	return false
}

// promote makes a block's vars its owner's, under their own names where the
// owner has none of the name.
func (st *blockState) promote(o *stateOwner, stmts []ir.Stmt) []ir.Stmt {
	var vars []*ir.Var
	stmts, vars = promoteLocalVarsToVars(stmts, nil)
	for _, v := range vars {
		if o.names[v.Name] {
			name := v.Name
			for i := 1; o.names[name]; i++ {
				name = v.Name + "__block" + strconv.Itoa(i)
			}
			v.Name = name
			st.renamed[v] = name
		}
		o.names[v.Name] = true
		*o.vars = append(*o.vars, v)
	}
	return stmts
}

// wrap is the instantiation of a component synthesized for a block rendered
// other than once, with the block as its body and the block's vars its own.
func (st *blockState) wrap(o *stateOwner, stmts []ir.Stmt) ir.Stmt {
	w := &ir.Component{Name: "__block_state" + strconv.Itoa(len(st.made)), Tree: treeOfStmts(stmts)}
	st.made = append(st.made, w)
	w.Body, w.Vars = promoteLocalVarsToVars(stmts, nil)
	w.Body = st.walk(st.componentOwner(w), w.Body, false, true)

	site := &ir.NodeInst{Name: w.Name, Component: w}
	inside := declaredWithin(w.Body)
	for _, v := range w.Vars {
		inside[v] = true
	}
	ownerVars := map[*ir.Var]bool{}
	if o.comp != nil {
		for _, v := range *o.vars {
			ownerVars[v] = true
		}
	}
	params := map[ir.Symbol]*ir.Param{}
	propNames := map[string]bool{}
	for _, v := range w.Vars {
		propNames[v.Name] = true
	}
	free := func(sym ir.Symbol, name string, t *ir.Type, twoWay bool) *ir.Param {
		if p := params[sym]; p != nil {
			return p
		}
		pn := name
		for i := 1; propNames[pn]; i++ {
			pn = name + "__" + strconv.Itoa(i)
		}
		propNames[pn] = true
		p := &ir.Param{Name: pn, Type: t}
		params[sym] = p
		w.Props = append(w.Props, &ir.Prop{Name: pn, Type: t, Bidirectional: twoWay, Sym: p})
		arg := &ir.Ident{Name: name, Type: t, Sym: sym}
		site.Props = append(site.Props, ir.Arg{Name: pn, Value: arg})
		if twoWay {
			site.Bindings = append(site.Bindings, ir.PropBinding{PropName: pn, Target: &ir.Ident{Name: name, Type: t, Sym: sym}})
		}
		return p
	}
	_ = ir.RewriteExprs(w, func(e ir.Expr) (ir.Expr, error) {
		id, ok := e.(*ir.Ident)
		if !ok || id.Sym == nil || inside[id.Sym] {
			return e, nil
		}
		var p *ir.Param
		switch s := id.Sym.(type) {
		case *ir.LoopVar:
			p = free(s, s.Name, s.Type, false)
		case *ir.Param:
			if !s.Receiver {
				p = free(s, s.Name, s.Type, false)
			}
		case *ir.Var:
			if ownerVars[s] {
				p = free(s, s.Name, s.Type, true)
			}
		case *ir.Func:
			if o.comp != nil && st.err == nil && containsFunc(o.comp.Funcs, s) {
				st.err = fmt.Errorf("%s: a block that declares state here is built once per copy, and it calls %s, a function of %s around it, which a copy cannot reach; call it from a handler outside the block, or move the state into a component", stmtsPos(stmts), s.Name, o.comp.Name)
			}
		}
		if p == nil {
			return e, nil
		}
		return &ir.Ident{AST: id.AST, Name: p.Name, Type: id.Type, Sym: p}, ir.SkipDir
	})
	return site
}

// treeOfStmts is the family the first node a block renders belongs to,
// which is the one the block's component stands in.
func treeOfStmts(stmts []ir.Stmt) *ir.Component {
	var tree *ir.Component
	_ = ir.WalkStmts(stmts, func(s ir.Stmt) error {
		if n, ok := s.(*ir.NodeInst); ok && n.Component != nil && n.Component.Tree != nil {
			tree = n.Component.Tree
			return ir.SkipAll
		}
		return nil
	})
	return tree
}

func containsFunc(fs []*ir.Func, f *ir.Func) bool {
	return slices.Contains(fs, f)
}

func stmtsPos(stmts []ir.Stmt) string {
	for _, s := range stmts {
		if lv, ok := s.(*ir.LocalVar); ok && lv.AST != nil {
			return lv.AST.Pos.String()
		}
	}
	return "<unknown>"
}
