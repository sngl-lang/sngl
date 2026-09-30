package lower

import (
	"fmt"
	"strconv"
	"strings"

	"git.duckfam.us/jonathan/sngl/ir"
)

// passImplicitState gives a two-way prop the call site left unbound a cell of
// the instance's own (ir.UnboundProps).
//
// `checkbox #box(label="x")` becomes an instantiation of a component written
// for it,
//
//	component __checkbox_state0(__checked bool = false, label string) node {
//	    var checked = __checked
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
	st := &implicitState{pkg: pkg, reads: map[*ir.Var]map[string]*ir.Var{}}
	for _, o := range ir.Owners(pkg) {
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
		id, ok := sel.Operand.(*ir.Ident)
		if !ok {
			return n, nil
		}
		h, ok := id.Sym.(*ir.Var)
		if !ok {
			return n, nil
		}
		cell := st.reads[h][sel.Field]
		if cell == nil {
			return n, nil
		}
		return &ir.Ident{Name: cell.Name, Type: cell.Type, Sym: cell}, ir.SkipDir
	})
}

type implicitState struct {
	pkg  *ir.Package
	made []*ir.Component
	// reads is each wrapped node's handle, and the cell behind each prop a
	// read of the handle names.
	reads map[*ir.Var]map[string]*ir.Var
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
	if comp == nil || ir.IsWindowNode(n) {
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
	if len(n.Slots) > 0 || len(n.Children) > 0 && (rest == nil || len(rest.Params) > 0) {
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
		init := &ir.Prop{Name: "__" + p.Name, Type: p.Type, Default: start}
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
			site = append(site, ir.Arg{Name: "__" + a.Name, NamePos: a.NamePos, Value: a.Value})
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
	// wrapper's own rest slot hands them on to the node.
	if len(n.Children) > 0 {
		fwd := &ir.SlotDecl{Name: rest.Name, Rest: true, Content: rest.Content, Card: rest.Card}
		w.Slots = []*ir.SlotDecl{fwd}
		inner.Children = []ir.Stmt{&ir.SlotInst{Name: fwd.Name, Rest: true, Decl: fwd}}
	}
	w.Body = []ir.Stmt{inner}

	if n.Handle != nil {
		st.reads[n.Handle] = cells
	}
	return &ir.NodeInst{
		AST:       n.AST,
		Name:      w.Name,
		Component: w,
		Props:     site,
		Handlers:  n.Handlers,
		Children:  n.Children,
		Key:       n.Key,
		Ref:       n.Ref,
	}, nil
}

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
