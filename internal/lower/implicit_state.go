package lower

import (
	"fmt"
	"slices"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// passImplicitState gives a two-way prop the call site left unbound a cell of
// the instance's own (ir.UnboundProps).
//
// `checkbox #box(label="x")` becomes an instantiation of a component written
// for it,
//
//	component __checkbox_state0(__start_checked bool = false, label string) node {
//	    var checked = __start_checked
//	    checkbox #box(:checked=checked, label=label)
//	}
//
// which is the whole of the meaning: the cell starts from what the call gave
// the prop, the host's reports write it through the ordinary binding, and the
// instance is a component with a `var`. So the inliner does with it what it
// does with any such component -- splices it once where it is written, builds
// it at run time in a reactive position, keeps a cell per copy under a `for`
// where the target keeps no instance state -- and no backend is told.
//
// A read of the prop off the node's `#id` names the cell. The inliner repoints
// it at the copy it splices (repointCellReads), since the read sits outside
// the component the cell belongs to.
//
// One component per call site rather than per declaration, because the `#id`
// goes inside with the node: the handle is what a test reaches the widget
// through, and it must name the widget rather than the wrapper.
//
// After PlatformExtensionBody, so an override body the program wrote is
// reached; before PropBindings, which lowers the binding this writes, and
// NodePropReads, which would otherwise answer the read with the value the call
// site wrote.
var passImplicitState = pass{
	name:    "ImplicitState",
	enabled: func(Features) bool { return true },
	apply:   lowerImplicitState,
}

func lowerImplicitState(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	st := &implicitState{pkg: pkg, reads: map[*ir.Var]map[string]*ir.Var{}, through: map[*ir.Component]map[string]*ir.Var{}}
	// The document a surface target writes its page from cannot leave the
	// screen and is told of no close, so its `visible` is a cell only where
	// the program reaches it through the `#id` -- a read of `home.visible`, a
	// `home.open()` -- and nowhere else.
	if surfaces, err := findSurfaces(pkg); err == nil && surfaces != nil && !handleReferenced(pkg, surfaces.doc.Handle) {
		st.skip = surfaces.doc
	}
	for _, o := range ir.Owners(pkg) {
		st.owner = o.Comp
		if err := st.stmts(*o.Body); err != nil {
			return err
		}
	}
	pkg.Components = append(pkg.Components, st.made...)
	if len(st.reads) == 0 {
		return nil
	}
	return ir.Rewrite(pkg, func(n ir.Node) (ir.Node, error) {
		sel, ok := n.(*ir.Select)
		if !ok {
			return n, nil
		}
		h := handleThrough(sel.Operand, st.through)
		if h == nil {
			return n, nil
		}
		cell := st.reads[h][sel.Field]
		if cell == nil {
			return n, nil
		}
		return &ir.Ident{Name: cell.Name, Type: cell.Type, Sym: cell}, ir.SkipDir
	})
}

// handleThrough is the node handle e names: the handle itself, or a select of
// it through an instance of the component that declares it -- a test's
// `c.box` -- whose read of `c.box.checked` is the same cell's.
func handleThrough(e ir.Expr, through map[*ir.Component]map[string]*ir.Var) *ir.Var {
	switch x := e.(type) {
	case *ir.Ident:
		if h, ok := x.Sym.(*ir.Var); ok {
			return h
		}
	case *ir.Select:
		if t := typeOf(x.Operand); t != nil {
			if c, ok := t.Decl.(*ir.Component); ok {
				return through[c][x.Field]
			}
		}
	}
	return nil
}

type implicitState struct {
	pkg  *ir.Package
	made []*ir.Component
	// owner is the component whose body is being walked, nil for any other
	// owner, and through each such component's wrapped handles by name.
	owner   *ir.Component
	through map[*ir.Component]map[string]*ir.Var
	// reads is each wrapped node's handle, and the cell behind each prop a
	// read of the handle names.
	reads map[*ir.Var]map[string]*ir.Var
	// skip is a node that keeps no cell: the document of a surface target
	// whose handle nothing reaches.
	skip *ir.NodeInst
}

func (st *implicitState) stmts(stmts []ir.Stmt) error {
	for i, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if err := st.stmts(n.Children); err != nil {
				return err
			}
			for _, sc := range n.Slots {
				if sc != nil {
					if err := st.stmts(sc.Body); err != nil {
						return err
					}
				}
			}
			w, err := st.wrap(n)
			if err != nil {
				return err
			}
			if w != nil {
				stmts[i] = w
			}
		case *ir.If:
			if err := st.stmts(n.Body); err != nil {
				return err
			}
			if err := st.stmts(n.Else); err != nil {
				return err
			}
		case *ir.For:
			if err := st.stmts(n.Body); err != nil {
				return err
			}
			if err := st.stmts(n.Else); err != nil {
				return err
			}
		case *ir.SlotInst:
			if err := st.stmts(n.Children); err != nil {
				return err
			}
		case *ir.ErrorBoundary:
			if err := st.stmts(n.Children); err != nil {
				return err
			}
			if err := st.stmts(n.Failed); err != nil {
				return err
			}
		case *ir.ContextProvider:
			if err := st.stmts(n.Children); err != nil {
				return err
			}
		}
	}
	return nil
}

// wrap is the instantiation that takes n's place, or nil when n leaves no
// two-way prop unbound.
func (st *implicitState) wrap(n *ir.NodeInst) (*ir.NodeInst, error) {
	comp := n.Component
	if comp == nil || n == st.skip {
		return nil, nil
	}
	unbound := ir.UnboundProps(comp, n.Bindings)
	if len(unbound) == 0 {
		return nil, nil
	}
	// Each is a gap no component in sngl:ui reaches: its two-way props are one
	// per component and none takes content.
	if len(n.Bindings) > 0 {
		return nil, fmt.Errorf("%s: %s binds :%s and leaves :%s unbound, which is not supported yet; bind both, or neither", nodePos(n), comp.DisplayName(), n.Bindings[0].PropName, unbound[0].Name)
	}
	rest := comp.RestSlot()
	var restPop *ir.SlotContent
	if rest != nil && len(n.Slots) == 1 {
		restPop = n.Slots[rest.Name]
	}
	if len(n.Slots) > 0 && restPop == nil || len(n.Children) > 0 && rest == nil {
		return nil, fmt.Errorf("%s: %s is given content and leaves :%s unbound, which is not supported yet; bind :%s", nodePos(n), comp.DisplayName(), unbound[0].Name, unbound[0].Name)
	}

	w := &ir.Component{
		Name: "__" + identSafe(comp.Name) + "_state" + strconv.Itoa(len(st.made)),
		Tree: comp.Tree,
	}
	st.made = append(st.made, w)

	inner := &ir.NodeInst{
		AST:       n.AST,
		Name:      n.Name,
		Component: comp,
		ID:        n.ID,
		Handle:    n.Handle,
	}
	cells := map[string]*ir.Var{}
	for _, p := range unbound {
		start := p.Default
		if start == nil {
			start = ir.DeclaredDefault(p.Type)
		}
		init := &ir.Prop{Name: cellStartProp(p.Name), Type: p.Type, Default: start}
		init.Sym = &ir.Param{Name: init.Name, Type: p.Type}
		w.Props = append(w.Props, init)
		cell := &ir.Var{
			Name: p.Name,
			Type: p.Type,
			Init: &ir.Ident{Name: init.Name, Type: p.Type, Sym: init.Sym},
			Cell: true,
		}
		w.Vars = append(w.Vars, cell)
		cells[p.Name] = cell
		inner.Props = append(inner.Props, ir.Arg{Name: p.Name, Value: &ir.Ident{Name: cell.Name, Type: cell.Type, Sym: cell}})
		inner.Bindings = append(inner.Bindings, ir.PropBinding{PropName: p.Name, Target: &ir.Ident{Name: cell.Name, Type: cell.Type, Sym: cell}})
	}

	// What the call site gave every other prop travels in as a prop of the
	// same name; what it gave an unbound one is the cell's start.
	site := make([]ir.Arg, 0, len(n.Props))
	for _, a := range n.Props {
		if _, isCell := cells[a.Name]; isCell {
			site = append(site, ir.Arg{Name: cellStartProp(a.Name), NamePos: a.NamePos, Value: a.Value})
			continue
		}
		decl := declaredPropOf(comp, a.Name)
		if decl == nil {
			// A wildcard's entries, or a positional arg the checker already
			// named: nothing declares it to forward through.
			inner.Props = append(inner.Props, a)
			continue
		}
		p := &ir.Prop{Name: a.Name, Type: decl.Type, Construct: decl.Construct}
		p.Sym = &ir.Param{Name: p.Name, Type: p.Type}
		w.Props = append(w.Props, p)
		inner.Props = append(inner.Props, ir.Arg{Name: a.Name, Value: &ir.Ident{Name: p.Name, Type: p.Type, Sym: p.Sym}})
		site = append(site, a)
	}

	// The events the call site subscribes to are the wrapper's too, and the
	// node inside hands each on as it arrives.
	for _, h := range n.Handlers {
		decl := eventDeclOf(comp, h.Name)
		if decl == nil {
			continue
		}
		fwd := &ir.EventDecl{Name: decl.Name}
		relay := &ir.Func{Synthesized: true}
		emit := &ir.Emit{Name: decl.Name}
		for j, p := range decl.Params {
			fp := &ir.Param{Name: "__" + decl.Name + strconv.Itoa(j), Type: p.Type}
			fwd.Params = append(fwd.Params, &ir.Param{Name: p.Name, Type: p.Type})
			relay.Params = append(relay.Params, fp)
			emit.Args = append(emit.Args, ir.CallArg{Value: &ir.Ident{Name: fp.Name, Type: fp.Type, Sym: fp}})
		}
		relay.Block = []ir.Stmt{emit}
		w.Events = append(w.Events, fwd)
		inner.Handlers = append(inner.Handlers, ir.EventHandler{Name: decl.Name, Func: relay})
	}
	// Bare children stay at the call site, written in its scope, and the
	// wrapper's own rest slot hands them on to the node. It takes no
	// arguments even where the node's rest slot is scoped -- a window's
	// `content ...component(v T)` -- because bare children bind none.
	//
	// A population of that slot written by name -- a window's
	// `component content(v)` -- is handed on the same way with its argument:
	// the wrapper's slot takes it, and the node's own population inserts that
	// slot with what the node hands it.
	switch {
	case restPop != nil:
		fwd := &ir.SlotDecl{Name: rest.Name, Rest: true, Content: rest.Content, Card: rest.Card}
		slot := &ir.SlotInst{Name: fwd.Name, Rest: true, Decl: fwd}
		var params []*ir.Param
		for _, p := range restPop.Params {
			fwd.Params = append(fwd.Params, &ir.Param{Name: p.Name, Type: p.Type})
			fp := &ir.Param{Name: "__" + p.Name, Type: p.Type}
			params = append(params, fp)
			slot.Args = append(slot.Args, &ir.Ident{Name: fp.Name, Type: fp.Type, Sym: fp})
		}
		w.Slots = []*ir.SlotDecl{fwd}
		inner.Slots = map[string]*ir.SlotContent{rest.Name: {Params: params, Body: []ir.Stmt{slot}}}
	case len(n.Children) > 0:
		fwd := &ir.SlotDecl{Name: rest.Name, Rest: true, Content: rest.Content, Card: rest.Card}
		w.Slots = []*ir.SlotDecl{fwd}
		inner.Children = []ir.Stmt{&ir.SlotInst{Name: fwd.Name, Rest: true, Decl: fwd}}
	}
	w.Body = []ir.Stmt{inner}

	if n.Handle != nil {
		st.reads[n.Handle] = cells
		if st.owner != nil {
			if st.through[st.owner] == nil {
				st.through[st.owner] = map[string]*ir.Var{}
			}
			st.through[st.owner][n.Handle.Name] = n.Handle
		}
	}
	return &ir.NodeInst{
		AST:       n.AST,
		Name:      w.Name,
		Component: w,
		Props:     site,
		Handlers:  n.Handlers,
		Children:  n.Children,
		Slots:     n.Slots,
		Key:       n.Key,
		Ref:       n.Ref,
	}, nil
}

// cellStartProp is the wrapper's prop carrying where the cell for prop starts.
// Not `__<prop>`: that is the parameter passPropBindings gives the write-back
// handler of the binding the wrapper writes, and the splice binds a param by
// name, so the handler's `visible = __visible` read the start instead of what
// the host reported.
func cellStartProp(prop string) string { return "__start_" + prop }

func declaredPropOf(comp *ir.Component, name string) *ir.Prop {
	for _, p := range comp.Props {
		if p.Name == name && p.Wildcard == "" {
			return p
		}
	}
	return nil
}

func eventDeclOf(comp *ir.Component, name string) *ir.EventDecl {
	for _, e := range comp.Events {
		if e.Name == name {
			return e
		}
	}
	return nil
}

// identSafe is name with anything an identifier cannot hold replaced.
func identSafe(name string) string {
	return strings.Map(func(r rune) rune {
		if r == '_' || r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' {
			return r
		}
		return '_'
	}, name)
}

// implicitCellOwners is the component passImplicitState wrapped around each
// cell, taken before the inliner drops the ones it splices.
func implicitCellOwners(pkg *ir.Package) map[*ir.Var]*ir.Component {
	out := map[*ir.Var]*ir.Component{}
	for _, c := range pkg.Components {
		if c == nil {
			continue
		}
		for _, v := range c.Vars {
			if v.Cell {
				out[v] = c
			}
		}
	}
	return out
}

// repointCellReads points each read of an implicit cell made outside its
// component at the copy the inliner spliced where the node is rendered.
//
// The read is a `#id` read of the prop, which passImplicitState turned into a
// read of the cell; the component around the node was then spliced, and its
// renames reached only the body it spliced. One copy is the one the read
// means. None -- the node is in a component built at run time -- or several
// is a read that cannot say which instance it asks, the rule uniqueNodeIDs
// holds a handle to.
func repointCellReads(pkg *ir.Package, owners map[*ir.Var]*ir.Component, clones map[*ir.Var][]*ir.Var) error {
	if len(owners) == 0 {
		return nil
	}
	// A kept component's own reads of its cell are the instance reading its
	// state, and stay.
	own := map[*ir.Ident]bool{}
	wrappers := map[*ir.Component]bool{}
	for _, w := range owners {
		wrappers[w] = true
	}
	for _, c := range pkg.Components {
		if wrappers[c] {
			_ = ir.Walk(c, func(n ir.Node) error {
				if id, ok := n.(*ir.Ident); ok {
					own[id] = true
				}
				return nil
			})
		}
	}
	var bad error
	err := ir.Rewrite(pkg, func(n ir.Node) (ir.Node, error) {
		id, ok := n.(*ir.Ident)
		if !ok || own[id] {
			return n, nil
		}
		v, ok := id.Sym.(*ir.Var)
		if !ok {
			return n, nil
		}
		w := owners[v]
		if w == nil {
			return n, nil
		}
		copies := clones[v]
		if len(copies) == 1 {
			id.Name, id.Sym = copies[0].Name, copies[0]
			return n, nil
		}
		if bad == nil {
			bad = cellReadError(w, v, len(copies))
		}
		return n, nil
	})
	if err != nil {
		return err
	}
	return bad
}

func cellReadError(w *ir.Component, v *ir.Var, copies int) error {
	pos, id := "<unknown>", "?"
	if len(w.Body) > 0 {
		if inner, ok := w.Body[0].(*ir.NodeInst); ok {
			pos, id = nodePos(inner), inner.ID
		}
	}
	why := "is built at run time here"
	if copies > 1 {
		why = "is rendered more than once"
	}
	return fmt.Errorf("%s: `#%s` %s, so a read of its unbound :%s cannot say which copy it means; bind :%s to a var and read that", pos, id, why, v.Name, v.Name)
}

// repointHandleCalls points each call of a method through a node's `#id` at
// the clone the inliner made of it where it spliced the node: `details.open()`
// is a call of the one instance's `open`, which reads and writes that
// instance's state. The receiver goes with the rewrite, since the clone is a
// method of whatever the node was spliced into, as every other call of it is.
//
// The call names the declaration's method and a handle, and the splice renamed
// neither: left alone it came out as `m.details.open()`, a field and a method
// no emitted type has. A handle whose node was rendered more than once cannot
// say which copy the call means, the rule repointCellReads holds a read to.
func repointHandleCalls(pkg *ir.Package, methods map[*ir.Var]map[*ir.Func][]*ir.Func) error {
	if len(methods) == 0 {
		return refuseUnsplicedHandleCalls(pkg)
	}
	var bad error
	err := ir.Rewrite(pkg, func(n ir.Node) (ir.Node, error) {
		call, ok := n.(*ir.Call)
		if !ok || call.Func == nil {
			return n, nil
		}
		h, asArg := handleReceiver(call)
		if h == nil {
			return n, nil
		}
		clones := methods[h][call.Func]
		switch {
		case len(clones) == 1:
			call.Func, call.Receiver = clones[0], nil
			if asArg {
				call.Args = slices.Delete(slices.Clone(call.Args), 0, 1)
			}
		case len(clones) > 1 && bad == nil:
			bad = fmt.Errorf("%s: `#%s` is rendered more than once, so a call of its %s cannot say which copy it means; give each copy its own id", handleCallPos(call), h.Name, call.Func.Name)
		}
		return n, nil
	})
	if err != nil {
		return err
	}
	if bad != nil {
		return bad
	}
	return refuseUnsplicedHandleCalls(pkg)
}

// refuseUnsplicedHandleCalls reports a method called through a node's `#id`
// that no splice answered: the node is built at run time, or the target
// renders it as the builtin it is marked -- a window on html -- and in neither
// case is there one instance's method to call. Emitted, it was
// `details.open()` against nothing on html.
func refuseUnsplicedHandleCalls(pkg *ir.Package) error {
	var bad error
	_ = ir.Walk(pkg, func(n ir.Node) error {
		call, ok := n.(*ir.Call)
		if !ok || call.Func == nil || call.Event != "" || bad != nil {
			return nil
		}
		fn := call.Func
		if fn.Intrinsic != "" || fn.Foreign.Name != "" || len(fn.Block) == 0 {
			return nil
		}
		if h, _ := handleReceiver(call); h != nil {
			bad = fmt.Errorf("%s: `%s.%s()` calls a method of the node `#%s`, which this target does not splice where it is written, so there is no one instance's method to call", handleCallPos(call), h.Name, fn.Name, h.Name)
		}
		return nil
	})
	return bad
}

// handleReceiver is the node handle a method call is made through, and
// whether it is passed as the call's first argument -- the receiver-as-param
// form a component method is called in -- rather than as its Receiver.
func handleReceiver(call *ir.Call) (*ir.Var, bool) {
	if id, ok := call.Receiver.(*ir.Ident); ok {
		if h, ok := id.Sym.(*ir.Var); ok && h.NodeHandle {
			return h, false
		}
	}
	if len(call.Args) > 0 && len(call.Func.Params) > 0 && call.Func.Params[0].Receiver {
		if id, ok := call.Args[0].Value.(*ir.Ident); ok {
			if h, ok := id.Sym.(*ir.Var); ok && h.NodeHandle {
				return h, true
			}
		}
	}
	return nil, false
}

// handleCallPos is where the call is written: the handle it is made through,
// which is where a reader looks for `details.open()`.
func handleCallPos(c *ir.Call) string {
	if c.AST == nil {
		return "<unknown>"
	}
	if sel, ok := c.AST.Func.(*ast.SelectExpr); ok {
		if id, ok := sel.Operand.(*ast.IdentExpr); ok {
			return id.Pos.String()
		}
	}
	return c.AST.Pos.String()
}

// handleReferenced reports whether anything in pkg names h.
func handleReferenced(pkg *ir.Package, h *ir.Var) bool {
	if h == nil {
		return false
	}
	found := false
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if id, ok := n.(*ir.Ident); ok && id.Sym == ir.Symbol(h) {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}
