package lower

import (
	"fmt"
	"slices"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// passNavigationHrefs is for a target whose pages are addresses -- html, which
// writes a document or serves a route per page -- and so answers navigation
// by following a href rather than by moving a stack it holds.
//
//	nav.link(to=pkg, text="x")   →  nav.link(to=pkg, params=Pkg{name="first"}, text="x")
//	pages.go(pkg)                →  pages.go(pkg, Pkg{name="first"})
//
// What a link is on such a target is the target's override of nav.link --
// html's is sngl:ui's link to `nav.href(to, params)`, which the build folds to
// the page's href with each `{name}` filled where the params are known then.
// What this pass adds is what only the call site knows: a link or a `go`
// passing no params passes the page's own, which is what it shows them with.
//
// It is also where what such a target cannot answer is refused, since this is
// the last point that sees the program's nav.page, nav.link and `go` as they
// were written: a page's params travel in its path, so each field is one of
// its placeholders. Whether a pattern can be served at all -- a static site
// writes each page once -- is the platform's to say, since it is the platform
// that knows which languages serve routes. A stack under an `if` that reads state is refused as
// well: each page is a document or a route of its own, decided at build time,
// and the `if` would build the stack when the program runs.
var passNavigationHrefs = pass{
	name:    "NavigationHrefs",
	enabled: func(f Features) bool { return f.NavigationHrefs },
	apply:   lowerNavigationHrefs,
}

type navHrefs struct {
	nv *navigation
	// hrefs is each page's, as written.
	hrefs map[*navPage]string
	// surfaces says which nodes are in a surface other than the document --
	// a window html shows inside it -- whose stacks navigate in place.
	surfaces *surfaceSet
}

func lowerNavigationHrefs(pkg *ir.Package, feats Features, opts Options) error {
	nv, err := collectNavigation(pkg)
	if nv == nil || err != nil {
		return err
	}
	h := &navHrefs{nv: nv, hrefs: map[*navPage]string{}}
	// A stack in a surface other than the document is no address: it
	// navigates in place, and passNavigation lowers it.
	surfaces, err := findSurfaces(pkg, opts)
	if err != nil {
		return err
	}
	if err := refuseStacksUnderReactiveIf(pkg, surfaces.inSurface); err != nil {
		return err
	}
	for _, st := range nv.stacks {
		if surfaces.inSurface(st.node) {
			continue
		}
		for _, pg := range st.pages {
			if err := h.checkPage(pg); err != nil {
				return err
			}
			h.pageCell(pg)
		}
	}
	h.surfaces = surfaces
	for _, b := range imperativeBlocks(pkg) {
		if err := h.calls(*b); err != nil {
			return err
		}
	}
	var werr error
	_ = ir.Walk(pkg, func(n ir.Node) error {
		inst, ok := n.(*ir.NodeInst)
		if !ok || werr != nil {
			return nil
		}
		switch {
		case isNavNode(inst, ir.BuiltinNavLink) && !surfaces.inSurface(inst):
			werr = h.navLink(inst)
		}
		return nil
	})
	return werr
}

// checkPage holds a page's href to its params: written as a constant, each
// placeholder naming a field a path segment can be parsed into, and each field
// named by one.
func (h *navHrefs) checkPage(pg *navPage) error {
	name := pageName(pg)
	if pg.looped() {
		// One page per element: the href is the copy's, known once the copy
		// is, and the document or route written for it is where a pattern is
		// refused. It takes no params (addPages), so it names no field.
		if !constInLoop(pg.node.Prop(ir.NavPageHref), pg.loops...) {
			return fmt.Errorf("%s: page %q's href is not a constant, and a page is served at its href", ir.NodePos(pg.node), name)
		}
		return nil
	}
	lit, ok := pg.node.Prop(ir.NavPageHref).(*ir.Literal)
	if !ok {
		return fmt.Errorf("%s: page %q's href is not a constant, and a page is served at its href", ir.NodePos(pg.node), name)
	}
	href := lit.Value
	h.hrefs[pg] = href
	holes := ir.HrefPlaceholders(href)
	sd, _ := pageParamsType(pg.node).Decl.(*ir.StructDef)
	for _, hole := range holes {
		f := structField(sd, hole)
		if f == nil {
			return fmt.Errorf("%s: page %q's href %s names {%s}, but its params have no field %q", ir.NodePos(pg.node), name, href, hole, hole)
		}
		if !pathParseable(f.Type) {
			return fmt.Errorf("%s: page %q's href %s names {%s}, which arrives as text, and field %q is %s, which a path cannot carry", ir.NodePos(pg.node), name, href, hole, hole, f.Type)
		}
	}
	if sd != nil {
		for _, f := range sd.Fields {
			if !slices.Contains(holes, f.Name) {
				return fmt.Errorf("%s: page %q's params have a field %q its href %s names no placeholder for; a page's params travel in its path", ir.NodePos(pg.node), name, f.Name, href)
			}
		}
	}
	return nil
}

// refuseStacksUnderReactiveIf refuses a stack under an `if` that reads state,
// in every body that renders one: a document holds its pages or does not.
//
// A stack navigating in place is not a document's, and is left out (skip).
func refuseStacksUnderReactiveIf(pkg *ir.Package, skip func(*ir.NodeInst) bool) error {
	fx := &effectState{pkg: pkg, reactive: collectReactiveVars(pkg)}
	var walk func(stmts []ir.Stmt, reactive bool) error
	walk = func(stmts []ir.Stmt, reactive bool) error {
		for _, s := range stmts {
			r := reactive
			switch n := s.(type) {
			case *ir.If:
				r = reactive || len(fx.reactiveVarsIn(n.Cond)) > 0
			case *ir.NodeInst:
				if skip(n) {
					continue
				}
				if isNavNode(n, ir.BuiltinNavStack) && reactive {
					return fmt.Errorf("%s: a nav.stack under an `if` that reads state cannot be decided per document; write the `if` inside a page", ir.NodePos(n))
				}
			}
			for _, b := range ir.ViewBlocks(s) {
				if err := walk(*b, r); err != nil {
					return err
				}
			}
		}
		return nil
	}
	for _, o := range ir.Owners(pkg) {
		if err := walk(o.Stmts(), false); err != nil {
			return err
		}
	}
	return nil
}

// pageCell makes a page's content its children, under `if pages.current.id ==
// <id>`, and the parameter its population binds the page's own
// (NodeInst.Params), the way a window carries one.
//
// The cell is what the target binds per document or per route, and the
// children are the page's view, which every lowering of a view walks. The `if`
// is what a page not showing is not mounted means: a document folds it for
// the page it is written for, and what the fold does not reach -- the step an
// effect in the page settles by, which is a function of the owner's -- asks
// the page the document was written for when it runs.
func (h *navHrefs) pageCell(pg *navPage) {
	n := pg.node
	for _, name := range ir.SlotNames(n.Slots) {
		sc := n.Slots[name]
		if sc == nil || len(sc.Params) != 1 {
			continue
		}
		p := sc.Params[0]
		if t := pageParamsType(n); t != nil {
			p.Type = t
		}
		n.Params = p
		n.Children = append(n.Children, sc.Body...)
		delete(n.Slots, name)
	}
	current := pg.stack.node.Component.MethodByIntrinsic(ir.NavCurrentID)
	if current == nil || len(n.Children) == 0 {
		return
	}
	call := &ir.Call{Type: h.nv.recordType(current.Return), Func: current}
	if st := pg.stack.node; st.Handle != nil {
		call.Args = []ir.CallArg{{Value: varIdent(st.Handle)}}
	}
	var cond ir.Expr = &ir.Binary{
		Type:  ir.TypBool,
		Op:    ast.BinEq,
		Left:  &ir.Select{Type: ir.TypInt, Operand: call, Field: "id"},
		Right: navInt(pg.id),
	}
	if pg.looped() {
		cond = &ir.Binary{Type: ir.TypBool, Op: ast.BinAnd, Left: cond,
			Right: copyMatch(&ir.Select{Type: ir.ListOf(ir.TypInt), Operand: ir.CloneExprSharingDecls(call), Field: "copy"}, pg.copyIndices())}
	}
	n.Children = []ir.Stmt{&ir.If{Cond: cond, Body: n.Children}}
}

// calls passes the page's own params to every `go` that passes none.
func (h *navHrefs) calls(stmts []ir.Stmt) error {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.CallStmt:
			if n.Call == nil || n.Call.Func == nil || n.Call.Func.Intrinsic != ir.NavGoID {
				continue
			}
			if st, _ := h.nv.resolve(callArg(n.Call, "", 0)).(*navStack); st != nil && h.surfaces.inSurface(st.node) {
				continue
			}
			pg, _ := h.nv.resolve(callArg(n.Call, "to", 1)).(*navPage)
			if pg == nil {
				return fmt.Errorf("%s: go names its page through a value; name the page's own #id", ir.StmtPos(n))
			}
			params, err := handed(callArg(n.Call, ir.NavGoParamsArg, 2))
			if err != nil {
				return fmt.Errorf("%s: %w", ir.StmtPos(n), err)
			}
			if params == nil {
				setCallArg(n.Call, ir.NavGoParamsArg, 2, pg.start())
			}
		case *ir.If:
			if err := h.calls(n.Body); err != nil {
				return err
			}
			if err := h.calls(n.Else); err != nil {
				return err
			}
		case *ir.For:
			if err := h.calls(n.Body); err != nil {
				return err
			}
			if err := h.calls(n.Else); err != nil {
				return err
			}
		}
	}
	return nil
}

// setCallArg replaces the argument a call passes for a parameter, or appends
// it by name where the call passes none.
func setCallArg(c *ir.Call, name string, pos int, v ir.Expr) {
	for i := range c.Args {
		if c.Args[i].Name == name || (c.Args[i].Name == "" && i == pos) {
			c.Args[i].Value = v
			return
		}
	}
	c.Args = append(c.Args, ir.CallArg{Name: name, Value: v})
}

// navLink hands a nav.link that passes no params its page's own, as a `go`
// passing none is handed them: html's override makes the link the address
// `nav.href(to, params)` names, and an address is built from the params it
// is handed. The page has to be named by its own #id, since this is where it
// is known which page that is.
func (h *navHrefs) navLink(n *ir.NodeInst) error {
	pg, _ := h.nv.resolve(n.Prop(ir.NavLinkTo)).(*navPage)
	if pg == nil {
		return fmt.Errorf("%s: a nav.link names its page through a value; name the page's own #id", ir.NodePos(n))
	}
	params, err := handed(n.Prop(ir.NavLinkParams))
	if err != nil {
		return fmt.Errorf("%s: %w", ir.NodePos(n), err)
	}
	if params == nil {
		setProp(n, ir.NavLinkParams, pg.start())
	}
	return nil
}

// setProp replaces the value n's call site writes for name, or adds it.
func setProp(n *ir.NodeInst, name string, v ir.Expr) {
	for i := range n.Props {
		if n.Props[i].Name == name {
			n.Props[i].Value = v
			return
		}
	}
	n.Props = append(n.Props, ir.Arg{Name: name, Value: v})
}

func structField(sd *ir.StructDef, name string) *ir.StructField {
	if sd == nil {
		return nil
	}
	for _, f := range sd.Fields {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// pathParseable is what a path segment can be read back into: the scalars,
// and nothing that would need an encoding the compiler would be choosing.
func pathParseable(t *ir.Type) bool {
	if t == nil {
		return false
	}
	switch t.Kind {
	case ir.TypeString, ir.TypeInt, ir.TypeFloat, ir.TypeBool:
		return true
	}
	return false
}

// constInLoop reports whether e is a constant once the variables of fs, a loop
// over a constant, are bound: one value per copy of what the loop writes.
func constInLoop(e ir.Expr, loops ...*ir.For) bool {
	if e == nil {
		return false
	}
	holder := &ir.Return{Value: ir.CloneExprSharingDecls(e)}
	_ = ir.RewriteExprs(holder, func(x ir.Expr) (ir.Expr, error) {
		if id, ok := x.(*ir.Ident); ok {
			if lv, ok := id.Sym.(*ir.LoopVar); ok {
				for _, fs := range loops {
					if lv == fs.KeySym || lv == fs.ValueSym {
						return &ir.Literal{Type: id.Type, Value: "0"}, ir.SkipDir
					}
				}
			}
		}
		return x, nil
	})
	return ir.IsConst(holder.Value)
}
