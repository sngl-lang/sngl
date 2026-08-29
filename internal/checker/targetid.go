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

// synthesizeTargetID declares a target's own identity const into its package,
// so `html.platform` resolves as an ordinary namespace member. name is the
// registered target name; typ is c.platformType or c.languageType, and the
// const takes that type's bare name — `platform` for a platform package,
// `language` for a language one.
//
// This is a file the compiler injects into the package: one declaration nobody
// can write by hand, in a package that otherwise comes off disk. So it binds
// the name the way a file would and reports a collision as a redeclaration,
// rather than asking first whether the name is free — a wildcard answers to
// every name it covers, and html's element wildcard covers `platform`, so
// asking left html with no identity at all.
//
// A target that could not serve a package has none to declare into, so its
// identity does not resolve either: naming an unavailable target is an error
// at the point that names it rather than an override silently registered for a
// platform this build has no vocabulary for.
func (c *checker) synthesizeTargetID(pkg *ir.Package, typ *ir.StructDef, name string) {
	if pkg == nil || pkg.Symbols == nil || typ == nil {
		return
	}
	member := typ.Name
	// Library packages are cached across the checks one build runs, so this
	// package may already hold the const an earlier check injected. Binding
	// that same symbol again is not a redeclaration; binding a second one
	// would be, so the first is reused rather than rebuilt.
	sym := c.libs.targetIDs[pkg]
	if sym == nil {
		sym = &ir.Var{
			Name:        member,
			Type:        typ.SymType(),
			Init:        &ir.Literal{Type: typ.SymType(), Raw: name},
			IsConst:     true,
			Synthesized: true,
			Doc:         "The " + name + " " + member + ", as a value: compare " + targetConstName(member) + " against it.",
		}
		if c.libs.targetIDs == nil {
			c.libs.targetIDs = map[*ir.Package]*ir.Var{}
		}
		c.libs.targetIDs[pkg] = sym
	}
	if err := pkg.Symbols.Root.Declare(sym); err != nil {
		c.error(ast.Pos{}, "package for target %q declares %q, the name of the identity const the compiler injects into it", name, member)
	}
}

// targetConstName is the predeclared const a target identity is compared
// against — the inverse of the member name synthesizeTargetID binds.
func targetConstName(member string) string {
	if member == "language" {
		return "LANGUAGE"
	}
	return "PLATFORM"
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
	sel, isSel := e.(*ast.SelectExpr)
	if !isSel {
		pos := ast.Pos{}
		if e != nil {
			if p := e.ExprPos(); p != nil {
				pos = *p
			}
		}
		c.error(pos, "an override's target is a target's own identity, e.g. html.platform")
		return "", ir.BuiltinNone, false
	}
	operand, isIdent := sel.Operand.(*ast.IdentExpr)
	if !isIdent {
		c.error(sel.Pos, "an override's target is a target's own identity, e.g. html.platform")
		return "", ir.BuiltinNone, false
	}
	sym, found := c.scope.Lookup(operand.Name)
	if !found {
		c.error(sel.Pos, "undefined: %s", operand.Name)
		return "", ir.BuiltinNone, false
	}
	ns, isNS := sym.(*ir.Namespace)
	if !isNS || ns.Pkg == nil || ns.Pkg.Symbols == nil {
		c.error(sel.Pos, "%s is not a target", operand.Name)
		return "", ir.BuiltinNone, false
	}
	member, found := ns.Pkg.Symbols.LookupMember(sel.Field)
	if !found {
		c.error(sel.Pos, "undefined: %s.%s", operand.Name, sel.Field)
		return "", ir.BuiltinNone, false
	}
	v, isVar := member.(*ir.Var)
	if !isVar || !v.IsConst || v.Type == nil || v.Init == nil {
		c.error(sel.Pos, "%s.%s is not a target identity", operand.Name, sel.Field)
		return "", ir.BuiltinNone, false
	}
	k := ir.BuiltinNone
	switch {
	case c.platformType != nil && v.Type.Decl == c.platformType:
		k = ir.BuiltinPlatform
	case c.languageType != nil && v.Type.Decl == c.languageType:
		k = ir.BuiltinLanguage
	default:
		c.error(sel.Pos, "%s.%s is a %s, not a target identity", operand.Name, sel.Field, v.Type)
		return "", ir.BuiltinNone, false
	}
	lit, isLit := v.Init.(*ir.Literal)
	if !isLit {
		return "", ir.BuiltinNone, false
	}
	return lit.Raw, k, true
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
	for _, decl := range c.userOverrides {
		plat, kind, ok := c.resolveTargetIndex(decl.Target)
		if !ok {
			continue
		}
		if kind != ir.BuiltinPlatform {
			// A language override has nowhere to be stored: a component's
			// bodies are keyed by platform. Refused where it is written rather
			// than registered under a key nothing reads.
			c.error(decl.Pos, "a component may be overridden for a platform, not for a language")
			continue
		}
		if len(decl.Props.Props) > 0 {
			pos := decl.Pos
			switch p := decl.Props.Props[0].(type) {
			case ast.Param:
				pos = p.Pos
			case ast.EventDecl:
				pos = p.Pos
			}
			c.error(pos, "override %q may not declare props (inherited from the declaration it overrides)", decl.Name)
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
		c.addPlatformBody(decl.Pos, base, plat, ns, local, decl.Body, true)
	}
}

// overrideBase resolves the declaration an override names, qualified
// (`sngl.button`, another package's) or not (`Avatar`, this program's).
func (c *checker) overrideBase(decl *ast.ComponentDecl) (base *ir.Component, ns, local string) {
	dot := strings.IndexByte(decl.Name, '.')
	if dot <= 0 {
		sym, found := c.symtab.LookupComponent(decl.Name)
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
		c.error(decl.Pos, "override namespace %q is not an imported library package; import it, e.g. import %s %q", ns, ns, "sngl://std")
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
	sym, found := nsDecl.Pkg.Symbols.LookupComponent(local)
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
	decl     *ast.FuncDef
}

// collectFuncOverrides merges each `func f[target] { ... }` a program declares
// into the function it names. The body is checked against the *base*
// declaration's signature -- an override inherits the params and the return
// type, and declaring either is what it means to declare a different function.
func (c *checker) collectFuncOverrides() {
	for _, decl := range c.userFuncOverrides {
		plat, kind, ok := c.resolveTargetIndex(decl.Target)
		if !ok {
			continue
		}
		if kind != ir.BuiltinPlatform {
			c.error(decl.Pos, "a function may be overridden for a platform, not for a language")
			continue
		}
		if len(decl.Params.Params) > 0 {
			c.error(decl.Pos, "override %q may not declare params (inherited from the declaration it overrides)", decl.Name)
			continue
		}
		if decl.ReturnType != nil {
			c.error(decl.Pos, "override %q may not declare a return type (inherited from the declaration it overrides)", decl.Name)
			continue
		}
		base := c.funcOverrideBase(decl)
		if base == nil {
			continue
		}
		if base.PlatformBodies == nil {
			base.PlatformBodies = map[string][]ir.Stmt{}
		}
		if _, dup := base.PlatformBodies[plat]; dup {
			c.error(decl.Pos, "function %q already has an implementation for %q", decl.Name, plat)
			continue
		}
		// Reserve the key so a duplicate is caught even when the body check
		// contributes nothing, as addPlatformBody does for a component.
		base.PlatformBodies[plat] = nil
		c.pendingFuncOverrides = append(c.pendingFuncOverrides, pendingFuncOverride{fn: base, platform: plat, decl: decl})
	}
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
	for _, po := range c.pendingFuncOverrides {
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
		po.fn.PlatformBodies[po.platform] = po.fn.Block
		po.fn.AST = saved
		po.fn.Block = savedBlock
	}
}
