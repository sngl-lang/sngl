package checker

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// The identity of a build target is a value of its own type rather than a
// string: `platform` and `language` are declared in lib/builtin and marked
// #[builtin], and each registered target gets one const of that type
// synthesized into its own package. So `html.platform` is the only way to name
// the html target, `PLATFORM == "html"` does not type-check, and a misspelled
// target name is an unresolved selector rather than a branch nobody takes.
//
// The consts are synthesized rather than declared because a plugin that
// declared its own could disagree with the name it registered under, and
// because a target outside this repository then needs to know nothing.

// typeTargetConsts gives PLATFORM and LANGUAGE their types. It runs after the
// whole of lib/builtin is registered: the consts and the types they carry are
// in different files (consts.sngl, types.sngl) and load order is not
// guaranteed, so neither mark can fill the other in as it is applied.
func (c *checker) typeTargetConsts() {
	if c.platformConst != nil && c.platformType != nil {
		c.platformConst.Type = c.platformType.SymType()
	}
	if c.languageConst != nil && c.languageType != nil {
		c.languageConst.Type = c.languageType.SymType()
	}
}

// targetConstName is the predeclared const a target identity is compared
// against — the inverse of the member name a target's identity const carries.
func targetConstName(member string) string {
	if member == "language" {
		return "LANGUAGE"
	}
	return "PLATFORM"
}

// targetTierMember is the identity a target of this kind carries, as it is
// written: `html.platform`, `go.language`.
func targetTierMember(kind ir.BuiltinKind) string {
	if kind == ir.BuiltinLanguage {
		return "language"
	}
	return "platform"
}

// resolveTargetIndex resolves the `[expr]` index on a declaration name to the
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
			c.error(t.Pos, "undefined: %s", t.Name)
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
// -- name one const, so what makes it an identity is checked in one place.
func (c *checker) targetIdentity(pos ast.Pos, sym ir.Symbol, spelling string) (string, ir.BuiltinKind, bool) {
	v, isVar := sym.(*ir.Var)
	if !isVar || !v.IsConst || v.Type == nil || v.Init == nil {
		c.error(pos, "%s is not a target identity", spelling)
		return "", ir.BuiltinNone, false
	}
	k := ir.BuiltinNone
	switch {
	case c.platformType != nil && v.Type.Decl == c.platformType:
		k = ir.BuiltinPlatform
	case c.languageType != nil && v.Type.Decl == c.languageType:
		k = ir.BuiltinLanguage
	default:
		c.error(pos, "%s is a %s, not a target identity", spelling, v.Type)
		return "", ir.BuiltinNone, false
	}
	lit, isLit := v.Init.(*ir.Literal)
	if !isLit {
		return "", ir.BuiltinNone, false
	}
	return lit.Value, k, true
}

// collectUserOverrides merges each `component X[target] { ... }` a program
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
		selection, ok := c.overrideSelection(decl, base)
		if !ok {
			continue
		}
		c.addOverrideBody(decl.Pos, base, kind, plat, ns, local, decl.Body, true, selection)
	}
}

// overrideSelection reads an override's prop list: the props of the
// declaration it overrides that its body consumes. The list is a selection,
// not a declaration -- the base owns the types, and restating one is a thing
// that can drift -- so each entry is a bare name, and `@name` selects an event.
//
// The list is optional. Without one the override reads every prop the base
// declares, which is what an override written before this could do and all a
// platform package's do today.
func (c *checker) overrideSelection(decl *ast.ComponentDecl, base *ir.Component) ([]string, bool) {
	if !decl.HasParens {
		return nil, true
	}
	declared := map[string]bool{}
	for _, p := range base.Props {
		declared[p.Name] = true
	}
	for _, e := range base.Events {
		declared["@"+e.Name] = true
	}
	out := make([]string, 0, len(decl.Props.Props))
	ok := true
	for _, p := range decl.Props.Props {
		var name string
		var pos ast.Pos
		switch pd := p.(type) {
		case ast.Param:
			name, pos = pd.Name, pd.Pos
			if pd.Type != nil || pd.Default != nil {
				c.error(pos, "override %q selects prop %q; its type belongs to the declaration being overridden", decl.Name, name)
				ok = false
				continue
			}
		case ast.EventDecl:
			name, pos = "@"+pd.Name, pd.Pos
			if pd.Type != nil {
				c.error(pos, "override %q selects event %q; its type belongs to the declaration being overridden", decl.Name, name)
				ok = false
				continue
			}
		default:
			continue
		}
		if !declared[name] {
			c.error(pos, "override %q selects %q, which %s does not declare", decl.Name, name, decl.Name)
			ok = false
			continue
		}
		out = append(out, name)
	}
	return out, ok
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
	fn       *ir.Func
	platform string
	kind     ir.BuiltinKind
	decl     *ast.FuncDef
	// libURI is the target package the override was written in, empty for one
	// the program wrote. It says which scope the body is checked in: a
	// package's own source is written against its own imports, none of which
	// the program has.
	libURI string
}

// collectFuncOverrides merges each `func f[target] { ... }` a program declares
// into the function it names.
func (c *checker) collectFuncOverrides() {
	for _, decl := range c.userFuncOverrides {
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
	if len(decl.Params.Params) > 0 {
		c.error(decl.Pos, "override %q may not declare params (inherited from the declaration it overrides)", decl.Name)
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
	overrides := &base.PlatformOverrides
	if kind == ir.BuiltinLanguage {
		overrides = &base.LanguageOverrides
	}
	if *overrides == nil {
		*overrides = map[string]ir.Body{}
	}
	if _, dup := (*overrides)[plat]; dup {
		c.error(decl.Pos, "function %q already has an implementation for %q", decl.Name, plat)
		return
	}
	// Reserve the key so a duplicate is caught even when the body check
	// contributes nothing, as addOverrideBody does for a component.
	(*overrides)[plat] = ir.Body{}
	c.pendingFuncOverrides = append(c.pendingFuncOverrides, pendingFuncOverride{fn: base, platform: plat, kind: kind, decl: decl, libURI: libURI})
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
// The body is checked with the base's AST in place of the override's so the
// params and the return type in scope are the ones the override inherits: the
// override declares neither, and checkFuncBody reads both off the declaration.
func (c *checker) checkPendingFuncOverrides() {
	savedScope := c.scope
	defer func() { c.scope = savedScope }()
	for _, po := range c.pendingFuncOverrides {
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
		saved := po.fn.AST
		savedBlock := po.fn.Block
		if saved != nil {
			swapped := *saved
			swapped.Body = po.decl.Body
			swapped.Block = po.decl.Block
			po.fn.AST = &swapped
		}
		po.fn.Block = nil
		c.checkFuncBody(po.fn)
		if po.kind == ir.BuiltinLanguage {
			po.fn.LanguageOverrides[po.platform] = ir.Body{Stmts: po.fn.Block}
		} else {
			po.fn.PlatformOverrides[po.platform] = ir.Body{Stmts: po.fn.Block}
		}
		po.fn.AST = saved
		po.fn.Block = savedBlock
	}
}
