package lower

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// passNavigation lowers sngl:ui/nav to plain UI, for a target that does not
// answer it in its own codegen (Features.Navigation).
//
//	nav.stack #pages {
//	    nav.page #home(href="/", title="Home") { … }
//	    nav.page #pkg(href=`/p/{name}`, params=Pkg{}) {
//	        component content(p) { … }
//	    }
//	}
//
// becomes, on the stack's owner,
//
//	var pages__current navigator__value = navigator__value{id=0, href="/", title="Home"}
//	var pkg__params Pkg = Pkg{}
//	var pages__history list<pages__entry> = []
//	if pages__current.id == 0 { … } else if pages__current.id == 1 { …p is pkg__params… }
//
// The record is the navigator family's: one struct per family, its props plus
// an `id`, so a value of the family is the same record whichever stack it came
// from, and ids are numbered across every page of the family in the package.
// A page whose params are the empty struct has no var. An entry holds the page
// it replaced and every params var of its stack, which is what `back`
// restores.
//
// `pages.go(pkg, P{…})` pushes an entry and writes the params and the current
// page; `pages.back()` pops one and restores both, and does nothing at the
// bottom. `nav.link` becomes a `ui.button` showing its text whose click runs
// the link's own `@click` and then the same `go`. A read of `pages.current`
// is a read of the var, a page's handle read as a value is its record, `==`
// against a navigator compares ids, and every navigator or page type the
// program names is the record's.
//
// A two-way prop the call site bound is the cell: `:params=picked` makes
// `picked` the page's params. A one-way value is where the var starts.
//
// Before ImplicitState: `current` and `params` are unbound two-way props, and
// a cell that pass gave them would be one nothing here writes.
var passNavigation = pass{
	name:    "Navigation",
	enabled: func(f Features) bool { return !f.Navigation },
	apply:   lowerNavigation,
}

type navStack struct {
	node    *ir.NodeInst
	name    string
	owner   ir.Owner
	current ir.Expr // the cell: a var of the owner's or what `:current=` bound
	history *ir.Var
	entry   *ir.StructDef
	pages   []*navPage
	fam     *navFamily
}

type navPage struct {
	node   *ir.NodeInst
	stack  *navStack
	id     int
	params ir.Expr   // the cell, nil for a page whose params are the empty struct
	ptype  *ir.Type  // the params' type
	empty  ir.Expr   // the params a page with no cell is handed
	field  string    // the entry's field holding the params
	pparam *ir.Param // the content population's parameter, nil if bare
}

type navFamily struct {
	comp   *ir.Component
	record *ir.StructDef
	typ    *ir.Type
	next   int
}

type navigation struct {
	pkg      *ir.Package
	stacks   []*navStack
	byNode   map[*ir.NodeInst]any // *navStack or *navPage
	byHandle map[*ir.Var]any
	byField  map[navKey]any // a handle reached by select through a component
	families map[*ir.Component]*navFamily
	button   *ir.Component
	names    map[string]bool
}

type navKey struct {
	comp *ir.Component
	id   string
}

func lowerNavigation(pkg *ir.Package, _ Features, _ Options) error {
	if pkg == nil {
		return nil
	}
	nv := &navigation{
		pkg:      pkg,
		byNode:   map[*ir.NodeInst]any{},
		byHandle: map[*ir.Var]any{},
		byField:  map[navKey]any{},
		families: map[*ir.Component]*navFamily{},
		names:    map[string]bool{},
	}
	for _, o := range ir.Owners(pkg) {
		if err := nv.collect(o, o.Stmts(), 0); err != nil {
			return err
		}
	}
	links := nv.hasLinks()
	if len(nv.stacks) == 0 && !links {
		return nil
	}
	if links {
		nv.button = findLibComponent(pkg, "sngl:ui", "button")
		if nv.button == nil {
			return fmt.Errorf("nav.link is lowered to sngl:ui's button, which this build does not load")
		}
	}
	for _, st := range nv.stacks {
		nv.declare(st)
	}
	for _, fn := range navFuncs(pkg) {
		block, err := nv.lowerCalls(fn.Block)
		if err != nil {
			return err
		}
		fn.Block = block
	}
	for _, o := range ir.Owners(pkg) {
		*o.Body = nv.lowerView(*o.Body)
	}
	if err := ir.Rewrite(pkg, nv.rewriteExpr); err != nil {
		return err
	}
	nv.retype()
	return nil
}

// collect finds every stack an owner's body renders, and the pages each
// holds. A window is an owner of its own, so it is not entered here.
func (nv *navigation) collect(o ir.Owner, stmts []ir.Stmt, loops int) error {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if n != o.Win && ir.IsWindowNode(n) {
				continue
			}
			if isNavNode(n, ir.BuiltinNavStack) {
				if loops > 0 {
					return fmt.Errorf("%s: a nav.stack under a `for` would be one stack for every copy; write it in a component the loop renders", ir.NodePos(n))
				}
				nv.addStack(o, n)
				continue
			}
			if err := nv.collect(o, n.Children, loops); err != nil {
				return err
			}
			for _, name := range ir.SlotNames(n.Slots) {
				if err := nv.collect(o, n.Slots[name].Body, loops); err != nil {
					return err
				}
			}
		case *ir.If:
			if err := nv.collect(o, n.Body, loops); err != nil {
				return err
			}
			if err := nv.collect(o, n.Else, loops); err != nil {
				return err
			}
		case *ir.For:
			if err := nv.collect(o, n.Body, loops+1); err != nil {
				return err
			}
			if err := nv.collect(o, n.Else, loops); err != nil {
				return err
			}
		case *ir.ErrorBoundary:
			if err := nv.collect(o, n.Children, loops); err != nil {
				return err
			}
			if err := nv.collect(o, n.Failed, loops); err != nil {
				return err
			}
		case *ir.ContextProvider:
			if err := nv.collect(o, n.Children, loops); err != nil {
				return err
			}
		}
	}
	return nil
}

func isNavNode(n *ir.NodeInst, kind ir.BuiltinKind) bool {
	return n != nil && n.Component != nil && n.Component.Builtin == kind
}

func (nv *navigation) addStack(o ir.Owner, n *ir.NodeInst) {
	st := &navStack{node: n, owner: o, name: n.ID}
	if st.name == "" {
		st.name = "__stack" + strconv.Itoa(len(nv.stacks))
	}
	nv.stacks = append(nv.stacks, st)
	nv.index(st, n, o)
	for _, s := range n.Children {
		p, ok := s.(*ir.NodeInst)
		if !ok || !isNavNode(p, ir.BuiltinNavPage) {
			continue
		}
		fam := nv.family(p.Component.Tree)
		st.fam = fam
		pg := &navPage{node: p, stack: st, id: fam.next}
		fam.next++
		st.pages = append(st.pages, pg)
		nv.index(pg, p, o)
	}
}

func (nv *navigation) index(v any, n *ir.NodeInst, o ir.Owner) {
	nv.byNode[n] = v
	if n.Handle != nil {
		nv.byHandle[n.Handle] = v
	}
	if n.ID != "" && o.Comp != nil {
		nv.byField[navKey{o.Comp, n.ID}] = v
	}
}

// family is the record a navigator family's values lower to, made the first
// time one of its pages is met.
func (nv *navigation) family(f *ir.Component) *navFamily {
	if fam := nv.families[f]; fam != nil {
		return fam
	}
	def := &ir.StructDef{Name: nv.fresh(f.Name + "__value")}
	def.Fields = append(def.Fields, &ir.StructField{Name: "id", Type: ir.TypInt})
	for _, p := range f.Props {
		def.Fields = append(def.Fields, &ir.StructField{Name: p.Name, Type: p.Type})
	}
	nv.pkg.Structs = append(nv.pkg.Structs, def)
	fam := &navFamily{comp: f, record: def, typ: &ir.Type{Kind: ir.TypeStruct, Decl: def}}
	nv.families[f] = fam
	return fam
}

// fresh is name, or name with a counter on it where the package already
// declares one.
func (nv *navigation) fresh(name string) string {
	taken := func(n string) bool {
		if nv.names[n] {
			return true
		}
		for _, s := range nv.pkg.Structs {
			if s.Name == n {
				return true
			}
		}
		for _, v := range nv.pkg.Vars {
			if v.Name == n {
				return true
			}
		}
		return false
	}
	out := name
	for i := 1; taken(out); i++ {
		out = name + strconv.Itoa(i)
	}
	nv.names[out] = true
	return out
}

// declare gives a stack its cells and its entry type.
func (nv *navigation) declare(st *navStack) {
	var vars []*ir.Var
	st.entry = &ir.StructDef{Name: nv.fresh(st.name + "__entry")}
	if st.fam != nil {
		st.entry.Fields = append(st.entry.Fields, &ir.StructField{Name: "page", Type: st.fam.typ})
	}
	for _, pg := range st.pages {
		pg.ptype = pageParamsType(pg.node)
		if sc := pg.node.Slots["content"]; sc != nil && len(sc.Params) > 0 {
			pg.pparam = sc.Params[0]
		}
		if isEmptyStruct(pg.ptype) {
			pg.empty = pg.node.Prop("params")
			if pg.empty == nil {
				pg.empty = ir.ZeroExpr(pg.ptype)
			}
			continue
		}
		pg.field = pageName(pg)
		st.entry.Fields = append(st.entry.Fields, &ir.StructField{Name: pg.field, Type: pg.ptype})
		if t := boundTarget(pg.node, "params"); t != nil {
			pg.params = t
			continue
		}
		init := pg.node.Prop("params")
		if init == nil {
			init = ir.ZeroExpr(pg.ptype)
		}
		v := &ir.Var{Name: nv.fresh(pageName(pg) + "__params"), Type: pg.ptype, Init: init}
		vars = append(vars, v)
		pg.params = varIdent(v)
	}
	nv.pkg.Structs = append(nv.pkg.Structs, st.entry)
	if t := boundTarget(st.node, "current"); t != nil {
		st.current = t
	} else if st.fam != nil {
		init := st.node.Prop("current")
		if init == nil {
			init = nv.record(st.start())
		}
		v := &ir.Var{Name: nv.fresh(st.name + "__current"), Type: st.fam.typ, Init: init}
		vars = append(vars, v)
		st.current = varIdent(v)
	}
	entryT := &ir.Type{Kind: ir.TypeStruct, Decl: st.entry}
	st.history = &ir.Var{Name: nv.fresh(st.name + "__history"), Type: ir.ListOf(entryT), Init: &ir.ListLit{Type: ir.ListOf(entryT)}}
	vars = append(vars, st.history)
	st.owner.AddVars(vars...)
}

// start is the page a stack shows first: the one at "/", or its first.
func (st *navStack) start() *navPage {
	if len(st.pages) == 0 {
		return nil
	}
	for _, pg := range st.pages {
		if lit, ok := pg.node.Prop("href").(*ir.Literal); ok && lit.Value == "/" {
			return pg
		}
	}
	return st.pages[0]
}

func pageName(pg *navPage) string {
	if pg.node.ID != "" {
		return pg.node.ID
	}
	return "__page" + strconv.Itoa(pg.id)
}

// pageParamsType is what a page's params are, read off what the call site
// wrote, or the empty struct.
func pageParamsType(n *ir.NodeInst) *ir.Type {
	if t := boundTarget(n, "params"); t != nil {
		if tt := typeOf(t); tt != nil {
			return tt
		}
	}
	if e := n.Prop("params"); e != nil {
		if tt := typeOf(e); tt != nil {
			return tt
		}
	}
	return &ir.Type{Kind: ir.TypeStruct}
}

func isEmptyStruct(t *ir.Type) bool {
	if t == nil || t.Kind != ir.TypeStruct {
		return false
	}
	if t.Decl == nil {
		return true
	}
	f, ok := t.Decl.(ir.Fielded)
	return ok && len(f.FieldList()) == 0
}

func boundTarget(n *ir.NodeInst, prop string) ir.Expr {
	for _, b := range n.Bindings {
		if b.PropName == prop {
			return b.Target
		}
	}
	return nil
}

func typeOf(e ir.Expr) *ir.Type {
	if e == nil {
		return nil
	}
	return e.ExprType()
}

// rebase is a cell read the way the handle was reached: `c.pages.current` in
// a test reads the var through `c`, where the cell is a var of the component
// `c` is an instance of.
func rebase(cell, via ir.Expr) ir.Expr {
	sel, ok := via.(*ir.Select)
	if !ok {
		return ir.CloneExprSharingDecls(cell)
	}
	id, ok := cell.(*ir.Ident)
	if !ok {
		return ir.CloneExprSharingDecls(cell)
	}
	v, ok := id.Sym.(*ir.Var)
	if !ok {
		return ir.CloneExprSharingDecls(cell)
	}
	return &ir.Select{Type: v.Type, Operand: ir.CloneExprSharingDecls(sel.Operand), Field: v.Name}
}

func varIdent(v *ir.Var) *ir.Ident {
	return &ir.Ident{Name: v.Name, Type: v.Type, Sym: v}
}

// record is a page's value: the family's record holding the page's id and the
// props its family declares.
func (nv *navigation) record(pg *navPage) ir.Expr {
	if pg == nil {
		return nil
	}
	fam := pg.stack.fam
	lit := &ir.StructLit{Type: fam.typ, Def: fam.record}
	lit.Fields = append(lit.Fields, ir.FieldInit{Name: "id", Value: navInt(pg.id)})
	for _, p := range fam.comp.Props {
		v := pg.node.Prop(p.Name)
		if v == nil {
			v = p.Default
		}
		if v == nil {
			v = ir.ZeroExpr(p.Type)
		}
		lit.Fields = append(lit.Fields, ir.FieldInit{Name: p.Name, Value: ir.CloneExprSharingDecls(v)})
	}
	return lit
}

func navInt(n int) *ir.Literal {
	return &ir.Literal{Type: ir.TypInt, Value: strconv.Itoa(n)}
}

// hasLinks reports whether anything the package renders is a nav.link.
func (nv *navigation) hasLinks() bool {
	found := false
	_ = ir.Walk(nv.pkg, func(n ir.Node) error {
		if inst, ok := n.(*ir.NodeInst); ok && isNavNode(inst, ir.BuiltinNavLink) {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}

// findLibComponent is the component name in the library package uri, reached
// through what pkg imports.
func findLibComponent(pkg *ir.Package, uri, name string) *ir.Component {
	seen := map[*ir.Package]bool{}
	var walk func(p *ir.Package) *ir.Component
	walk = func(p *ir.Package) *ir.Component {
		if p == nil || seen[p] {
			return nil
		}
		seen[p] = true
		for _, imp := range p.Imports {
			if imp == nil || imp.Pkg == nil {
				continue
			}
			if imp.Path == uri && imp.Pkg.Symbols != nil {
				if sym, ok := imp.Pkg.Symbols.LookupRootComponent(name); ok {
					if c, ok := sym.(*ir.Component); ok {
						return c
					}
				}
			}
			if c := walk(imp.Pkg); c != nil {
				return c
			}
		}
		return nil
	}
	return walk(pkg)
}

// navFuncs is every function whose block a `go` or `back` may be written in:
// the package's and each component's funcs, and every handler and lambda.
func navFuncs(pkg *ir.Package) []*ir.Func {
	var out []*ir.Func
	seen := map[*ir.Func]bool{}
	add := func(f *ir.Func) {
		if f != nil && !seen[f] {
			seen[f] = true
			out = append(out, f)
		}
	}
	for _, f := range pkg.Funcs {
		add(f)
	}
	for _, c := range pkg.Components {
		for _, f := range c.Funcs {
			add(f)
		}
		for _, v := range c.Vars {
			for _, h := range v.Handlers {
				add(h.Func)
			}
		}
	}
	for _, v := range pkg.Vars {
		for _, h := range v.Handlers {
			add(h.Func)
		}
	}
	_ = ir.Walk(pkg, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.NodeInst:
			for _, h := range x.Handlers {
				add(h.Func)
			}
			if x.ErrorHandler != nil {
				add(x.ErrorHandler.Func)
			}
		case *ir.Lambda:
			add(x.Func)
		case *ir.ErrorBoundary:
			if x.Handler != nil {
				add(x.Handler.Func)
			}
		}
		return nil
	})
	return out
}

// lowerCalls replaces each `go` and `back` in an imperative block, at any
// depth, with the statements that move the stack.
func (nv *navigation) lowerCalls(stmts []ir.Stmt) ([]ir.Stmt, error) {
	var out []ir.Stmt
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.CallStmt:
			if n.Call != nil && n.Call.Func != nil {
				switch n.Call.Func.Intrinsic {
				case "nav.go":
					repl, err := nv.lowerGo(n)
					if err != nil {
						return nil, err
					}
					out = append(out, repl...)
					continue
				case "nav.back":
					repl, err := nv.lowerBack(n)
					if err != nil {
						return nil, err
					}
					out = append(out, repl...)
					continue
				}
			}
		case *ir.If:
			var err error
			if n.Body, err = nv.lowerCalls(n.Body); err != nil {
				return nil, err
			}
			if n.Else, err = nv.lowerCalls(n.Else); err != nil {
				return nil, err
			}
		case *ir.For:
			var err error
			if n.Body, err = nv.lowerCalls(n.Body); err != nil {
				return nil, err
			}
			if n.Else, err = nv.lowerCalls(n.Else); err != nil {
				return nil, err
			}
		}
		out = append(out, s)
	}
	return out, nil
}

// resolve names the stack or page an expression is the handle of: a `#id`
// read where it was declared, or a select reaching one through the component
// declaring it (`c.pages` in a test).
func (nv *navigation) resolve(e ir.Expr) any {
	switch x := e.(type) {
	case *ir.Ident:
		if v, ok := x.Sym.(*ir.Var); ok {
			return nv.byHandle[v]
		}
	case *ir.Select:
		t := typeOf(x.Operand)
		if t == nil {
			return nil
		}
		if c, ok := t.Decl.(*ir.Component); ok {
			return nv.byField[navKey{c, x.Field}]
		}
	}
	return nil
}

func callArg(c *ir.Call, name string, pos int) ir.Expr {
	for _, a := range c.Args {
		if a.Name == name {
			return a.Value
		}
	}
	if pos < len(c.Args) && c.Args[pos].Name == "" {
		return c.Args[pos].Value
	}
	return nil
}

func (nv *navigation) callStack(n *ir.CallStmt, verb string) (*navStack, error) {
	recv := callArg(n.Call, "", 0)
	st, _ := nv.resolve(recv).(*navStack)
	if st == nil {
		return nil, fmt.Errorf("%s: %s names its stack through a value; name the stack's own #id", ir.StmtPos(n), verb)
	}
	return st, nil
}

func (nv *navigation) lowerGo(n *ir.CallStmt) ([]ir.Stmt, error) {
	st, err := nv.callStack(n, "go")
	if err != nil {
		return nil, err
	}
	to := callArg(n.Call, "to", 1)
	pg, _ := nv.resolve(to).(*navPage)
	if pg == nil {
		return nil, fmt.Errorf("%s: %s.go names its page through a value; name the page's own #id", ir.StmtPos(n), st.name)
	}
	if pg.stack != st {
		return nil, fmt.Errorf("%s: %s.go names %s, which is a page of %s", ir.StmtPos(n), st.name, pageName(pg), pg.stack.name)
	}
	return nv.goStmts(pg, callArg(n.Call, "params", 2)), nil
}

// goStmts shows pg, handed params: push the entry, then write the params and
// the current page.
func (nv *navigation) goStmts(pg *navPage, params ir.Expr) []ir.Stmt {
	st := pg.stack
	entryT := &ir.Type{Kind: ir.TypeStruct, Decl: st.entry}
	entry := &ir.StructLit{Type: entryT, Def: st.entry}
	entry.Fields = append(entry.Fields, ir.FieldInit{Name: "page", Value: ir.CloneExprSharingDecls(st.current)})
	for _, p := range st.pages {
		if p.params != nil {
			entry.Fields = append(entry.Fields, ir.FieldInit{Name: p.field, Value: ir.CloneExprSharingDecls(p.params)})
		}
	}
	out := []ir.Stmt{callListPush(ir.CloneExprSharingDecls(varIdent(st.history)), entry, entryT)}
	if pg.params != nil {
		if params == nil {
			params = ir.ZeroExpr(pg.ptype)
		}
		out = append(out, &ir.Assign{Target: ir.CloneExprSharingDecls(pg.params), Op: ast.AssignSet, Value: params})
	}
	out = append(out, &ir.Assign{Target: ir.CloneExprSharingDecls(st.current), Op: ast.AssignSet, Value: nv.record(pg)})
	return out
}

// lowerBack pops the last entry and restores what it held. At the bottom of
// the stack there is no entry, and nothing happens.
func (nv *navigation) lowerBack(n *ir.CallStmt) ([]ir.Stmt, error) {
	st, err := nv.callStack(n, "back")
	if err != nil {
		return nil, err
	}
	hist := func() ir.Expr { return varIdent(st.history) }
	last := func() ir.Expr {
		return &ir.Binary{Type: ir.TypInt, Op: ast.BinSub, Left: listLength(hist()), Right: navInt(1)}
	}
	entryT := &ir.Type{Kind: ir.TypeStruct, Decl: st.entry}
	top := func() ir.Expr { return &ir.Index{Type: entryT, Operand: hist(), Idx: last()} }
	var body []ir.Stmt
	for _, p := range st.pages {
		if p.params != nil {
			body = append(body, &ir.Assign{Target: ir.CloneExprSharingDecls(p.params), Op: ast.AssignSet,
				Value: &ir.Select{Type: p.ptype, Operand: top(), Field: p.field}})
		}
	}
	body = append(body,
		&ir.Assign{Target: ir.CloneExprSharingDecls(st.current), Op: ast.AssignSet,
			Value: &ir.Select{Type: st.fam.typ, Operand: top(), Field: "page"}},
		callListRemove(hist(), last(), entryT),
	)
	return []ir.Stmt{&ir.If{
		Cond: &ir.Binary{Type: ir.TypBool, Op: ast.BinGt, Left: listLength(hist()), Right: navInt(0)},
		Body: body,
	}}, nil
}

func listLength(list ir.Expr) ir.Expr {
	fn := &ir.Func{Name: "length", Receiver: "list", Intrinsic: "list.length", Return: ir.TypInt}
	if def := ir.LookupIntrinsic("list.length"); def != nil {
		fn.Params, fn.Return = def.Instantiate(elemOf(typeOf(list)))
	}
	return &ir.Call{Type: ir.TypInt, Func: fn, Args: []ir.CallArg{{Value: list}}}
}

func callListRemove(list, index ir.Expr, elem *ir.Type) ir.Stmt {
	fn := &ir.Func{Name: "remove", Receiver: "list", Intrinsic: "list.remove"}
	if def := ir.LookupIntrinsic("list.remove"); def != nil {
		fn.Params, fn.Return = def.Instantiate(elem)
	}
	return &ir.CallStmt{Call: &ir.Call{Type: ir.TypVoid, Func: fn, Args: []ir.CallArg{{Value: list}, {Value: index}}}}
}

func elemOf(t *ir.Type) *ir.Type {
	if t != nil && len(t.Elems) > 0 {
		return t.Elems[0]
	}
	return ir.TypDyn
}

// lowerView replaces each stack with the if-chain over its pages and each
// link with a button, at any depth of a view body.
func (nv *navigation) lowerView(stmts []ir.Stmt) []ir.Stmt {
	out := stmts[:0:0]
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if st, ok := nv.byNode[n].(*navStack); ok {
				if chain := nv.chain(st); chain != nil {
					out = append(out, chain)
				}
				continue
			}
			if isNavNode(n, ir.BuiltinNavLink) {
				out = append(out, nv.link(n))
				continue
			}
			n.Children = nv.lowerView(n.Children)
			for _, name := range ir.SlotNames(n.Slots) {
				n.Slots[name].Body = nv.lowerView(n.Slots[name].Body)
			}
		case *ir.If:
			n.Body = nv.lowerView(n.Body)
			n.Else = nv.lowerView(n.Else)
		case *ir.For:
			n.Body = nv.lowerView(n.Body)
			n.Else = nv.lowerView(n.Else)
		case *ir.ErrorBoundary:
			n.Children = nv.lowerView(n.Children)
			n.Failed = nv.lowerView(n.Failed)
		case *ir.ContextProvider:
			n.Children = nv.lowerView(n.Children)
		}
		out = append(out, s)
	}
	return out
}

// chain is the stack's pages as an if-chain on the current page's id, so a
// page not showing is not mounted.
func (nv *navigation) chain(st *navStack) ir.Stmt {
	var head *ir.If
	var tail *ir.If
	for _, pg := range st.pages {
		cond := &ir.Binary{Type: ir.TypBool, Op: ast.BinEq,
			Left:  &ir.Select{Type: ir.TypInt, Operand: ir.CloneExprSharingDecls(st.current), Field: "id"},
			Right: navInt(pg.id)}
		branch := &ir.If{Cond: cond, Body: nv.lowerView(nv.content(pg))}
		if head == nil {
			head = branch
		} else {
			tail.Else = []ir.Stmt{branch}
		}
		tail = branch
	}
	if head == nil {
		return nil
	}
	return head
}

// content is what a page renders, its population's parameter read as the
// page's params.
func (nv *navigation) content(pg *navPage) []ir.Stmt {
	body := pg.node.Children
	if sc := pg.node.Slots["content"]; sc != nil {
		body = sc.Body
	}
	if pg.pparam == nil {
		return body
	}
	with := pg.params
	if with == nil {
		with = pg.empty
	}
	_ = ir.RewriteExprs(body, func(e ir.Expr) (ir.Expr, error) {
		if id, ok := e.(*ir.Ident); ok && id.Sym == ir.Symbol(pg.pparam) {
			return ir.CloneExprSharingDecls(with), ir.SkipDir
		}
		return e, nil
	})
	return body
}

// link is a nav.link as a button showing its text, whose click runs the
// link's own `@click` and then goes to its page.
func (nv *navigation) link(n *ir.NodeInst) ir.Stmt {
	btn := &ir.NodeInst{
		AST:       n.AST,
		Site:      n.Site,
		Name:      n.Name,
		Component: nv.button,
		ID:        n.ID,
		Handle:    n.Handle,
		Key:       n.Key,
	}
	for _, p := range n.Props {
		if p.Name == "text" || p.Name == "style" {
			btn.Props = append(btn.Props, p)
		}
	}
	var click *ir.EventHandler
	for i := range n.Handlers {
		if n.Handlers[i].Name == "click" {
			h := n.Handlers[i]
			click = &h
		}
	}
	if click == nil {
		click = &ir.EventHandler{Name: "click", Func: &ir.Func{Return: ir.TypVoid}}
	}
	if pg, ok := nv.resolve(n.Prop("to")).(*navPage); ok {
		click.Func.Block = append(click.Func.Block, nv.goStmts(pg, n.Prop("params"))...)
	}
	btn.Handlers = append(btn.Handlers, *click)
	if n.Handle != nil {
		n.Handle.Type = nv.button.SymType()
	}
	return btn
}

// rewriteExpr reads every handle and every `current` as the lowering's cells
// and records.
func (nv *navigation) rewriteExpr(n ir.Node) (ir.Node, error) {
	switch x := n.(type) {
	case *ir.Binary:
		if x.Op != ast.BinEq && x.Op != ast.BinNeq {
			return n, nil
		}
		if !nv.isNavValue(x.Left) && !nv.isNavValue(x.Right) {
			return n, nil
		}
		return &ir.Binary{AST: x.AST, Type: x.Type, Op: x.Op, Left: nv.idOf(x.Left), Right: nv.idOf(x.Right)}, ir.SkipDir
	case *ir.Select:
		switch x.Field {
		case "current":
			if st, ok := nv.resolve(x.Operand).(*navStack); ok {
				return rebase(st.current, x.Operand), ir.SkipDir
			}
		case "params":
			if pg, ok := nv.resolve(x.Operand).(*navPage); ok {
				if pg.params != nil {
					return rebase(pg.params, x.Operand), ir.SkipDir
				}
				return ir.CloneExprSharingDecls(pg.empty), ir.SkipDir
			}
		}
		if pg, ok := nv.resolve(x).(*navPage); ok {
			return nv.record(pg), ir.SkipDir
		}
	case *ir.Ident:
		if pg, ok := nv.resolve(x).(*navPage); ok {
			return nv.record(pg), ir.SkipDir
		}
	}
	return n, nil
}

// isNavValue reports whether e is a navigator's value: a page's handle, a
// stack's current, or anything typed by a lowered family.
func (nv *navigation) isNavValue(e ir.Expr) bool {
	if _, ok := nv.resolve(e).(*navPage); ok {
		return true
	}
	return nv.navFamilyOf(typeOf(e)) != nil
}

// idOf is a navigator value's id: a page's own literal, or the `id` field of
// the record e reads.
func (nv *navigation) idOf(e ir.Expr) ir.Expr {
	if pg, ok := nv.resolve(e).(*navPage); ok {
		return navInt(pg.id)
	}
	_ = ir.Rewrite(e, nv.rewriteExpr)
	var out ir.Expr = e
	if n, _ := nv.rewriteExpr(e); n != nil {
		if x, ok := n.(ir.Expr); ok {
			out = x
		}
	}
	return &ir.Select{Type: ir.TypInt, Operand: out, Field: "id"}
}

// navFamilyOf is the lowered family a navigator or page type is a value of.
func (nv *navigation) navFamilyOf(t *ir.Type) *navFamily {
	if t == nil || t.Kind != ir.TypeComponent {
		return nil
	}
	c, _ := t.Decl.(*ir.Component)
	if c == nil {
		return nil
	}
	if fam := nv.families[c]; fam != nil {
		return fam
	}
	if c.Tree != nil {
		return nv.families[c.Tree]
	}
	return nil
}

// retype makes every navigator or page type the program names the family's
// record: a func's parameter, a var, a field, and the type each expression
// carries.
func (nv *navigation) retype() {
	if len(nv.families) == 0 {
		return
	}
	fix := func(t **ir.Type) {
		if nt := nv.recordType(*t); nt != *t {
			*t = nt
		}
	}
	fixFunc := func(f *ir.Func) {
		if f == nil {
			return
		}
		for _, p := range f.Params {
			fix(&p.Type)
		}
		fix(&f.Return)
	}
	for _, v := range nv.pkg.Vars {
		fix(&v.Type)
	}
	for _, f := range nv.pkg.Funcs {
		fixFunc(f)
	}
	for _, s := range nv.pkg.Structs {
		for _, f := range s.Fields {
			fix(&f.Type)
		}
	}
	for _, c := range nv.pkg.Components {
		if c.Stdlib {
			continue
		}
		for _, v := range c.Vars {
			fix(&v.Type)
		}
		for _, p := range c.Props {
			fix(&p.Type)
		}
		for _, f := range c.Funcs {
			fixFunc(f)
		}
	}
	for _, f := range navFuncs(nv.pkg) {
		fixFunc(f)
	}
	exprType := reflect.TypeFor[*ir.Type]()
	_ = ir.Rewrite(nv.pkg, func(n ir.Node) (ir.Node, error) {
		if lv, ok := n.(*ir.LocalVar); ok {
			fix(&lv.Type)
			if lv.Sym != nil {
				fix(&lv.Sym.Type)
			}
		}
		rv := reflect.ValueOf(n)
		if rv.Kind() != reflect.Pointer || rv.Elem().Kind() != reflect.Struct {
			return n, nil
		}
		f := rv.Elem().FieldByName("Type")
		if f.IsValid() && f.Type() == exprType && f.CanSet() {
			t := f.Interface().(*ir.Type)
			if nt := nv.recordType(t); nt != t {
				f.Set(reflect.ValueOf(nt))
			}
		}
		return n, nil
	})
}

// recordType is t with every navigator or page type in it the record.
func (nv *navigation) recordType(t *ir.Type) *ir.Type {
	if t == nil {
		return nil
	}
	if fam := nv.navFamilyOf(t); fam != nil {
		return fam.typ
	}
	if len(t.Elems) == 0 {
		return t
	}
	elems := make([]*ir.Type, len(t.Elems))
	changed := false
	for i, e := range t.Elems {
		elems[i] = nv.recordType(e)
		changed = changed || elems[i] != e
	}
	if !changed {
		return t
	}
	cp := *t
	cp.Elems = slices.Clip(elems)
	return &cp
}
