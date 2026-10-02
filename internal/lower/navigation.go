package lower

import (
	"fmt"
	"reflect"
	"slices"
	"strconv"
	"strings"

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
//	var pages__current _page__value = _page__value{id=0, href="/", title="Home", meta=…}
//	var pkg__params Pkg = Pkg{}
//	var pages__history list<pages__entry> = []
//	if pages__current.id == 0 { … } else if pages__current.id == 1 { …p is pkg__params… }
//
// The record is the page family's: one struct per instantiation of it -- the
// family's props at the meta type its stack's pages carry -- plus an `id`, so
// a value of the family is the same record whichever stack it came from, and
// ids are numbered across every page of the family in the package.
// A page whose params are the empty struct has no var. An entry holds the page
// it replaced and every params var of its stack, which is what `back`
// restores.
//
// `pages.go(pkg, P{…})` pushes an entry and writes the params and the current
// page, and `pages.go(pkg)` writes the params the page's call site wrote;
// `pages.back()` pops one and restores both, and does nothing at the bottom. `nav.link` becomes a `ui.button` showing its text whose click runs
// the link's own `@click` and then the same `go`. A read of `pages.current`
// is a read of the var, a page's handle read as a value is its record, `==`
// against a page's value compares ids, and every page type the program names
// is the record's. `pkg.params` reads what the page's call site wrote, which
// is where the page starts; the params it is showing reach its content alone.
var passNavigation = pass{
	name:    "Navigation",
	enabled: func(Features) bool { return true },
	apply:   lowerNavigation,
}

// passNavigationValues is the half of the lowering every target gets, the one
// that declares the navigation's own nodes included: what a page *is*, read as
// a value. Each page's record -- its family's props at the stack's meta type,
// plus its id -- is made once and hung on the node (NodeInst.Record), a
// page's handle read anywhere is that record, `pkg.params` is what the page's
// call site wrote, `==` against a page compares ids, `pages.current` is the
// one call `stack.current(pages)` whichever way it was spelled, and every page
// type the program names is the record's. What stays is the nodes and the
// `go`, `back` and `current` calls, which name the stack by its handle and the
// page by its record: passNavigation answers them where the target does not,
// and android answers them with a NavHost.
var passNavigationValues = pass{
	name:    "NavigationValues",
	enabled: func(Features) bool { return true },
	apply:   lowerNavigationValues,
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
	// startExpr is the record the stack starts at, made once.
	startExpr ir.Expr
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
	// loops are the `for`s the page is written under, outermost first, one
	// page per iteration of the nest; none for a page written once. Each
	// loop's key is its index (ensureLoopIndex), and the indices are the copy
	// a record of the page holds.
	loops []*ir.For
	// conds are what the `if`s around the page say, outermost first, an else
	// branch's negated: the page is one of the stack's where all hold.
	conds []ir.Expr
}

type navFamily struct {
	comp   *ir.Component
	props  []*ir.Prop // the family's props at this instantiation's types
	record *ir.StructDef
	typ    *ir.Type
	next   int
	// copied says a page of the family is written under a `for`: its record
	// then holds the iteration beside the id, which is what tells two copies
	// of one page apart.
	copied bool
}

type navigation struct {
	pkg      *ir.Package
	stacks   []*navStack
	byNode   map[*ir.NodeInst]any // *navStack or *navPage
	byHandle map[*ir.Var]any
	byField  map[navKey]any // a handle reached by select through a component
	byRecord map[navRecordKey]*navPage
	families map[string]*navFamily
	button   *ir.Component
	names    map[string]bool
	err      error // the first a view lowering refused
	// lenient leaves a call naming no stack of these for the target, which
	// answers the stacks it composed (lowerNavigation).
	lenient bool
}

// navRecordKey names a page by its record, which is how a `go` and a link
// name one once passNavigationValues has read the handle as its value.
type navRecordKey struct {
	def *ir.StructDef
	id  int
}

type navKey struct {
	comp *ir.Component
	id   string
}

// collectNavigation finds every stack the package renders and the pages it
// holds, numbered as passNavigationValues numbered them. Nil when the package
// renders none and links to none.
func collectNavigation(pkg *ir.Package) (*navigation, error) {
	if pkg == nil {
		return nil, nil
	}
	nv := &navigation{
		pkg:      pkg,
		byNode:   map[*ir.NodeInst]any{},
		byHandle: map[*ir.Var]any{},
		byField:  map[navKey]any{},
		byRecord: map[navRecordKey]*navPage{},
		families: map[string]*navFamily{},
		names:    map[string]bool{},
	}
	for _, o := range ir.Owners(pkg) {
		if err := nv.collect(o, o.Stmts(), 0); err != nil {
			return nil, err
		}
	}
	if len(nv.stacks) == 0 && !nv.hasLinks() {
		return nil, nil
	}
	return nv, nil
}

func lowerNavigationValues(pkg *ir.Package, feats Features, _ Options) error {
	if err := spliceGroups(pkg); err != nil {
		return err
	}
	nv, err := collectNavigation(pkg)
	if nv == nil || err != nil {
		return err
	}
	for _, st := range nv.stacks {
		for _, pg := range st.pages {
			for depth, fs := range pg.loops {
				ensureLoopIndex(fs, depth)
				st.fam.copied = true
			}
		}
	}
	for _, fam := range nv.families {
		if fam.copied && !hasField(fam.record, "copy") {
			fam.record.Fields = append(fam.record.Fields, &ir.StructField{Name: "copy", Type: ir.ListOf(ir.TypInt)})
		}
	}
	for _, st := range nv.stacks {
		for _, pg := range st.pages {
			pg.node.Record = nv.record(pg)
		}
	}
	if err := ir.Rewrite(pkg, nv.valueExpr); err != nil {
		return err
	}
	nv.retype()
	// A copy's params are what its page is written with, read where the copy
	// is: a `go` or a link naming a page under a loop and passing none passes
	// those, so a target answering the navigation itself is handed them where
	// the loop's variables are bound rather than at the page's declaration.
	nv.handLoopedParams()
	// A target that declares the stack in its own code is handed the page it
	// starts at, which for a copy is found by a search the fold after
	// lowering answers.
	if feats.Navigation && !feats.NavigationHrefs {
		for _, st := range nv.stacks {
			if st.fam != nil && st.wrapped() {
				st.node.Start = nv.startRecord(st)
			}
		}
	}
	return nil
}

// handLoopedParams writes a looped page's own params into every `go` and
// nav.link naming it that passes none.
func (nv *navigation) handLoopedParams() {
	looped := func(e ir.Expr) *navPage {
		if pg, ok := nv.resolve(e).(*navPage); ok && pg.looped() && !isEmptyStruct(pageParamsType(pg.node)) {
			return pg
		}
		return nil
	}
	_ = ir.Walk(nv.pkg, func(n ir.Node) error {
		switch x := n.(type) {
		case *ir.CallStmt:
			if x.Call == nil || x.Call.Func == nil || x.Call.Func.Intrinsic != "nav.go" {
				return nil
			}
			if pg := looped(callArg(x.Call, "to", 1)); pg != nil {
				if p, err := handed(callArg(x.Call, "params", 2)); err == nil && p == nil {
					setCallArg(x.Call, "params", 2, pg.start())
				}
			}
		case *ir.NodeInst:
			if !isNavNode(x, ir.BuiltinNavLink) {
				return nil
			}
			if pg := looped(x.Prop("to")); pg != nil {
				if p, err := handed(x.Prop("params")); err == nil && p == nil {
					set := false
					for i := range x.Props {
						if x.Props[i].Name == "params" {
							x.Props[i].Value, set = pg.start(), true
						}
					}
					if !set {
						x.Props = append(x.Props, ir.Arg{Name: "params", Value: pg.start()})
					}
				}
			}
		}
		return nil
	})
}

// On a target holding Navigation it lowers only what the composition left a
// builtin there: the stacks in a surface other than the document (a window
// html shows inside it, #[gen.renders(surface)]), which navigate in place. The
// document's own were composed into the target's primitives, so a call naming
// one resolves to no stack here and is left for the target; and a target that
// renders no surface -- android -- answers every stack itself.
func lowerNavigation(pkg *ir.Package, feats Features, _ Options) error {
	if feats.Navigation {
		if surfaces, err := findSurfaces(pkg); surfaces == nil || err != nil {
			return err
		}
	}
	nv, err := collectNavigation(pkg)
	if nv == nil || err != nil {
		return err
	}
	nv.lenient = feats.Navigation
	if nv.hasLinks() {
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
	if nv.err != nil {
		return nv.err
	}
	return ir.Rewrite(pkg, nv.structureExpr)
}

// collect finds every stack an owner's body renders, and the pages each
// holds.
func (nv *navigation) collect(o ir.Owner, stmts []ir.Stmt, loops int) error {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if isNavNode(n, ir.BuiltinNavStack) {
				if loops > 0 {
					return fmt.Errorf("%s: a nav.stack under a `for` would be one stack for every copy; write it in a component the loop renders", ir.NodePos(n))
				}
				if err := nv.addStack(o, n); err != nil {
					return err
				}
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

func (nv *navigation) addStack(o ir.Owner, n *ir.NodeInst) error {
	st := &navStack{node: n, owner: o, name: n.ID}
	if st.name == "" {
		st.name = "__stack" + strconv.Itoa(len(nv.stacks))
	}
	nv.stacks = append(nv.stacks, st)
	nv.index(st, n, o)
	return nv.addPages(st, o, n.Children, nil)
}

// addPages numbers the pages a stack holds, in the order written: directly,
// under an `if` the build decides, and under a `for` over a constant, which
// is one page per element -- every copy shares the page's id, and its record
// holds the iterations beside it, one per loop. Groups were spliced in before
// (spliceGroups).
func (nv *navigation) addPages(st *navStack, o ir.Owner, stmts []ir.Stmt, loops []*ir.For, conds ...ir.Expr) error {
	for _, s := range stmts {
		switch p := s.(type) {
		case *ir.NodeInst:
			if !isNavNode(p, ir.BuiltinNavPage) {
				continue
			}
			if st.fam == nil {
				st.fam = nv.familyOf(p)
			}
			fam := st.fam
			pg := &navPage{node: p, stack: st, id: fam.next, loops: loops, conds: conds}
			if id, ok := recordID(p.Record); ok {
				pg.id = id
			}
			fam.next++
			if _, taken := nv.byRecord[navRecordKey{fam.record, pg.id}]; !taken {
				nv.byRecord[navRecordKey{fam.record, pg.id}] = pg
			}
			st.pages = append(st.pages, pg)
			nv.index(pg, p, o)
		case *ir.If:
			if !ir.IsConst(p.Cond) {
				return fmt.Errorf("%s: a stack's pages are fixed when it is built, and this `if` reads state; decide it from constants, or write it inside a page", ir.StmtPos(p))
			}
			if err := nv.addPages(st, o, p.Body, loops, append(slices.Clip(conds), p.Cond)...); err != nil {
				return err
			}
			not := &ir.Unary{Type: ir.TypBool, Op: ast.UnaryNot, Operand: p.Cond}
			if err := nv.addPages(st, o, p.Else, loops, append(slices.Clip(conds), not)...); err != nil {
				return err
			}
		case *ir.For:
			if !constInLoops(p.Iter, loops) {
				return fmt.Errorf("%s: a stack's pages are fixed when it is built, and this `for` iterates state; iterate a constant", ir.StmtPos(p))
			}
			if len(p.Else) > 0 && holdsNavPage(p.Else) {
				return fmt.Errorf("%s: a page under a `for`'s else is not supported: write it beside the loop", ir.StmtPos(p))
			}
			if err := nv.addPages(st, o, p.Body, append(slices.Clip(loops), p), conds...); err != nil {
				return err
			}
		}
	}
	return nil
}

func holdsNavPage(stmts []ir.Stmt) bool {
	found := false
	_ = ir.WalkStmts(stmts, func(s ir.Stmt) error {
		if n, ok := s.(*ir.NodeInst); ok && isNavNode(n, ir.BuiltinNavPage) {
			found = true
			return ir.SkipAll
		}
		return nil
	})
	return found
}

// ensureLoopIndex gives a loop of pages an index, which is the copy each
// page's record holds: `for var it = xs` becomes `for var __copy, it = xs`,
// `__copy1` one loop in, and so on.
func ensureLoopIndex(fs *ir.For, depth int) {
	if fs.Value != "" {
		return
	}
	fs.Value, fs.ValueSym = fs.Key, fs.KeySym
	fs.Key = "__copy"
	if depth > 0 {
		fs.Key += strconv.Itoa(depth)
	}
	fs.KeySym = &ir.LoopVar{Name: fs.Key, Type: ir.TypInt}
}

func hasField(sd *ir.StructDef, name string) bool {
	return sd != nil && slices.ContainsFunc(sd.Fields, func(f *ir.StructField) bool { return f.Name == name })
}

// copyOf is the iterations a page's record holds: the index of each loop it
// is written under, outermost first, and none for a page written once.
func (pg *navPage) copyOf() ir.Expr {
	lit := &ir.ListLit{Type: ir.ListOf(ir.TypInt)}
	for _, e := range pg.copyIndices() {
		lit.Elems = append(lit.Elems, e)
	}
	return lit
}

// copyIndices is each loop's index, as the expressions a copy is read by.
func (pg *navPage) copyIndices() []ir.Expr {
	var out []ir.Expr
	for _, fs := range pg.loops {
		if fs.KeySym != nil {
			out = append(out, &ir.Ident{Name: fs.KeySym.Name, Type: ir.TypInt, Sym: fs.KeySym})
		}
	}
	return out
}

func (pg *navPage) looped() bool { return len(pg.loops) > 0 }

// copyMatch is the test that the copy list c names the copy whose indices
// are want: as long, and equal element by element. Go's slices have no ==,
// and JavaScript's arrays compare by reference, so it is spelled out.
func copyMatch(c ir.Expr, want []ir.Expr) ir.Expr {
	var out ir.Expr = &ir.Binary{Type: ir.TypBool, Op: ast.BinEq, Left: listLength(ir.CloneExprSharingDecls(c)), Right: navInt(len(want))}
	for i, w := range want {
		at := &ir.Index{Type: ir.TypInt, Operand: ir.CloneExprSharingDecls(c), Idx: navInt(i)}
		out = &ir.Binary{Type: ir.TypBool, Op: ast.BinAnd, Left: out, Right: &ir.Binary{Type: ir.TypBool, Op: ast.BinEq, Left: at, Right: ir.CloneExprSharingDecls(w)}}
	}
	return out
}

// constInLoops reports whether e is a constant once the variables of the
// loops around it are bound -- each a loop over a constant.
func constInLoops(e ir.Expr, loops []*ir.For) bool {
	if len(loops) == 0 {
		return ir.IsConst(e)
	}
	return constInLoop(e, loops...)
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

// familyOf is the family a page's record is: the one passNavigationValues
// made, when it has run, and otherwise made here.
func (nv *navigation) familyOf(p *ir.NodeInst) *navFamily {
	lit, ok := p.Record.(*ir.StructLit)
	if !ok || lit.Def == nil {
		return nv.family(p.Component.Tree, ir.FamilyArgs(p.Component, pageArgs(p)))
	}
	key := fmt.Sprintf("record:%p", lit.Def)
	if fam := nv.families[key]; fam != nil {
		return fam
	}
	fam := &navFamily{comp: p.Component.Tree, record: lit.Def, typ: &ir.Type{Kind: ir.TypeStruct, Decl: lit.Def}, copied: hasField(lit.Def, "copy")}
	nv.families[key] = fam
	return fam
}

// recordID is the id a page's record literal holds.
func recordID(e ir.Expr) (int, bool) {
	lit, ok := e.(*ir.StructLit)
	if !ok {
		return 0, false
	}
	for _, f := range lit.Fields {
		if f.Name != "id" {
			continue
		}
		if l, ok := f.Value.(*ir.Literal); ok {
			n, err := strconv.Atoi(l.Value)
			return n, err == nil
		}
	}
	return 0, false
}

// family is the record a page family's values lower to at args, made the
// first time one of its pages is met.
func (nv *navigation) family(f *ir.Component, args []*ir.Type) *navFamily {
	args = familyArgs(f, args)
	key := familyKey(f, args)
	if fam := nv.families[key]; fam != nil {
		return fam
	}
	def := &ir.StructDef{Name: nv.fresh(f.Name + "__value")}
	def.Fields = append(def.Fields, &ir.StructField{Name: "id", Type: ir.TypInt})
	bindings := map[string]*ir.Type{}
	for i, tp := range f.TypeParams {
		bindings[tp.Name] = args[i]
	}
	props := make([]*ir.Prop, len(f.Props))
	for i, p := range f.Props {
		cp := *p
		if cp.Type != nil {
			cp.Type = cp.Type.Substitute(bindings)
		}
		props[i] = &cp
		def.Fields = append(def.Fields, &ir.StructField{Name: p.Name, Type: cp.Type})
		nv.adoptAnon(cp.Type)
	}
	nv.pkg.Structs = append(nv.pkg.Structs, def)
	fam := &navFamily{comp: f, props: props, record: def, typ: &ir.Type{Kind: ir.TypeStruct, Decl: def}}
	nv.families[key] = fam
	return fam
}

// familyArgs is args with the family's defaults filling what it leaves off.
func familyArgs(f *ir.Component, args []*ir.Type) []*ir.Type {
	out := make([]*ir.Type, len(f.TypeParams))
	for i, tp := range f.TypeParams {
		switch {
		case i < len(args) && args[i] != nil:
			out[i] = args[i]
		case tp.Default != nil:
			out[i] = tp.Default
		default:
			out[i] = ir.TypDyn
		}
	}
	return out
}

func familyKey(f *ir.Component, args []*ir.Type) string {
	var key strings.Builder
	key.WriteString(fmt.Sprintf("%p", f))
	for _, a := range args {
		key.WriteString("," + a.String())
	}
	return key.String()
}

// pageArgs is what a page's call site bound its type parameters to, read off
// the props each is the type of -- `params` is T and `meta` is M -- and its
// defaults where nothing was written.
func pageArgs(n *ir.NodeInst) []*ir.Type {
	comp := n.Component
	out := make([]*ir.Type, len(comp.TypeParams))
	for i, tp := range comp.TypeParams {
		out[i] = tp.Default
		for _, p := range comp.Props {
			if p.Type == nil || p.Type.Kind != ir.TypeTypeParam || p.Type.ParamName != tp.Name {
				continue
			}
			if t := typeOf(n.Prop(p.Name)); t != nil && t.Kind != ir.TypeDyn {
				out[i] = t
			}
		}
	}
	return out
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
			pg.empty = pg.start()
			continue
		}
		pg.field = pageName(pg)
		st.entry.Fields = append(st.entry.Fields, &ir.StructField{Name: pg.field, Type: pg.ptype})
		init := pg.start()
		if pg.looped() {
			// One cell for every copy, holding the showing copy's params: it
			// starts at the starting copy's, where that is a copy of this page.
			var err error
			if init, err = nv.loopedStartParams(st, pg); err != nil {
				if nv.err == nil {
					nv.err = err
				}
				init = ir.ZeroExpr(pg.ptype)
			}
		}
		v := &ir.Var{Name: nv.fresh(pageName(pg) + "__params"), Type: pg.ptype, Init: init}
		vars = append(vars, v)
		pg.params = varIdent(v)
	}
	nv.pkg.Structs = append(nv.pkg.Structs, st.entry)
	if st.fam != nil {
		v := &ir.Var{Name: nv.fresh(st.name + "__current"), Type: st.fam.typ, Init: nv.startRecord(st)}
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

// startRecord is the record of the page a stack shows first. A page under a
// `for` or an `if` has its href, and its being there at all, only once the
// program runs, so where one could be the start the stack asks a function
// that looks: the page at "/", or the first.
func (nv *navigation) startRecord(st *navStack) ir.Expr {
	pg := st.start()
	if pg == nil {
		return nil
	}
	if st.startExpr != nil {
		return ir.CloneExprSharingDecls(st.startExpr)
	}
	if lit, ok := pg.node.Prop("href").(*ir.Literal); (ok && lit.Value == "/" && !pg.looped() && len(pg.conds) == 0) || !st.wrapped() {
		st.startExpr = nv.record(pg)
		return ir.CloneExprSharingDecls(st.startExpr)
	}
	fn := &ir.Func{Name: nv.fresh(st.name + "__start"), Return: st.fam.typ, Purity: ir.PurityPure}
	at := func(pg *navPage, test ir.Expr) ir.Stmt {
		cond := test
		for _, v := range slices.Backward(pg.conds) {
			c := ir.CloneExprSharingDecls(v)
			if cond == nil {
				cond = c
			} else {
				cond = &ir.Binary{Type: ir.TypBool, Op: ast.BinAnd, Left: c, Right: cond}
			}
		}
		var s ir.Stmt = &ir.Return{Value: nv.record(pg)}
		if cond != nil {
			s = &ir.If{Cond: cond, Body: []ir.Stmt{s}}
		}
		for _, fs := range slices.Backward(pg.loops) {

			s = &ir.For{AST: fs.AST, Key: fs.Key, Value: fs.Value, KeySym: fs.KeySym, ValueSym: fs.ValueSym,
				Iter: ir.CloneExprSharingDecls(fs.Iter), ElemType: fs.ElemType, Body: []ir.Stmt{s}}
		}
		return s
	}
	for _, pg := range st.pages {
		root := &ir.Binary{Type: ir.TypBool, Op: ast.BinEq, Left: ir.CloneExprSharingDecls(pg.node.Prop("href")), Right: &ir.Literal{Type: ir.TypString, Value: "/"}}
		fn.Block = append(fn.Block, at(pg, root))
	}
	for _, pg := range st.pages {
		fn.Block = append(fn.Block, at(pg, nil))
		if !pg.looped() && len(pg.conds) == 0 {
			// The first page written once is the start where none is at "/".
			nv.pkg.Funcs = append(nv.pkg.Funcs, fn)
			st.startExpr = &ir.Call{Type: st.fam.typ, Func: fn}
			return ir.CloneExprSharingDecls(st.startExpr)
		}
	}
	none := &ir.StructLit{Type: st.fam.typ, Def: st.fam.record, Fields: []ir.FieldInit{{Name: "id", Value: navInt(-1)}}}
	fn.Block = append(fn.Block, &ir.Return{Value: none})
	nv.pkg.Funcs = append(nv.pkg.Funcs, fn)
	st.startExpr = &ir.Call{Type: st.fam.typ, Func: fn}
	return ir.CloneExprSharingDecls(st.startExpr)
}

// wrapped reports whether a stack's pages are written under a `for` or an
// `if`, rather than one after another.
func (st *navStack) wrapped() bool {
	return slices.ContainsFunc(st.pages, func(pg *navPage) bool { return pg.looped() || len(pg.conds) > 0 })
}

func pageName(pg *navPage) string {
	if pg.node.ID != "" {
		return pg.node.ID
	}
	return "__page" + strconv.Itoa(pg.id)
}

// start is the params a page starts at, and is shown with by a `go` or a
// link that hands it none: what its call site wrote, or the type's zero.
func (pg *navPage) start() ir.Expr {
	if e := pg.node.Prop("params"); e != nil {
		return ir.CloneExprSharingDecls(e)
	}
	return ir.ZeroExpr(pg.ptype)
}

// handed is the params a `go` or a link passed, nil where it passed none:
// the argument is an option, the absent one `null` and a written one the
// promotion of a value into it.
func handed(e ir.Expr) (ir.Expr, error) {
	if e == nil {
		return nil, nil
	}
	if t := typeOf(e); t != nil && t.Kind == ir.TypeNull {
		return nil, nil
	}
	if conv, ok := e.(*ir.Conversion); ok && conv.Type != nil && conv.Type.Kind == ir.TypeOption {
		if t := typeOf(conv.Operand); t != nil && t.Kind == ir.TypeNull {
			return nil, nil
		}
		if t := typeOf(conv.Operand); t != nil && t.Kind != ir.TypeOption {
			return conv.Operand, nil
		}
	}
	if t := typeOf(e); t != nil && t.Kind == ir.TypeOption {
		return nil, fmt.Errorf("params handed as an option value, which may be absent when the program runs, is not supported yet; pass the value or nothing")
	}
	return e, nil
}

// pageParamsType is what a page's params are, read off what the call site
// wrote, or the empty struct.
func pageParamsType(n *ir.NodeInst) *ir.Type {
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
	if pg.node.Record != nil {
		return ir.CloneExprSharingDecls(pg.node.Record)
	}
	fam := pg.stack.fam
	lit := &ir.StructLit{Type: fam.typ, Def: fam.record}
	lit.Fields = append(lit.Fields, ir.FieldInit{Name: "id", Value: navInt(pg.id)})
	for _, p := range fam.props {
		v := pg.node.Prop(p.Name)
		if v == nil {
			v = p.Default
		}
		if v == nil {
			v = ir.ZeroExpr(p.Type)
		}
		lit.Fields = append(lit.Fields, ir.FieldInit{Name: p.Name, Value: ir.CloneExprSharingDecls(v)})
	}
	if fam.copied {
		lit.Fields = append(lit.Fields, ir.FieldInit{Name: "copy", Value: pg.copyOf()})
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
				if nv.lenient {
					if _, ok := nv.resolve(callArg(n.Call, "", 0)).(*navStack); !ok {
						break
					}
				}
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
	case *ir.StructLit:
		if id, ok := recordID(x); ok && x.Def != nil {
			if pg := nv.byRecord[navRecordKey{x.Def, id}]; pg != nil {
				return pg
			}
		}
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
	params, err := handed(callArg(n.Call, "params", 2))
	if err != nil {
		return nil, fmt.Errorf("%s: %w", ir.StmtPos(n), err)
	}
	return nv.goStmts(pg, params), nil
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
			params = pg.start()
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
				out = append(out, nv.chain(st)...)
				continue
			}
			if isNavNode(n, ir.BuiltinNavLink) {
				btn, err := nv.link(n)
				if err != nil && nv.err == nil {
					nv.err = err
				}
				if btn != nil {
					out = append(out, btn)
				}
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
// page not showing is not mounted. Pages written under a `for` or an `if`
// keep it around them, each page the `if` on its id and, under a `for`, on
// the copy its iteration is.
func (nv *navigation) chain(st *navStack) []ir.Stmt {
	if st.wrapped() {
		return nv.wrappedChain(st, st.node.Children)
	}
	var head *ir.If
	var tail *ir.If
	for _, pg := range st.pages {
		branch := &ir.If{Cond: nv.showing(pg), Body: nv.lowerView(nv.content(pg))}
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
	return []ir.Stmt{head}
}

// showing is the test that pg is the page the stack shows.
func (nv *navigation) showing(pg *navPage) ir.Expr {
	st := pg.stack
	var cond ir.Expr = &ir.Binary{Type: ir.TypBool, Op: ast.BinEq,
		Left:  &ir.Select{Type: ir.TypInt, Operand: ir.CloneExprSharingDecls(st.current), Field: "id"},
		Right: navInt(pg.id)}
	if pg.looped() {
		cond = &ir.Binary{Type: ir.TypBool, Op: ast.BinAnd, Left: cond,
			Right: copyMatch(&ir.Select{Type: ir.ListOf(ir.TypInt), Operand: ir.CloneExprSharingDecls(st.current), Field: "copy"}, pg.copyIndices())}
	}
	return cond
}

func (nv *navigation) wrappedChain(st *navStack, stmts []ir.Stmt) []ir.Stmt {
	var out []ir.Stmt
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.NodeInst:
			if pg, ok := nv.byNode[n].(*navPage); ok {
				out = append(out, &ir.If{Cond: nv.showing(pg), Body: nv.lowerView(nv.content(pg))})
			}
		case *ir.If:
			n.Body, n.Else = nv.wrappedChain(st, n.Body), nv.wrappedChain(st, n.Else)
			out = append(out, n)
		case *ir.For:
			n.Body = nv.wrappedChain(st, n.Body)
			out = append(out, n)
		}
	}
	return out
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
func (nv *navigation) link(n *ir.NodeInst) (ir.Stmt, error) {
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
		params, err := handed(n.Prop("params"))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", ir.NodePos(n), err)
		}
		click.Func.Block = append(click.Func.Block, nv.goStmts(pg, params)...)
	}
	btn.Handlers = append(btn.Handlers, *click)
	if n.Handle != nil {
		n.Handle.Type = nv.button.SymType()
	}
	return btn, nil
}

// valueExpr reads every page handle as its record, `pkg.params` as what the
// page's call site wrote, `==` against a page as a comparison of ids, and
// `pages.current` as the one call `stack.current(pages)`.
func (nv *navigation) valueExpr(n ir.Node) (ir.Node, error) {
	switch x := n.(type) {
	case *ir.Binary:
		if x.Op != ast.BinEq && x.Op != ast.BinNeq {
			return n, nil
		}
		if !nv.isNavValue(x.Left) && !nv.isNavValue(x.Right) {
			return n, nil
		}
		ids := &ir.Binary{AST: x.AST, Type: x.Type, Op: x.Op, Left: nv.idOf(x.Left), Right: nv.idOf(x.Right)}
		if !nv.copied(x.Left) && !nv.copied(x.Right) {
			return ids, ir.SkipDir
		}
		// Two copies of one page share its id, so the iteration is compared
		// too: equal when both are, different when either is.
		ids.Op = ast.BinEq
		var same ir.Expr = &ir.Binary{Type: x.Type, Op: ast.BinAnd, Left: ids, Right: nv.copiesEqual(x.Left, x.Right)}
		if x.Op == ast.BinNeq {
			same = &ir.Unary{Type: x.Type, Op: ast.UnaryNot, Operand: same}
		}
		return same, ir.SkipDir
	case *ir.Select:
		switch x.Field {
		case "current":
			if st, ok := nv.resolve(x.Operand).(*navStack); ok {
				if fn := st.node.Component.Methods["current"]; fn != nil {
					return &ir.Call{Type: x.Type, Func: fn, Args: []ir.CallArg{{Value: x.Operand}}}, nil
				}
			}
		case "params":
			// What the page's call site wrote: where it starts.
			if pg, ok := nv.resolve(x.Operand).(*navPage); ok {
				return pg.start(), ir.SkipDir
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

// structureExpr reads `pages.current` as the cell that holds it.
func (nv *navigation) structureExpr(n ir.Node) (ir.Node, error) {
	if x, ok := n.(*ir.Call); ok && x.Func != nil && x.Func.Intrinsic == "nav.current" && len(x.Args) > 0 {
		if st, ok := nv.resolve(x.Args[0].Value).(*navStack); ok {
			return rebase(st.current, x.Args[0].Value), ir.SkipDir
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
	if id, ok := recordID(e); ok {
		return navInt(id)
	}
	_ = ir.Rewrite(e, nv.valueExpr)
	var out ir.Expr = e
	if n, _ := nv.valueExpr(e); n != nil {
		if x, ok := n.(ir.Expr); ok {
			out = x
		}
	}
	return &ir.Select{Type: ir.TypInt, Operand: out, Field: "id"}
}

// copied reports whether e is a value of a family whose records hold a copy.
func (nv *navigation) copied(e ir.Expr) bool {
	if pg, ok := nv.resolve(e).(*navPage); ok {
		return pg.stack.fam != nil && pg.stack.fam.copied
	}
	if lit, ok := e.(*ir.StructLit); ok {
		return hasField(lit.Def, "copy")
	}
	if fam := nv.navFamilyOf(typeOf(e)); fam != nil {
		return fam.copied
	}
	// A record already: `pages.current` once read as the stack's call.
	if t := typeOf(e); t != nil {
		for _, fam := range nv.families {
			if t.Decl == ir.Symbol(fam.record) {
				return fam.copied
			}
		}
	}
	return false
}

// copyOfExpr is a navigator value's copy, as idOf is its id.
func (nv *navigation) copyOfExpr(e ir.Expr) ir.Expr {
	if pg, ok := nv.resolve(e).(*navPage); ok {
		return pg.copyOf()
	}
	if lit, ok := e.(*ir.StructLit); ok {
		for _, f := range lit.Fields {
			if f.Name == "copy" {
				return ir.CloneExprSharingDecls(f.Value)
			}
		}
	}
	var out ir.Expr = ir.CloneExprSharingDecls(e)
	if n, _ := nv.valueExpr(out); n != nil {
		if x, ok := n.(ir.Expr); ok {
			out = x
		}
	}
	return &ir.Select{Type: ir.TypInt, Operand: out, Field: "copy"}
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
	if c.IsFamily() {
		return nv.families[familyKey(c, familyArgs(c, t.Elems))]
	}
	if c.Tree != nil && c.Builtin == ir.BuiltinNavPage {
		return nv.families[familyKey(c.Tree, familyArgs(c.Tree, ir.FamilyArgs(c, t.Elems)))]
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

// spliceGroups puts the body of every group a stack holds in its place, so
// the pages the group renders are pages of the stack when they are numbered:
// a group (ir.Component.Group) renders pages without being one, and nothing
// else will splice it before the inliner, which runs long after navigation.
// What a group's call site writes -- its props and slot populations -- is
// bound the way the inliner binds it.
func spliceGroups(pkg *ir.Package) error {
	if pkg == nil {
		return nil
	}
	var err error
	var stacks func(stmts []ir.Stmt)
	var pages func(stmts []ir.Stmt, depth int) []ir.Stmt
	pages = func(stmts []ir.Stmt, depth int) []ir.Stmt {
		var out []ir.Stmt
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.NodeInst:
				if c := n.Component; c != nil && c.Group && err == nil {
					if depth > maxInlineGroupDepth {
						err = fmt.Errorf("%s: group %s renders itself", ir.NodePos(n), c.Name)
						return out
					}
					if len(c.Vars) > 0 || len(c.Funcs) > 0 {
						err = fmt.Errorf("%s: %s renders pages, and a group of pages keeps no state of its own: move it into a page", ir.NodePos(n), c.Name)
						return out
					}
					body := substituteParams(deepCloneStmts(c.Body), groupBindings(c, n))
					body = substituteSlots(body, n)
					out = append(out, pages(body, depth+1)...)
					continue
				}
			case *ir.If:
				n.Body, n.Else = pages(n.Body, depth), pages(n.Else, depth)
			case *ir.For:
				n.Body = pages(n.Body, depth)
			}
			out = append(out, s)
		}
		return out
	}
	stacks = func(stmts []ir.Stmt) {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ir.NodeInst:
				if isNavNode(n, ir.BuiltinNavStack) {
					n.Children = pages(n.Children, 0)
					continue
				}
				stacks(n.Children)
				for _, name := range ir.SlotNames(n.Slots) {
					stacks(n.Slots[name].Body)
				}
			case *ir.If:
				stacks(n.Body)
				stacks(n.Else)
			case *ir.For:
				stacks(n.Body)
				stacks(n.Else)
			case *ir.ErrorBoundary:
				stacks(n.Children)
				stacks(n.Failed)
			case *ir.ContextProvider:
				stacks(n.Children)
			}
		}
	}
	for _, o := range ir.Owners(pkg) {
		stacks(o.Stmts())
	}
	if err != nil {
		return err
	}
	// A group nothing instantiates any more is gone with its splice: its body
	// renders pages outside a stack, which no target answers.
	used := map[*ir.Component]bool{}
	_ = ir.Walk(pkg, func(n ir.Node) error {
		if inst, ok := n.(*ir.NodeInst); ok && inst.Component != nil && inst.Component.Group {
			used[inst.Component] = true
		}
		return nil
	})
	pkg.Components = slices.DeleteFunc(pkg.Components, func(c *ir.Component) bool {
		return c != nil && c.Group && !used[c]
	})
	return nil
}

// maxInlineGroupDepth bounds a group that renders a group, which only a group
// rendering itself reaches.
const maxInlineGroupDepth = 16

// groupBindings is what a group's call site binds its props to, by name, and
// each default it leaves standing.
func groupBindings(c *ir.Component, n *ir.NodeInst) map[string]ir.Expr {
	out := map[string]ir.Expr{}
	for _, p := range c.Props {
		if v := n.Prop(p.Name); v != nil {
			out[p.Name] = v
		} else if p.Default != nil {
			out[p.Name] = p.Default
		}
	}
	return out
}

// adoptAnon declares in the package an anonymous struct a record's field is
// typed by: `meta`'s default, `struct {}`, is interned in sngl:ui/nav where
// the family's type parameter defaults to it, and a backend declares the
// program's structs, so a record holding one named a type nothing emitted.
func (nv *navigation) adoptAnon(t *ir.Type) {
	if t == nil {
		return
	}
	if sd, ok := t.Decl.(*ir.StructDef); ok && strings.HasPrefix(sd.Name, "__anon_") && !slices.Contains(nv.pkg.Structs, sd) {
		nv.pkg.Structs = append(nv.pkg.Structs, sd)
		for _, f := range sd.Fields {
			nv.adoptAnon(f.Type)
		}
	}
	for _, e := range t.Elems {
		nv.adoptAnon(e)
	}
}

// knownCopy is the indices a navigator value's copy is spelled with where
// they are known where it is written: a page's handle, its loop's indices,
// and a record literal, its copy's elements.
func (nv *navigation) knownCopy(e ir.Expr) ([]ir.Expr, bool) {
	if pg, ok := nv.resolve(e).(*navPage); ok {
		return pg.copyIndices(), true
	}
	if lit, ok := e.(*ir.StructLit); ok {
		for _, f := range lit.Fields {
			if f.Name == "copy" {
				if l, ok := f.Value.(*ir.ListLit); ok {
					return l.Elems, true
				}
			}
		}
	}
	return nil, false
}

// copiesEqual is the test that two navigator values name one copy: element
// by element against whichever side's indices are known, and through
// `__nav_copy_eq` where neither is.
func (nv *navigation) copiesEqual(a, b ir.Expr) ir.Expr {
	if want, ok := nv.knownCopy(a); ok {
		return copyMatch(nv.copyOfExpr(b), want)
	}
	if want, ok := nv.knownCopy(b); ok {
		return copyMatch(nv.copyOfExpr(a), want)
	}
	return &ir.Call{Type: ir.TypBool, Func: nv.copyEq(), Args: []ir.CallArg{{Value: nv.copyOfExpr(a)}, {Value: nv.copyOfExpr(b)}}}
}

// copyEq is the package's `__nav_copy_eq(a, b list<int>) bool`, made the
// first time two copies neither of which is known are compared.
func (nv *navigation) copyEq() *ir.Func {
	for _, f := range nv.pkg.Funcs {
		if f.Name == "__nav_copy_eq" {
			return f
		}
	}
	lt := ir.ListOf(ir.TypInt)
	a := &ir.Param{Name: "a", Type: lt}
	b := &ir.Param{Name: "b", Type: lt}
	ref := func(p *ir.Param) ir.Expr { return &ir.Ident{Name: p.Name, Type: p.Type, Sym: p} }
	i := &ir.LoopVar{Name: "i", Type: ir.TypInt}
	x := &ir.LoopVar{Name: "x", Type: ir.TypInt}
	no := func() ir.Stmt { return &ir.Return{Value: &ir.Literal{Type: ir.TypBool, Value: "false"}} }
	fn := &ir.Func{Name: "__nav_copy_eq", Params: []*ir.Param{a, b}, Return: ir.TypBool, Purity: ir.PurityPure, Block: []ir.Stmt{
		&ir.If{Cond: &ir.Binary{Type: ir.TypBool, Op: ast.BinNeq, Left: listLength(ref(a)), Right: listLength(ref(b))}, Body: []ir.Stmt{no()}},
		&ir.For{Key: "i", Value: "x", KeySym: i, ValueSym: x, Iter: ref(a), ElemType: ir.TypInt, Body: []ir.Stmt{
			&ir.If{Cond: &ir.Binary{Type: ir.TypBool, Op: ast.BinNeq,
				Left:  &ir.Ident{Name: "x", Type: ir.TypInt, Sym: x},
				Right: &ir.Index{Type: ir.TypInt, Operand: ref(b), Idx: &ir.Ident{Name: "i", Type: ir.TypInt, Sym: i}}}, Body: []ir.Stmt{no()}},
		}},
		&ir.Return{Value: &ir.Literal{Type: ir.TypBool, Value: "true"}},
	}}
	nv.pkg.Funcs = append(nv.pkg.Funcs, fn)
	return fn
}

// loopedStartParams is where a page under loops starts its params cell: the
// params the starting copy is written with, where the stack starts at one of
// this page's copies, and the type's zero otherwise. Each loop's variables
// are read back from the starting record's copy, by index into the loop's
// iterable, so the iterable has to be a list.
func (nv *navigation) loopedStartParams(st *navStack, pg *navPage) (ir.Expr, error) {
	start := nv.startRecord(st)
	copyOf := &ir.Select{Type: ir.ListOf(ir.TypInt), Operand: start, Field: "copy"}
	bound := map[ir.Symbol]ir.Expr{}
	subst := func(e ir.Expr) ir.Expr {
		holder := &ir.Return{Value: ir.CloneExprSharingDecls(e)}
		_ = ir.RewriteExprs(holder, func(x ir.Expr) (ir.Expr, error) {
			if id, ok := x.(*ir.Ident); ok {
				if v, ok := bound[id.Sym]; ok {
					return ir.CloneExprSharingDecls(v), ir.SkipDir
				}
			}
			return x, nil
		})
		return holder.Value
	}
	for k, fs := range pg.loops {
		iter := subst(fs.Iter)
		if conv, ok := iter.(*ir.Conversion); ok {
			iter = conv.Operand
		}
		if t := typeOf(iter); t == nil || t.Kind != ir.TypeList {
			return nil, fmt.Errorf("%s: page %q is written under a `for` over a sequence and takes params, and its params cell starts from the copy the stack starts at, read back by index; iterate a list", ir.StmtPos(fs), pageName(pg))
		}
		idx := &ir.Index{Type: ir.TypInt, Operand: ir.CloneExprSharingDecls(copyOf), Idx: navInt(k)}
		if fs.KeySym != nil {
			bound[fs.KeySym] = idx
		}
		if fs.ValueSym != nil {
			bound[fs.ValueSym] = &ir.Index{Type: fs.ValueSym.Type, Operand: iter, Idx: ir.CloneExprSharingDecls(idx)}
		}
	}
	isThis := &ir.Binary{Type: ir.TypBool, Op: ast.BinEq,
		Left: &ir.Select{Type: ir.TypInt, Operand: ir.CloneExprSharingDecls(start), Field: "id"}, Right: navInt(pg.id)}
	// A function rather than a ternary, so the fold after lowering answers
	// the call as it answers the start's, and neither reaches a target.
	fn := &ir.Func{Name: nv.fresh(pageName(pg) + "__start_params"), Return: pg.ptype, Purity: ir.PurityPure, Block: []ir.Stmt{
		&ir.If{Cond: isThis, Body: []ir.Stmt{&ir.Return{Value: subst(pg.start())}}},
		&ir.Return{Value: ir.ZeroExpr(pg.ptype)},
	}}
	nv.pkg.Funcs = append(nv.pkg.Funcs, fn)
	return &ir.Call{Type: pg.ptype, Func: fn}, nil
}
