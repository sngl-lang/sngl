package checker

import (
	"cmp"
	"maps"
	"slices"
	"strconv"
	"strings"

	"duckfam.us/sngl/ast"
	"duckfam.us/sngl/ir"
)

// A build target is named by its node: the `component platform(…)
// build.platform` or `component language(…) build.language` its own package
// declares, carrying `#[gen.name]`. Inside that package it is `platform`,
// and a program names it through the package, `html.platform`, the way it
// names any other declaration there. Nothing is synthesized: the declaration
// that is the target's option schema and carries its `#[gen]` marks is also
// its identity, so there is no second one to disagree with it.
//
// As a value it is the identity PLATFORM and LANGUAGE are compared against:
// its type is sngl:build's `platform` or `language` family (targetValueType), so
// `PLATFORM == "html"` still does not type-check, and a misspelled target is
// an unresolved name rather than a branch nobody takes.

// targetValueType is the type a reference to a build-target node has where it
// is read as a value: the family it is a member of, sngl:build's `platform` or
// `language`, which PLATFORM and LANGUAGE are declared as. Nil for any other
// symbol.
func (c *checker) targetValueType(sym ir.Symbol) *ir.Type {
	node, _, ok := ir.TargetNode(sym)
	if !ok || node.Family == nil {
		return nil
	}
	return node.Family.SymType()
}

// targetTierMember is the word for a tier: the segment of a target package's
// URI (`sngl:platform/html`), and what a message calls one. Its node is
// conventionally named the same, but is found by its family (ir.TargetNodeOf).
func targetTierMember(kind ir.BuiltinKind) string {
	if kind == ir.BuiltinFamilyLanguage {
		return "language"
	}
	return "platform"
}

// resolveTargetIndex resolves the `[target]` index on a declaration name to the
// target it implements: the registered name, and whether the identity is a
// platform or a language.
//
// The index is resolved rather than read as text, so it can only name a target
// whose package this build actually loaded. A misspelling is an unresolved
// name, and a value of any other type -- `html.color`, a string -- is refused
// where it is written rather than becoming an override nothing ever selects.
func (c *checker) resolveTargetIndex(e ast.Expr) (name string, kind ir.BuiltinKind, ok bool) {
	switch t := e.(type) {
	case *ast.IdentExpr:
		// A target's own package writes its identity unqualified: inside
		// android.sngl the const is `platform`, and naming it `android.platform`
		// there says the package's name twice. The qualified form is what a
		// program outside the package writes.
		sym, found := c.scope.Lookup(t.Name)
		if !found {
			// A bracket holding one bare name is a target, so a type
			// parameter written with no constraint reads as one, and an
			// undefined one: say what the author more likely meant.
			c.error(t.Pos, "undefined: %s (a type parameter names its constraint: write `%s any`)", t.Name, t.Name)
			return "", ir.BuiltinNone, false
		}
		return c.targetIdentity(t.Pos, sym, t.Name)
	case *ast.SelectExpr:
		operand, isIdent := t.Operand.(*ast.IdentExpr)
		if !isIdent {
			c.error(t.Pos, "an override's target is a target's own identity, e.g. html.platform")
			return "", ir.BuiltinNone, false
		}
		sym, found := c.scope.Lookup(operand.Name)
		if !found {
			c.error(t.Pos, "undefined: %s", operand.Name)
			return "", ir.BuiltinNone, false
		}
		ns, isNS := sym.(*ir.Namespace)
		if !isNS || ns.Pkg == nil || ns.Pkg.Symbols == nil {
			c.error(t.Pos, "%s is not a target", operand.Name)
			return "", ir.BuiltinNone, false
		}
		member, found := ns.Pkg.Symbols.LookupMember(t.Field)
		if !found {
			c.error(t.Pos, "undefined: %s.%s", operand.Name, t.Field)
			return "", ir.BuiltinNone, false
		}
		return c.targetIdentity(t.Pos, member, operand.Name+"."+t.Field)
	default:
		pos := ast.Pos{}
		if e != nil {
			if p := e.ExprPos(); p != nil {
				pos = *p
			}
		}
		c.error(pos, "an override's target is a target's own identity, e.g. html.platform")
		return "", ir.BuiltinNone, false
	}
}

// targetIdentity reads the target a symbol identifies. Both spellings of the
// index -- the package's own `platform` and another package's `html.platform`
// -- name one build-target node, so what makes it an identity is checked in one
// place.
func (c *checker) targetIdentity(pos ast.Pos, sym ir.Symbol, spelling string) (string, ir.BuiltinKind, bool) {
	node, name, ok := ir.TargetNode(sym)
	if !ok {
		c.error(pos, "%s is not a target identity: an override names a target's build node, e.g. html.platform", spelling)
		return "", ir.BuiltinNone, false
	}
	return name, ir.TargetTier(node.Family), true
}

// collectUserOverrides merges each `component X[target](…) { ... }` a program
// declares into the component it names, the way a platform package's overrides
// are merged. It runs after pass1: an override may be written above the
// declaration it overrides.
//
// A program overrides a library component (`component sngl.button[...]`) or
// one of its own (`component Avatar[...]`) by the same rule -- the name says
// which, and the index says for which target.
func (c *checker) collectUserOverrides() {
	defer c.saveFile()()
	for _, decl := range c.userOverrides {
		c.enterFileOf(decl.Pos)
		plat, kind, ok := c.resolveTargetIndex(decl.Target)
		if !ok {
			continue
		}
		if decl.ChildrenType != nil {
			c.error(decl.Pos, "override %q may not declare children type (inherited from the declaration it overrides)", decl.Name)
			continue
		}
		base, ns, local := c.overrideBase(decl)
		if base == nil {
			continue
		}
		selection := c.overrideSelection(decl, base)
		c.addOverrideBody(decl.Pos, base, kind, plat, ns, local, decl.Body, true, selection, decl.Const)
	}
}

// overrideSelection reads an override's parameter list: the props, slots and
// events of the declaration it overrides that its body reads. The list is a
// selection, not a declaration -- the base owns each type, so an entry names
// one of the base's parameters and may restate its type only where it
// matches (selectEntry). `@name` selects an event.
//
// The list is required, `()` where the body reads nothing: it is what makes
// every name in the body traceable to the declaration, rather than appearing
// from it unannounced.
func (c *checker) overrideSelection(decl *ast.ComponentDecl, base *ir.Component) []string {
	if !decl.HasParens {
		// Reported, and checked as though every parameter were listed, so the
		// one thing wrong is the one thing said: dropped, the override left
		// the base reported as bodyless and its body unchecked.
		c.error(decl.Pos, "override %q must list the parameters its body reads, `()` for none: component %s[…](name, …)", decl.Name, decl.Name)
		all := make([]string, 0, len(base.Props)+len(base.Slots)+len(base.Events))
		for _, p := range base.Props {
			all = append(all, p.Name)
		}
		for _, s := range base.Slots {
			all = append(all, s.Name)
		}
		for _, e := range base.Events {
			all = append(all, "@"+e.Name)
		}
		return all
	}
	props := map[string]*ir.Prop{}
	for _, p := range base.Props {
		props[p.Name] = p
	}
	slots := map[string]bool{}
	for _, s := range base.Slots {
		slots[s.Name] = true
	}
	events := map[string]bool{}
	for _, e := range base.Events {
		events[e.Name] = true
	}
	out := make([]string, 0, len(decl.Props.Props))
	seen := map[string]bool{}
	for _, p := range decl.Props.Props {
		switch pd := p.(type) {
		case ast.Param:
			var want *ir.Type
			found := false
			switch prop := props[pd.Name]; {
			case prop != nil:
				want, found = prop.Type, true
				if pd.Bidirectional && !prop.Bidirectional {
					c.error(pd.Pos, "override %q selects %q as two-way, but %s declares it one-way", decl.Name, pd.Name, decl.Name)
				}
			case slots[pd.Name]:
				found = true
				if pd.Type != nil {
					c.error(pd.Pos, "override %q selects slot %q; a slot's type belongs to the declaration, so write its name alone", decl.Name, pd.Name)
				}
				pd.Type = nil
			}
			if !c.selectEntry(decl.Name, pd, want, found, seen) {
				continue
			}
			out = append(out, pd.Name)
		case ast.EventDecl:
			name := "@" + pd.Name
			if len(pd.Params.Params) > 0 || pd.HasParens {
				c.error(pd.Pos, "override %q selects event %q; its parameters belong to the declaration, so write its name alone", decl.Name, name)
				continue
			}
			if c.selectEntry(decl.Name, ast.Param{Pos: pd.Pos, Name: name}, nil, events[pd.Name], seen) {
				out = append(out, name)
			}
		}
	}
	return out
}

// selectEntry holds one entry of an override's parameter list to the
// declaration it overrides, for a component's and a function's alike: it
// names a parameter the declaration has, once, and restates nothing but a
// matching type. want is that parameter's type and found whether there is
// one; seen collects the names already selected.
func (c *checker) selectEntry(over string, p ast.Param, want *ir.Type, found bool, seen map[string]bool) bool {
	switch {
	case !found:
		c.error(p.Pos, "override %q selects %q, which %s does not declare", over, p.Name, over)
		return false
	case seen[p.Name]:
		c.error(p.Pos, "override %q selects %q twice", over, p.Name)
		return false
	case p.Default != nil:
		c.error(p.Pos, "override %q selects %q, which takes no default here: the declaration's is the one a call omitting it gets", over, p.Name)
		return false
	case p.Const:
		c.error(p.Pos, "override %q selects %q, which takes no `const` here: whether it is constant is the declaration's to say", over, p.Name)
		return false
	case len(p.Attrs) > 0:
		c.error(p.Pos, "override %q selects %q, which takes no mark here: its marks are the declaration's", over, p.Name)
		return false
	}
	seen[p.Name] = true
	if p.Type != nil {
		c.bindParamType(p.Type, want, bindParamWhat(p.Name, "override "+strconv.Quote(over)))
	}
	return true
}

// selectFuncParams reads a function override's parameter list, the same
// selection a component override's is: each entry names a parameter of the
// function it overrides, and those are the only ones its body reads. The
// parameters themselves stay the declaration's, in its order, since that is
// what every call passes. An entry it refuses is reported and left out, so
// the body is still checked.
func (c *checker) selectFuncParams(decl *ast.FuncDef, base *ir.Func) []*ir.Param {
	byName := map[string]*ir.Param{}
	for _, p := range base.Params {
		byName[p.Name] = p
	}
	seen := map[string]bool{}
	for _, p := range decl.Params.Params {
		bp := byName[p.Name]
		var want *ir.Type
		if bp != nil {
			want = bp.Type
		}
		c.selectEntry(decl.Name, p, want, bp != nil, seen)
	}
	var out []*ir.Param
	for _, p := range base.Params {
		// The receiver a method on a generic built-in carries as a parameter
		// is no parameter a call writes, and is read as the method's own.
		if seen[p.Name] || p.Name == ir.ReceiverParam {
			out = append(out, p)
		}
	}
	return out
}

// overrideBase resolves the declaration an override names, qualified
// (`sngl.button`, another package's) or not (`Avatar`, this program's).
func (c *checker) overrideBase(decl *ast.ComponentDecl) (base *ir.Component, ns, local string) {
	dot := strings.IndexByte(decl.Name, '.')
	if dot <= 0 {
		sym, found := c.symtab.LookupRootComponent(decl.Name)
		if !found {
			c.error(decl.Pos, "override names unknown component %q", decl.Name)
			return nil, "", ""
		}
		comp, isComp := sym.(*ir.Component)
		if !isComp {
			return nil, "", ""
		}
		return comp, "", decl.Name
	}
	ns, local = decl.Name[:dot], decl.Name[dot+1:]
	if !c.isLibraryNamespace(ns) {
		c.error(decl.Pos, "override namespace %q is not an imported library package; import it, e.g. import %s %q", ns, ns, "sngl:ui")
		return nil, "", ""
	}
	// The package the namespace binds, not the user symtab: a library package
	// reaches user scope only through an import, but an override targets the
	// declaration either way -- and it must be the same *ir.Component instance
	// the rest of the build holds, or the body lands on a copy nothing renders.
	nsSym, _ := c.scope.Lookup(ns)
	nsDecl, _ := nsSym.(*ir.Namespace)
	if nsDecl == nil || nsDecl.Pkg == nil {
		return nil, "", ""
	}
	sym, found := nsDecl.Pkg.Symbols.LookupRootComponent(local)
	if !found {
		c.error(decl.Pos, "override %q references unknown component %q in %s", decl.Name, local, ns)
		return nil, "", ""
	}
	comp, isComp := sym.(*ir.Component)
	if !isComp {
		return nil, "", ""
	}
	return comp, ns, local
}

// pendingFuncOverride is one `func f[target] { ... }` body, checked after
// pass1 the way a component override's is and for the same reason: it may be
// written above the declaration it overrides.
type pendingFuncOverride struct {
	fn *ir.Func
	// params are the parameters of fn its override listed, and so the only
	// ones in scope for its body.
	params   []*ir.Param
	platform string
	kind     ir.BuiltinKind
	decl     *ast.FuncDef
	// libURI is the target package the override was written in, empty for one
	// the program wrote. It says which scope the body is checked in: a
	// package's own source is written against its own imports, none of which
	// the program has.
	libURI string
	// pkg is the package a program's override is written in, nil for a
	// target package's.
	pkg *ir.Package
}

// collectFuncOverrides merges each `func f[target] { ... }` a program declares
// into the function it names.
//
// Each in the file it was written in, whose imports its target names: read in
// whichever file pass1 ended on, `[html.platform]` was undefined unless that
// file happened to import html too.
func (c *checker) collectFuncOverrides() {
	defer c.saveFile()()
	for _, decl := range c.userFuncOverrides {
		c.enterFileOf(decl.Pos)
		c.mergeFuncOverride(decl, nil, "")
	}
}

// mergeFuncOverride records one `func f[target] { ... }` as the body target
// implements f with. The body is checked against the *base* declaration's
// signature -- an override inherits the params and the return type, and
// declaring either is what it means to declare a different function.
//
// base is the declaration to override when the caller resolved it already: a
// target package's own source resolves it against its own imports, which is
// where the name it overrides comes from. nil resolves it here, in the
// program's scope, which is where a program's own override is written.
//
// libURI names the target package an override was written in, empty for a
// program's. Two things follow from it: the override may only implement that
// package's own target, and its body is checked in that package's scope.
func (c *checker) mergeFuncOverride(decl *ast.FuncDef, base *ir.Func, libURI string) {
	plat, kind, ok := c.resolveTargetIndex(decl.Target)
	if !ok {
		return
	}
	if owner, tier, isTarget := targetTierName(libURI); isTarget && (kind != tier || plat != owner) {
		// As for a component override: an override for another target would
		// only merge when this package loads, which is when that target is not
		// the one being built.
		c.error(decl.Pos, "package for %q may not declare an override for %q", owner, plat)
		return
	}
	if decl.ReturnType != nil {
		c.error(decl.Pos, "override %q may not declare a return type (inherited from the declaration it overrides)", decl.Name)
		return
	}
	if base == nil {
		if base = c.funcOverrideBase(decl); base == nil {
			return
		}
	}
	// No target emits a native, so a body overriding one would be checked and
	// then never run.
	if base.Foreign.Name != "" && !base.Foreign.Marked {
		c.error(decl.Pos, "%q is declared outside SNGL, so no target emits it and an override would never run", decl.Name)
		return
	}
	overrides := c.overrideMap(base, libURI == "", kind)
	if *overrides == nil {
		*overrides = map[string]ir.Override{}
	}
	all := c.overridesOf(base)
	if _, dup := (*all.Of(kind))[plat]; dup {
		c.error(decl.Pos, "function %q already has an implementation for %q", decl.Name, plat)
		return
	}
	// Reserve the key so a duplicate is caught even when the body check
	// contributes nothing, as addOverrideBody does for a component.
	(*overrides)[plat] = ir.Override{}
	po := pendingFuncOverride{fn: base, params: c.selectFuncParams(decl, base), platform: plat, kind: kind, decl: decl, libURI: libURI}
	if libURI == "" {
		po.pkg = c.declPkg()
	}
	c.pendingFuncOverrides = append(c.pendingFuncOverrides, po)
}

// libFuncOverrideBase resolves the function a target package's own override
// names. The prefix is whatever alias that document imported the package
// under, as a component override's is -- a target package is not registered
// into the checker's scope, so its imports are the only thing that says what
// `http.get` means. Anything else (an unqualified name, a method on a type)
// falls back to the ordinary resolution, which reads the scope this merge runs
// in and has the package's own declarations in it.
func (c *checker) libFuncOverrideBase(decl *ast.FuncDef, aliases map[string]string) *ir.Func {
	dot := strings.IndexByte(decl.Name, '.')
	if dot <= 0 {
		return c.funcOverrideBase(decl)
	}
	pkgName, ok := aliases[decl.Name[:dot]]
	if !ok {
		return c.funcOverrideBase(decl)
	}
	local := decl.Name[dot+1:]
	pkg := c.libPkg(pkgName)
	if pkg == nil || pkg.Symbols == nil {
		return nil
	}
	member, found := pkg.Symbols.LookupMember(local)
	if !found {
		c.error(decl.Pos, "override %q references unknown function %q in sngl:%s", decl.Name, local, pkgName)
		return nil
	}
	fn, isFunc := member.(*ir.Func)
	if !isFunc {
		c.error(decl.Pos, "%s is not a function", decl.Name)
		return nil
	}
	return fn
}

// funcOverrideBase resolves the function an override names: one this program
// declares, one a library package declares (`i18n.tr`), or a method on a type
// (`int.max`) -- the three ways a function has a name.
func (c *checker) funcOverrideBase(decl *ast.FuncDef) *ir.Func {
	dot := strings.IndexByte(decl.Name, '.')
	if dot <= 0 {
		sym, found := c.scope.Lookup(decl.Name)
		if !found {
			c.error(decl.Pos, "override names unknown function %q", decl.Name)
			return nil
		}
		fn, isFunc := sym.(*ir.Func)
		if !isFunc {
			c.error(decl.Pos, "%q is not a function", decl.Name)
			return nil
		}
		return fn
	}
	prefix, local := decl.Name[:dot], decl.Name[dot+1:]
	if sym, found := c.scope.Lookup(prefix); found {
		if ns, isNS := sym.(*ir.Namespace); isNS && ns.Pkg != nil && ns.Pkg.Symbols != nil {
			member, found := ns.Pkg.Symbols.LookupMember(local)
			if !found {
				c.error(decl.Pos, "override %q references unknown function %q in %s", decl.Name, local, prefix)
				return nil
			}
			fn, isFunc := member.(*ir.Func)
			if !isFunc {
				c.error(decl.Pos, "%s.%s is not a function", prefix, local)
				return nil
			}
			return fn
		}
	}
	// Not a namespace: a receiver, so the name is a method on that type.
	fn, found := c.lookupMethod(prefix, local)
	if !found {
		c.error(decl.Pos, "override names unknown function %q", decl.Name)
		return nil
	}
	return fn
}

// checkPendingFuncOverrides checks each override body against the signature of
// the function it overrides, and stashes the result under its platform.
//
// The body is checked on a copy of the base carrying the override's body, so
// the params and the return type in scope are the ones the override inherits:
// the override declares neither, and checkFuncBody reads both off the
// declaration.
func (c *checker) checkPendingFuncOverrides() {
	c.checkFuncOverrides(c.pendingFuncOverrides)
}

// checkFuncOverrides checks each of pending as checkPendingFuncOverrides
// describes.
func (c *checker) checkFuncOverrides(pending []pendingFuncOverride) {
	savedScope := c.scope
	defer func() { c.scope = savedScope }()
	for _, po := range pending {
		// A target package's own override is compiler-internal source: it
		// resolves in that package, chained to the library scope it is written
		// against, and not where a program's declarations are visible. A
		// program's own resolves where it was written.
		c.scope = savedScope
		if po.libURI != "" {
			c.scope = c.stdlibPkg.Symbols.Root
			if ps := c.targetPkgScope(po.libURI); ps != nil {
				ps.Parent = c.scope
				c.scope = ps
			}
		}
		// On a copy, as a component override is, so the declaration is not
		// written.
		ov := new(ir.Func)
		*ov = *po.fn
		if po.fn.AST != nil {
			swapped := *po.fn.AST
			swapped.Body = po.decl.Body
			swapped.Block = po.decl.Block
			ov.AST = &swapped
		}
		ov.Block = nil
		ov.Params = po.params
		c.checkFuncBody(ov)
		checked := ir.Override{Stmts: ov.Block, Pkg: po.pkg}
		if po.fn.RecvParam == nil {
			checked.RecvParam = ov.RecvParam
		}
		(*c.overrideMap(po.fn, po.libURI == "", po.kind))[po.platform] = checked
	}
}

// reportTargetNames holds every build-target node this check can see to a
// name no other node of its tier carries, and a target package's node to its
// package's own name.
//
// The name is what the command line, the Go registry and an output block look
// a target up by, so two nodes answering to one would make `--platform html`
// mean whichever was found first -- and a package's node answering to another
// package's name is found by nothing, since the lookup goes through the
// package the name says. The tier is part of the key: `none` is both a
// language and a platform, and each is found in its own tier.
//
// Last, like the bodyless sweep, because a library package may be loaded by
// anything in the check, and the answer must not depend on which came first:
// the nodes are sorted by package and position, library packages ahead of
// the program, and every one after the first of a name is reported, naming
// that first.
func (c *checker) reportTargetNames() {
	var nodes []*ir.Component
	collect := func(pkg *ir.Package) {
		if pkg == nil {
			return
		}
		for _, comp := range pkg.Components {
			if _, _, ok := ir.TargetNode(comp); ok && comp.AST != nil {
				nodes = append(nodes, comp)
			}
		}
	}
	collect(c.pkg)
	if c.libs != nil {
		for _, path := range slices.Sorted(maps.Keys(c.libs.pkgs)) {
			collect(c.libs.pkgs[path])
		}
	}
	// A library's nodes first: a program's node that takes a shipped
	// target's name is the one that has to change, so it is the one reported.
	slices.SortStableFunc(nodes, func(a, b *ir.Component) int {
		if (a.PkgPath() == "") != (b.PkgPath() == "") {
			if a.PkgPath() == "" {
				return 1
			}
			return -1
		}
		if n := cmp.Compare(a.PkgPath(), b.PkgPath()); n != 0 {
			return n
		}
		if n := cmp.Compare(a.AST.Pos.File, b.AST.Pos.File); n != 0 {
			return n
		}
		if n := cmp.Compare(a.AST.Pos.Line, b.AST.Pos.Line); n != 0 {
			return n
		}
		return cmp.Compare(a.AST.Pos.Column, b.AST.Pos.Column)
	})
	first := map[string]*ir.Component{}
	for _, n := range nodes {
		tierKind, name := ir.TargetTier(n.Family), n.Gen.TargetName
		tier := targetTierMember(tierKind)
		if owner, kind, ok := targetTierName(strings.TrimPrefix(n.PkgPath(), "sngl:")); ok &&
			kind == tierKind && owner != name {
			c.error(n.AST.Pos, "%s names its %s %q: a target package's node carries the package's own name, %q", n.PkgPath(), tier, name, owner)
			continue
		}
		key := tier + "/" + name
		if prev, dup := first[key]; dup {
			c.error(n.AST.Pos, "%s %q is already declared at %s: a target's name is how the build finds it, so no two %ss share one",
				tier, name, prev.AST.Pos, tier)
			continue
		}
		first[key] = n
	}
}

// reportEmitterPlacement holds gen.emit and gen.node to the one place each is
// read: the whole body of an override, of a family for gen.emit and of a
// member for gen.node. The emitter pass reads them there and nowhere else, so
// one written in a window, a component body or beside other statements would
// be checked, lowered and then generate nothing.
//
// A family's override is a gen.emit and nothing more, for the same reason: it
// is never rendered, so anything beside the gen.emit is read by nobody.
func (c *checker) reportEmitterPlacement() {
	if c.pkg == nil {
		return
	}
	allowed := map[*ir.NodeInst]bool{}
	var bodies [][]ir.Stmt
	place := func(comp *ir.Component, target string, body ir.Override, at *ast.Pos) {
		want := ir.BuiltinGenNode
		if comp.IsFamily() {
			want = ir.BuiltinGenEmit
		}
		if ni := soleEmitterNode(body.Body, want); ni != nil {
			allowed[ni] = true
		} else if at != nil && comp.IsFamily() {
			c.error(*at, "the %s family's override for %s is a gen.emit and nothing else: a family is never rendered, so nothing else in it would be read", comp.Name, target)
		}
		bodies = append(bodies, body.Stmts)
	}
	tiers := []ir.BuiltinKind{ir.BuiltinFamilyPlatform, ir.BuiltinFamilyLanguage}
	for _, comp := range c.pkg.Components {
		bodies = append(bodies, comp.Stmts)
		var at *ast.Pos
		if comp.AST != nil {
			at = &comp.AST.Pos
		}
		for _, kind := range tiers {
			overrides := *ir.OverridesOf(comp).Of(kind)
			for _, target := range slices.Sorted(maps.Keys(overrides)) {
				place(comp, target, overrides[target], at)
			}
		}
	}
	// An override the program wrote of another package's component is the
	// program's to answer for; one an import added was its own package's.
	for _, sym := range bySymName(maps.Keys(c.pkg.Overlay.Overrides)) {
		comp, ok := sym.(*ir.Component)
		if !ok {
			continue
		}
		for _, kind := range tiers {
			overrides := *c.pkg.Overlay.Overrides[sym].Of(kind)
			for _, target := range slices.Sorted(maps.Keys(overrides)) {
				if body := overrides[target]; body.Pkg == c.declPkg() {
					at := c.addedOverrideAt[addedOverride{comp, kind, target}]
					place(comp, target, body, &at)
				}
			}
		}
	}
	for _, owner := range ir.Owners(c.pkg) {
		bodies = append(bodies, owner.Stmts())
	}
	seen := map[*ir.NodeInst]bool{}
	for _, b := range bodies {
		for _, st := range b {
			c.reportMisplacedEmitters(st, allowed, seen)
		}
	}
}

type addedOverride struct {
	decl   ir.Symbol
	kind   ir.BuiltinKind
	target string
}

func (c *checker) reportMisplacedEmitters(st ir.Stmt, allowed, seen map[*ir.NodeInst]bool) {
	{
		ir.Walk(st, func(n ir.Node) error { //nolint:errcheck // the visit never fails
			ni, ok := n.(*ir.NodeInst)
			if !ok || ni.Component == nil || seen[ni] {
				return nil
			}
			kind := ni.Component.Builtin
			if !kind.IsCommand() && (!kind.IsEmitter() || allowed[ni]) {
				return nil
			}
			seen[ni] = true
			var at ast.Pos
			if sp := stmtPos(ni.AST); sp != nil {
				at = *sp
			}
			// A command is the target package's: a program is never asked
			// one, so it would be read by nobody, and a repository choosing
			// what `sngl build` runs is what trust exists to stop.
			if kind.IsCommand() {
				c.error(at, "gen.%s is written in the body of a target package's build-tree node, where the command reads it, and not in a program", ni.Component.Name)
				return nil
			}
			what := "a member's override for the target whose family emits"
			if ni.Component.Builtin == ir.BuiltinGenEmit {
				what = "a family's override"
			}
			c.error(at, "gen.%s is written as the whole body of %s, where the build reads it", ni.Component.Name, what)
			return nil
		})
	}
}

// soleEmitterNode is the one statement of an override body, when it is a gen
// node of the given kind.
func soleEmitterNode(body ir.Body, kind ir.BuiltinKind) *ir.NodeInst {
	if len(body.Stmts) != 1 {
		return nil
	}
	ni, ok := body.Stmts[0].(*ir.NodeInst)
	if !ok || ni.Component == nil || ni.Component.Builtin != kind {
		return nil
	}
	return ni
}

// overrideMap is where an override of decl for the tier kind is recorded: on
// decl when its own package writes it, and otherwise in the overlay of the
// package that writes it -- the program's, or a library package's -- since
// the declaration belongs to another package, which every check shares.
func (c *checker) overrideMap(decl ir.Symbol, user bool, kind ir.BuiltinKind) *map[string]ir.Override {
	if writer := c.overrideWriter(user); writer != nil {
		if owner := ir.DeclaringPackage(decl); owner != nil && owner != writer {
			return writer.Overlay.For(decl).Of(kind)
		}
	}
	ir.MustWriteDecl(decl, "an override of "+decl.SymName())
	switch d := decl.(type) {
	case *ir.Component:
		if kind == ir.BuiltinFamilyLanguage {
			return &d.LanguageOverrides
		}
		return &d.PlatformOverrides
	case *ir.Func:
		if kind == ir.BuiltinFamilyLanguage {
			return &d.LanguageOverrides
		}
		return &d.PlatformOverrides
	}
	return nil
}

// overrideWriter is the package an override being recorded belongs to: the
// program for a user's, else the target package being merged or the library
// package loading.
func (c *checker) overrideWriter(user bool) *ir.Package {
	switch {
	case user:
		return c.declPkg()
	case c.overrideOwner != nil:
		return c.overrideOwner
	}
	return c.libLoadPkg
}

// overridesOf is every override decl has: its own, the library's, the
// package being merged's and the program's.
func (c *checker) overridesOf(decl ir.Symbol) ir.Overrides {
	all := *ir.OverridesOf(decl)
	for _, o := range []ir.Overlay{c.libs.targets, c.overrideOwnerOverlay(), c.pkg.Overlay} {
		if added := o.Overrides[decl]; added != nil {
			all = ir.Overrides{Platform: mergedOverrides(all.Platform, added.Platform), Language: mergedOverrides(all.Language, added.Language)}
		}
	}
	return all
}

func (c *checker) overrideOwnerOverlay() ir.Overlay {
	if w := c.overrideWriter(false); w != nil {
		return w.Overlay
	}
	return ir.Overlay{}
}

func mergedOverrides(own, added map[string]ir.Override) map[string]ir.Override {
	if len(added) == 0 {
		return own
	}
	out := maps.Clone(own)
	if out == nil {
		out = make(map[string]ir.Override, len(added))
	}
	maps.Copy(out, added)
	return out
}
