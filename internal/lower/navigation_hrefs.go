package lower

import (
	"fmt"
	"net/url"
	"slices"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// passNavigationHrefs is for a target whose pages are addresses -- html, which
// writes a document or serves a route per page -- and so answers navigation
// by following a href rather than by moving a stack it holds.
//
//	nav.link(to=pkg, params=Pkg{name="x"}, text="x")   →  ui.link(href="/p/x", text="x")
//	pages.go(pkg)                                       →  pages.go(pkg, Pkg{name="first"})
//
// A link is sngl:ui's link to the page's href, with each `{name}` filled from
// the field of that name. Only a link: a clickable whose handler goes to a
// page keeps its look and navigates when it runs, since `<a>` around a
// `<button>` is not HTML a browser agrees on. A link whose params are not known until it runs is handed the
// address `nav._href` builds, which the target answers. A `go` passing no
// params passes the page's own, which is what it shows them with.
//
// It is also where what such a target cannot answer is refused, since this is
// the last point that sees the program's nav.page, nav.link and `go` as they
// were written: a page's params travel in its path, so each field is one of
// its placeholders, and a static site -- a target with no language to serve
// it -- writes each page once and serves no pattern, so a page there has no
// params to be handed. A stack under an `if` that reads state is refused as
// well: each page is a document or a route of its own, decided at build time,
// and the `if` would build the stack when the program runs.
var passNavigationHrefs = pass{
	name:    "NavigationHrefs",
	enabled: func(f Features) bool { return f.NavigationHrefs },
	apply:   lowerNavigationHrefs,
}

type navHrefs struct {
	nv     *navigation
	static bool
	link   *ir.Component
	href   *ir.Func
	// hrefs is each page's, as written.
	hrefs map[*navPage]string
}

func lowerNavigationHrefs(pkg *ir.Package, _ Features, opts Options) error {
	nv, err := collectNavigation(pkg)
	if nv == nil || err != nil {
		return err
	}
	h := &navHrefs{nv: nv, static: opts.Language == "none", hrefs: map[*navPage]string{}}
	h.link = findLibComponent(pkg, "sngl:ui", "link")
	h.href = findLibFunc(pkg, "sngl:ui/nav", "_href")
	if err := refuseStacksUnderReactiveIf(pkg); err != nil {
		return err
	}
	for _, st := range nv.stacks {
		for _, pg := range st.pages {
			if err := h.checkPage(pg); err != nil {
				return err
			}
			h.pageCell(pg)
		}
	}
	for _, fn := range navFuncs(pkg) {
		if err := h.calls(fn.Block); err != nil {
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
		case isNavNode(inst, ir.BuiltinNavLink):
			werr = h.navLink(inst)
		}
		return nil
	})
	return werr
}

// checkPage holds a page's href to its params: written as a constant, each
// placeholder naming a field a path segment can be parsed into, and each field
// named by one. A static site serves no pattern at all.
func (h *navHrefs) checkPage(pg *navPage) error {
	name := pageName(pg)
	if pg.loop != nil {
		// One page per element: the href is the copy's, known once the copy
		// is, and the document or route written for it is where a pattern is
		// refused. It takes no params (addPages), so it names no field.
		if !constInLoop(pg.node.Prop("href"), pg.loop) {
			return fmt.Errorf("%s: page %q's href is not a constant, and a page is served at its href", ir.NodePos(pg.node), name)
		}
		return nil
	}
	lit, ok := pg.node.Prop("href").(*ir.Literal)
	if !ok {
		return fmt.Errorf("%s: page %q's href is not a constant, and a page is served at its href", ir.NodePos(pg.node), name)
	}
	href := lit.Value
	h.hrefs[pg] = href
	holes := ir.HrefPlaceholders(href)
	if h.static && len(holes) > 0 {
		return fmt.Errorf("%s: page %q is served at %s, a pattern: a static site writes one document per page and cannot serve one; compile with a server language (e.g. --lang go)", ir.NodePos(pg.node), name, href)
	}
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

// refuseStacksUnderReactiveIf is refuseWindowsUnderIf for a stack, in every
// body that renders one.
func refuseStacksUnderReactiveIf(pkg *ir.Package) error {
	fx := &effectState{pkg: pkg, reactive: collectReactiveVars(pkg)}
	var walk func(stmts []ir.Stmt, reactive bool) error
	walk = func(stmts []ir.Stmt, reactive bool) error {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.If:
				r := reactive || len(fx.reactiveVarsIn(n.Cond)) > 0
				if err := walk(n.Body, r); err != nil {
					return err
				}
				if err := walk(n.Else, r); err != nil {
					return err
				}
			case *ir.NodeInst:
				if isNavNode(n, ir.BuiltinNavStack) && reactive {
					return fmt.Errorf("%s: a nav.stack under an `if` that reads state cannot be decided per document; write the `if` inside a page", ir.NodePos(n))
				}
				if err := walk(n.Children, reactive); err != nil {
					return err
				}
				for _, name := range ir.SlotNames(n.Slots) {
					if err := walk(n.Slots[name].Body, reactive); err != nil {
						return err
					}
				}
			case *ir.For:
				if err := walk(n.Body, reactive); err != nil {
					return err
				}
			case *ir.ErrorBoundary:
				if err := walk(n.Children, reactive); err != nil {
					return err
				}
			case *ir.ContextProvider:
				if err := walk(n.Children, reactive); err != nil {
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
	current := pg.stack.node.Component.Methods["current"]
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
	if pg.loop != nil {
		cond = &ir.Binary{Type: ir.TypBool, Op: ast.BinAnd, Left: cond, Right: &ir.Binary{
			Type:  ir.TypBool,
			Op:    ast.BinEq,
			Left:  &ir.Select{Type: ir.TypInt, Operand: ir.CloneExprSharingDecls(call), Field: "copy"},
			Right: pg.copyOf(),
		}}
	}
	n.Children = []ir.Stmt{&ir.If{Cond: cond, Body: n.Children}}
}

// calls passes the page's own params to every `go` that passes none.
func (h *navHrefs) calls(stmts []ir.Stmt) error {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.CallStmt:
			if n.Call == nil || n.Call.Func == nil || n.Call.Func.Intrinsic != "nav.go" {
				continue
			}
			pg, _ := h.nv.resolve(callArg(n.Call, "to", 1)).(*navPage)
			if pg == nil {
				return fmt.Errorf("%s: go names its page through a value; name the page's own #id", ir.StmtPos(n))
			}
			params, err := handed(callArg(n.Call, "params", 2))
			if err != nil {
				return fmt.Errorf("%s: %w", ir.StmtPos(n), err)
			}
			if params == nil {
				setCallArg(n.Call, "params", 2, pg.start())
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

// navLink makes a nav.link the ui.link to its page's address, in place: the
// node keeps its `#id`, so a test clicks it as it would have, and its own
// `@click` runs before the browser follows the href, as ui.link's does.
func (h *navHrefs) navLink(n *ir.NodeInst) error {
	pg, _ := h.nv.resolve(n.Prop("to")).(*navPage)
	if pg == nil {
		return fmt.Errorf("%s: a nav.link names its page through a value; name the page's own #id", ir.NodePos(n))
	}
	params, err := handed(n.Prop("params"))
	if err != nil {
		return fmt.Errorf("%s: %w", ir.NodePos(n), err)
	}
	if h.link == nil {
		return fmt.Errorf("%s: a nav.link is sngl:ui's link here, which this build does not load", ir.NodePos(n))
	}
	href, err := h.address(pg, params)
	if err != nil {
		return fmt.Errorf("%s: %w", ir.NodePos(n), err)
	}
	h.becomeLink(n, href)
	return nil
}

func (h *navHrefs) becomeLink(n *ir.NodeInst, href ir.Expr) {
	props := []ir.Arg{{Name: "href", Value: href}}
	for _, p := range n.Props {
		if p.Name == "text" || p.Name == "style" {
			props = append(props, p)
		}
	}
	n.Component = h.link
	n.Props = props
	n.Bindings = nil
	if n.Handle != nil {
		n.Handle.Type = h.link.SymType()
	}
}

// address is where pg is reached with params, the page's own when nil: a
// string when the params are known at build time, and otherwise the call
// that builds it when it runs.
func (h *navHrefs) address(pg *navPage, params ir.Expr) (ir.Expr, error) {
	if pg.loop != nil {
		// The copy's own href, which the document folds where the loop's
		// variables are bound.
		return ir.CloneExprSharingDecls(pg.node.Prop("href")), nil
	}
	if params == nil {
		params = pg.start()
	}
	if href, ok := h.constAddress(pg, params); ok {
		return &ir.Literal{Type: ir.TypString, Value: href}, nil
	}
	if h.href == nil {
		return nil, fmt.Errorf("the address of page %q is built when the link is shown, which needs sngl:ui/nav's _href", pageName(pg))
	}
	return &ir.Call{
		Type: ir.TypString,
		Func: h.href,
		Args: []ir.CallArg{{Value: h.nv.record(pg)}, {Value: params}},
	}, nil
}

// constAddress fills pg's href from params, where each field a placeholder
// names is a literal.
func (h *navHrefs) constAddress(pg *navPage, params ir.Expr) (string, bool) {
	if pg.loop != nil {
		return "", false
	}
	href := h.hrefs[pg]
	holes := ir.HrefPlaceholders(href)
	if len(holes) == 0 {
		return href, true
	}
	lit, ok := params.(*ir.StructLit)
	if !ok {
		return "", false
	}
	values := map[string]string{}
	for _, f := range lit.Fields {
		l, ok := f.Value.(*ir.Literal)
		if !ok {
			return "", false
		}
		values[f.Name] = l.Value
	}
	return ir.FillHref(href, func(name string) (string, bool) {
		v, ok := values[name]
		return url.PathEscape(v), ok
	})
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

// findLibFunc is the function name in the library package uri, reached
// through what pkg imports.
func findLibFunc(pkg *ir.Package, uri, name string) *ir.Func {
	seen := map[*ir.Package]bool{}
	var walk func(p *ir.Package) *ir.Func
	walk = func(p *ir.Package) *ir.Func {
		if p == nil || seen[p] {
			return nil
		}
		seen[p] = true
		for _, imp := range p.Imports {
			if imp == nil || imp.Pkg == nil {
				continue
			}
			if imp.Path == uri {
				for _, f := range imp.Pkg.Funcs {
					if f != nil && f.Name == name && f.Receiver == "" {
						return f
					}
				}
			}
			if f := walk(imp.Pkg); f != nil {
				return f
			}
		}
		return nil
	}
	return walk(pkg)
}

// constInLoop reports whether e is a constant once the variables of fs, a loop
// over a constant, are bound: one value per copy of what the loop writes.
func constInLoop(e ir.Expr, fs *ir.For) bool {
	if e == nil {
		return false
	}
	holder := &ir.Return{Value: ir.CloneExprSharingDecls(e)}
	_ = ir.RewriteExprs(holder, func(x ir.Expr) (ir.Expr, error) {
		if id, ok := x.(*ir.Ident); ok && fs != nil {
			if lv, ok := id.Sym.(*ir.LoopVar); ok && (lv == fs.KeySym || lv == fs.ValueSym) {
				return &ir.Literal{Type: id.Type, Value: "0"}, ir.SkipDir
			}
		}
		return x, nil
	})
	return ir.IsConst(holder.Value)
}
