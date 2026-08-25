package checker

import (
	"fmt"
	"io/fs"
	"slices"
	"strings"
	"sync"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/expand"
	"git.duckfam.us/jonathan/sngl/internal/parser"
	"git.duckfam.us/jonathan/sngl/ir"
	"git.duckfam.us/jonathan/sngl/lib"

	// Registers the #[builtin] macro. The stdlib source is macro-expanded
	// below, so the handlers must be present whenever the checker runs.
	_ "git.duckfam.us/jonathan/sngl/internal/macros/marks"

	// Registers #[foreign], which sngl://std carries for user and plugin code.
	_ "git.duckfam.us/jonathan/sngl/internal/macros/foreign"

	// Registers #[options], which every platform and language package carries.
	_ "git.duckfam.us/jonathan/sngl/internal/macros/platforms"
)

// Cached parsed stdlib ASTs. Parsed once, reused across Check() calls.
var (
	stdlibOnce     sync.Once
	stdlibDocs     []*ast.Document
	stdlibTierDocs map[string][]*ast.Document
)

// PackageDocsFor returns the parsed documents of the embedded package
// `sngl://<name>`. Packages are directories on disk, so the set follows the
// layout rather than a list maintained in Go.
func PackageDocsFor(name string) []*ast.Document {
	parseStdlibDocs()
	return slices.Clone(stdlibTierDocs[name])
}

// HasPackage reports whether `sngl://<name>` names an embedded package.
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
// The results are cached after the first call.
//
// Returns a copy of the cached slice so a caller that appends can't write into
// the shared package-global backing array (bugs.md #12).
func StdlibDocs() []*ast.Document {
	return slices.Clone(parseStdlibDocs())
}

// unmarkedOptions names the library packages that declare a top-level struct
// called Options without the #[options] mark. Every options lookup keys on the
// mark, so an unmarked one would silently contribute no options at all; the
// name match here exists only to catch that omission, and is the one place the
// name means anything.
func unmarkedOptions() []string {
	var out []string
	for pkg, docs := range stdlibTierDocs {
		for _, doc := range docs {
			for _, stmt := range doc.Stmts {
				sd, ok := stmt.(*ast.StructDef)
				if ok && sd.Name == "Options" && !sd.Options {
					out = append(out, fmt.Sprintf("sngl://%s: struct Options at %s needs #[options] (and import . \"sngl://platforms\")", pkg, sd.Pos))
				}
			}
		}
	}
	slices.Sort(out)
	return out
}

// targetTier reports whether a package path names a platform or language
// package — one contributed by a codegen plugin rather than by the library.
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
		// Macros expand over every tier, including the per-target ones the
		// merged view drops.
		var allDocs []*ast.Document
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
				allDocs = append(allDocs, doc)
				stdlibTierDocs[tier] = append(stdlibTierDocs[tier], doc)
			}
		}
		// Run pre-check macro expansion over the stdlib source so #[builtin]
		// marks (e.g. stringrepr on color/date/time) are applied before the
		// checker registers these declarations.
		var expandErrs []string
		for _, d := range expand.ExpandPre(allDocs) {
			if d.Severity == ir.Error {
				expandErrs = append(expandErrs, fmt.Sprintf("%s: %s", d.Pos, d.Msg))
			}
		}
		if len(expandErrs) > 0 {
			panic("sngl: expanding stdlib macros:\n  " + strings.Join(expandErrs, "\n  "))
		}
		if missing := unmarkedOptions(); len(missing) > 0 {
			panic("sngl: options schema not marked #[options]:\n  " + strings.Join(missing, "\n  "))
		}
	})
	return stdlibDocs
}

// loadStdlib builds the packages every check needs up front: sngl://builtin,
// which registers into the checker's scope and symbol table for unqualified
// access everywhere, and sngl://std, which the checker itself reads to find
// the #[builtin]-marked window/timer/slot/errorBoundary components. Any other
// library package loads on first import (libPkg).
//
// Being ambient is the only way sngl://builtin is special. sngl://std is
// eager rather than special: it is loaded here because the checker needs its
// node components to build ir.Window and ir.Timer at all, not because user
// code sees it differently from sngl://draw.
//
// Declarations are grouped by kind across a package's files and registered in
// a fixed order — imports, then types (units, structs, enums), then functions,
// then components — so the file a declaration lives in does not affect
// resolution.
func (c *checker) loadStdlib() (builtinPkg, stdPkg *ir.Package) {
	// sngl://builtin is ambient — the one implicit import. Every other lib
	// package loads into its own package and reaches scope only through an
	// explicit import, so it registers against a detached symtab/scope chained
	// to the builtins it is written against.
	builtinPkg = c.loadStdlibPackage("builtin", true)
	return builtinPkg, c.libPkg("std")
}

// libDocs returns the parsed source of lib package name, letting Config
// substitute it (see Config.LibSources).
func (c *checker) libDocs(name string) []*ast.Document {
	if c.cfg != nil {
		if docs, ok := c.cfg.LibSources[name]; ok {
			return docs
		}
	}
	return PackageDocsFor(name)
}

// hasLibPkg reports whether name resolves to a lib package for this check.
func (c *checker) hasLibPkg(name string) bool {
	if c.cfg != nil {
		if _, ok := c.cfg.LibSources[name]; ok {
			return true
		}
	}
	return len(PackageDocsFor(name)) > 0
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

// libPkg returns the loaded sngl://<name> package, loading it on first use.
// Loading is lazy and memoized rather than a pass over lib.Packages() because
// lib packages import each other (sngl://draw is written against sngl://std),
// and the import has to resolve to the same instance the user sees.
func (c *checker) libPkg(name string) *ir.Package {
	if pkg, ok := c.libPkgs[name]; ok {
		return pkg
	}
	if c.libLoading[name] {
		// An import cycle inside lib/ is a compiler bug, not user input.
		panic("sngl: import cycle in embedded library at sngl://" + name)
	}
	if c.libLoading == nil {
		c.libLoading = map[string]bool{}
	}
	c.libLoading[name] = true
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
	pkg := c.loadStdlibPackage(name, false)
	if name == i18nPkg {
		c.declarePluralKeyConstants(pkg)
	}
	delete(c.libLoading, name)
	if c.libPkgs == nil {
		c.libPkgs = map[string]*ir.Package{}
	}
	c.libPkgs[name] = pkg
	return pkg
}

// inLibSource reports whether the declarations being registered come from
// lib/ rather than from a program. Every path into lib/ source runs through
// loadStdlibPackage, including the nested loads an import inside lib/ starts,
// so the counter covers transitive loads too.
func (c *checker) inLibSource() bool { return c.libDepth > 0 || c.cfg.libSource }

func (c *checker) loadStdlibPackage(pkgName string, ambient bool) *ir.Package {
	c.libDepth++
	savedPkgName := c.libPkgName
	c.libPkgName = "sngl://" + pkgName
	defer func() { c.libDepth--; c.libPkgName = savedPkgName }()

	stdlibPkg := &ir.Package{
		Symbols:        NewSymbolTable(),
		LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{},
		AddressedVars:  map[*ir.Var]bool{},
	}
	if !ambient {
		savedSymtab, savedScope := c.symtab, c.scope
		stdlibPkg.Symbols.Root.Parent = savedScope
		c.symtab, c.scope = stdlibPkg.Symbols, stdlibPkg.Symbols.Root
		defer func() { c.symtab, c.scope = savedSymtab, savedScope }()
	}
	savedLoadPkg := c.libLoadPkg
	c.libLoadPkg = stdlibPkg
	defer func() { c.libLoadPkg = savedLoadPkg }()

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
	for _, doc := range c.libDocs(pkgName) {
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
	// PluralKey's Go runtime type is qualified (i18n.PluralKey) so IRTypeToGo
	// emits it rather than the bare SNGL name. A #[foreign] mark on the
	// declaration is the shape this wants, but the mark does not reach codegen
	// from a lib struct yet.
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
	// well put an empty `html` namespace in the ambient scope, which shadowed
	// the real one and lost the platform Resolve fallback attached to it; and
	// building them while any other package loads would re-enter the package
	// PluralKey lives in.
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
	// Restricted to the target tiers: sngl://std's components carry bodies
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

// macroPackage returns the URI of the package a macro of this name is
// registered under, or "" for a name that is not a macro.
func macroPackage(name string) string {
	for uri, names := range expand.Registered() {
		if slices.Contains(names, name) {
			return uri
		}
	}
	return ""
}

// IsMacroDecl reports whether a declaration is a macro, from the AST alone.
// The documentation layer has no type information, so it matches the return
// type's name under whatever alias the file imported sngl://internal/ir as;
// the checker matches the declaration itself (isMacroSig), which is the
// authority.
func IsMacroDecl(f *ast.FuncDef) bool {
	nt, ok := f.ReturnType.(*ast.NamedType)
	return ok && nt.Name == macroTypeName
}

// isMacroSig reports whether a declared signature returns sngl://internal/ir's
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

// buildHtmlNamespacePkg constructs a synthetic ir.Package for the "html"
// namespace, exposing the html.* placement directives (frontend/backend),
// declared as methods on receiver "html", as free functions so calls like
// html.frontend(v) resolve.
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
	sd := &ir.StructDef{AST: s, Name: s.Name, Pkg: c.libPkgName, Builtin: s.Builtin, Options: s.Options, Foreign: irForeign(s.Foreign)}
	// Macro carries no #[builtin] kind: a kind names the IR construct a
	// declaration dispatches to, and this one dispatches to none. It is found
	// by name within the compiler's own package, which no program can import.
	if c.libPkgName == "sngl://"+irPkg && s.Name == macroTypeName {
		c.macroStruct = sd
	}
	// An ambient package's own root and the ambient scope are the same scope,
	// so this binds the same symbol twice — which Declare tolerates, while
	// still refusing a different symbol under a name already taken.
	c.bindLib(s.Pos, c.scope, sd)
	// Stdlib package for qualified sngl.Type access.
	pkg.Structs = append(pkg.Structs, sd)
	c.bindLib(s.Pos, pkg.Symbols.Root, sd)
	// Publish the canonical date/time/datetime struct types so non-checker
	// phases (foreign-type importers) can synthesize them without scope access.
	switch sd.Builtin {
	case ast.BuiltinDate:
		ir.RegisterStringReprStructs(sd.SymType(), nil, nil)
	case ast.BuiltinTime:
		ir.RegisterStringReprStructs(nil, sd.SymType(), nil)
	case ast.BuiltinDateTime:
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
	c.bindLib(e.Pos, c.scope, ed)
	pkg.Enums = append(pkg.Enums, ed)
	c.bindLib(e.Pos, pkg.Symbols.Root, ed)
}

func (c *checker) registerStdlibUnit(u *ast.UnitDef, pkg *ir.Package) {
	ud := c.buildUnitDef(u)
	c.bindLib(u.Pos, c.scope, ud)
	for _, s := range ud.Suffixes {
		c.unitBySuffix[s.Name] = ud
	}
	// Stdlib package.
	pkg.Units = append(pkg.Units, ud)
	c.bindLib(u.Pos, pkg.Symbols.Root, ud)
}

// registerStdlibConst registers a library const. The #[builtin] mark travels
// from the declaration onto every name it declares, so collectBuiltins can
// find the predeclared constants; for an unmarked const this is an ordinary
// registration.
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
				Builtin: decl.Builtin,
			}
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
	// that imports sngl://std.
	if c.isMacroSig(fn.Return) {
		return nil
	}
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

	// Handle expression-body functions (=> expr)
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
		// Handle block-body functions ({ ... })
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

// mergePlatformExtensions walks every registered platform's package source
// for `component sngl.X` declarations and collects the checked IR body of
// each `platform <name> { ... }` block into the stdlib *ir.Component's
// PlatformBodies map (keyed by platform name).
//
// The checker is platform-agnostic: it does not know or care which platform
// will be the active build target. The lowering pass passPlatformExtensionBody
// reads PlatformBodies[opts.Platform] and swaps it into Component.Body before
// any other pass runs.
//
// Duplicate platform entries for the same stdlib X (e.g., two registered
// platforms both shipping `component sngl.text { platform foo { ... } }`)
// are an error.
//
// Only the parenless form is an extension. `component sngl.X() { body }`
// (android) is ignored here: that platform reads such a body itself.
func (c *checker) mergePlatformExtensions() {
	if len(c.cfg.Platforms) == 0 {
		return
	}
	for _, p := range c.cfg.Platforms {
		// An unavailable platform's overrides are built from types it cannot
		// resolve (gtk4's widgets come from a GIR file installed outside this
		// repo). Every registered platform is merged regardless of the build
		// target, so merging them would fail every compile.
		if targetUnavailable(p) != nil {
			continue
		}
		for _, doc := range c.libDocs("platforms/" + p.PlatformIdentifier()) {
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
				// An unmatched prefix is an error, not a skip: silently
				// ignoring these drops every override the file declares and
				// still produces a successful build with unstyled output.
				ns := decl.Name[:dot]
				pkgName, ok := aliases[ns]
				if !ok {
					// An unmatched prefix is an error, not a skip: silently
					// ignoring these drops every override the file declares
					// and still produces a successful build with unstyled
					// output.
					c.error(decl.Pos, "extension namespace %q is not an imported library package; import it, e.g. import %s %q", ns, ns, "sngl://std")
					continue
				}
				local := decl.Name[dot+1:]
				// The library package, not the user symtab: a library package
				// reaches user scope only through an import, but a platform
				// extension targets its declaration either way.
				target := c.libPkg(pkgName)
				stdSym, ok := target.Symbols.LookupComponent(local)
				if !ok {
					c.error(decl.Pos, "extension %q references unknown component %q in sngl://%s", decl.Name, local, pkgName)
					continue
				}
				stdComp, ok := stdSym.(*ir.Component)
				if !ok {
					continue
				}
				// Walk each platform block; collect every (platformName → body)
				// pairing this decl declares. Duplicate keys across all
				// registered platforms for the same stdlib component error.
				for i := range decl.Body.Stmts {
					pl, ok := decl.Body.Stmts[i].(*ast.PlatformStmt)
					if !ok {
						continue
					}
					if stdComp.PlatformBodies == nil {
						stdComp.PlatformBodies = map[string][]ir.Stmt{}
					}
					if _, dup := stdComp.PlatformBodies[pl.Platform]; dup {
						c.error(pl.Pos, "component %s.%s has duplicate platform block for %q", ns, local, pl.Platform)
						continue
					}
					// Reserve the key first so duplicate-detection works even
					// when the body check appends nothing (e.g., empty body).
					stdComp.PlatformBodies[pl.Platform] = nil
					c.pendingExtensions = append(c.pendingExtensions, pendingExtension{
						comp:     stdComp,
						platform: pl.Platform,
						body:     pl.Body,
					})
				}
			}
		}
	}
}

// pendingExtension records a single `platform <name> { ... }` body that
// needs to be checked into IR and stashed under stdComp.PlatformBodies.
// Body-checking is deferred until after user pass1 so user-declared symbols
// are in scope when the platform body resolves identifiers.
type pendingExtension struct {
	comp     *ir.Component
	platform string
	body     ast.StmtBlock
}

// checkPendingExtensions runs after user pass1. For each pending extension,
// temporarily install the platform block as the stdlib component's AST.Body,
// invoke checkComponentBody, capture the resulting IR Body into the
// PlatformBodies map, and restore the component's Body slot for the next
// extension (or the final pass2). The stdlib component's AST.Body and Body
// are left empty after this routine — the active platform's IR body is
// swapped in by lower's passPlatformExtensionBody.
func (c *checker) checkPendingExtensions() {
	if len(c.pendingExtensions) == 0 {
		return
	}
	// Group by platform so each platform's blueprint types (the enums/structs/
	// units declared at the top level of its Package() docs — e.g. bubbletea's
	// JoinDir/Model) are in scope while its extension bodies are checked. They
	// must be scoped per platform: several platforms each declare a distinct
	// `struct Options`.
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
		// to the ambient builtins.
		c.scope = c.stdlibPkg.Symbols.Root
		c.pushScope()
		c.registerPlatformExtensionTypes(platform)
		for _, pe := range byPlatform[platform] {
			savedAST := pe.comp.AST.Body
			savedBody := pe.comp.Body
			savedPlatform := c.currentPlatform
			pe.comp.AST.Body = pe.body
			pe.comp.Body = nil
			c.currentPlatform = pe.platform
			c.checkComponentBody(pe.comp)
			c.currentPlatform = savedPlatform
			pe.comp.PlatformBodies[pe.platform] = pe.comp.Body
			pe.comp.AST.Body = savedAST
			pe.comp.Body = savedBody
		}
		c.popScope()
	}
}

// registerPlatformExtensionTypes declares the blueprint enum/struct/unit types
// from the named platform's package source into the current (pushed) scope, so
// platform-extension bodies resolve them as real types rather than falling to
// the platform's component resolver. Scope-local by design — these names are
// not globally visible and do not collide across platforms.
func (c *checker) registerPlatformExtensionTypes(platform string) {
	var p ir.Platform
	for _, pl := range c.cfg.Platforms {
		if pl.PlatformIdentifier() == platform {
			p = pl
			break
		}
	}
	if p == nil {
		return
	}
	var enums []*ast.EnumDef
	var structs []*ast.StructDef
	var units []*ast.UnitDef
	for _, doc := range c.libDocs("platforms/" + platform) {
		for _, s := range doc.Stmts {
			switch d := s.(type) {
			case *ast.EnumDef:
				enums = append(enums, d)
			case *ast.StructDef:
				structs = append(structs, d)
			case *ast.UnitDef:
				units = append(units, d)
			}
		}
	}
	for _, u := range units {
		c.bindLib(u.Pos, c.scope, c.buildUnitDef(u))
	}
	for _, e := range enums {
		c.bindLib(e.Pos, c.scope, c.buildEnumDef(e))
	}
	// Declare struct names first so fields can reference sibling types.
	stubs := make([]*ir.StructDef, len(structs))
	for i, s := range structs {
		sd := &ir.StructDef{AST: s, Name: s.Name, Builtin: s.Builtin, Options: s.Options}
		c.bindLib(s.Pos, c.scope, sd)
		stubs[i] = sd
	}
	for i, s := range structs {
		stubs[i].Fields = c.buildStructDef(s).Fields
	}
}

// highestCalledPurity walks fn.Block looking at every function call and
// returns the max purity among the called functions. Returns PurityPure
// when the body contains no function calls or all called functions are
// pure. Used by the Phase 2b stdlib propagation pass.
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
		case *ir.PlatformFilter:
			for _, c := range x.Body {
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
		AST:     comp,
		Name:    comp.Name,
		Stdlib:  true,
		Pkg:     c.libPkgName,
		Builtin: comp.Builtin,
	}

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
			irComp.Props = append(irComp.Props, prop)
		case ast.EventDecl:
			evt := &ir.EventDecl{
				Name: pd.Name,
				Type: c.resolveType(pd.Type),
			}
			irComp.Events = append(irComp.Events, evt)
		}
	}

	if comp.ChildrenType != nil {
		irComp.ChildrenType = c.resolveType(comp.ChildrenType)
	}

	c.bindLib(comp.Pos, c.scope, irComp)
	// Stdlib package for qualified sngl.Component access.
	pkg.Components = append(pkg.Components, irComp)
	c.bindLib(comp.Pos, pkg.Symbols.Root, irComp)
}

// stdlibImportAlias returns the alias a document binds the standard library
// under, or "" if it does not import it. Platform sources use this alias as
// the `component <alias>.X` extension prefix.
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
