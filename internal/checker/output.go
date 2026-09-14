package checker

import (
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// buildPkg declares the two tree kinds a build directive is written from:
// `output` hosts `build.language` members and a language node hosts
// `build.platform` ones.
const buildPkg = "build"

// buildTrees are the tree structs `sngl:build` declares, resolved once.
// Membership is compared against these declarations, so a package declaring its
// own `struct language` declares a different tree and its nodes do not pass.
func (c *checker) buildTrees() (lang, platform *ir.StructDef) {
	if c.buildTreesSet {
		return c.langTree, c.platformTree
	}
	c.buildTreesSet = true
	pkg := c.libPkgOutsideDirective(buildPkg)
	if pkg == nil {
		return nil, nil
	}
	for _, sd := range pkg.Structs {
		switch sd.Name {
		case "language":
			c.langTree = sd
		case "platform":
			c.platformTree = sd
		}
	}
	return c.langTree, c.platformTree
}

// targetNode is the build-directive node a target package declares under name.
//
// The tier this level of the tree accepts is tried first and the other second,
// so a language written where a platform belongs resolves to the language and
// is reported as a tree-membership error rather than as an unresolved name.
// The order matters because the two tiers share a namespace: `none` is both a
// language and a platform, and which one a bare `none` means is which level it
// is written at.
//
// The package is loaded on demand rather than read out of what a build already
// loaded: which targets load is decided by the caller's selection, and a
// directive names targets that selection does not.
func (c *checker) targetNode(name string, depth int) *ir.Component {
	tiers := [2]ir.BuiltinKind{ir.BuiltinLanguage, ir.BuiltinPlatform}
	if depth != 1 {
		tiers = [2]ir.BuiltinKind{ir.BuiltinPlatform, ir.BuiltinLanguage}
	}
	for _, kind := range tiers {
		target := c.lookupTargetIn(name, kind)
		if target == nil || targetUnavailable(target) != nil {
			continue
		}
		uri := targetTierMember(kind) + "/" + name
		pkg := c.libPkgOutsideDirective(uri)
		if pkg == nil || pkg.Symbols == nil {
			continue
		}
		sym, ok := pkg.Symbols.LookupRootComponent(name)
		if !ok {
			continue
		}
		if comp, ok := sym.(*ir.Component); ok {
			return comp
		}
	}
	if c.judgesTargets(depth) {
		return nil
	}
	return c.unregisteredTargetNode(name, depth)
}

// judgesTargets reports whether this check is in a position to call a name at
// this level a misspelling. Only a caller holding the whole registry is: a
// platform's own test harness registers itself while its fixtures name five
// other targets, which is a build this check does not have rather than a name
// nobody serves.
func (c *checker) judgesTargets(depth int) bool {
	if !c.cfg.TargetsComplete {
		return false
	}
	if depth == 1 {
		return len(c.cfg.Languages) > 0
	}
	return len(c.cfg.Platforms) > 0
}

// unregisteredTargetNode stands in for a target this check has no registry
// for. It belongs to the right family, so the nesting is still enforced, and
// it declares no props and carries no AST, which is what turns off the schema
// checks a declaration it does not have could not support.
func (c *checker) unregisteredTargetNode(name string, depth int) *ir.Component {
	key := targetTierMember(ir.BuiltinPlatform) + "/" + name
	if depth == 1 {
		key = targetTierMember(ir.BuiltinLanguage) + "/" + name
	}
	if comp, ok := c.synthTargets[key]; ok {
		return comp
	}
	langTree, platTree := c.buildTrees()
	comp := &ir.Component{Name: name, Stdlib: true, Tree: platTree}
	if depth == 1 {
		comp.Tree = langTree
		comp.ChildrenType = &ir.Type{Kind: ir.TypeList, Elems: []*ir.Type{ir.TypDyn}}
	}
	if c.synthTargets == nil {
		c.synthTargets = map[string]*ir.Component{}
	}
	c.synthTargets[key] = comp
	return comp
}

// libPkgOutsideDirective loads a library package with the directive's depth
// put aside. Loading one checks its bodies, and a body checked at depth 2
// resolves every bare node name as a platform -- fyne's own `Widget` was
// reported as an unknown build target.
func (c *checker) libPkgOutsideDirective(uri string) *ir.Package {
	saved := c.outputDepth
	c.outputDepth = 0
	defer func() { c.outputDepth = saved }()
	return c.libPkg(uri)
}

// checkOutputTree checks the package's build directive as the component tree
// it is, and projects the result into ir.Package.Outputs.
//
// It runs in pass2, unlike the registration that recorded the node: the tree
// instantiates declarations a target package carries, and resolving those in
// pass1 would read a scope still being built.
func (c *checker) checkOutputTree() {
	vn := c.outputDecl
	if vn == nil {
		return
	}
	c.pushScope()
	defer c.popScope()
	root, ok := c.checkVisualNodeIR(vn).(*ir.NodeInst)
	if !ok {
		return
	}
	c.requireConstOutputTree(root)
	c.collectOutputs(root)
	c.resolveEntryWindow(root)
}

// requireConstOutputTree holds every value in the directive to what the build
// can evaluate. The tree is read at build time and never again, so a value
// that changes while the program runs would compile to a snapshot of whatever
// it was first.
//
// The test is ir.IsConst, the one `const(...)` applies: a literal, a const, an
// enum member, or a call to a pure function -- including a `#[foreign(pure)]`
// one, which is a build-time value a target may reasonably compute. What it
// rejects is a read of anything the program can write.
func (c *checker) requireConstOutputTree(n *ir.NodeInst) {
	for _, p := range n.Props {
		// `entry` names a declaration rather than holding a value, so there is
		// nothing for the build to evaluate: resolveEntryWindow reads the
		// reference itself.
		if p.Name == entryOption || ir.IsConst(p.Value) {
			continue
		}
		at := p.NamePos
		if at == (ast.Pos{}) {
			if sp := stmtPos(n.AST); sp != nil {
				at = *sp
			}
		}
		c.error(at, "output option %q must be constant: the build directive is read once, before the program runs", p.Name)
	}
	for _, child := range n.Children {
		if ni, ok := child.(*ir.NodeInst); ok {
			c.requireConstOutputTree(ni)
		}
	}
}

// collectOutputs projects the checked tree into one ir.Output per
// language/platform pair. A node whose membership was already reported is
// skipped: the diagnostic is written, and reading past it would invent a
// target out of a mistake.
func (c *checker) collectOutputs(root *ir.NodeInst) {
	langTree, platTree := c.buildTrees()
	for _, langStmt := range root.Children {
		langNode, ok := langStmt.(*ir.NodeInst)
		if !ok || langNode.Component == nil || langNode.Component.Tree != langTree {
			continue
		}
		for _, platStmt := range langNode.Children {
			platNode, ok := platStmt.(*ir.NodeInst)
			if !ok || platNode.Component == nil || platNode.Component.Tree != platTree {
				continue
			}
			out := &ir.Output{
				Lang:     langNode.Component.Name,
				Platform: platNode.Component.Name,
				LangComp: langNode.Component,
				PlatComp: platNode.Component,
				Options:  outputOptions(root, langNode, platNode),
			}
			if vn, ok := platNode.AST.(*ast.VisualNode); ok {
				out.AST = vn
			} else if vn, ok := root.AST.(*ast.VisualNode); ok {
				out.AST = vn
			}
			c.pkg.Outputs = append(c.pkg.Outputs, out)
		}
	}
}

// outputOptions is the one options record a build reads, gathered from the
// three levels that contribute to it: what every target shares on `output`,
// what the language accepts, and what the platform accepts. The nearer level
// wins a name the two share, which is the only order a reader would expect
// from `output(test = true) { go { bubbletea(tests = false) } }`.
func outputOptions(nodes ...*ir.NodeInst) *ir.StructLit {
	def := &ir.StructDef{Name: "Options"}
	lit := &ir.StructLit{Def: def, Type: &ir.Type{Kind: ir.TypeStruct, Decl: def}}
	set := func(name string, value ir.Expr, pos ast.Pos) {
		for i := range lit.Fields {
			if lit.Fields[i].Name == name {
				lit.Fields[i].Value = value
				return
			}
		}
		lit.Fields = append(lit.Fields, ir.FieldInit{Name: name, NamePos: pos, Value: value})
	}
	written := func(name string) bool {
		for _, f := range lit.Fields {
			if f.Name == name {
				return true
			}
		}
		return false
	}
	for _, n := range nodes {
		if n.Component != nil {
			for _, p := range n.Component.Props {
				if findField(def, p.Name) == nil {
					def.Fields = append(def.Fields, &ir.StructField{Name: p.Name, Type: p.Type, Default: p.Default})
				}
			}
		}
		for _, prop := range n.Props {
			set(prop.Name, prop.Value, prop.NamePos)
		}
	}
	// A declared default is part of the schema, so it lands in the record the
	// same as a written one. Merging the two by hand is what the props replaced.
	for _, f := range def.Fields {
		if f.Default != nil && !written(f.Name) {
			set(f.Name, f.Default, ast.Pos{})
		}
	}
	return lit
}

// declaredOutputTargets reads the language/platform pairs an `output` node
// names. It reads only the two names -- options are the checker's business
// later, and getting them wrong here would only mean loading a package that
// was going to load anyway.
func declaredOutputTargets(vn *ast.VisualNode) []ir.StaticTarget {
	var out []ir.StaticTarget
	for _, stmt := range vn.Block.Stmts {
		langNode, ok := stmt.(*ast.VisualNode)
		if !ok {
			continue
		}
		lang := visualNodeTarget(langNode)
		if len(langNode.Block.Stmts) == 0 {
			out = append(out, ir.StaticTarget{Language: lang})
			continue
		}
		for _, langStmt := range langNode.Block.Stmts {
			// A platform carrying options parses as a call, not a visual node.
			// Reading only the node form here dropped every optioned platform
			// from the target set, so its overrides never merged.
			switch s := langStmt.(type) {
			case *ast.VisualNode:
				out = append(out, ir.StaticTarget{Language: lang, Platform: visualNodeTarget(s)})
			case *ast.CallStmt:
				if ident, ok := s.Call.Func.(*ast.IdentExpr); ok {
					out = append(out, ir.StaticTarget{Language: lang, Platform: ident.Name})
				}
			}
		}
	}
	return out
}

// reportUnknownTarget reports a name no target package declares, at the level
// it was written: a build directive names languages under `output` and
// platforms under a language, so the set it could have named is known.
func (c *checker) reportUnknownTarget(pos ast.Pos, name string, depth int) {
	var names []string
	what := "platform"
	if depth == 1 {
		what = "language"
		for _, l := range c.cfg.Languages {
			names = append(names, l.LanguageIdentifier())
		}
	} else {
		for _, p := range c.cfg.Platforms {
			names = append(names, p.PlatformIdentifier())
		}
	}
	slices.Sort(names)
	c.error(pos, "unknown %s %q in output (available: %s)", what, name, strings.Join(names, ", "))
}
