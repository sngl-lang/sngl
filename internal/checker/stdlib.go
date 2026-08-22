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
	_ "git.duckfam.us/jonathan/sngl/internal/macros/builtin"
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
// The results are cached after the first call.
//
// Returns a copy of the cached slice so a caller that appends can't write into
// the shared package-global backing array (bugs.md #12).
func StdlibDocs() []*ast.Document {
	return slices.Clone(parseStdlibDocs())
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
				stdlibDocs = append(stdlibDocs, doc)
				stdlibTierDocs[tier] = append(stdlibTierDocs[tier], doc)
			}
		}
		// Run pre-check macro expansion over the stdlib source so #[builtin]
		// marks (e.g. stringrepr on color/date/time) are applied before the
		// checker registers these declarations.
		var expandErrs []string
		for _, d := range expand.ExpandPre(stdlibDocs) {
			if d.Severity == ir.Error {
				expandErrs = append(expandErrs, fmt.Sprintf("%s: %s", d.Pos, d.Msg))
			}
		}
		if len(expandErrs) > 0 {
			panic("sngl: expanding stdlib macros:\n  " + strings.Join(expandErrs, "\n  "))
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
	pkg := c.loadStdlibPackage(name, false)
	delete(c.libLoading, name)
	if c.libPkgs == nil {
		c.libPkgs = map[string]*ir.Package{}
	}
	c.libPkgs[name] = pkg
	return pkg
}

func (c *checker) loadStdlibPackage(pkgName string, ambient bool) *ir.Package {
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
	for _, doc := range PackageDocsFor(pkgName) {
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
				if isContextDeclCallStmt(s) {
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
	// Annotate stdlib structs that have known Go-runtime native type names.
	// These annotations ensure that IRTypeToGo emits the qualified Go type
	// (e.g. "i18n.PluralKey") rather than the plain SNGL name ("PluralKey").
	for _, sd := range structDefs {
		if sd.Native == "" {
			switch sd.Name {
			case "PluralKey":
				sd.Native = "i18n.PluralKey"
			}
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
	declaresI18n := pkgName == stdPkg
	var i18nNS, htmlNS *ir.Namespace
	if declaresI18n {
		i18nNS = &ir.Namespace{Name: "i18n"}
		htmlNS = &ir.Namespace{Name: "html"}
		c.bindLib(ast.Pos{}, c.scope, i18nNS)
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

	if declaresI18n {
		// i18n so that i18n.plural(...), i18n.one, etc. resolve: the package
		// exposes every i18n.* receiver method as a free function, plus the
		// predeclared PluralKey constants (zero, one, two, few, many, other).
		i18nNS.Pkg = c.buildI18nNamespacePkg(registeredFuncs)
		// html so the placement directives html.frontend(...) /
		// html.backend(...) (GitLab #27) resolve as free-function calls. The
		// directives are declared as methods on receiver "html" in
		// lib/std/html.sngl; expose them here as namespace functions.
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

// intlPkg is the library package declaring the locale-aware primitives and the
// PluralKey they are keyed by.
const intlPkg = "internal/intl"

// stdPkg is the library package that declares the i18n and html namespaces.
const stdPkg = "std"

// buildI18nNamespacePkg constructs a synthetic ir.Package for the "i18n"
// namespace, exposing i18n.* receiver methods as free functions and
// predeclaring the CLDR PluralKey constants (zero/one/two/few/many/other).
func (c *checker) buildI18nNamespacePkg(stdlibFuncs []*ir.Func) *ir.Package {
	pkg := &ir.Package{
		Symbols:        NewSymbolTable(),
		LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{},
		AddressedVars:  map[*ir.Var]bool{},
	}

	// Expose all i18n.* receiver methods as free functions in the namespace.
	c.addReceiverFuncs(pkg, stdlibFuncs, "i18n")

	// The constants are PluralKeys, and PluralKey is declared beside the intl
	// primitives that consume it. std has already imported that package by the
	// time this runs, so the lookup is a cache hit, not a load.
	var pluralKeyType *ir.Type
	if intl := c.libPkg(intlPkg); intl != nil {
		if sym, ok := intl.Symbols.Root.LookupLocal("PluralKey"); ok {
			if sd, isStruct := sym.(*ir.StructDef); isStruct {
				pluralKeyType = sd.SymType()
			}
		}
	}
	if pluralKeyType == nil {
		// PluralKey not found; skip constant registration.
		return pkg
	}

	// Register predeclared CLDR plural-category vars: zero, one, two, few,
	// many, other. These are opaque sentinel values; their actual runtime
	// values are supplied by the Go i18n runtime (PluralZero, PluralOne, …).
	for _, name := range []string{"zero", "one", "two", "few", "many", "other"} {
		v := &ir.Var{Name: name, Type: pluralKeyType, IsConst: true}
		pkg.Vars = append(pkg.Vars, v)
		c.bindLib(ast.Pos{}, pkg.Symbols.Root, v)
	}

	return pkg
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
	sd := &ir.StructDef{AST: s, Name: s.Name, Builtin: s.Builtin}
	// Main symtab + scope for unqualified access.
	// The loader binds every declaration into the ambient scope and into the
	// package's own root, which for an ambient package are the same scope.
	// Rebinding is the norm here, not a mistake; duplicates inside lib/ are
	// caught by the one-name rule in the register* paths.
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
	// Impure stdlib (alert.show, file.contents, anything with a NativePkg
	// effect) gets its purity overridden later by stdlib.SetImpure or via
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

// mergePlatformExtensions walks every registered platform's Package() docs
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
// Note: this pass intentionally only acts on the new form (`HasParens=false`).
// Legacy `component sngl.X() { body }` declarations in platform .sngl files
// (html, bubbletea) continue to be ignored until Phase C rewrites them.
func (c *checker) mergePlatformExtensions() {
	if len(c.cfg.Platforms) == 0 {
		return
	}
	for _, p := range c.cfg.Platforms {
		for _, doc := range p.Package() {
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
					// Legacy form — skip until Phase C rewrites.
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
// from the named platform's Package() docs into the current (pushed) scope, so
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
	for _, doc := range p.Package() {
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
		sd := &ir.StructDef{AST: s, Name: s.Name, Builtin: s.Builtin}
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
