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
// The platform/ and language/ tiers are left out: they are per-target and
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
	_, _, ok := targetTierName(pkg)
	return ok
}

// targetTierName splits a target package name into the target it belongs to and
// the identity that target carries. The two tiers are one mechanism -- a
// package a plugin serves, holding the overrides a build targeting it loads --
// so what tells them apart is the kind, not a separate code path.
func targetTierName(pkg string) (string, ir.BuiltinKind, bool) {
	if name, ok := strings.CutPrefix(pkg, "platform/"); ok {
		return name, ir.BuiltinPlatform, true
	}
	if name, ok := strings.CutPrefix(pkg, "language/"); ok {
		return name, ir.BuiltinLanguage, true
	}
	return "", ir.BuiltinNone, false
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
// access everywhere, and sngl:ui, which the checker itself reads to find
// the #[builtin]-marked window/slot components. Any other library package
// loads on first import (libPkg).
//
// Being ambient is the only way sngl:builtin is special. sngl:ui is
// eager rather than special: it is loaded here because the checker needs its
// node components to build ir.Window and ir.Timer at all, not because user
// code sees it differently from sngl:ui/draw.
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
	// Loaded but not bound: these carry node kinds the checker dispatches a
	// visual node on (`timer` in sngl:time) and type kinds a foreign importer
	// hands out where it has no scope to resolve a name in (`duration`). A
	// kind is registered when the marked declaration is, so the package has to
	// load even when nothing imported it. Nothing here puts those names in
	// scope -- a program still imports sngl:time to write a timer.
	c.libPkg(timePkg)
	c.libPkg(drawIntrinsicsPkg)
	return builtinPkg, c.libPkg("ui")
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
	savedScope, savedTab := c.scope, c.symtab
	c.scope, c.symtab = pkg.Symbols.Root, pkg.Symbols
	defer func() { c.scope, c.symtab = savedScope, savedTab }()
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
}

// libDocs returns the parsed source of lib package name: what lib/ embeds,
// plus what the target of that name synthesizes (providedDocs). Config.LibSources
// substitutes the whole package instead, for the in-test stubs.
func (c *checker) libDocs(name string) []*ast.Document {
	var docs []*ast.Document
	if c.cfg != nil {
		if src, ok := c.cfg.LibSources[name]; ok {
			docs = src
		}
	}
	if docs == nil {
		docs = append(PackageDocsFor(name), c.providedDocs(name)...)
	}
	return append(docs, c.identityDoc(name)...)
}

// identityFile is the name the generated identity declaration is mounted
// under. The prefix keeps it clear of anything a plugin would write, and it
// reads as a file of the package because it is one.
const identityFile = "sngl__identity.sngl"

// identityDoc is the target identity mounted into a target's package: one
// generated file declaring the const that `[platform]` names.
//
// It is added here rather than to the fs.FS a plugin serves, because that is
// not the only way a target package's source arrives -- Config.LibSources
// substitutes the whole package for the in-test stubs, and a stub is as much a
// target as a plugin is. This is the one place they meet.
//
// Generated rather than injected into the loaded IR: the identity is then an
// ordinary declaration of the package, parsed, registered and documented like
// everything else it serves, and nothing downstream has to know the compiler
// wrote it.
func (c *checker) identityDoc(pkgName string) []*ast.Document {
	member, ok := targetNamespaceMember(pkgName)
	if !ok {
		return nil
	}
	name, _ := targetNamespaceName(pkgName)
	// Qualified, not dot-imported: imports are file scope, so a dot import
	// here would collide with nothing -- it would just lift the package's
	// names into a file that uses one mark and declares one const.
	src := fmt.Sprintf(`import macro "sngl:macro"

// The %s %s, as a value: compare %s against it. The comparison folds at
// build time, so the branch not taken is removed.
#[macro.identity]
const %s = %q
`, name, member, targetConstName(member), member, name)
	doc, err := parser.Parse(identityFile, []byte(src))
	if err != nil {
		panic("sngl: generated target identity does not parse: " + err.Error())
	}
	return []*ast.Document{doc}
}

// targetNamespaceMember is the identity const a package of this name carries:
// `platform` for a platform tier, `language` for a language one.
func targetNamespaceMember(pkgName string) (string, bool) {
	switch {
	case strings.HasPrefix(pkgName, "platform/"):
		return "platform", true
	case strings.HasPrefix(pkgName, "language/"):
		return "language", true
	}
	return "", false
}

// providedDocs is the source the registered target of this package name
// provides, or nil for any other package.
func (c *checker) providedDocs(name string) []*ast.Document {
	if c.cfg == nil {
		return nil
	}
	// Both tiers: a language declares its foreign-type surface the way a
	// platform declares its widgets. Within its own tier, though -- a language
	// asked for its platform package answers with itself, and the package then
	// exists under a name no target has.
	target, kind, ok := targetTierName(name)
	if !ok {
		return nil
	}
	// This config's targets and no others. A check is defined by the targets
	// it was configured with, so a target absent from them contributes
	// nothing here even when it is registered process-wide -- PackageSource is
	// where the registry answers, for readers that have no config to carry.
	//
	// Memoized for the life of this checker and no longer. ProvidedDocs
	// re-reads and re-parses on every call, and one check asks about the same
	// package around 26 times -- hasLibPkg on each lookup, targetPkgScope,
	// libDocs, libPkg, mergeTargetExtensions. Parsing gtk4's synthesized
	// widget package that many times was the single largest cost in the
	// checker's own test package.
	//
	// Per-checker is the scope the freshness constraint asks for: what must
	// not be shared is documents across *checks*, since a check splices
	// platform bodies into them and a target may be reconfigured between
	// checks. Within one check the callers are meant to see the same ASTs --
	// they previously got a different *ast.Document for the same package
	// depending on which of them asked.
	if docs, ok := c.providedCache[name]; ok {
		return slices.Clone(docs)
	}
	docs := ProvidedDocs(c.lookupTargetIn(target, kind))
	if c.providedCache == nil {
		c.providedCache = map[string][]*ast.Document{}
	}
	c.providedCache[name] = docs
	return slices.Clone(docs)
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

// registeredTargets is the language and platform lists a Config carries,
// recovered from the packages the codegen registry recorded. The registry hands
// this package the target itself, so which of the two a target is, is the
// question its own interface answers.
func registeredTargets() ([]ir.Language, []ir.Platform) {
	targetPkgMu.RLock()
	defer targetPkgMu.RUnlock()
	var langs []ir.Language
	var plats []ir.Platform
	for _, t := range targetPkgs {
		if l, ok := t.(ir.Language); ok {
			langs = append(langs, l)
		}
		if p, ok := t.(ir.Platform); ok {
			plats = append(plats, p)
		}
	}
	return langs, plats
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
	// the fs.FS behind them can be shared between checks. Callers that want
	// one parse memoize at their own scope: PackageSource for the readers
	// outside a check, checker.providedDocs for the length of one.
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
// lib packages import each other (sngl:ui/draw is written against sngl:ui),
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
	// name the user's dot imports lifted — `import tree "sngl:tree"`
	// against std's `tree` component.
	savedTopLevel := c.topLevel
	c.topLevel = nil
	defer func() { c.topLevel = savedTopLevel }()
	// Deferred const initialisers are per declaration set for the same reason:
	// this package's are checked before it finishes loading, and the enclosing
	// set is put back untouched.
	savedConstInits := c.pendingConstInits
	c.pendingConstInits = nil
	defer func() { c.pendingConstInits = savedConstInits }()
	// pass1 builds the replace map from the documents it is given, so the
	// enclosing package's is put back: a library package loads part-way
	// through it, and its own `=>` redirects are not the program's.
	savedReplaces := c.replaces
	defer func() { c.replaces = savedReplaces }()
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
// which is the target's own name: sngl:platform/html is `html`.
func targetNamespaceName(pkgName string) (string, bool) {
	for _, prefix := range []string{"platform/", "language/"} {
		if name, ok := strings.CutPrefix(pkgName, prefix); ok {
			return name, true
		}
	}
	return "", false
}

// loadStdlibPackage, including the nested loads an import inside lib/ starts,
// so the counter covers transitive loads too.
// Library source is source a package load is checking. Nothing else is: the
// one caller that used to check lib/ as its own document was LoadStdlib, whose
// merged corpus had no package to belong to.
func (c *checker) inLibSource() bool { return c.libDepth > 0 }

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

	// The same pass1 a program's package gets. Registration order -- every
	// type name before any field that could refer to one, components before
	// struct bodies, const initialisers last -- is a property of a package and
	// not of the tier it came from, and this loader used to state it a second
	// time in a different order.
	//
	// Running it means running everything it resets, which is why the restore
	// covers more than the documents: see enterPackage.
	defer c.enterPackage(docs)()
	c.pass1()
	// A field default is a value expression, so it waits until every name it
	// could refer to is registered. pass2 does this for a program; a library
	// package does not get one, so it happens here.
	c.checkStructFieldDefaults()

	// PluralKey's Go runtime type is qualified (i18n.PluralKey) so IRTypeToGo
	// emits it rather than the bare SNGL name. A #[foreign] mark cannot say
	// this: a marked declaration is the program's own, and the Go emitter
	// deliberately ignores a marked name for that reason.
	for _, sd := range stdlibPkg.Structs {
		if sd.Foreign.Name == "" && sd.Name == "PluralKey" {
			sd.Foreign.Name = "i18n.PluralKey"
		}
	}

	// Library funcs are not body-checked by pass2, which walks a program's own
	// declarations, so their bodies are checked below. An expression body
	// (`=> expr`) is lowered into ir.Block, and a block body is lowered too so
	// the optimizer can fold through it; a bodyless signature has nothing to
	// check.
	type stdlibFuncBody struct {
		ast *ast.FuncDef
		fn  *ir.Func
	}
	var pendingBodies []stdlibFuncBody
	for _, fn := range stdlibPkg.Funcs {
		if fn.AST != nil && (fn.AST.Body != nil || fn.AST.Block.IsDefined()) {
			pendingBodies = append(pendingBodies, stdlibFuncBody{ast: fn.AST, fn: fn})
		}
	}

	// Phase 2: check deferred stdlib expression-body wrappers. Run last so
	// that bodies can read freshly-registered context decls (e.g. the
	// `#locale` context used by i18n.* wrappers).
	for _, pb := range pendingBodies {
		c.checkFuncBody(pb.fn)
	}

	// Phase 2b: refine stdlib function purity by propagating from called
	// functions. The auto-Pure default registerFunc gives library source is a
	// placeholder;
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
	// The body's own declarations were collected when the component was
	// registered, as a program's are: registerComponent does that for every
	// tier, so this only has to check what is already there.
	//
	// Restricted to the target tiers: sngl:ui's components carry bodies too
	// (the _example_* documentation fixtures), and checking those here would
	// resolve them against the library's scope rather than a program's.
	if targetTier(pkgName) {
		for _, irComp := range stdlibPkg.Components {
			if strings.Contains(irComp.Name, ".") || !irComp.AST.Body.IsDefined() {
				continue
			}
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

// TargetNode returns the build-directive node `sngl:<uri>` declares -- the
// component an output block writes to name that target -- or nil when the
// package declares none. A target package names its node after itself, which
// is what an output block writes.
func TargetNode(uri string) *ir.Component {
	pkg := LibPackage(uri)
	if pkg == nil || pkg.Symbols == nil {
		return nil
	}
	name := uri
	if i := strings.LastIndexByte(uri, '/'); i >= 0 {
		name = uri[i+1:]
	}
	sym, ok := pkg.Symbols.LookupRootComponent(name)
	if !ok {
		return nil
	}
	comp, _ := sym.(*ir.Component)
	return comp
}

// OutputNode is the #[builtin("output")] component of `sngl:builtin`: the root
// of a build directive, whose props are the options every target accepts. Found
// by its mark, like every other built-in.
func OutputNode() *ir.Component {
	pkg := LibPackage("builtin")
	if pkg == nil {
		return nil
	}
	for _, comp := range pkg.Components {
		if comp.Builtin == ir.BuiltinOutput {
			return comp
		}
	}
	return nil
}

// i18nPkg declares the translation entry points, the locale-aware primitives
// behind them, and the PluralKey those are keyed by.
const i18nPkg = "i18n"

// timePkg declares the clock: the `timer` node kind a visual tree dispatches
// on, and the `duration` a foreign importer hands out.
const timePkg = "time"

// drawIntrinsicsPkg declares the primitives passCanvas emits. No SNGL source
// imports it, so loadStdlib is the only thing that loads it -- and without the
// load, the signatures the pass reads off these declarations do not exist.
const drawIntrinsicsPkg = "internal/draw"

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
	if t == nil || t.Kind != ir.TypeStruct {
		return false
	}
	sd, ok := t.Decl.(*ir.StructDef)
	if !ok {
		return false
	}
	// By declaration site rather than by pointer. A directory import is checked
	// by a sub-checker with its own load of the library, so the Macro it
	// resolved is a different *StructDef from this checker's — equal in every
	// way that matters and not the same pointer. Comparing the two made a mark
	// in an imported package "not a macro".
	return sd.Name == macroTypeName && sd.Pkg == "sngl:"+irPkg
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

// publishIntrinsic records what a #[intrinsic] declaration says, for the passes
// that need a signature with no declaration in hand (see ir.RegisterIntrinsic).
// The purity is the mark's, already applied by markIntrinsic.
func (c *checker) publishIntrinsic(fn *ir.Func) {
	// Only a library package publishes, which is what lets RegisterIntrinsic
	// panic on a duplicate rather than diagnose one. User source importing
	// sngl:internal/marks is refused, but a refusal is a diagnostic and
	// checking continues, so the mark still stamps -- see
	// cmd/sngl/testdata/check_internal_import.txt, which exists because this
	// reaching codegen used to panic.
	if !c.inLibSource() {
		return
	}
	ir.RegisterIntrinsic(ir.IntrinsicDef{
		Name:            fn.Intrinsic,
		Params:          fn.Params,
		Return:          fn.Return,
		TypeParams:      intrinsicTypeParamNames(fn),
		Purity:          fn.Purity,
		MutatesReceiver: fn.MutatesReceiver,
		Pkg:             c.libPkgName,
		DeclaredAs:      funcDeclName(fn),
	})
}

// funcDeclName is how a declaration is spelled: `list.push` for a method,
// `tr` for a free function.
func funcDeclName(fn *ir.Func) string {
	if fn.Receiver != "" {
		return fn.Receiver + "." + fn.Name
	}
	return fn.Name
}

// intrinsicTypeParamNames is the order Instantiate binds in: the receiver's
// parameters first, then the method's own -- T then U for `list<T>.map<U>`.
func intrinsicTypeParamNames(fn *ir.Func) []string {
	if len(fn.RecvTypeParams) == 0 && len(fn.TypeParams) == 0 {
		return nil
	}
	names := make([]string, 0, len(fn.RecvTypeParams)+len(fn.TypeParams))
	for _, tp := range fn.RecvTypeParams {
		names = append(names, tp.Name)
	}
	for _, tp := range fn.TypeParams {
		names = append(names, tp.Name)
	}
	return names
}

// targetPackages is every target package this check loads. Building for a
// target is an `import _ "sngl:platform/<it>"` nobody wrote, and these are
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
			addPkg("platform/" + t.Platform)
		}
		if t.Language != "" {
			addPkg("language/" + t.Language)
		}
	}

	// Explicit imports, which nothing overrides. Through the same `=>`
	// resolution registerImport applies before it looks at the scheme, so a
	// replace pointing at a target package counts and one pointing away from
	// it does not -- pass1 has not built its map yet, and this is the same
	// scan it will do.
	replaces := map[string]string{}
	for _, stmt := range c.stmts() {
		if imp, ok := stmt.(*ast.Import); ok && imp.Replace != "" {
			if _, dup := replaces[imp.Path]; !dup {
				replaces[imp.Path] = imp.Replace
			}
		}
	}
	maps.Copy(replaces, c.cfg.Replaces)

	for _, stmt := range c.stmts() {
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
			addPkg("platform/" + p.PlatformIdentifier())
		}
		for _, l := range c.cfg.Languages {
			addPkg("language/" + l.LanguageIdentifier())
		}
	}
	return out
}

// resolvedTargets is what this check builds for: what the caller named wins
// over what the document's own `output` blocks declare, which is the order
// `sngl build` resolves them in. A caller that named nothing and a document
// that declares nothing give none, and every registered target loads.
//
// The name match on `output` is a pre-scope heuristic and stays one: this runs
// before pass1, so there is no scope to resolve the mark through, and reading
// one name too many only loads a package.
//
// Read from the AST rather than from pkg.Outputs, which does not exist yet:
// the overrides have to be spliced before anything reads a stdlib component's
// body, and that is earlier than checking an output block.
func (c *checker) resolvedTargets() []ir.StaticTarget {
	if len(c.cfg.Targets) > 0 {
		return c.cfg.Targets
	}
	var declared []ir.StaticTarget
	for _, stmt := range c.stmts() {
		switch s := stmt.(type) {
		case *ast.ComponentDecl:
			if s.Name == "output" {
				return nil
			}
		case *ast.VisualNode:
			if visualNodeTarget(s) == "output" {
				declared = append(declared, declaredOutputTargets(s)...)
			}
		}
	}
	return declared
}

// mergeTargetExtensions collects the overrides one target's package declares --
// `component sngl.X` and `func pkg.f[target]` alike -- into the declaration
// each names, under the target it implements. ir.Specialize swaps the entry
// for the target being built into the declaration's own body before any other
// pass runs.
//
// A target's package is loaded the way a side-effect import is, and for the
// same reason: building for a target is an `import _ "sngl:platform/<it>"`
// nobody wrote, and a language's package is one of those imports too -- which
// is what makes a language able to implement a declaration the library leaves
// open (`func http.get[language]`) rather than only describe types. So this
// runs for the build target, and for any target package the program imported
// itself -- which is how a program asks to be held to a target's rules without
// naming one of its declarations.
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
	name, tier, ok := targetTierName(pkgName)
	if !ok {
		return
	}
	if p := c.lookupTargetIn(name, tier); p != nil && targetUnavailable(p) != nil {
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
			// Its own declarations first, so the package's source reads them
			// the way it does everywhere else in the package: `[platform]` is
			// the identity const the compiler injects into it, and resolving
			// the bare name against the program's scope would find the ambient
			// `platform` type instead.
			for _, sym := range pkg.Symbols.Root.Symbols {
				c.scope.Replace(sym)
			}
			// Replace for the reason targetPkgScope gives: the package's own
			// build-directive node shares the package's name.
			c.scope.Replace(&ir.Namespace{Name: name, Pkg: pkg})
		}
		defer c.popScope()
		for _, doc := range c.libDocs(pkgName) {
			// The extension prefix is whatever alias this document imported the
			// stdlib under. Platform docs are not registered into the checker's
			// scope, so resolve it from the document's own imports.
			aliases := libImportAliases(doc)
			for _, stmt := range doc.Stmts {
				if fd, isFunc := stmt.(*ast.FuncDef); isFunc {
					// The package's own overrides. Resolved here rather than
					// left to mergeFuncOverride, which would resolve them in
					// the program's scope: the name a target package overrides
					// comes from its own imports.
					if fd.Target != nil {
						if base := c.libFuncOverrideBase(fd, aliases); base != nil {
							c.mergeFuncOverride(fd, base, pkgName)
						}
					}
					continue
				}
				decl, ok := stmt.(*ast.ComponentDecl)
				if !ok {
					continue
				}
				// Parens with nothing in them are the marker for a
				// component the platform's own codegen reads by name rather
				// than from an override body -- android's `component ui.input()`
				// and its three siblings, which #213 is deleting.
				//
				// A parens form that *names* props is a prop selection, and
				// testing HasParens alone dropped one here with no override
				// registered and no diagnostic: the body simply vanished.
				if decl.HasParens && len(decl.Props.Props) == 0 && decl.ChildrenType == nil {
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
					c.error(decl.Pos, "extension namespace %q is not an imported library package; import it, e.g. import %s %q", ns, ns, "sngl:ui")
					continue
				}
				local := decl.Name[dot+1:]
				// The library package, not the user symtab: a library package
				// reaches user scope only through an import, but a platform
				// extension targets its declaration either way.
				target := c.libPkg(pkgName)
				stdSym, ok := target.Symbols.LookupRootComponent(local)
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
					c.error(decl.Pos, "override %q must name the target it implements: component %s[%s.%s]", decl.Name, decl.Name, name, targetTierMember(tier))
					continue
				}
				plat, kind, ok := c.resolveTargetIndex(decl.Target)
				if !ok {
					continue
				}
				if kind != tier || plat != name {
					// A target package implements its own target. Naming
					// another would register an override that only merges when
					// this package loads, which is when the *other* target is
					// not the one being built.
					c.error(decl.Pos, "package for %q may not declare an override for %q", name, plat)
					continue
				}
				// A selection is read the same way a program's override has
				// it read: the base owns the prop types, and an entry names
				// one of them.
				selection, ok := c.overrideSelection(decl, stdComp)
				if !ok {
					continue
				}
				c.addOverrideBody(decl.Pos, stdComp, kind, plat, ns, local, decl.Body, false, selection)
			}
		}
	}
}

// qualifiedComponentName is how an override names what it overrides: qualified
// by the namespace it reached the declaration through, or bare for one this
// package declares itself, where there is no namespace to name.
func qualifiedComponentName(ns, local string) string {
	if ns == "" {
		return local
	}
	return ns + "." + local
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
		c.error(pos, "component %s already has an implementation for %q", qualifiedComponentName(ns, local), target)
		return
	}
	// Reserve the key first so duplicate detection works even when the body
	// check appends nothing (an empty body).
	(*overrides)[target] = ir.Body{}
	// An override is the body a target renders, so there is nothing left for
	// one with no body to be -- and it would satisfy the base declaration's
	// own no-body rule while rendering nothing, which is the silence that rule
	// exists to refuse. Reported after the key is reserved, so the base is not
	// reported too: a supplier was written, and this is the one thing wrong.
	if !body.IsDefined() {
		c.error(pos, "override %s for %q has no body: an override is the body the target renders", qualifiedComponentName(ns, local), target)
		return
	}
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
// body declares, the way pass1 does for an ordinary component body. A func in
// an extension body is out of scope here and stays unbound; the type
// declarations are collectExtensionBodyDecls'.
func (c *checker) collectExtensionVars(body ast.StmtBlock) []*ir.Var {
	var out []*ir.Var
	for _, stmt := range body.Stmts {
		out = append(out, c.collectComponentVarDecl(stmt)...)
	}
	return out
}

func (c *checker) collectExtensionBodyDecls(body ast.StmtBlock) []ir.Symbol {
	var out []ir.Symbol
	for _, stmt := range body.Stmts {
		switch stmt.(type) {
		case *ast.StructDef, *ast.EnumDef, *ast.UnitDef, *ast.ComponentDecl:
			if sym := c.registerBodyDecl(stmt); sym != nil {
				out = append(out, sym)
			}
		}
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
	// Group by target package so each target's own is in scope while its
	// extension bodies are checked. It must be scoped per target: several of
	// them each declare a distinct `struct Options`. Keyed by the package
	// rather than the bare name, because the two tiers share a namespace and a
	// language of some name is not the platform of it.
	var order []string
	byTarget := map[string][]pendingExtension{}
	for _, pe := range c.pendingExtensions {
		uri := targetTierMember(pe.kind) + "/" + pe.platform
		if _, seen := byTarget[uri]; !seen {
			order = append(order, uri)
		}
		byTarget[uri] = append(byTarget[uri], pe)
	}
	// Check against the stdlib scope, not the user root: these bodies are
	// compiler-internal source, and resolving them where user declarations are
	// visible lets a user component capture a name the platform source depends
	// on (e.g. android.sngl's 52 bare `slot` references).
	savedScope := c.scope
	defer func() { c.scope = savedScope }()
	for _, uri := range order {
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
		if ps := c.targetPkgScope(uri); ps != nil {
			ps.Parent = c.scope
			c.scope = ps
		}
		c.pushScope()
		libBodyScope := c.scope
		for _, pe := range byTarget[uri] {
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
			savedBodyDecls := pe.comp.BodyDecls
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
			c.pushScope()
			bodyDecls := c.collectExtensionBodyDecls(pe.body)
			c.popScope()
			pe.comp.BodyDecls = append(slices.Clip(savedBodyDecls), bodyDecls...)
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
			pe.comp.BodyDecls = savedBodyDecls
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
// sngl:platform/gtk4`.
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
	// The registered targets, because a target package imports other target
	// packages -- html's source imports sngl:language/js -- and resolving one
	// is what says the language exists. Without them `sngl check
	// sngl:platform/html` reported `unknown language "js"` about the platform's
	// own import, while the same package checked clean inside a build.
	langs, plats := registeredTargets()
	cfg := &Config{
		LibSources: map[string][]*ast.Document{name: PackageSource(name)},
		Languages:  langs,
		Platforms:  plats,
	}
	c := newChecker(nil, cfg)
	pkg := c.libPkg(name)
	// A membership check a library body deferred is drained by the pass2 of
	// the program that loaded the package, and this entry point has no
	// program. Nothing to infer first: a library declaration names its family.
	c.runTreeChecks()
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
