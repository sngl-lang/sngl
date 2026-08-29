package checker

import (
	"fmt"
	"io/fs"
	"maps"
	"slices"
	"strings"
	"sync"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
	"git.duckfam.us/jonathan/sngl/lib"
)

var (
	stdlibOnce     sync.Once
	stdlibDocs     []*ast.Document
	stdlibTierDocs map[string][]*ast.Document
)

// Packages are directories on disk, so the set follows the layout rather than
// a list maintained in Go.
func PackageDocsFor(name string) []*ast.Document {
	parseStdlibDocs()
	return slices.Clone(stdlibTierDocs[name])
}

// PackageSource returns the whole parsed source of `sngl:<name>`: what lib/
// embeds, plus what a registered target serves for its own package. A reader
// outside a check wants the source the loaded IR was built from, and for a
// target package that is not under lib/ at all.
//
// One parse, shared with LibPackage. A caller that reads a mark off the loaded
// IR and then finds that declaration in the source (`sngl doc`, the LSP) is
// comparing *ast.StructDef pointers, and two parses of one file never share
// one. These readers are outside any check, so nothing splices into what they
// get back.
func PackageSource(name string) []*ast.Document {
	packageSourceMu.Lock()
	defer packageSourceMu.Unlock()
	if docs, ok := packageSourceCache[name]; ok {
		return docs
	}
	docs := append(PackageDocsFor(name), ProvidedDocs(registeredTarget(name))...)
	packageSourceCache[name] = docs
	return docs
}

var (
	packageSourceMu    sync.Mutex
	packageSourceCache = map[string][]*ast.Document{}
)

func HasPackage(name string) bool {
	parseStdlibDocs()
	_, ok := stdlibTierDocs[name]
	return ok
}

// StdlibDocs returns the parsed stdlib documents, every tier merged.
//
// The platforms/ and languages/ tiers are left out: they are per-target and
// mutually exclusive (each declares its own `Options`), so merging them into
// one corpus produces collisions no program could ever hit. Reach one through
// PackageDocsFor, or import it.
//
// Returns a copy of the cached slice so a caller that appends can't write into
// the shared package-global backing array (bugs.md #12).
func StdlibDocs() []*ast.Document {
	return slices.Clone(parseStdlibDocs())
}

// A platform or language package: one contributed by a codegen plugin rather
// than by the library.
func targetTier(pkg string) bool {
	return strings.HasPrefix(pkg, "platforms/") || strings.HasPrefix(pkg, "languages/")
}

// parseStdlibDocs parses every .sngl file embedded in the lib package. File
// ordering is not significant: loadStdlib groups declarations by kind before
// registering them, so new stdlib files can be dropped into lib/ without
// touching this code.
func parseStdlibDocs() []*ast.Document {
	stdlibOnce.Do(func() {
		// The stdlib is embedded and compiler-controlled: any read/parse/expand
		// failure is a build invariant violation, not a runtime condition.
		// Failing loudly here surfaces the real cause immediately, instead of
		// leaving a partial stdlib that produces confusing "undefined
		// component/func" errors downstream (bugs.md #20).
		stdlibTierDocs = map[string][]*ast.Document{}
		for _, tier := range lib.Packages() {
			entries, err := lib.FS.ReadDir(tier)
			if err != nil {
				panic(fmt.Sprintf("sngl: reading embedded stdlib tier %q: %v", tier, err))
			}
			for _, e := range entries {
				if e.IsDir() || !strings.HasSuffix(e.Name(), ".sngl") {
					continue
				}
				name := tier + "/" + e.Name()
				data, err := fs.ReadFile(lib.FS, name)
				if err != nil {
					panic(fmt.Sprintf("sngl: reading embedded stdlib file %q: %v", name, err))
				}
				doc, err := parser.Parse(e.Name(), data)
				if err != nil {
					panic(fmt.Sprintf("sngl: parsing stdlib file %q: %v", name, err))
				}
				if !targetTier(tier) {
					stdlibDocs = append(stdlibDocs, doc)
				}
				stdlibTierDocs[tier] = append(stdlibTierDocs[tier], doc)
			}
		}
	})
	return stdlibDocs
}

// loadStdlib builds the packages every check needs up front: sngl:builtin,
// which registers into the checker's scope and symbol table for unqualified
// access everywhere, and sngl:std, which the checker itself reads to find
// the #[builtin]-marked window/timer/slot/errorBoundary components. Any other
// library package loads on first import (libPkg).
//
// Being ambient is the only way sngl:builtin is special. sngl:std is
// eager rather than special: it is loaded here because the checker needs its
// node components to build ir.Window and ir.Timer at all, not because user
// code sees it differently from sngl:draw.
//
// Declarations are grouped by kind across a package's files and registered in
// a fixed order — imports, then types (units, structs, enums), then functions,
// then components — so the file a declaration lives in does not affect
// resolution.
func (c *checker) loadStdlib() (builtinPkg, stdPkg *ir.Package) {
	// sngl:builtin is ambient — the one implicit import. It still loads as
	// an ordinary package and is then adopted into the ambient scope, so being
	// ambient is a property of where its declarations end up and not of how
	// they are built.
	builtinPkg = c.libPkg("builtin")
	c.adoptAmbient(builtinPkg)
	return builtinPkg, c.libPkg("std")
}

// resolveMacroSig resolves a macro's declared parameter types, once, the
// first time a mark of it is applied. Not as the package loads: the mark
// package loads from inside sngl:builtin's own imports, where `list` names
// nothing yet and `list<ir.IntrinsicFlag>` would degrade to `list<dyn>`.
func (c *checker) resolveMacroSig(pkg *ir.Package, fn *ir.Func) {
	if c.libs.macroSigs[fn] || fn.AST == nil || len(fn.Params) != len(fn.AST.Params.Params) {
		return
	}
	c.libs.macroSigs[fn] = true
	// The package's own root, whose parent is the scope its imports bound
	// their namespaces in — the scope the declaration was written in.
	savedScope, savedTab, savedTP := c.scope, c.symtab, c.typeParams
	c.scope, c.symtab, c.typeParams = pkg.Symbols.Root, pkg.Symbols, nil
	defer func() { c.scope, c.symtab, c.typeParams = savedScope, savedTab, savedTP }()
	for i, p := range fn.AST.Params.Params {
		fn.Params[i].Type = c.resolveType(p.Type)
	}
}

// adoptAmbient binds a library package's declarations into the ambient scope,
// where every file sees them unqualified. It works off the loaded package
// rather than the load, so a package the cache already holds is adopted the
// same way as one just built.
func (c *checker) adoptAmbient(pkg *ir.Package) {
	if pkg == nil {
		return
	}
	for _, sym := range pkg.Symbols.Root.Symbols {
		c.bindLib(symPos(sym), c.scope, sym)
	}
	// The suffix index is the checker's, not the package's, so it is rebuilt
	// from the declarations rather than only where a unit is first registered.
	for _, u := range pkg.Units {
		for _, sfx := range u.Suffixes {
			c.unitBySuffix[sfx.Name] = u
		}
	}
}

// libDocs returns the parsed source of lib package name: what lib/ embeds,
// plus what the target of that name synthesizes (providedDocs). Config.LibSources
// substitutes the whole package instead, for the in-test stubs.
func (c *checker) libDocs(name string) []*ast.Document {
	if c.cfg != nil {
		if docs, ok := c.cfg.LibSources[name]; ok {
			return docs
		}
	}
	return append(PackageDocsFor(name), c.providedDocs(name)...)
}

// providedDocs is the source the registered target of this package name
// provides, or nil for any other package.
func (c *checker) providedDocs(name string) []*ast.Document {
	if c.cfg == nil {
		return nil
	}
	// Both tiers: a language declares its foreign-type surface the way a
	// platform declares its widgets, and lookupTarget already answers for
	// either.
	target, ok := strings.CutPrefix(name, "platforms/")
	if !ok {
		if target, ok = strings.CutPrefix(name, "languages/"); !ok {
			return nil
		}
	}
	// This config's targets and no others. A check is defined by the targets
	// it was configured with, so a target absent from them contributes
	// nothing here even when it is registered process-wide -- PackageSource is
	// where the registry answers, for readers that have no config to carry.
	return ProvidedDocs(c.lookupTarget(target))
}

// Targets that serve a library package, keyed by its `sngl:<uri>`. A
// target's package lives with its plugin rather than under lib/, and this
// package cannot import the plugin registry that knows them -- so the registry
// registers into this one.
var (
	targetPkgMu sync.RWMutex
	targetPkgs  = map[string]any{}
)

// RegisterTargetPackage records that `sngl:<uri>` is served by t. Called by
// the codegen registry as each target registers, so that a reader outside a
// check can load a target package the same way a check does.
func RegisterTargetPackage(uri string, t any) {
	targetPkgMu.Lock()
	defer targetPkgMu.Unlock()
	targetPkgs[uri] = t
}

func registeredTarget(uri string) any {
	targetPkgMu.RLock()
	defer targetPkgMu.RUnlock()
	return targetPkgs[uri]
}

// ProvidedDocs parses the .sngl source a target synthesizes for its own
// library package, which it provides as an fs.FS the way lib.FS is one.
//
// A target whose declarations are derived from the host cannot embed them:
// gtk4's widget set is whatever the GTK introspection data installed here
// describes. The interface is matched structurally, as PlatformAvailability is,
// because codegen imports this package.
func ProvidedDocs(t any) []*ast.Document {
	p, ok := t.(interface{ PackageFS() fs.FS })
	if !ok {
		return nil
	}
	// Parsed fresh every call. A check splices platform bodies into the
	// documents it is given, and a target may be reconfigured to serve a
	// different package (gtk4 against another GIR), so neither the ASTs nor
	// the fs.FS behind them can be shared between checks. The package-level
	// readers memoize their own copy -- see packageSourceOnce.
	fsys := p.PackageFS()
	if fsys == nil {
		return nil
	}
	entries, err := fs.ReadDir(fsys, ".")
	if err != nil {
		panic(fmt.Sprintf("sngl: reading target-provided source: %v", err))
	}
	var docs []*ast.Document
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".sngl") {
			continue
		}
		data, err := fs.ReadFile(fsys, e.Name())
		if err != nil {
			panic(fmt.Sprintf("sngl: reading target-provided file %q: %v", e.Name(), err))
		}
		doc, err := parser.Parse(e.Name(), data)
		if err != nil {
			panic(fmt.Sprintf("sngl: parsing target-provided file %q: %v", e.Name(), err))
		}
		docs = append(docs, doc)
	}
	return docs
}

func (c *checker) hasLibPkg(name string) bool {
	if c.cfg != nil {
		if _, ok := c.cfg.LibSources[name]; ok {
			return true
		}
	}
	return len(PackageDocsFor(name)) > 0 || len(c.providedDocs(name)) > 0
}

// targetUnavailable reports why a platform cannot be used in this
// environment. The interface is matched structurally rather than named
// (codegen.PlatformAvailability) because codegen imports this package.
func targetUnavailable(t any) error {
	if a, ok := t.(interface{ Unavailable() error }); ok {
		return a.Unavailable()
	}
	return nil
}

// libPkg returns the loaded sngl:<name> package, loading it on first use.
// Loading is lazy and memoized rather than a pass over lib.Packages() because
// lib packages import each other (sngl:draw is written against sngl:std),
// and the import has to resolve to the same instance the user sees.
func (c *checker) libPkg(name string) *ir.Package {
	if pkg, ok := c.libs.pkgs[name]; ok {
		c.adoptLib(name, pkg)
		return pkg
	}
	if c.libs.loading[name] {
		// An import cycle inside lib/ is a compiler bug, not user input.
		panic("sngl: import cycle in embedded library at sngl:" + name)
	}
	c.libs.loading[name] = true
	// Load against the ambient builtin scope rather than whatever scope the
	// first import happened to sit in: loading is lazy and memoized, so a
	// scope captured from inside a function body (a `platform x { ... }`
	// block's first mention of a platform package) would become the parent of
	// every later use of the package.
	if c.stdlibScope != nil {
		saved := c.scope
		c.scope = c.stdlibScope
		defer func() { c.scope = saved }()
	}
	// Library source has its own file scopes, but loading is lazy: it happens
	// part-way through the importing document's pass1, whose claims are still
	// in c.topLevel. A lib file's import alias would otherwise collide with a
	// name the user's dot imports lifted — `import tree "sngl:internal/tree"`
	// against std's `tree` component.
	savedTopLevel := c.topLevel
	c.topLevel = nil
	defer func() { c.topLevel = savedTopLevel }()
	pkg := c.loadStdlibPackage(name)
	if name == i18nPkg {
		c.declarePluralKeyConstants(pkg)
	}
	delete(c.libs.loading, name)
	c.libs.pkgs[name] = pkg
	c.adoptLib(name, pkg)
	return pkg
}

// adoptLib takes the checker-side references a loaded library package earns.
// They are re-derived from the package rather than recorded as it loads,
// because the cache is shared: the package a check reaches was often built by
// an outer one, which is the point — the declaration a platform extension is
// attached to has to be the declaration user code resolves.
func (c *checker) adoptLib(name string, pkg *ir.Package) {
	for _, u := range pkg.Units {
		for _, sfx := range u.Suffixes {
			c.unitBySuffix[sfx.Name] = u
		}
	}
	if name == irPkg && c.macroStruct == nil {
		if sym, ok := pkg.Symbols.Root.LookupLocal(macroTypeName); ok {
			if sd, isStruct := sym.(*ir.StructDef); isStruct {
				c.macroStruct = sd
			}
		}
	}
}

// inLibSource reports whether the declarations being registered come from
// lib/ rather than from a program. Every path into lib/ source runs through
// targetNamespaceName is the namespace a target's package is reached through,
// which is the target's own name: sngl:platforms/html is `html`.
func targetNamespaceName(pkgName string) (string, bool) {
	for _, prefix := range []string{"platforms/", "languages/"} {
		if name, ok := strings.CutPrefix(pkgName, prefix); ok {
			return name, true
		}
	}
	return "", false
}

// loadStdlibPackage, including the nested loads an import inside lib/ starts,
// so the counter covers transitive loads too.
func (c *checker) inLibSource() bool { return c.libDepth > 0 || c.cfg.libSource }

func (c *checker) loadStdlibPackage(pkgName string) *ir.Package {
	c.libDepth++
	savedPkgName := c.libPkgName
	c.libPkgName = "sngl:" + pkgName
	defer func() { c.libDepth--; c.libPkgName = savedPkgName }()

	stdlibPkg := &ir.Package{
		Symbols:        NewSymbolTable(),
		LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{},
		AddressedVars:  map[*ir.Var]bool{},
	}
	savedImportScope := c.libImportScope
	savedSymtab, savedScope := c.symtab, c.scope
	// The package's imports go one scope above its root: reachable while it
	// loads, absent from what a dot import of it lifts.
	c.libImportScope = NewScope(savedScope)
	// A target's own source reaches its own declarations through its own
	// namespace -- `html.div` inside html.sngl, where the prefix separates the
	// element from the stdlib component of the same name. That namespace is in
	// scope while the package loads and nowhere else: a program reaches it by
	// importing the package, like any other.
	if nsName, ok := targetNamespaceName(pkgName); ok {
		c.bindLib(ast.Pos{}, c.libImportScope, &ir.Namespace{Name: nsName, Pkg: stdlibPkg})
	}
	stdlibPkg.Symbols.Root.Parent = c.libImportScope
	c.symtab, c.scope = stdlibPkg.Symbols, stdlibPkg.Symbols.Root
	defer func() {
		c.symtab, c.scope, c.libImportScope = savedSymtab, savedScope, savedImportScope
	}()
	savedLoadPkg := c.libLoadPkg
	c.libLoadPkg = stdlibPkg
	defer func() { c.libLoadPkg = savedLoadPkg }()

	docs := c.libDocs(pkgName)
	defer c.setMarkScope(docs)()

	var (
		imports    []*ast.Import
		units      []*ast.UnitDef
		structs    []*ast.StructDef
		enums      []*ast.EnumDef
		consts     []*ast.ConstDecl
		funcs      []*ast.FuncDef
		components []*ast.ComponentDecl
		contexts   []*ast.CallStmt
	)
	for _, doc := range docs {
		for _, stmt := range doc.Stmts {
			switch s := stmt.(type) {
			case *ast.Import:
				imports = append(imports, s)
			case *ast.UnitDef:
				units = append(units, s)
			case *ast.StructDef:
				structs = append(structs, s)
			case *ast.EnumDef:
				enums = append(enums, s)
			case *ast.ConstDecl:
				consts = append(consts, s)
			case *ast.FuncDef:
				funcs = append(funcs, s)
			case *ast.ComponentDecl:
				components = append(components, s)
			case *ast.CallStmt:
				if c.isContextDeclCallStmt(s) {
					contexts = append(contexts, s)
				}
			}
		}
	}

	for _, s := range imports {
		c.registerImport(s)
	}
	for _, s := range units {
		c.registerStdlibUnit(s, stdlibPkg)
	}
	for _, s := range enums {
		c.registerStdlibEnum(s, stdlibPkg)
	}
	// Structs may reference any type — including other structs — so register
	// names as empty stubs first, then resolve fields in a second pass.
	structDefs := make([]*ir.StructDef, len(structs))
	for i, s := range structs {
		structDefs[i] = c.declareStdlibStruct(s, stdlibPkg)
	}
	for i, s := range structs {
		c.resolveStdlibStructFields(s, structDefs[i])
	}
	c.assertOptionsMarked(pkgName, structDefs)
	// PluralKey's Go runtime type is qualified (i18n.PluralKey) so IRTypeToGo
	// emits it rather than the bare SNGL name. A #[foreign] mark cannot say
	// this: a marked declaration is the program's own, and the Go emitter
	// deliberately ignores a marked name for that reason.
	for _, sd := range structDefs {
		if sd.Foreign.Name == "" && sd.Name == "PluralKey" {
			sd.Foreign.Name = "i18n.PluralKey"
		}
	}
	for _, s := range consts {
		c.registerStdlibConst(s, stdlibPkg)
	}
	// Phase 1: register stdlib func signatures (no body checking yet) so
	// later phases — context default expressions, context-reading wrapper
	// bodies — can resolve names against fully-populated symbol tables.
	type stdlibFuncBody struct {
		ast *ast.FuncDef
		fn  *ir.Func
	}
	// The i18n and html namespaces describe std's own declarations, so they
	// belong to that package only. Declaring them from the builtin pass as
	// well puts an empty `html` namespace in the ambient scope, which shadows
	// the real one; and building them while any other package loads would
	// re-enter the package PluralKey lives in.
	//
	// They are declared before the funcs whose receiver names them, so that
	// `func i18n.tr(...)` finds a declaration to be a member of. Their
	// packages are built from those same funcs, so the Pkg is filled in below
	// once they exist.
	// html's placement directives are declared as methods on a receiver named
	// "html" in lib/std, so the namespace has to exist before they are
	// registered. i18n has its own package and needs nothing here.
	declaresHtml := pkgName == stdPkg
	var htmlNS *ir.Namespace
	if declaresHtml {
		htmlNS = &ir.Namespace{Name: "html"}
		c.bindLib(ast.Pos{}, c.scope, htmlNS)
	}

	var pendingBodies []stdlibFuncBody
	var registeredFuncs []*ir.Func
	for _, s := range funcs {
		fn := c.registerStdlibFunc(s, stdlibPkg)
		if fn != nil {
			registeredFuncs = append(registeredFuncs, fn)
		}
		// Defer body check: expression-body funcs (=> expr) are lowered into
		// ir.Block. Block-body stdlib funcs are also lowered for constant-folding
		// support (e.g., color.lighten, color.darken). Bodyless signatures
		// (e.g., `func i18n.exactly(n int) PluralKey {}`) are unaffected.
		if fn != nil && (s.Body != nil || s.Block.IsDefined()) {
			pendingBodies = append(pendingBodies, stdlibFuncBody{ast: s, fn: fn})
		}
	}
	for _, s := range components {
		c.registerStdlibComponent(s, stdlibPkg)
	}

	if declaresHtml {
		// The directives html.frontend(...) / html.backend(...) (GitLab #27)
		// are declared as methods on receiver "html"; expose them here as
		// namespace functions so a call resolves.
		htmlNS.Pkg = c.buildHtmlNamespacePkg(registeredFuncs)
	}

	// Register stdlib context declarations last — after the "i18n" namespace is
	// in scope — so that default-value expressions like `i18n.defaultLocale()`
	// resolve correctly. Stdlib contexts are declared into the stdlib scope and
	// their *ir.Context pointers are appended to c.pkg.Contexts so the
	// interpreter and codegen discover them alongside user-declared contexts.
	for _, s := range contexts {
		c.registerStdlibContextDecl(s)
	}

	// Phase 2: check deferred stdlib expression-body wrappers. Run last so
	// that bodies can read freshly-registered context decls (e.g. the
	// `#locale` context used by i18n.* wrappers).
	for _, pb := range pendingBodies {
		c.checkStdlibFuncBody(pb.ast, pb.fn)
	}

	// Phase 2b: refine stdlib function purity by propagating from called
	// functions. The auto-Pure mark in registerStdlibFunc is a placeholder;
	// now that every body is checked, lift each func's purity to
	// max(self, max(called.Purity)) and iterate to a fixed point. This
	// makes wrappers like `i18n.defaultLocale() => intl.DefaultLocale()`
	// inherit PurityReadonly from the intrinsic, which prevents the
	// optimizer from folding them.
	for changed := true; changed; {
		changed = false
		for _, pb := range pendingBodies {
			bodyPurity := highestCalledPurity(pb.fn)
			if bodyPurity > pb.fn.Purity {
				pb.fn.Purity = bodyPurity
				changed = true
			}
		}
	}

	// A platform or language package may ship its own body-bearing
	// components (the wrappers lower's strict InlinePure pass is written
	// against), and pass2 only walks the program's own components, so their
	// bodies are checked here. A `component sngl.X` extension declaration is
	// not one of those: mergePlatformExtensions splices its platform blocks
	// into the component it names and checkPendingExtensions checks them
	// there.
	//
	// Restricted to the target tiers: sngl:std's components carry bodies
	// too, but they are declared without the pass1 pre-pass that binds their
	// props and vars, so checking them here reports every one as undefined.
	if targetTier(pkgName) {
		for _, irComp := range stdlibPkg.Components {
			if strings.Contains(irComp.Name, ".") || !irComp.AST.Body.IsDefined() {
				continue
			}
			nested := c.collectComponentDecls(irComp.AST, irComp)
			irComp.Funcs = c.registerNestedMethods(irComp.Name, nil, nested)
			c.checkComponentBody(irComp)
		}
	}

	// Phase 3: refine stdlib context types from their default expressions.
	// Context decls run BEFORE wrapper body checks (so wrapper bodies can
	// read them), at which point a default like `i18n.defaultLocale()` still
	// types as dyn. After Phase 2 the wrapper has its concrete return type,
	// so re-pull ctx.Typ from the default expression's Call.Func.Return.
	for _, ctx := range c.pkg.Contexts {
		if ctx.Typ != nil && ctx.Typ.Kind != ir.TypeDyn {
			continue
		}
		if call, ok := ctx.Default.(*ir.Call); ok && call.Func != nil && call.Func.Return != nil {
			ctx.Typ = call.Func.Return
		}
	}

	return stdlibPkg
}

// assertOptionsMarked fails the build when a library package declares a
// top-level struct called Options without the #[options] mark. Every options
// lookup keys on the mark, so an unmarked one would silently contribute no
// options at all; the name match here exists only to catch that omission, and
// is the one place the name means anything.
//
// Embedded source only: a Config.LibSources substitution is test input, and one
// of them plants an unmarked Options on purpose.
func (c *checker) assertOptionsMarked(pkgName string, structs []*ir.StructDef) {
	if c.cfg != nil && c.cfg.LibSources[pkgName] != nil {
		return
	}
	for _, sd := range structs {
		if sd.Name == "Options" && !sd.Options {
			panic(fmt.Sprintf("sngl: sngl:%s: struct Options at %s needs #[options] (and import . %q)",
				pkgName, sd.AST.Pos, "sngl:platforms"))
		}
	}
}

// i18nPkg declares the translation entry points, the locale-aware primitives
// behind them, and the PluralKey those are keyed by.
const i18nPkg = "i18n"

// stdPkg is the library package that declares the html namespace.
const stdPkg = "std"

// irPkg is the compiler's own package, and macroTypeName the return type it
// declares that makes a function a macro.
const (
	irPkg         = "internal/ir"
	macroTypeName = "Macro"
)

// macroPackage returns the URI of the package that declares a macro of this
// name, or "" for a name that is not a macro. It reads the parsed source
// rather than a loaded package: the caller is an error path, and loading a
// package to build a hint would re-enter one mid-load.
func macroPackage(name string) string {
	for _, pkg := range lib.Packages() {
		for _, doc := range PackageDocsFor(pkg) {
			for _, stmt := range doc.Stmts {
				if f, ok := stmt.(*ast.FuncDef); ok && f.Name == name && IsMacroDecl(f) {
					return pkg
				}
			}
		}
	}
	return ""
}

// IsMacroDecl reports whether a declaration is a macro, from the AST alone.
// The documentation layer has no type information, so it matches the return
// type's name under whatever alias the file imported sngl:internal/ir as;
// the checker matches the declaration itself (isMacroSig), which is the
// authority.
func IsMacroDecl(f *ast.FuncDef) bool {
	nt, ok := f.ReturnType.(*ast.NamedType)
	return ok && nt.Name == macroTypeName
}

// isMacroSig reports whether a declared signature returns sngl:internal/ir's
// Macro — the whole of what makes a declaration a macro rather than a function.
func (c *checker) isMacroSig(t *ir.Type) bool {
	if c.macroStruct == nil || t == nil || t.Kind != ir.TypeStruct {
		return false
	}
	sd, ok := t.Decl.(*ir.StructDef)
	return ok && sd == c.macroStruct
}

// declarePluralKeyConstants registers the six CLDR plural categories on the
// package that declares PluralKey. They are opaque sentinels whose runtime
// values come from the target's i18n runtime, so there is no literal to
// declare them with — the compiler supplies them, as it does for null and
// PLATFORM.
func (c *checker) declarePluralKeyConstants(pkg *ir.Package) {
	if pkg == nil {
		return
	}
	sym, ok := pkg.Symbols.Root.LookupLocal("PluralKey")
	if !ok {
		return
	}
	sd, isStruct := sym.(*ir.StructDef)
	if !isStruct {
		return
	}
	for _, name := range []string{"zero", "one", "two", "few", "many", "other"} {
		v := &ir.Var{Name: name, Type: sd.SymType(), IsConst: true}
		pkg.Vars = append(pkg.Vars, v)
		c.bindLib(ast.Pos{}, pkg.Symbols.Root, v)
	}
}

// The html.* placement directives are declared as methods on receiver "html";
// a synthetic package republishes them as free functions so html.frontend(v)
// resolves.
func (c *checker) buildHtmlNamespacePkg(stdlibFuncs []*ir.Func) *ir.Package {
	pkg := &ir.Package{
		Symbols:        NewSymbolTable(),
		LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{},
		AddressedVars:  map[*ir.Var]bool{},
	}
	c.addReceiverFuncs(pkg, stdlibFuncs, "html")
	return pkg
}

// addReceiverFuncs publishes every func in funcs whose receiver is recv as a
// declaration of pkg. A namespace's members are its package's declarations, so
// this is what makes `i18n.tr` resolve — the funcs themselves say which
// receiver they belong to, so no index of them is needed.
func (c *checker) addReceiverFuncs(pkg *ir.Package, funcs []*ir.Func, recv string) {
	for _, fn := range funcs {
		if fn.Receiver != recv {
			continue
		}
		pkg.Funcs = append(pkg.Funcs, fn)
		c.bindLib(ast.Pos{}, pkg.Symbols.Root, fn)
	}
}

// declareStdlibStruct registers a struct name (without fields) so other
// declarations can reference it while we are still processing the stdlib.
// Fields are filled in by resolveStdlibStructFields once every name is in
// scope.
func (c *checker) declareStdlibStruct(s *ast.StructDef, pkg *ir.Package) *ir.StructDef {
	sd := &ir.StructDef{AST: s, Name: s.Name, Pkg: c.libPkgName}
	c.applyMarks(s, sd)
	// Macro carries no #[builtin] kind: a kind names the IR construct a
	// declaration dispatches to, and this one dispatches to none. It is found
	// by name within the compiler's own package, which no program can import.
	if c.libPkgName == "sngl:"+irPkg && s.Name == macroTypeName {
		c.macroStruct = sd
	}
	// A loading package's own root is the current scope, so this binds the
	// same symbol twice — which Declare tolerates, while still refusing a
	// different symbol under a name already taken.
	c.bindLib(s.Pos, c.scope, sd)
	// Stdlib package for qualified sngl.Type access.
	pkg.Structs = append(pkg.Structs, sd)
	c.bindLib(s.Pos, pkg.Symbols.Root, sd)
	// Publish the canonical date/time/datetime struct types so non-checker
	// phases (foreign-type importers) can synthesize them without scope access.
	switch sd.Builtin {
	case ir.BuiltinDate:
		ir.RegisterStringReprStructs(sd.SymType(), nil, nil)
	case ir.BuiltinTime:
		ir.RegisterStringReprStructs(nil, sd.SymType(), nil)
	case ir.BuiltinDateTime:
		ir.RegisterStringReprStructs(nil, nil, sd.SymType())
	}
	return sd
}

// resolveStdlibStructFields populates the fields of an already-declared
// struct. Safe to run after every stdlib type name is in scope, which allows
// fields to reference any stdlib type regardless of declaration order.
func (c *checker) resolveStdlibStructFields(s *ast.StructDef, sd *ir.StructDef) {
	built := c.buildStructDef(s)
	sd.Fields = built.Fields
	// Every stdlib type name is in scope by now, which is exactly the
	// condition a default needs.
	c.fillStructFieldDefaults(sd)
}

func (c *checker) registerStdlibEnum(e *ast.EnumDef, pkg *ir.Package) {
	ed := c.buildEnumDef(e)
	c.applyMarks(e, ed)
	c.bindLib(e.Pos, c.scope, ed)
	pkg.Enums = append(pkg.Enums, ed)
	c.bindLib(e.Pos, pkg.Symbols.Root, ed)
}

func (c *checker) registerStdlibUnit(u *ast.UnitDef, pkg *ir.Package) {
	ud := c.buildUnitDef(u)
	c.applyMarks(u, ud)
	c.bindLib(u.Pos, c.scope, ud)
	for _, s := range ud.Suffixes {
		c.unitBySuffix[s.Name] = ud
	}
	pkg.Units = append(pkg.Units, ud)
	c.bindLib(u.Pos, pkg.Symbols.Root, ud)
}

// registerStdlibConst registers a library const. A mark on the declaration is
// applied to every name it declares, which is why #[builtin] on a grouped
// const reports the kind twice rather than picking one.
func (c *checker) registerStdlibConst(decl *ast.ConstDecl, pkg *ir.Package) {
	for _, spec := range decl.Specs {
		typ := c.resolveType(spec.Type)
		var init ir.Expr
		if spec.Default != nil {
			init = c.checkExprExpecting(spec.Default, typ)
			if typ.Kind == ir.TypeDyn {
				typ = exprType(init)
			}
		}
		for _, name := range spec.Names {
			v := &ir.Var{
				AST:     decl,
				Name:    name,
				Type:    typ,
				Init:    init,
				IsConst: true,
			}
			c.applyMarks(decl, v)
			pkg.Consts = append(pkg.Consts, v)
			c.bindLib(decl.Pos, c.scope, v)
			c.bindLib(decl.Pos, pkg.Symbols.Root, v)
		}
	}
}

func (c *checker) registerStdlibFunc(f *ast.FuncDef, pkg *ir.Package) *ir.Func {
	fn := c.buildFunc(f)
	// A macro declaration is not a function: it is the place a `#[...]` mark's
	// documentation and argument list are written, and a Go handler is what
	// runs. Binding it would put the name in scope, where a program could call
	// it — and a dot import of the package that dot-imports the mark's package
	// would lift it on, so `#[builtin]` would end up callable from any file
	// that imports sngl:std.
	if c.isMacroSig(fn.Return) {
		// The declaration is the whole of what the compiler knows about a
		// macro except what it does, so it is kept on the package where mark
		// resolution reads it.
		pkg.Macros = append(pkg.Macros, fn)
		return nil
	}
	c.applyMarks(f, fn)
	// Stdlib funcs skip the body-check pass. When a stdlib signature omits a
	// return annotation (common for the "=>" forms that delegate to an
	// intrinsic), treat the missing return as an explicit dyn escape hatch
	// rather than void — the stdlib is trusted to know what it's doing, and
	// body-level inference would conflict with the primitive/struct aliasing
	// used internally (e.g. the `color` struct vs the `color` primitive).
	if fn.Return == nil && f.Body != nil {
		fn.Return = TypDyn
	}
	// buildFunc copied the #[intrinsic] mark; the effect metadata that goes
	// with the id follows from it. An id no intrinsic answers to is a typo in
	// the mark, and nothing downstream would notice it — the call would just
	// never be recognized.
	if fn.Intrinsic != "" {
		if !applyIntrinsicMetadata(fn, fn.Intrinsic) {
			c.error(f.Pos, "unknown intrinsic %q on %s", fn.Intrinsic, fn.Name)
		}
	}
	// Stdlib funcs are not body-checked, so the usual purity analysis never
	// runs. Mark them pure so the optimizer can constant-fold pure stdlib
	// methods (int.min, string.upper, etc.) when called with constant args.
	// Impure stdlib (alert.show, file.contents, anything reaching outside the
	// program) gets its purity overridden later by stdlib.SetImpure or via
	// scheme registration.
	if fn.Purity == ir.PurityUnknown {
		fn.Purity = ir.PurityPure
	}
	fn.Stdlib = true
	if fn.Receiver != "" {
		// Type-attached method, hosted on the receiver's declaration. A
		// receiver that names a namespace rather than a type (i18n, html) has
		// no declaration to host it; those funcs become members of the
		// namespace's own package, built from this same list below.
		if prev := c.declareMethod(f.Pos, fn); prev != nil {
			c.error(f.Pos, "duplicate declaration of %q on type %s", fn.Name, fn.Receiver)
		}
	} else {
		// Free function — available both qualified and unqualified.
		c.bindLib(f.Pos, c.scope, fn)
		pkg.Funcs = append(pkg.Funcs, fn)
		c.bindLib(f.Pos, pkg.Symbols.Root, fn)
	}
	return fn
}

// checkStdlibFuncBody type-checks a stdlib function body (either expression-body
// `=>` wrapper or block-body statement) into an ir.Block. The package-level scope
// must already contain all stdlib decls (imports, types, funcs, contexts) so the
// body can resolve references like `intl.Translate` or the active `locale` context.
//
// Intentional limits:
//   - Bodyless stdlib funcs are unaffected.
//   - If checking the body produces no return type (void), the func is left
//     with Return == nil so existing dyn-fallback in registerStdlibFunc
//     remains active.
func (c *checker) checkStdlibFuncBody(f *ast.FuncDef, fn *ir.Func) {
	if f.Body == nil && !f.Block.IsDefined() {
		return
	}
	c.pushScope()
	defer c.popScope()
	for _, p := range fn.Params {
		c.declare(f.Pos, p)
	}
	prevReturn := c.returnType
	c.returnType = fn.Return
	defer func() { c.returnType = prevReturn }()
	prevTypeParams := c.typeParams
	// Receiver type parameters (the `<T>` in `list<T>.push`) plus any
	// method-level ones must be in scope to resolve the receiver type and the
	// body. RecvTypeParams come first so the receiver type `list<T>` resolves.
	c.typeParams = append(append([]string{}, fn.RecvTypeParams...), fn.TypeParams...)
	defer func() { c.typeParams = prevTypeParams }()

	// Implicit-receiver methods (generic receiver, e.g. list<T>.push) carry no
	// receiver parameter — the receiver is referenced as `this`. Bind it so
	// such methods can have an expression body that delegates to an intrinsic,
	// e.g. `func list<T>.push(item T) => stdlib.ListPush(this, item)`.
	// Concrete-type methods (string.length(s string), color.hex(c color)) name
	// the receiver explicitly and need no `this` binding. Only expression
	// bodies are considered: the bodyless `{ }` generic stubs (map<K,V>.get,
	// list<T>.filter, …) never reference `this`, and resolving their receiver
	// type here would spuriously trip the map-key comparability check on the
	// abstract key type parameter.
	if f.Body != nil && fn.Receiver != "" && len(fn.RecvTypeParams) > 0 {
		if thisType := c.resolveType(synthRecvTypeExpr(f.Pos, fn.Receiver, fn.RecvTypeParams)); thisType != nil {
			c.declare(f.Pos, &ir.Param{Name: ir.ReceiverParam, Type: thisType, Receiver: true})
		}
	}

	if f.Body != nil {
		bodyExpr := c.checkExpr(f.Body)
		if bodyExpr == nil {
			return
		}
		pos := f.Pos
		if p := f.Body.ExprPos(); p != nil {
			pos = *p
		}
		// Infer concrete return type from the body when the declaration left it
		// as dyn (the fallback applied in registerStdlibFunc). Stdlib wrappers
		// like `i18n.numberInt(n, style) => intl.NumberInt(locale, n, style)`
		// otherwise stay dyn and downstream codegen has no concrete Go/JS/Kotlin
		// type for the method signature.
		//
		// Restrict to primitive body types to dodge a known ambiguity: the
		// `color` struct vs the `color` primitive share a name. Wrappers like
		// `color.rgb(...) => color{...}` produce a struct type whose
		// stringification collides with the primitive in callers like
		// `color.hex(c color)`; leaving those Returns as dyn preserves the
		// historical wildcard behaviour. Primitives don't have this clash.
		if fn.Return != nil && fn.Return.Kind == ir.TypeDyn {
			if t := exprType(bodyExpr); t != nil && isPrimitiveTypeKind(t.Kind) {
				fn.Return = t
			}
		}
		fn.Block = []ir.Stmt{&ir.Return{
			AST:   &ast.ReturnStmt{Pos: pos, Value: f.Body},
			Value: bodyExpr,
		}}
	} else if f.Block.IsDefined() {
		fn.Block = c.checkBlockIR(&f.Block)
	}

}

// applyIntrinsicMetadata copies effect metadata from the named intrinsic onto a
// stdlib wrapper that delegates to it. The wrapper would otherwise default to
// PurityPure (registerStdlibFunc), which is wrong for effecting intrinsics like
// ListPush (mutates its receiver) — letting the optimizer fold or drop a real
// mutation. Backends and reactivity read the mutation semantics back via
// fn.Intrinsic and ir.IntrinsicByName, so no name matching is needed downstream.
func applyIntrinsicMetadata(fn *ir.Func, id string) bool {
	def, ok := ir.IntrinsicByName(id)
	if !ok {
		return false
	}
	if def.Purity != ir.PurityUnknown {
		fn.Purity = def.Purity
	}
	return true
}

// targetPackages is every target package this check loads. Building for a
// target is an `import _ "sngl:platforms/<it>"` nobody wrote, and these are
// the ways a document comes to have written one.
//
// An explicit import always counts: it is the program asking to be held to a
// platform's rules, and no flag takes that back. The target itself is whatever
// the caller named, or -- when the caller named nothing -- whatever the
// document's own `output` blocks declare, which is the same order `sngl build`
// resolves them in. The target is added to the imports rather than replacing
// them.
//
// Read from the AST rather than from pkg.Outputs, which does not exist yet:
// the overrides have to be spliced before anything reads a stdlib component's
// body, and that is earlier than checking an output block.
func (c *checker) targetPackages() []string {
	seen := map[string]bool{}
	var out []string
	addPkg := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	addTarget := func(t ir.StaticTarget) {
		if t.Platform != "" {
			addPkg("platforms/" + t.Platform)
		}
		if t.Language != "" {
			addPkg("languages/" + t.Language)
		}
	}

	// Explicit imports, which nothing overrides. Through the same `=>`
	// resolution registerImport applies before it looks at the scheme, so a
	// replace pointing at a target package counts and one pointing away from
	// it does not -- pass1 has not built its map yet, and this is the same
	// scan it will do.
	replaces := map[string]string{}
	for _, stmt := range c.doc.Stmts {
		if imp, ok := stmt.(*ast.Import); ok && imp.Replace != "" {
			if _, dup := replaces[imp.Path]; !dup {
				replaces[imp.Path] = imp.Replace
			}
		}
	}
	maps.Copy(replaces, c.cfg.Replaces)

	for _, stmt := range c.doc.Stmts {
		switch s := stmt.(type) {
		case *ast.Import:
			target := s.Path
			if s.Replace != "" {
				target = s.Replace
			} else if mapped, ok := replaces[s.Path]; ok {
				target = mapped
			}
			if uri, ok := strings.CutPrefix(target, "sngl:"); ok && targetTier(uri) {
				addPkg(uri)
			}
		}
	}

	for _, t := range c.targets {
		addTarget(t)
	}

	if len(out) == 0 {
		// Nothing named a target anywhere: no flag, no output block, no
		// import. There is no build to restrict to, so every registered
		// target's overrides load -- what a bare `check`, the LSP and `doc`
		// want, and what this did for every caller before a target could be
		// named at all.
		for _, p := range c.cfg.Platforms {
			addPkg("platforms/" + p.PlatformIdentifier())
		}
		for _, l := range c.cfg.Languages {
			addPkg("languages/" + l.LanguageIdentifier())
		}
	}
	return out
}

// resolvedTargets is what this check builds for: what the caller named wins
// over what the document's own `output` blocks declare, which is the order
// `sngl build` resolves them in. A caller that named nothing and a document
// that declares nothing give none, and every registered target loads.
//
// Read from the AST rather than from pkg.Outputs, which does not exist yet:
// the overrides have to be spliced before anything reads a stdlib component's
// body, and that is earlier than checking an output block.
func (c *checker) resolvedTargets() []ir.StaticTarget {
	if len(c.cfg.Targets) > 0 {
		return c.cfg.Targets
	}
	var declared []ir.StaticTarget
	for _, stmt := range c.doc.Stmts {
		if s, ok := stmt.(*ast.VisualNode); ok && visualNodeTarget(s) == "output" {
			declared = append(declared, declaredOutputTargets(s)...)
		}
	}
	return declared
}

// declaredOutputTargets reads the lang/platform pairs an `output` node names,
// in either form: `output(lang=..., platform=...)` and
// `output { <lang> { <platform>(...) } }`. It reads only those two names --
// options are the checker's business later, and getting them wrong here would
// only mean loading a package that was going to load anyway.
func declaredOutputTargets(vn *ast.VisualNode) []ir.StaticTarget {
	var out []ir.StaticTarget
	var flat ir.StaticTarget
	for _, a := range vn.Args.Args {
		arg, ok := a.(ast.Arg)
		if !ok || arg.Name == "" {
			continue
		}
		switch arg.Name {
		case "lang":
			flat.Language = literalString(arg.Value)
		case "platform":
			flat.Platform = literalString(arg.Value)
		}
	}
	if flat.Language != "" || flat.Platform != "" {
		out = append(out, flat)
	}
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
			platNode, ok := langStmt.(*ast.VisualNode)
			if !ok {
				continue
			}
			out = append(out, ir.StaticTarget{Language: lang, Platform: visualNodeTarget(platNode)})
		}
	}
	return out
}

// mergeTargetExtensions collects the `component sngl.X` overrides one target's
// package declares, checking each `platform <name> { ... }` block into the
// stdlib *ir.Component's PlatformOverrides map. The lowering pass
// passPlatformExtensionBody reads PlatformOverrides[opts.Platform] and swaps it
// into Component.Body before any other pass runs.
//
// A target's package is loaded the way a side-effect import is, and for the
// same reason: building for a platform is an `import _ "sngl:platforms/<it>"`
// nobody wrote. So this runs for the build target, and for any target package
// the program imported itself -- which is how a program asks to be held to a
// platform's rules without naming one of its declarations.
//
// It used to run for every registered platform at once, which made one
// platform's problems everybody's: an override naming a widget its own host
// could not describe was reported in a build targeting something else
// entirely, so a platform had to withhold its whole package rather than serve
// a half of it. Loading only what a build actually reaches removes the need
// for that, and gives a program the choice.
//
// Duplicate entries for the same stdlib X under one platform name are an
// error. Only the parenless form is an extension; `component sngl.X() { body }`
// (android) is ignored here, because that platform reads such a body itself.
func (c *checker) mergeTargetExtensions(pkgName string) {
	if c.libs.extended[pkgName] {
		return
	}
	c.libs.extended[pkgName] = true
	name, ok := strings.CutPrefix(pkgName, "platforms/")
	if !ok {
		// Only a platform declares overrides; a language package has no
		// `component sngl.X` to merge.
		return
	}
	if p := c.lookupTarget(name); p != nil && targetUnavailable(p) != nil {
		// An unavailable target's overrides are written against declarations
		// it cannot provide.
		return
	}
	{
		// An override names its target, and a target package's own source is
		// no exception -- but it reaches its namespace the way its bodies do,
		// from the package itself, not from the program's scope, which is
		// where this runs and where a target is only in scope if imported.
		c.pushScope()
		if pkg := c.libPkg(pkgName); pkg != nil {
			c.bindLib(ast.Pos{}, c.scope, &ir.Namespace{Name: name, Pkg: pkg})
		}
		defer c.popScope()
		for _, doc := range c.libDocs(pkgName) {
			// The extension prefix is whatever alias this document imported the
			// stdlib under. Platform docs are not registered into the checker's
			// scope, so resolve it from the document's own imports.
			aliases := libImportAliases(doc)
			for _, stmt := range doc.Stmts {
				decl, ok := stmt.(*ast.ComponentDecl)
				if !ok {
					continue
				}
				if decl.HasParens {
					// Parens form: the platform reads this body itself.
					continue
				}
				dot := strings.IndexByte(decl.Name, '.')
				if dot <= 0 {
					continue
				}
				ns := decl.Name[:dot]
				pkgName, ok := aliases[ns]
				if !ok {
					// An unmatched prefix is an error, not a skip: silently
					// ignoring these drops every override the file declares
					// and still produces a successful build with unstyled
					// output.
					c.error(decl.Pos, "extension namespace %q is not an imported library package; import it, e.g. import %s %q", ns, ns, "sngl:std")
					continue
				}
				local := decl.Name[dot+1:]
				// The library package, not the user symtab: a library package
				// reaches user scope only through an import, but a platform
				// extension targets its declaration either way.
				target := c.libPkg(pkgName)
				stdSym, ok := target.Symbols.LookupComponent(local)
				if !ok {
					c.error(decl.Pos, "extension %q references unknown component %q in sngl:%s", decl.Name, local, pkgName)
					continue
				}
				stdComp, ok := stdSym.(*ir.Component)
				if !ok {
					continue
				}
				// An override names the target it implements, here as
				// everywhere: a platform package is not an exception, so the
				// one rule covers a package's own overrides and a program's.
				if decl.Target == nil {
					c.error(decl.Pos, "override %q must name the target it implements: component %s[%s.platform]", decl.Name, decl.Name, name)
					continue
				}
				plat, kind, ok := c.resolveTargetIndex(decl.Target)
				if !ok {
					continue
				}
				if kind != ir.BuiltinPlatform || plat != name {
					// A platform package implements its own target. Naming
					// another would register an override that only merges when
					// this package loads, which is when the *other* target is
					// not the one being built.
					c.error(decl.Pos, "package for %q may not declare an override for %q", name, plat)
					continue
				}
				c.addOverrideBody(decl.Pos, stdComp, kind, plat, ns, local, decl.Body, false, nil)
			}
		}
	}
}

// addPlatformBody records body as comp's implementation for platform, or
// reports that one is already recorded. A duplicate is an error rather than a
// silent overwrite: two implementations of one component for one target are
// two answers to a question with one.
func (c *checker) addOverrideBody(pos ast.Pos, comp *ir.Component, kind ir.BuiltinKind, target, ns, local string, body ast.StmtBlock, user bool, selection []string) {
	overrides := &comp.PlatformOverrides
	if kind == ir.BuiltinLanguage {
		overrides = &comp.LanguageOverrides
	}
	if *overrides == nil {
		*overrides = map[string]ir.Body{}
	}
	if _, dup := (*overrides)[target]; dup {
		c.error(pos, "component %s.%s already has an implementation for %q", ns, local, target)
		return
	}
	// Reserve the key first so duplicate detection works even when the body
	// check appends nothing (an empty body).
	(*overrides)[target] = ir.Body{}
	c.pendingExtensions = append(c.pendingExtensions, pendingExtension{
		comp:      comp,
		platform:  target,
		kind:      kind,
		body:      body,
		user:      user,
		selection: selection,
	})
}

// selectProps is the props and events an override named, in the declaring
// component's order. A name it did not name is not in scope for its body.
func selectProps(comp *ir.Component, selection []string) ([]*ir.Prop, []*ir.EventDecl) {
	want := make(map[string]bool, len(selection))
	for _, n := range selection {
		want[n] = true
	}
	var props []*ir.Prop
	for _, p := range comp.Props {
		if want[p.Name] {
			props = append(props, p)
		}
	}
	var events []*ir.EventDecl
	for _, e := range comp.Events {
		if want["@"+e.Name] {
			events = append(events, e)
		}
	}
	return props, events
}

// collectExtensionVars pre-registers the vars and consts a platform extension
// body declares, the way pass1 does for an ordinary component body. Only state
// declarations are collected: a struct, enum, unit or func in an extension body
// is out of scope here and stays unbound.
func (c *checker) collectExtensionVars(body ast.StmtBlock) []*ir.Var {
	var out []*ir.Var
	for _, stmt := range body.Stmts {
		out = append(out, c.collectComponentVarDecl(stmt)...)
	}
	return out
}

// pendingExtension records a single `platform <name> { ... }` body that
// needs to be checked into IR and stashed under stdComp.PlatformOverrides.
// Body-checking is deferred until after user pass1 so user-declared symbols
// are in scope when the platform body resolves identifiers.
type pendingExtension struct {
	comp     *ir.Component
	platform string
	body     ast.StmtBlock
	// kind says which axis the target names -- a platform or a language --
	// and so which of the component's two override maps the checked body
	// belongs in.
	kind ir.BuiltinKind
	// selection is the props the override's body reads, when it listed them.
	// nil means it listed none and reads all of them.
	selection []string
	// user marks an override a program declared rather than a target package.
	// Its body is the program's own source and resolves in the program's
	// scope, where the file's imports are; a target package's body is
	// compiler-internal and deliberately resolves where they are not.
	user bool
}

// Runs after user pass1, so user-declared symbols are in scope. Each extension
// body is checked with the platform block temporarily installed as the stdlib
// component's AST.Body, then the slot is restored: the component is left with
// an empty Body, and lower's passPlatformExtensionBody swaps the active
// platform's in.
func (c *checker) checkPendingExtensions() {
	if len(c.pendingExtensions) == 0 {
		return
	}
	// Group by platform so each platform's own package is in scope while its
	// extension bodies are checked. It must be scoped per platform: several
	// platforms each declare a distinct `struct Options`.
	var order []string
	byPlatform := map[string][]pendingExtension{}
	for _, pe := range c.pendingExtensions {
		if _, seen := byPlatform[pe.platform]; !seen {
			order = append(order, pe.platform)
		}
		byPlatform[pe.platform] = append(byPlatform[pe.platform], pe)
	}
	// Check against the stdlib scope, not the user root: these bodies are
	// compiler-internal source, and resolving them where user declarations are
	// visible lets a user component capture a name the platform source depends
	// on (e.g. android.sngl's 52 bare `slot` references).
	savedScope := c.scope
	defer func() { c.scope = savedScope }()
	for _, platform := range order {
		// Platform bodies are written against the standard library they
		// extend (bare `slot`, `text`, …), which reaches user scope only by
		// import. Resolve them in the std package's own scope, which chains
		// to the ambient builtins, with the platform's own package between the
		// two — the same scope a `platform x { ... }` block in user code gets,
		// so a body names its package's declarations (bubbletea's Layout, its
		// JoinDir) as the instances that package registered rather than as
		// re-registered copies, which would not unify with the prop types
		// built from them.
		c.scope = c.stdlibPkg.Symbols.Root
		if ps := c.buildPlatformPkgScope(platform); ps != nil {
			ps.Parent = c.scope
			c.scope = ps
		}
		c.pushScope()
		libBodyScope := c.scope
		for _, pe := range byPlatform[platform] {
			// A program's override resolves against the program: the names its
			// body reaches are the ones its own file imported.
			if pe.user {
				c.scope = c.symtab.Root
				c.pushScope()
			} else {
				c.scope = libBodyScope
			}
			savedAST := pe.comp.AST.Body
			savedBody := pe.comp.Body
			savedVars := pe.comp.Vars
			savedProps, savedEvents := pe.comp.Props, pe.comp.Events
			// An override that listed the props it consumes reads those and no
			// others: the list is what makes the names in its body traceable
			// to a declaration rather than appearing from the surrounding
			// component. One that listed none reads them all.
			if pe.selection != nil {
				pe.comp.Props, pe.comp.Events = selectProps(pe.comp, pe.selection)
			}
			pe.comp.AST.Body = pe.body
			pe.comp.Body = nil
			// checkComponentBody declares comp.Vars into the body scope and
			// checkComponentVars looks the pre-registered var up there by
			// name, so the body's own state has to be collected before the
			// body is checked — pass1's collectComponentDecls only ever saw
			// the component's parenless stub. The list starts from the
			// component's own vars so a var the stdlib declaration made stays
			// visible to the override.
			vars := append(slices.Clip(savedVars), c.collectExtensionVars(pe.body)...)
			pe.comp.Vars = vars
			c.checkComponentBody(pe.comp)
			checked := ir.Body{Vars: pe.comp.Vars, Stmts: pe.comp.Body}
			if pe.kind == ir.BuiltinLanguage {
				pe.comp.LanguageOverrides[pe.platform] = checked
			} else {
				pe.comp.PlatformOverrides[pe.platform] = checked
			}
			pe.comp.AST.Body = savedAST
			pe.comp.Body = savedBody
			pe.comp.Vars = savedVars
			pe.comp.Props, pe.comp.Events = savedProps, savedEvents
			if pe.user {
				c.popScope()
			}
		}
		c.scope = libBodyScope
		c.popScope()
	}
}

// The max purity among the functions fn's body calls, PurityPure when it calls
// none. Drives the Phase 2b propagation above.
func highestCalledPurity(fn *ir.Func) ir.Purity {
	if fn == nil || len(fn.Block) == 0 {
		return ir.PurityPure
	}
	maxP := ir.PurityPure
	bump := func(p ir.Purity) {
		if p > maxP {
			maxP = p
		}
	}
	var walkExpr func(e ir.Expr)
	var walkStmt func(s ir.Stmt)
	walkExpr = func(e ir.Expr) {
		switch x := e.(type) {
		case nil:
			return
		case *ir.Call:
			if x.Func != nil {
				bump(x.Func.Purity)
			}
			if x.Callee != nil {
				walkExpr(x.Callee)
			}
			if x.Receiver != nil {
				walkExpr(x.Receiver)
			}
			for _, a := range x.Args {
				walkExpr(a.Value)
			}
		case *ir.Binary:
			walkExpr(x.Left)
			walkExpr(x.Right)
		case *ir.Unary:
			walkExpr(x.Operand)
		case *ir.Ternary:
			walkExpr(x.Cond)
			walkExpr(x.Then)
			walkExpr(x.Else)
		case *ir.Conversion:
			walkExpr(x.Operand)
		case *ir.Select:
			walkExpr(x.Operand)
		case *ir.Index:
			walkExpr(x.Operand)
			walkExpr(x.Idx)
		case *ir.ListLit:
			for _, el := range x.Elems {
				walkExpr(el)
			}
		case *ir.StructLit:
			for _, f := range x.Fields {
				walkExpr(f.Value)
			}
		case *ir.MapLitIR:
			for _, kv := range x.Entries {
				walkExpr(kv.Key)
				walkExpr(kv.Value)
			}
		case *ir.Spread:
			walkExpr(x.Operand)
		case *ir.Lambda:
			if x.Func != nil {
				for _, s := range x.Func.Block {
					walkStmt(s)
				}
			}
		case *ir.Closure:
			if x.Func != nil {
				for _, s := range x.Func.Block {
					walkStmt(s)
				}
			}
			if x.State != nil {
				walkExpr(x.State)
			}
		}
	}
	walkStmt = func(s ir.Stmt) {
		switch x := s.(type) {
		case nil:
			return
		case *ir.Return:
			walkExpr(x.Value)
		case *ir.LocalVar:
			walkExpr(x.Init)
		case *ir.Assign:
			walkExpr(x.Value)
			walkExpr(x.Target)
		case *ir.If:
			walkExpr(x.Cond)
			for _, c := range x.Body {
				walkStmt(c)
			}
			for _, c := range x.Else {
				walkStmt(c)
			}
		case *ir.For:
			walkExpr(x.Iter)
			for _, c := range x.Body {
				walkStmt(c)
			}
			for _, c := range x.Else {
				walkStmt(c)
			}
		case *ir.CallStmt:
			walkExpr(x.Call)
		case *ir.Toggle:
			walkExpr(x.Target)
		case *ir.Emit:
			for _, a := range x.Args {
				walkExpr(a.Value)
			}
		case *ir.NodeInst:
			for _, p := range x.Props {
				walkExpr(p.Value)
			}
			for _, h := range x.Handlers {
				if h.Func != nil {
					for _, c := range h.Func.Block {
						walkStmt(c)
					}
				}
			}
			for _, c := range x.Children {
				walkStmt(c)
			}
		case *ir.SlotInst:
			for _, c := range x.Children {
				walkStmt(c)
			}
		case *ir.ErrorBoundary:
			for _, c := range x.Children {
				walkStmt(c)
			}
		case *ir.ContextProvider:
			for _, c := range x.Children {
				walkStmt(c)
			}
		}
	}
	for _, s := range fn.Block {
		walkStmt(s)
	}
	return maxP
}

// isPrimitiveTypeKind reports whether k is a simple value-type kind safe to
// use for inferred wrapper return types. Restricted to dodge the
// color-struct-vs-primitive ambiguity noted in checkStdlibFuncBody.
func isPrimitiveTypeKind(k ir.TypeKind) bool {
	switch k {
	case ir.TypeBool, ir.TypeInt, ir.TypeFloat, ir.TypeString:
		return true
	}
	return false
}

// registerStdlibContextDecl registers a stdlib `context #name(default)` decl.
// The *ir.Context is declared in the current scope (stdlib scope) so user
// source can read it as an identifier, and appended to c.pkg.Contexts so the
// interpreter and codegen discover it alongside user-declared contexts.
func (c *checker) registerStdlibContextDecl(s *ast.CallStmt) {
	name := s.Call.ID
	ctx := &ir.Context{AST: s, Name: name, Stdlib: true}
	if name == "" {
		c.error(s.Pos, "stdlib context decl requires #identifier")
		return
	}
	if _, exists := c.scope.LookupLocal(name); exists {
		// Already declared (e.g. duplicate stdlib file); skip silently.
		return
	}
	args := s.Call.Args.Args
	if len(args) != 1 {
		c.error(s.Pos, "stdlib context decl requires exactly one default value")
		c.pkg.Contexts = append(c.pkg.Contexts, ctx)
		c.bindLib(s.Pos, c.scope, ctx)
		return
	}
	a, isArg := args[0].(ast.Arg)
	if !isArg || a.Name != "" {
		c.error(s.Pos, "stdlib context default must be positional, not named")
		c.pkg.Contexts = append(c.pkg.Contexts, ctx)
		c.bindLib(s.Pos, c.scope, ctx)
		return
	}
	// A context default is an initializer expression (see registerContextDecl),
	// not a compile-time constant.
	def := c.checkExpr(a.Value)
	ctx.Default = def
	if def != nil {
		ctx.Typ = def.ExprType()
	}
	c.pkg.Contexts = append(c.pkg.Contexts, ctx)
	c.bindLib(s.Pos, c.scope, ctx)
}

func (c *checker) registerStdlibComponent(comp *ast.ComponentDecl, pkg *ir.Package) {
	irComp := &ir.Component{
		AST:    comp,
		Name:   comp.Name,
		Stdlib: true,
		Pkg:    c.libPkgName,
	}
	c.applyMarks(comp, irComp)

	for _, p := range comp.Props.Props {
		switch pd := p.(type) {
		case ast.Param:
			typ := c.resolveType(pd.Type)
			var def ir.Expr
			if pd.Default != nil {
				// Placeholder; stdlib prop defaults don't need full checking.
				def = &ir.Literal{Type: typ}
			}
			prop := &ir.Prop{
				Name:          pd.Name,
				Type:          typ,
				Default:       def,
				Bidirectional: pd.Bidirectional,
			}
			c.applyParamMarks(pd, prop)
			irComp.Props = append(irComp.Props, prop)
		case ast.EventDecl:
			evt := &ir.EventDecl{
				Name: pd.Name,
				Type: c.resolveType(pd.Type),
			}
			c.applyEventMarks(pd, evt)
			irComp.Events = append(irComp.Events, evt)
		}
	}

	finishTreeMarks(comp, irComp, pkg)
	c.finishWildcardMarks(comp.Pos, irComp)
	if comp.ChildrenType != nil {
		irComp.ChildrenType = c.resolveType(comp.ChildrenType)
	}

	c.bindLib(comp.Pos, c.scope, irComp)
	// Stdlib package for qualified sngl.Component access.
	pkg.Components = append(pkg.Components, irComp)
	c.bindLib(comp.Pos, pkg.Symbols.Root, irComp)
}

// Platform sources use a library import's alias as the `component <alias>.X`
// extension prefix.
func libImportAliases(doc *ast.Document) map[string]string {
	out := map[string]string{}
	for _, stmt := range doc.Stmts {
		imp, ok := stmt.(*ast.Import)
		if !ok {
			continue
		}
		scheme, uri := ParseScheme(imp.Path)
		if scheme != "sngl" {
			continue
		}
		if imp.IsDot() {
			// Flattened: the components are unqualified, so there is no
			// prefix to declare an extension against.
			continue
		}
		alias := imp.Alias
		if alias == "" {
			alias = NamespaceFromPath(imp.Path)
		}
		out[alias] = uri
	}
	return out
}

// Loaded library packages for callers outside a check — the documentation
// tools and the language server. A lib package is immutable once built and
// costs a full load, so one instance is shared. The diagnostics are cached
// with it: the load happens once, so a later caller asking for them cannot
// re-run it.
var (
	libPkgMu    sync.Mutex
	libPkgCache = map[string]libPkgEntry{}
)

type libPkgEntry struct {
	pkg   *ir.Package
	diags []ir.Diagnostic
}

// LibPackage returns the built IR of the embedded package `sngl:<name>`, or
// nil when no such package exists.
//
// A mark states its fact on the IR, so a caller that wants to know what a
// declaration was marked has to load the package that declares it — the parsed
// source says only what was written. Loading is memoized: the packages are the
// compiler's own and do not change within a process.
func LibPackage(name string) *ir.Package {
	pkg, _ := CheckLibPackage(name)
	return pkg
}

// CheckLibPackage is LibPackage plus the diagnostics the load produced, for a
// caller reporting on the package rather than reading it — `sngl check
// sngl:platforms/gtk4`.
//
// Loading a lib package is not the same as checking its source as a document:
// it runs with the lib-source rules that permit the `sngl:internal/` imports
// a platform package writes, and it sees the declarations a target synthesizes
// and never wrote to a file. Both are why this is the only way to check one.
func CheckLibPackage(name string) (*ir.Package, []ir.Diagnostic) {
	if !HasPackage(name) && registeredTarget(name) == nil {
		return nil, nil
	}
	libPkgMu.Lock()
	defer libPkgMu.Unlock()
	if e, ok := libPkgCache[name]; ok {
		return e.pkg, e.diags
	}
	// Through LibSources so the IR is built from the same documents
	// PackageSource hands back: a mark is read off the IR and its declaration
	// then looked up in the source by pointer.
	cfg := &Config{LibSources: map[string][]*ast.Document{name: PackageSource(name)}}
	c := newChecker(&ast.Document{}, cfg)
	pkg := c.libPkg(name)
	libPkgCache[name] = libPkgEntry{pkg: pkg, diags: c.diags}
	return pkg, c.diags
}

// Packages lists the `sngl:` packages this process can reach: the public
// tiers embedded under lib/, plus the package each registered target serves
// for itself. A target that cannot serve one — gtk4 with no introspection
// data — contributes nothing, so the list is what is actually addressable
// here rather than what the build could in principle offer.
func Packages() []string {
	out := lib.PublicPackages()
	targetPkgMu.RLock()
	names := slices.Collect(maps.Keys(targetPkgs))
	targetPkgMu.RUnlock()
	for _, name := range names {
		if len(PackageSource(name)) > 0 {
			out = append(out, name)
		}
	}
	slices.Sort(out)
	return out
}

// OptionsStruct returns the #[options]-marked struct of `sngl:<name>`, or
// nil when the package declares none. The mark, not the declaration's name, is
// what a target's option schema is found by.
func OptionsStruct(name string) *ir.StructDef {
	pkg := LibPackage(name)
	if pkg == nil {
		return nil
	}
	for _, sd := range pkg.Structs {
		if sd.Options {
			return sd
		}
	}
	return nil
}
