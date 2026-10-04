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
				// Named by its path within lib/, not by its base name. A
				// position's file is what `fileScopesByName` keys a pass2 body
				// back to its imports by, so a library file sharing a base
				// name with one of the program's own resolved through the
				// program's scope: a user file called draw.sngl made
				// `math.pi` in lib/ui/draw/draw.sngl undefined.
				doc, err := parser.Parse(name, data)
				if err != nil {
					panic(fmt.Sprintf("sngl: parsing stdlib file %q: %v", name, err))
				}
				// A plugin is not a declaration library: it is checked as a
				// package of its own when its scheme is asked for.
				if !targetTier(tier) && !strings.HasPrefix(tier, SchemeTier) {
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
// node components to build ir.Window at all, not because user
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
	return docs
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
	// re-reads the target's files on every call (the parse behind them is
	// shared, parseProvided), and one check asks about the same package
	// around 26 times -- hasLibPkg on each lookup, targetPkgScope, libDocs,
	// libPkg, mergeTargetExtensions.
	//
	// Per-checker because a target may be reconfigured between checks, and a
	// check must see one answer throughout: the callers are meant to see the
	// same ASTs -- they previously got a different *ast.Document for the same
	// package depending on which of them asked.
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
	// The package a file's name is qualified by, from the target's own
	// identity: `ProvidedDocs` is handed the target and not its URI.
	prefix := "target"
	switch id := t.(type) {
	case ir.Platform:
		prefix = "platform/" + id.PlatformIdentifier()
	case ir.Language:
		prefix = "language/" + id.LanguageIdentifier()
	}
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
		// Qualified for the reason the embedded tiers are: a target's own
		// source must not share a file name with the program's.
		docs = append(docs, parseProvided(prefix+"/"+e.Name(), data))
	}
	return docs
}

// providedParses memoizes parseProvided for the life of the process.
var (
	providedParseMu sync.Mutex
	providedParses  = map[providedKey]*ast.Document{}
)

// providedKey is a file by name and content. The content is part of the key
// because a target can be reconfigured to serve a different package -- gtk4
// against another GIR generates another widget file under the same name --
// and that has to be a new parse, while the same bytes are the same AST.
type providedKey struct{ name, data string }

// parseProvided parses one file of a target's package, once per content.
//
// Shared across checks the way the embedded tiers (parseStdlibDocs) always
// have been: nothing downstream of the parser writes to an AST, which
// TestProvidedDocsSurviveChecks holds it to. Re-parsing was a tenth of the
// bytes a build allocated -- html.sngl alone is 31KB of source, read again by
// every check that targets html.
func parseProvided(name string, data []byte) *ast.Document {
	key := providedKey{name, string(data)}
	providedParseMu.Lock()
	defer providedParseMu.Unlock()
	if doc, ok := providedParses[key]; ok {
		return doc
	}
	doc, err := parser.Parse(name, data)
	if err != nil {
		panic(fmt.Sprintf("sngl: parsing target-provided file %q: %v", name, err))
	}
	providedParses[key] = doc
	return doc
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

// enterDeclSet starts a declaration set of its own: the work a package's bodies
// defer to the end of its check. A library package loads part-way through a
// program's check, on the same checker, so the program's deferred work is put
// aside while the library's is done and comes back untouched.
func (c *checker) enterDeclSet() func() {
	bodyComps, viewSpreads, nestedOrder := c.bodyComps, c.viewSpreads, c.nestedOrder
	treeChecks, narrowChecks, constAsserts := c.treeChecks, c.narrowChecks, c.constAsserts
	constArgs, constSlotNodes := c.constArgs, c.constSlotNodes
	c.bodyComps, c.viewSpreads, c.nestedOrder = nil, nil, nil
	c.treeChecks, c.narrowChecks, c.constAsserts = nil, nil, nil
	c.constArgs, c.constSlotNodes = nil, nil
	return func() {
		c.bodyComps, c.viewSpreads, c.nestedOrder = bodyComps, viewSpreads, nestedOrder
		c.treeChecks, c.narrowChecks, c.constAsserts = treeChecks, narrowChecks, constAsserts
		c.constArgs, c.constSlotNodes = constArgs, constSlotNodes
	}
}

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
	// Its own declaration set from the first registration on: pass1 defers
	// work as well as the bodies.
	defer c.enterDeclSet()()
	c.pass1()

	// PluralKey's Go runtime type is qualified (i18n.PluralKey) so IRTypeToGo
	// emits it rather than the bare SNGL name. A #[foreign] mark cannot say
	// this: a marked declaration is the program's own, and the Go emitter
	// deliberately ignores a marked name for that reason.
	for _, sd := range stdlibPkg.Structs {
		if sd.Foreign.Name == "" && sd.Name == "PluralKey" {
			sd.Foreign.Name = "i18n.PluralKey"
		}
	}

	// The same two declaration halves a program's pass2 runs, with nothing
	// between them: a library has no tests, no package body and no build
	// directive. Its own bodies are judged as one declaration set, apart from
	// the program it loaded part-way through (enterDeclSet, above).
	//
	// Every component body, not only the target tiers'. A body is what a
	// component renders, and a bodied component nobody checks renders
	// *nothing*: the conversion happens here or not at all, so
	// `sngl:ui/markup`'s blocks reached every backend as empty declarations
	// and `md.list { … }` emitted its children and neither body. A bodyless
	// declaration is checked too: its props' defaults are read here, and a
	// target node with no command to write has no body, so skipped it lost
	// every option default it declared.
	//
	// A library names its family, so nothing is inferred: the membership
	// checks its bodies deferred are drained by finishDeclarations.
	c.checkDeclarationBodies(stdlibPkg, nil)
	c.finishDeclarations(stdlibPkg)

	if _, ok := targetNamespaceName(pkgName); ok {
		c.checkTargetComponentsConst(stdlibPkg)
	}

	// Refine stdlib context types from their default expressions. Context
	// decls run BEFORE wrapper body checks (so wrapper bodies can read them),
	// at which point a default like `i18n.defaultLocale()` still types as dyn.
	// Once the bodies are checked the wrapper has its concrete return type,
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
// package declares none. The node is found by its family and carries the
// target's name in #[gen.name], which is what an output block writes.
func TargetNode(uri string) *ir.Component {
	pkg := LibPackage(uri)
	if pkg == nil || pkg.Symbols == nil {
		return nil
	}
	name, kind, ok := targetTierName(uri)
	if !ok {
		return nil
	}
	comp := ir.TargetNodeOf(pkg, kind)
	if comp == nil || comp.Gen.TargetName != name {
		return nil
	}
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
		BuildOnly:       fn.BuildOnly,
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
				c.addOverrideBody(decl.Pos, stdComp, kind, plat, ns, local, decl.Body, false, selection, decl.Const)
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
func (c *checker) addOverrideBody(pos ast.Pos, comp *ir.Component, kind ir.BuiltinKind, target, ns, local string, body ast.StmtBlock, user bool, selection []string, isConst bool) {
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
	var userPkg *ir.Package
	if user {
		userPkg = c.declPkg()
	}
	c.pendingExtensions = append(c.pendingExtensions, pendingExtension{
		comp:      comp,
		pos:       pos,
		platform:  target,
		kind:      kind,
		body:      body,
		user:      user,
		selection: selection,
		isConst:   isConst,
		name:      qualifiedComponentName(ns, local),
		pkg:       userPkg,
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

// collectExtensionDecls pre-registers what a platform extension body declares
// -- its vars and consts, the struct, enum, unit and component declarations
// that are body-scoped, and its funcs -- the way pass1's collectComponentDecls
// does for an ordinary component body.
//
// owner is the component the override implements, recorded as the body a
// nested component was written in: an override *is* the body its target
// renders, so #202's capture applies to it unchanged (#230).
//
// One walk in source order, and it has to stay one: collectComponentVarDecl
// resolves an annotation eagerly, so a body-local type is a name only if its
// registration already happened.
//
// The funcs are returned rather than registered, on collectComponentDecls'
// terms: the caller decides what receiver they get, and registering one
// resolves its signature, which has to happen with the body-local types in
// scope and the override's method table installed.
func (c *checker) collectExtensionDecls(owner *ir.Component, body ast.StmtBlock) ([]*ir.Var, []ir.Symbol, []*ast.FuncDef) {
	var vars []*ir.Var
	var decls []ir.Symbol
	var funcs []*ast.FuncDef
	for _, stmt := range body.Stmts {
		switch s := stmt.(type) {
		case *ast.StructDef, *ast.EnumDef, *ast.UnitDef, *ast.ComponentDecl:
			if sym := c.registerBodyDecl(stmt, owner.Name); sym != nil {
				decls = append(decls, sym)
				c.noteBodyOwner(owner, sym)
			}
		case *ast.FuncDef:
			funcs = append(funcs, s)
		default:
			vars = append(vars, c.collectComponentVarDecl(stmt)...)
		}
	}
	return vars, decls, funcs
}

// reportOverrideFuncShadows refuses an override-body func whose bare name the
// base declaration's body already gave a method, and returns the ones that
// stand. A component method is emitted under one name per component and
// nothing renames it per body, so both would reach a backend as one host
// identifier. Two *overrides* each writing one is fine: each gets its own
// clone of the method table.
func (c *checker) reportOverrideFuncShadows(pe pendingExtension, base map[string]*ir.Func, defs []*ast.FuncDef) []*ast.FuncDef {
	if len(base) == 0 || len(defs) == 0 {
		return defs
	}
	out := make([]*ast.FuncDef, 0, len(defs))
	for _, fd := range defs {
		// An explicit receiver (`func int.double`) is a method on the type it
		// names, not on the component, so the component's table says nothing
		// about it.
		if _, _, isMethod := ast.SplitMethodName(fd.Name); !isMethod {
			if _, clash := base[fd.Name]; clash {
				c.error(fd.Pos, "func %q is declared in component %s and again in its override for %q: the two bodies are separate, but a component method is emitted under one name per component and nothing renames it per body -- rename one", fd.Name, pe.comp.Name, pe.platform)
				continue
			}
		}
		out = append(out, fd)
	}
	return out
}

// reportOverrideFuncUnreachableReceiver refuses an override-body func whose
// component this scope does not bind by its bare name, and returns the ones
// that stand.
//
// Every route to a component-body method resolves the receiver as a bare name,
// and an override written against a qualified import binds only the alias.
func (c *checker) reportOverrideFuncUnreachableReceiver(pe pendingExtension, defs []*ast.FuncDef) []*ast.FuncDef {
	if len(defs) == 0 {
		return defs
	}
	if sym, ok := c.scope.Lookup(pe.comp.Name); ok && sym == ir.Symbol(pe.comp) {
		return defs
	}
	out := make([]*ast.FuncDef, 0, len(defs))
	for _, fd := range defs {
		// An explicit receiver names its own type and does not go through the
		// component at all.
		if _, _, isMethod := ast.SplitMethodName(fd.Name); isMethod {
			out = append(out, fd)
			continue
		}
		c.error(fd.Pos, "func %q cannot be declared in this override of %q: a component method attaches to its receiver by bare name, and %q names no component here -- dot-import the package, or lift the helper to a top-level func", fd.Name, pe.comp.Name, pe.comp.Name)
	}
	return out
}

// pendingExtension records a single `platform <name> { ... }` body that
// needs to be checked into IR and stashed under stdComp.PlatformOverrides.
// Body-checking is deferred until after user pass1 so user-declared symbols
// are in scope when the platform body resolves identifiers.
type pendingExtension struct {
	comp     *ir.Component
	pos      ast.Pos
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
	// isConst is the override's own `const` prefix, and name how it spelled
	// what it overrides.
	isConst bool
	name    string
	// pkg is the package a user override is written in: who a handler in it
	// runs as when the build runs one. Nil for a target package's, which is
	// library source.
	pkg *ir.Package
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
	defer c.saveFile()()
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
				c.enterFileOf(pe.pos)
				c.pushScope()
			} else {
				c.scope = libBodyScope
			}
			savedAST := pe.comp.AST.Body
			savedBody := pe.comp.Body
			savedVars := pe.comp.Vars
			savedBodyDecls := pe.comp.BodyDecls
			savedFuncs, savedMethods := pe.comp.Funcs, pe.comp.Methods
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
			// pass1 only ever saw the component's parenless stub, so the
			// override's own state has to be collected before the body check
			// that declares it. Starting from the component's own keeps a var
			// the stdlib declaration made visible to the override.
			c.pushScope()
			vars, bodyDecls, funcDefs := c.collectExtensionDecls(pe.comp, pe.body)
			pe.comp.Vars = append(slices.Clip(savedVars), vars...)
			pe.comp.BodyDecls = append(slices.Clip(savedBodyDecls), bodyDecls...)
			// A method is attached by receiver, so the base's table is where
			// an override's helper would land and stay -- visible to the base
			// body and to every other target's override, and a redeclaration
			// of the next target's helper of the same name. The clone is what
			// keeps each override's helpers to itself; it starts from the
			// base's so a method the declaration made stays callable.
			pe.comp.Methods = maps.Clone(savedMethods)
			// Registered inside the collect scope: a signature may name a type
			// the override body declared, which is bound there and nowhere
			// else.
			funcDefs = c.reportOverrideFuncShadows(pe, savedMethods, funcDefs)
			funcDefs = c.reportOverrideFuncUnreachableReceiver(pe, funcDefs)
			overrideFuncs := c.registerNestedMethods(pe.comp.Name, pe.comp.AST.TypeParams, funcDefs)
			pe.comp.Funcs = append(slices.Clip(savedFuncs), overrideFuncs...)
			c.popScope()
			// checkComponentBody checks comp.Funcs in the component's own
			// scope, so the override's are checked with its vars and props
			// visible by having been appended above.
			if pe.user {
				c.overrideFile = overrideFile{comp: pe.comp, pos: pe.pos}
			}
			c.checkComponentBody(pe.comp)
			c.overrideFile = overrideFile{}
			// While the override's state is still installed, because that is
			// the body a component nested in it was written in. Left to
			// pass2's checkComponentBodies it runs after the restore below,
			// where the owner is the base declaration again and the names the
			// nested body captured are declared by nobody.
			c.checkOverrideNestedBodies(bodyDecls)
			c.checkOverrideConst(pe)
			checked := ir.Body{
				Vars:      pe.comp.Vars,
				Stmts:     pe.comp.Body,
				BodyDecls: pe.comp.BodyDecls,
				Funcs:     pe.comp.Funcs,
				Methods:   pe.comp.Methods,
				Const:     pe.isConst,
				Pkg:       pe.pkg,
			}
			if pe.kind == ir.BuiltinLanguage {
				pe.comp.LanguageOverrides[pe.platform] = checked
			} else {
				pe.comp.PlatformOverrides[pe.platform] = checked
			}
			pe.comp.AST.Body = savedAST
			pe.comp.Body = savedBody
			pe.comp.Vars = savedVars
			pe.comp.BodyDecls = savedBodyDecls
			pe.comp.Funcs, pe.comp.Methods = savedFuncs, savedMethods
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
				// A callee nothing says anything about -- a native not
				// declared const, whose body is the host's -- may depend on
				// anything, so a call to it is not a pure one. It cannot write
				// a program's state, which it has no way to name, so it reads.
				p := x.Func.Purity
				if p == ir.PurityUnknown {
					p = ir.PurityReadonly
				}
				bump(p)
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
		Targets:    ownTarget(name),
	}
	c := newChecker(nil, cfg)
	pkg := c.libPkg(name)
	libPkgCache[name] = libPkgEntry{pkg: pkg, diags: c.diags}
	return pkg, c.diags
}

// ownTarget is the build a target package is loaded as: the target it
// belongs to, and no other. Selecting nothing would fall through to every
// registered target's overrides (targetPackages), which is a full load of each
// target package -- gtk4's introspection data included -- to answer a question
// about one. `codegen.CapsFor` asks it of every target a build names, so that
// was most of a cold `sngl generate`. A package that belongs to no target
// still loads against all of them: a reader of sngl:ui wants every override.
func ownTarget(name string) []ir.StaticTarget {
	target, kind, ok := targetTierName(name)
	if !ok {
		return nil
	}
	if kind == ir.BuiltinLanguage {
		return []ir.StaticTarget{{Language: target}}
	}
	return []ir.StaticTarget{{Platform: target}}
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
