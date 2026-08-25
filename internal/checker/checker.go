package checker

import (
	"fmt"
	"io/fs"
	"maps"
	"regexp"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/expand"
	"git.duckfam.us/jonathan/sngl/ir"
	"git.duckfam.us/jonathan/sngl/lib"
)

// Config holds checker configuration.
type Config struct {
	FS        fs.FS            // filesystem for resolving relative imports
	Dir       string           // OS directory for scheme imports
	IsMain    bool             // whether output declarations are allowed
	Resolver  ImportResolver   // import resolver (nil = no imports)
	Languages []ir.Language    // registered languages
	Platforms []ir.Platform    // registered platforms
	Target    *ir.StaticTarget // current compile target (nil = check all)
	// Replaces maps local import paths to replacement URLs, supplied by an
	// outer (main) package. Entries here override any `=>` mapping declared
	// in the package being checked.
	Replaces map[string]string
	// LibSources substitutes the source of an embedded package, keyed by lib
	// path ("platforms/teststub"). It exists for the in-test platform stubs,
	// which register a plugin with no lib/ directory behind it; production
	// callers leave it nil and every package is read from lib.FS.
	LibSources map[string][]*ast.Document
	// libSource permits sngl://internal/ imports in the document itself, for
	// the one caller that checks lib/ source as the document rather than
	// loading it as a package. Unexported: no program is lib source.
	libSource bool
}

// ImportResolver resolves import paths to parsed documents or native declarations.
type ImportResolver interface {
	// Resolve resolves a directory import to parsed AST documents.
	Resolve(fsys fs.FS, importPath string) ([]*ast.Document, error)

	// ResolveScheme resolves a scheme-based import to native (language-level)
	// declarations (e.g. go://pkg/path).
	ResolveScheme(scheme, uri, dir string) (*ir.NativeImport, error)

	// ResolveSchemeFS resolves a scheme-based import to a set of SNGL .sngl
	// documents plus the filesystem they were read from. Used by FS schemes
	// like git:// that vend remote SNGL packages. Returns (nil, nil, nil) when
	// the scheme is not FS-registered — callers should then try ResolveScheme.
	// The returned subFS is rooted at the package's cache directory so that
	// transitive imports inside the package resolve against it.
	ResolveSchemeFS(scheme, uri, dir string) (docs []*ast.Document, subFS fs.FS, err error)
}

// Check type-checks a parsed v2 AST Document and returns the IR Package.
// The Package is populated best-effort even when diagnostics are present.
func Check(doc *ast.Document, cfg *Config) (*ir.Package, []ir.Diagnostic) {
	c := newChecker(doc, cfg)
	c.pass1()
	// Body-check pending stdlib platform extensions after user pass1 so that
	// user-declared symbols are in scope when the platform body resolves
	// identifiers. Collection (the AST walk that enumerates pending bodies)
	// happened in newChecker; checkPendingExtensions populates each stdlib
	// Component's PlatformBodies map.
	c.checkPendingExtensions()
	c.pass2()
	c.analyzeErrors()
	c.analyzeAsync()
	analyzePointsTo(c.pkg)
	c.analyzeAsyncWithPointsTo()
	c.checkAsyncRules()
	c.pkg.Symbols = c.symtab
	ir.Normalize(c.pkg)
	return c.pkg, c.diags
}

// checker is the internal state for a single Check invocation.
type checker struct {
	doc *ast.Document
	cfg *Config

	pkg    *ir.Package
	diags  []ir.Diagnostic
	scope  *ir.Scope
	symtab *ir.SymbolTable

	// Type resolution context.
	typeParams []string // active generic type params (set during function checking)

	// Unit suffix reverse lookup.
	unitBySuffix map[string]*ir.UnitDef

	// Import cycle detection.
	visited map[string]bool

	// foreign marks declarations that arrived from another package, so their
	// unexported members stay private to it.
	foreign map[ir.Symbol]bool

	// topLevel records every name bound at file scope and how it got there,
	// so two bindings of one name are reported instead of silently resolving
	// by declaration order.
	topLevel map[string]topLevelBinding

	// Effective replace map for this package: outer overrides layered over
	// this package's own `import "p" => "url"` declarations. Populated at the
	// start of pass1 before any import is resolved.
	replaces map[string]string

	// Current function return type (for return stmt checking).
	returnType *ir.Type

	// Expected type for the expression currently being checked.
	// When set to an enum type, bare enum member names resolve automatically.
	expected *ir.Type

	// nativeValues checks encoded native-language values rather than SNGL
	// source: a struct literal takes its declaration from the expected type
	// and names its fields as the source language does. Set only by
	// CheckNativeValue.
	nativeValues bool

	// nativeTypes resolves the foreign declaration an encoded value names.
	nativeTypes ir.NativeDecls

	// Current component (for event validation).
	currentComponent *ir.Component

	// Tracks window #id collisions at package scope.
	pkgWindowIDs map[string]bool

	// Current platform block name (e.g., "html" inside `platform html { }`).
	// Used to try platform Resolve() on unknown identifiers.
	currentPlatform string

	// Cached Options structs from platform/language packages, keyed by target
	// identifier (e.g. "html", "kotlin").
	optionsCache map[string]*ir.StructDef

	// Cached merged Options structs (stdlib ∪ lang ∪ platform), keyed by
	// "lang|platform". A nil value means we resolved and cached "no options".
	mergedOptionsCache map[string]*ir.StructDef

	// Cached stdlib Options struct. Built lazily.
	stdlibOptions    *ir.StructDef
	stdlibOptionsSet bool

	// The #[builtin("window")] component, and its instance type. Window
	// symbols are typed with the component's own type, so `home.href` resolves
	// through the regular component-member machinery against its props.
	// windowComp is what makes window dispatch tag-based rather than a check
	// against the literal name "window".
	// stdlibPkg is the loaded standard library, bound as a namespace by an
	// `import <alias> "sngl://std"` and flattened by the dot form.
	stdlibPkg *ir.Package
	// libPkgs memoizes loaded sngl://<name> packages; libLoading guards
	// against a cycle among them.
	libPkgs    map[string]*ir.Package
	libLoading map[string]bool
	libDepth   int
	// libLoadPkg is the library package currently loading, and the owner of
	// any import registered while it does. nil outside a lib load.
	libLoadPkg *ir.Package
	// libPkgName is the URI of the lib package currently being loaded, stamped
	// onto every declaration it builds as that declaration's identity (see
	// ir.StructDef.Pkg). Saved and restored around each load, because a lib
	// package's import of another nests one load inside the other.
	libPkgName string
	// macroStruct is sngl://internal/ir's `Macro`, the return type that makes a
	// declared function a macro. Held as the declaration rather than the name
	// because type identity is per-declaration.
	macroStruct *ir.StructDef

	// builtinPkg is sngl://builtin, registered ambiently into every file.
	builtinPkg *ir.Package

	// stdlibScope is the scope holding stdlib declarations, between the base
	// scope and the user root. Platform-extension bodies are checked against it
	// so compiler-internal source cannot be captured by user declarations.
	stdlibScope *ir.Scope

	windowComp *ir.Component
	// contextComp is the declaration `context #name(default)` names. Matching
	// the mark rather than the word is what lets a program shadow `context`.
	contextComp *ir.Component
	windowType  *ir.Type

	// durationUnit is the #[builtin("duration")] unit. Held so the type can be
	// registered for phases that have no scope — see ir.DurationType.
	durationUnit *ir.UnitDef

	// The predeclared constants, bound by collectBuiltins. Held so a second
	// declaration of the same kind is an error rather than a silent
	// overwrite; resolution itself goes through the scope chain like any
	// other name.
	nullConst     *ir.Var
	platformConst *ir.Var
	languageConst *ir.Var

	// Cached platform scopes built from Platform.Package() docs.
	platformScopeCache map[string]*ir.Scope

	// Platform extension bodies enqueued by mergePlatformExtensions for IR
	// check after user pass1 (so platform-body identifiers can resolve against
	// the full user scope chain). Each entry produces one
	// stdComp.PlatformBodies[platformName] = checkedIRBody mapping.
	pendingExtensions []pendingExtension

	// Deferred const(expr) assertions. Const-ness can depend on function
	// purity, which is only assigned after all bodies are checked, so the
	// assertions are run in a final pass.
	constAsserts []constAssertion

	// pendingConstInits holds top-level const initializers whose names are
	// registered as shells in pass1 but whose values are checked in a
	// sub-pass after every const shell (and func) is registered, so a const
	// may forward-reference another const or use a bare enum member.
	pendingConstInits []pendingConstInit
}

// constAssertion captures a const(expr) use site and the IR expression to
// validate once purity analysis has populated func purities.
type constAssertion struct {
	pos     ast.Pos
	operand ir.Expr
}

// pendingConstInit is a top-level const spec whose value check is deferred to
// checkPendingConstInits (after all shells are registered).
type pendingConstInit struct {
	decl *ast.ConstDecl
	spec ast.VarSpec
	typ  *ir.Type
	vars []*ir.Var
}

func newChecker(doc *ast.Document, cfg *Config) *checker {
	symtab := NewSymbolTable()
	c := &checker{
		doc:          doc,
		cfg:          cfg,
		pkg:          &ir.Package{LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{}, AddressedVars: map[*ir.Var]bool{}},
		symtab:       symtab,
		scope:        symtab.Root,
		unitBySuffix: make(map[string]*ir.UnitDef),
		visited:      make(map[string]bool),
		pkgWindowIDs: make(map[string]bool),
	}
	// Insert stdlib scope between base and Root so user declarations shadow stdlib.
	stdlibScope := NewScope(symtab.Root.Parent) // parent = baseScope
	c.scope = stdlibScope
	c.builtinPkg, c.stdlibPkg = c.loadStdlib()
	// Both packages, because a mark says which construct a declaration is, not
	// which package declares it: the predeclared constants are in
	// sngl://builtin and the visual nodes are in sngl://std.
	c.collectBuiltins(c.builtinPkg, c.stdlibPkg)
	c.stdlibScope = stdlibScope
	if c.windowComp == nil {
		// The stdlib is embedded and compiler-controlled; a missing window
		// declaration would silently turn every `window #id` into "unexpected
		// root-level visual node". Fail loudly, as parseStdlibDocs does.
		panic("sngl: embedded stdlib declares no #[builtin(\"window\")] component")
	}
	c.windowType = c.windowComp.SymType()
	symtab.Root.Parent = stdlibScope
	c.scope = symtab.Root

	// Inject all registered platform and language names as namespaces with
	// Resolve fallback so raw element access (e.g., html.div) works. If a
	// stdlib namespace with the same name already exists (e.g. the "html"
	// namespace declared by loadStdlib for the html.frontend/html.backend
	// placement directives), preserve its Pkg and attach the Resolve fallback
	// to the same namespace so named directives resolve via Pkg first and raw
	// elements fall through to Resolve.
	// Raw element access is ambient for every platform, so the Resolve-bearing
	// namespace always goes in the ambient scope. The standard library may also
	// declare a namespace of the same name (lib/std/html.sngl declares `html`
	// for the html.frontend/html.backend directives); attach Resolve to that
	// one too, so importing std keeps raw elements working rather than
	// shadowing them with a directives-only namespace.
	declareNS := func(name string, resolve func(string) ir.Symbol) {
		if existing, ok := c.stdlibPkg.Symbols.Root.LookupLocal(name); ok {
			if ns, ok := existing.(*ir.Namespace); ok {
				ns.Resolve = resolve
			}
		}
		if existing, ok := stdlibScope.LookupLocal(name); ok {
			if ns, ok := existing.(*ir.Namespace); ok {
				ns.Resolve = resolve
				return
			}
		}
		c.bindLib(ast.Pos{}, stdlibScope, &ir.Namespace{Name: name, Resolve: resolve})
	}
	for _, p := range cfg.Platforms {
		declareNS(p.PlatformIdentifier(), p.Resolve)
	}
	for _, l := range cfg.Languages {
		declareNS(l.LanguageIdentifier(), l.Resolve)
	}

	// Splice platform extension bodies into the stdlib components they target.
	// AST splicing happens here so that user pass1/pass2 see body-bearing
	// stdlib components; IR body checking is run from Check() after user
	// pass1 (so user-declared symbols are visible if a body references them).
	c.mergePlatformExtensions()

	return c
}

// declare binds sym in the current scope, reporting a name already bound there
// instead of letting the later binding silently win.
func (c *checker) declare(pos ast.Pos, sym ir.Symbol) {
	if err := c.scope.Declare(sym); err != nil {
		c.error(pos, "%s is already declared in this scope", sym.SymName())
	}
}

// varPos is the source position of a var's declaration, or the zero position
// for a synthesized var that has no AST.
func varPos(v *ir.Var) ast.Pos {
	if v.AST != nil {
		if p := v.AST.StmtPos(); p != nil {
			return *p
		}
	}
	return ast.Pos{}
}

// funcDeclPos and compDeclPos are the source positions of a func or component
// declaration, or the zero position for one the checker synthesized.
func funcDeclPos(fn *ir.Func) ast.Pos {
	if fn != nil && fn.AST != nil {
		return fn.AST.Pos
	}
	return ast.Pos{}
}

func compDeclPos(comp *ir.Component) ast.Pos {
	if comp != nil && comp.AST != nil {
		return comp.AST.Pos
	}
	return ast.Pos{}
}

// bindVar binds a var or const from a registration path that serves both file
// scope and a component or window body. At file scope the one-name rule
// applies, so shadowing a lifted name stays legal; inside a body any name
// already bound in the same scope is a duplicate.
func (c *checker) bindVar(pos ast.Pos, v *ir.Var) {
	if c.scope == c.symtab.Root {
		c.bindDeclared(c.claimTopLevel(v.Name, pos, bindDecl, ""), v)
		return
	}
	c.declare(pos, v)
}

func (c *checker) error(pos ast.Pos, format string, args ...any) {
	c.diags = append(c.diags, ir.Diagnostic{
		Pos:      pos,
		Msg:      fmt.Sprintf(format, args...),
		Severity: ir.Error,
	})
}

func (c *checker) warn(pos ast.Pos, format string, args ...any) {
	c.diags = append(c.diags, ir.Diagnostic{
		Pos:      pos,
		Msg:      fmt.Sprintf(format, args...),
		Severity: ir.Warning,
	})
}

// pushScope creates a child scope and makes it current.
func (c *checker) pushScope() {
	c.scope = NewScope(c.scope)
}

// popScope restores the parent scope.
func (c *checker) popScope() {
	c.scope = c.scope.Parent
}

// --- pass1: declaration registration ---

// topLevelKind distinguishes how a file-scope name was bound. Only a
// declaration written in the file may shadow an imported name; every other
// pairing is ambiguous.
type topLevelKind int

const (
	bindDecl  topLevelKind = iota // declared in this file
	bindAlias                     // an import's namespace alias
	bindDot                       // lifted by a dot import
)

func (k topLevelKind) String() string {
	switch k {
	case bindAlias:
		return "an import alias"
	case bindDot:
		return "a dot import"
	default:
		return "a declaration"
	}
}

type topLevelBinding struct {
	kind topLevelKind
	path string // import path, for the two import kinds
	pos  ast.Pos
}

// claimTopLevel records name as bound at file scope and reports a conflict
// with an existing binding. Returns false when the caller should skip binding.
//
// A declaration written in the file wins over a dot-imported name — that is the
// documented override story, and it is unambiguous because only one of the two
// is written here. Everything else (two declarations, two aliases, two dot
// imports of one name, an alias against a declaration) has no tiebreak, so it
// is an error rather than a silent last-wins.
// markForeign records every declaration an import contributes, so member
// access can tell a type declared here from one that merely arrived here.
func (c *checker) markForeign(pkg *ir.Package) {
	if pkg == nil {
		return
	}
	if c.foreign == nil {
		c.foreign = map[ir.Symbol]bool{}
	}
	// The package's own root only. EachSymbol would walk the parent chain,
	// and an imported package's root parents whatever scope was current when
	// it loaded — for an import inside a platform block, the importing file's
	// own scope, whose declarations are not foreign to it.
	if pkg.Symbols.Root == nil {
		return
	}
	for _, sym := range pkg.Symbols.Root.Symbols {
		if _, isComp := sym.(*ir.Component); isComp || ir.IsTypeDecl(sym) {
			c.foreign[sym] = true
		}
	}
}

// rejectForeignUnexported reports an unexported member read through a
// declaration that belongs to another package. Unexported names are private to
// their declaring package; without this they were reachable from anywhere the
// type itself was, since only the type name is gated on import.
func (c *checker) rejectForeignUnexported(pos ast.Pos, owner ir.Symbol, ownerName, member string) bool {
	if !c.foreign[owner] || isExportedMemberName(member) {
		return false
	}
	c.error(pos, "%s.%s is unexported and cannot be used outside its package", ownerName, member)
	return true
}

// structField resolves a field on sd by name, enforcing that an unexported
// field stays private to the package declaring sd. Every by-name field lookup
// goes through here rather than ranging over sd.Fields, so a new access path
// cannot reach a private field by forgetting a check.
func (c *checker) structField(pos ast.Pos, sd *ir.StructDef, name string) *ir.StructField {
	if sd == nil || c.rejectForeignUnexported(pos, sd, sd.Name, name) {
		return nil
	}
	if f := findField(sd, name); f != nil {
		return f
	}
	if c.nativeValues {
		return findNativeField(sd, name)
	}
	return nil
}

// enumMember reports whether ed declares name, under the same visibility rule
// as structField.
func (c *checker) enumMember(pos ast.Pos, ed *ir.EnumDef, name string) bool {
	if ed == nil || c.rejectForeignUnexported(pos, ed, ed.Name, name) {
		return false
	}
	for _, m := range ed.Members {
		if m.Name == name {
			return true
		}
	}
	return false
}

// suggestAlias proposes a short alias for a package whose default name is
// taken, so the diagnostic can show a working import line.
func suggestAlias(name string) string {
	for n := 2; n <= len(name); n++ {
		if candidate := name[:n]; candidate != name {
			return candidate
		}
	}
	return name + "pkg"
}

func isExportedMemberName(name string) bool {
	return name != "" && name[0] != '_'
}

func (c *checker) claimTopLevel(name string, pos ast.Pos, kind topLevelKind, path string) bool {
	if name == "" || name == "_" {
		return true
	}
	if c.topLevel == nil {
		c.topLevel = map[string]topLevelBinding{}
	}
	prev, exists := c.topLevel[name]
	if !exists {
		c.topLevel[name] = topLevelBinding{kind: kind, path: path, pos: pos}
		return true
	}
	// A declaration shadows a dot-imported name — that is the documented
	// override story, and it is unambiguous because only one of the two is
	// written here. It does not shadow an alias: the alias names a package,
	// and silently rebinding it would break every qualified reference to it.
	if kind == bindDecl && prev.kind == bindDot {
		c.topLevel[name] = topLevelBinding{kind: kind, pos: pos}
		return true
	}
	// Re-binding the same name from the same import is not a conflict.
	if kind == prev.kind && kind != bindDecl && path == prev.path {
		return true
	}
	switch {
	case kind == bindDot && prev.kind == bindDot:
		c.error(pos, "dot import of %q lifts %q, already lifted by dot import of %q; qualify one of them with an alias",
			path, name, prev.path)
	case kind == bindDecl && prev.kind == bindDecl:
		c.error(pos, "%q redeclared in this file (previous declaration at %s)", name, prev.pos)
	case kind == bindAlias:
		// An import whose alias is already taken. The alias is the caller's to
		// choose, so naming the way out is more useful than naming the clash.
		c.error(pos, "%q is already bound at file scope by %s (at %s); import it under a different alias, e.g. import %s %q",
			name, prev.kind, prev.pos, suggestAlias(name), path)
	default:
		c.error(pos, "%q is already bound at file scope by %s (at %s)", name, prev.kind, prev.pos)
	}
	return false
}

func (c *checker) pass1() {
	// File-scope name tracking covers this document only. Loading the library
	// runs through the same register paths with its own scopes, and its names
	// reach the user by import, where flattenDotImport claims them.
	c.topLevel = nil

	// Collect replace map for this package before any import is resolved, so
	// declaration order of `import "p" => "url"` relative to bare `import "p"`
	// does not matter. Outer (cfg.Replaces) wins over this package's own.
	c.replaces = map[string]string{}
	for _, stmt := range c.doc.Stmts {
		imp, ok := stmt.(*ast.Import)
		if !ok || imp.Replace == "" {
			continue
		}
		if _, dup := c.replaces[imp.Path]; dup {
			c.error(imp.Pos, "duplicate import replace for %q", imp.Path)
			continue
		}
		c.replaces[imp.Path] = imp.Replace
	}
	maps.Copy(c.replaces, c.cfg.Replaces)

	// Imports must be processed first so their namespaces are in scope before
	// any resolveType call inside a component, struct, or func declaration.
	for _, stmt := range c.doc.Stmts {
		if imp, ok := ast.UnwrapStmt(stmt).(*ast.Import); ok {
			c.registerImport(imp)
		}
	}

	// Pre-register type declarations so they're visible for forward references
	// (test functions referencing later types, a struct field or component prop
	// naming a type declared later, mutually recursive structs). Register every
	// type NAME first — structs as field-less shells — then resolve struct
	// fields in a sub-pass once all shells exist. Components are registered
	// after the shells because their prop/children types may name any of them.
	var structShells []*ir.StructDef
	var pendingComponents []*ast.ComponentDecl
	for _, stmt := range c.doc.Stmts {
		switch s := ast.UnwrapStmt(stmt).(type) {
		case *ast.StructDef:
			structShells = append(structShells, c.registerStructShell(s))
		case *ast.EnumDef:
			c.registerEnum(s)
		case *ast.UnitDef:
			c.registerUnit(s)
		case *ast.ComponentDecl:
			pendingComponents = append(pendingComponents, s)
		}
	}
	for _, comp := range pendingComponents {
		c.registerComponent(comp)
	}
	for _, sd := range structShells {
		c.resolveStructBody(sd)
	}

	for _, stmt := range c.doc.Stmts {
		switch s := ast.UnwrapStmt(stmt).(type) {
		case *ast.Import, *ast.EnumDef, *ast.StructDef, *ast.UnitDef, *ast.ComponentDecl:
			continue // already registered above
		case *ast.ConstDecl:
			c.registerConstShells(s)
		case *ast.VarDecl:
			c.registerVars(s)
		case *ast.FuncDef:
			c.registerFunc(s)
		case *ast.VisualNode:
			c.registerRootVisualNode(s)
		case *ast.PlatformStmt:
			c.pass1PlatformStmt(s)
		case *ast.CallStmt:
			if c.isContextDeclCallStmt(s) {
				c.registerRootContextDecl(s)
			} else {
				c.error(s.Pos, "unexpected top-level call statement")
			}
		case *ast.DisabledDecl:
			// Skip disabled declarations.
		case *ast.Comment:
			// Skip comments.
		default:
			// IfStmt, ForStmt at top level are checked in pass2.
		}
	}

	// Check deferred top-level const values now that every const shell, type,
	// and func is registered (enables forward references between consts and
	// bare enum members in const initializers).
	c.checkPendingConstInits()
}

func (c *checker) registerImport(imp *ast.Import) {
	// Resolve the effective target URL: the import's own Replace wins, else
	// look the local path up in the replace map, else use the path as-is.
	target := imp.Path
	if imp.Replace != "" {
		target = imp.Replace
	} else if mapped, ok := c.replaces[imp.Path]; ok {
		target = mapped
	}

	scheme, uri := ParseScheme(target)
	// A dot import keeps "." here rather than deriving a namespace name it
	// never binds: consumers match ir.Import.Alias against a namespace they
	// are resolving, and a derived name would make those matches succeed.
	alias := imp.Alias
	if alias == "" {
		alias = NamespaceFromPath(imp.Path)
	}

	irImport := &ir.Import{
		AST:     imp,
		Path:    imp.Path,
		Alias:   alias,
		Replace: imp.Replace,
	}

	// Optional Resolve fallback for platform/language namespace imports.
	var nsResolve func(string) ir.Symbol

	// sngl://platforms/<n> and sngl://languages/<n> load through libPkg like
	// any other embedded package. The registered plugin is still consulted,
	// for two things the lib tree cannot say: whether the target exists at all
	// here, and the namespace Resolve fallback that makes raw primitives
	// (html.div) resolve.
	platName, isPlatform := "", false
	langName, isLanguage := "", false
	if scheme == "sngl" {
		platName, isPlatform = strings.CutPrefix(uri, "platforms/")
		langName, isLanguage = strings.CutPrefix(uri, "languages/")
	}

	if isPlatform {
		var target ir.Platform
		for _, p := range c.cfg.Platforms {
			if p.PlatformIdentifier() == platName {
				target = p
				break
			}
		}
		if target == nil {
			c.error(imp.Pos, "unknown platform %q", platName)
		} else if err := targetUnavailable(target); err != nil {
			c.error(imp.Pos, "platform %q is unavailable here: %v", platName, err)
		} else {
			nsResolve = target.Resolve
			// A platform need not ship declarations (`none` does not); the
			// namespace is still bound, for its Resolve fallback.
			if c.hasLibPkg(uri) {
				irImport.Pkg = c.libPkg(uri)
			}
		}
	} else if isLanguage {
		var target ir.Language
		for _, l := range c.cfg.Languages {
			if l.LanguageIdentifier() == langName {
				target = l
				break
			}
		}
		if target == nil {
			c.error(imp.Pos, "unknown language %q", langName)
		} else {
			nsResolve = target.Resolve
			if c.hasLibPkg(uri) {
				irImport.Pkg = c.libPkg(uri)
			}
		}
	} else if scheme == "sngl" {
		// The standard library. A scheme keeps it from colliding with a local
		// package directory of any name — the collision a reserved bare path
		// like "std" would reintroduce.
		// sngl://internal/<name> is the compiler's own tier, importable only
		// from library source.
		internal := strings.HasPrefix(uri, "internal/")
		if internal && !c.inLibSource() {
			c.error(imp.Pos, "%q is internal to the compiler and cannot be imported", target)
			return
		}
		// A package may contribute macros to the expand pass, declarations to
		// the program, or both, so a sngl:// path resolves against the macro
		// registry and the lib/ layout together — a macro-only package has no
		// directory, and a declarations package has no macros. This is not
		// confined to the internal/ tier: sngl://platforms is the public
		// vocabulary an out-of-tree platform plugin marks its source with.
		if !HasPackage(uri) {
			if !expand.HasPackage(uri) {
				if internal {
					c.error(imp.Pos, "unknown internal package %q", uri)
				} else {
					c.error(imp.Pos, "unknown stdlib package %q (have: %s)", uri, strings.Join(lib.PublicPackages(), ", "))
				}
				return
			}
			irImport.Pkg = &ir.Package{
				Symbols:        NewSymbolTable(),
				LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{},
				AddressedVars:  map[*ir.Var]bool{},
			}
			owner := c.importOwner()
			owner.Imports = append(owner.Imports, irImport)
			return
		}
		if uri == "builtin" {
			c.error(imp.Pos, "sngl://builtin is always in scope; remove the import")
			return
		}
		irImport.Pkg = c.libPkg(uri)
	} else if scheme != "" && c.cfg.Resolver != nil {
		// Scheme import. Try FS-backed schemes first (git://, http://, …) so
		// remote SNGL packages resolve to .sngl docs; fall back to native
		// scheme importers (go://, ts://, …) for language sources.
		docs, subFS, err := c.cfg.Resolver.ResolveSchemeFS(scheme, uri, c.cfg.Dir)
		if err != nil {
			c.error(imp.Pos, "import %q: %v", imp.Path, err)
		} else if len(docs) > 0 {
			merged := &ir.Package{Symbols: NewSymbolTable(), LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{}, AddressedVars: map[*ir.Var]bool{}}
			for _, d := range docs {
				pkg, diags := Check(d, &Config{
					FS:        subFS,
					Dir:       c.cfg.Dir,
					Resolver:  c.cfg.Resolver,
					Languages: c.cfg.Languages,
					Platforms: c.cfg.Platforms,
					Replaces:  c.replaces,
				})
				c.diags = append(c.diags, diags...)
				c.mergePkgInto(merged, pkg)
			}
			irImport.Pkg = merged
		} else {
			native, err := c.cfg.Resolver.ResolveScheme(scheme, uri, c.cfg.Dir)
			if err != nil {
				c.error(imp.Pos, "import %q: %v", imp.Path, err)
			}
			irImport.Native = native
			if native != nil {
				// Register native declarations under the namespace.
				nsPkg := &ir.Package{
					Structs:        native.Structs,
					Enums:          native.Enums,
					Funcs:          native.Funcs,
					Vars:           native.Vars,
					Symbols:        NewSymbolTable(),
					LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{},
					AddressedVars:  map[*ir.Var]bool{},
				}
				for _, s := range native.Structs {
					c.bindLib(imp.Pos, nsPkg.Symbols.Root, s)
				}
				for _, e := range native.Enums {
					c.bindLib(imp.Pos, nsPkg.Symbols.Root, e)
				}
				for _, f := range native.Funcs {
					c.bindLib(imp.Pos, nsPkg.Symbols.Root, f)
				}
				for _, v := range native.Vars {
					c.bindLib(imp.Pos, nsPkg.Symbols.Root, v)
				}
				irImport.Pkg = nsPkg
			}
		}
	} else if c.cfg.Resolver != nil {
		// Directory import.
		if c.visited[imp.Path] {
			c.error(imp.Pos, "import cycle detected: %q", imp.Path)
		} else {
			c.visited[imp.Path] = true
			docs, err := c.cfg.Resolver.Resolve(c.cfg.FS, uri)
			if err != nil {
				c.error(imp.Pos, "import %q: %v", imp.Path, err)
			}
			if len(docs) > 0 {
				merged := &ir.Package{Symbols: NewSymbolTable(), LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{}, AddressedVars: map[*ir.Var]bool{}}
				for _, d := range docs {
					pkg, diags := Check(d, &Config{
						FS:        c.cfg.FS,
						Dir:       c.cfg.Dir,
						Resolver:  c.cfg.Resolver,
						Languages: c.cfg.Languages,
						Platforms: c.cfg.Platforms,
						Replaces:  c.replaces,
					})
					c.diags = append(c.diags, diags...)
					c.mergePkgInto(merged, pkg)
				}
				irImport.Pkg = merged
			}
		}
	}

	owner := c.importOwner()
	owner.Imports = append(owner.Imports, irImport)

	// A canvas reaches this package if any package it imports declares one:
	// inlining will bring the shapes here, and the pass that lowers them runs
	// on this package. The flag records the construct, and the construct is
	// wherever it was resolved.
	if irImport.Pkg != nil && irImport.Pkg.UsesShapes {
		owner.UsesShapes = true
	}

	// Check for component main in imported library packages. The package's
	// own root only: a lib package's root parents whatever scope was current
	// when it loaded, so a lookup that walks the chain finds the importing
	// file's own main and blames the import for it.
	if irImport.Pkg != nil && irImport.Pkg.Symbols != nil && irImport.Pkg.Symbols.Root != nil {
		if sym, ok := irImport.Pkg.Symbols.Root.LookupLocal("main"); ok {
			if _, isComp := sym.(*ir.Component); isComp {
				c.error(imp.Pos, "component main can only be defined in the main package")
			}
		}
	}

	// A dot import flattens the package's symbols into this scope instead of
	// binding a namespace, so its declarations are referenced unqualified.
	if imp.IsDot() {
		c.flattenDotImport(imp, irImport)
		return
	}

	// Declare namespace in scope.
	// If the new namespace is inert (nil pkg and no resolver) and a namespace
	// with the same alias already exists in scope with a non-nil package (e.g.
	// the predeclared "i18n" stdlib namespace), skip re-declaration so the
	// existing, richer namespace stays accessible. This prevents `import "i18n"`
	// from shadowing the predeclared i18n namespace with a no-op nil-pkg entry.
	ns := &ir.Namespace{
		Name:    alias,
		Pkg:     irImport.Pkg,
		Resolve: nsResolve,
	}
	if ns.Pkg == nil && ns.Resolve == nil {
		if existing, ok := c.scope.Lookup(alias); ok {
			if existingNS, ok := existing.(*ir.Namespace); ok && existingNS.Pkg != nil {
				return // keep the existing richer namespace; don't shadow it
			}
		}
	}
	c.markForeign(irImport.Pkg)
	c.bindDeclared(c.claimTopLevel(alias, imp.Pos, bindAlias, imp.Path), ns)
}

// importOwner is the package an import belongs to. While a library package
// loads, its imports are its own: appending them to the program's package puts
// `import std "sngl://std"` in the IR of every program that reaches i18n.
func (c *checker) importOwner() *ir.Package {
	if c.libLoadPkg != nil {
		return c.libLoadPkg
	}
	return c.pkg
}

// mergePkgInto merges all declarations from src into dst, registering symbols.
func (c *checker) mergePkgInto(dst, src *ir.Package) {
	if src == nil {
		return
	}
	dst.Structs = append(dst.Structs, src.Structs...)
	dst.Enums = append(dst.Enums, src.Enums...)
	dst.Units = append(dst.Units, src.Units...)
	dst.UsesShapes = dst.UsesShapes || src.UsesShapes
	dst.Funcs = append(dst.Funcs, src.Funcs...)
	dst.Components = append(dst.Components, src.Components...)
	dst.Vars = append(dst.Vars, src.Vars...)
	dst.Consts = append(dst.Consts, src.Consts...)
	dst.Imports = append(dst.Imports, src.Imports...)
	for _, sd := range src.Structs {
		c.mergeInto(declPos(sd), dst.Symbols.Root, sd)
	}
	for _, ed := range src.Enums {
		c.mergeInto(declPos(ed), dst.Symbols.Root, ed)
	}
	for _, ud := range src.Units {
		c.mergeInto(declPos(ud), dst.Symbols.Root, ud)
	}
	for _, fn := range src.Funcs {
		// A method is reached through its receiver, not by a package-root
		// name, so it never claims one — two types in one package may each
		// declare a `get`.
		if fn.Receiver != "" {
			continue
		}
		c.mergeInto(declPos(fn), dst.Symbols.Root, fn)
	}
	for _, comp := range src.Components {
		c.mergeInto(declPos(comp), dst.Symbols.Root, comp)
	}
	for _, v := range src.Vars {
		c.mergeInto(declPos(v), dst.Symbols.Root, v)
	}
	for _, v := range src.Consts {
		c.mergeInto(declPos(v), dst.Symbols.Root, v)
	}
}

func (c *checker) registerEnum(e *ast.EnumDef) {
	claimed := c.claimTopLevel(e.Name, e.Pos, bindDecl, "")
	ed := c.buildEnumDef(e)
	c.pkg.Enums = append(c.pkg.Enums, ed)
	c.bindDeclared(claimed, ed)
	c.registerNestedMethods(ed.Name, nil, e.Funcs())
}

func (c *checker) registerStruct(s *ast.StructDef) {
	sd := c.buildStructDef(s)
	c.pkg.Structs = append(c.pkg.Structs, sd)
	c.bindDeclared(c.claimTopLevel(s.Name, s.Pos, bindDecl, ""), sd)
	c.registerNestedMethods(sd.Name, sd.TypeParams, s.Funcs())
}

// registerStructShell registers a struct's name and type parameters without
// resolving its fields, so the type is visible for forward and mutually
// recursive references. resolveStructBody fills in the fields (and nested
// methods) in a later pass1 sub-pass, once every type shell exists.
func (c *checker) registerStructShell(s *ast.StructDef) *ir.StructDef {
	claimed := c.claimTopLevel(s.Name, s.Pos, bindDecl, "")
	sd := &ir.StructDef{AST: s, Name: s.Name, TypeParams: s.TypeParams, Foreign: irForeign(s.Foreign), Options: s.Options}
	c.pkg.Structs = append(c.pkg.Structs, sd)
	c.bindDeclared(claimed, sd)
	return sd
}

func (c *checker) resolveStructBody(sd *ir.StructDef) {
	sd.Fields = c.resolveStructFields(sd.AST)
	c.registerNestedMethods(sd.Name, sd.TypeParams, sd.AST.Funcs())
}

func (c *checker) registerUnit(u *ast.UnitDef) {
	claimed := c.claimTopLevel(u.Name, u.Pos, bindDecl, "")
	ud := c.buildUnitDef(u)
	c.pkg.Units = append(c.pkg.Units, ud)
	c.bindDeclared(claimed, ud)
	// Populate reverse suffix lookup.
	for _, s := range ud.Suffixes {
		c.unitBySuffix[s.Name] = ud
	}
}

func (c *checker) registerConsts(decl *ast.ConstDecl) {
	for _, spec := range decl.Specs {
		typ := c.resolveType(spec.Type)
		var initExpr ir.Expr
		// Validate const initializer only references consts/literals.
		if spec.Default != nil {
			if name := c.nonConstRef(spec.Default); name != "" {
				// Forward-reference: a plain identifier that isn't yet in scope.
				// Sentinel names like "<function call>" come from non-ident
				// non-const refs and stay in the original "non-const" wording.
				if !strings.HasPrefix(name, "<") {
					if _, declared := c.scope.Lookup(name); !declared {
						c.error(decl.Pos, "const initializer forward-references %q (declare it earlier)", name)
					} else {
						c.error(decl.Pos, "const initializer references non-const %q", name)
					}
				} else {
					c.error(decl.Pos, "const initializer references non-const %q", name)
				}
			}
			// Type check initializer.
			initExpr = c.checkExprExpecting(spec.Default, typ)
			initType := exprType(initExpr)
			if typ.Kind != ir.TypeDyn && initType.Kind != ir.TypeDyn && !initType.IsAssignableTo(typ) {
				if adapted, ok := adaptLiteralZero(initExpr, typ); ok {
					initExpr = adapted
				} else {
					want, got := ir.Contrast(typ, initType)
					c.error(decl.Pos, "cannot initialize %s with %s", want, got)
				}
			}
			if typ.Kind != ir.TypeDyn {
				initExpr = wrapIfNeeded(initExpr, typ)
			}
			// Infer type from init if not declared.
			if c.requireValueType(initType, decl.Pos) {
				// Don't propagate void into an inferred const type.
			} else if typ.Kind == ir.TypeDyn {
				typ = initType
			}
		}
		for _, name := range spec.Names {
			v := &ir.Var{
				AST:     decl,
				Name:    name,
				Type:    typ,
				Init:    initExpr,
				IsConst: true,
			}
			c.pkg.Consts = append(c.pkg.Consts, v)
			c.bindVar(decl.Pos, v)
		}
	}
}

// registerConstShells registers a top-level const decl's names (as const Var
// shells with resolved types but no value yet) and defers value checking to
// checkPendingConstInits. This lets a const forward-reference another const or
// use a bare enum member of its declared type — both of which the eager
// registerConsts path rejects because it checks the value before later
// declarations exist.
func (c *checker) registerConstShells(decl *ast.ConstDecl) {
	for _, spec := range decl.Specs {
		typ := c.resolveType(spec.Type)
		// An un-annotated const backed by a literal gets its concrete type on
		// the shell immediately, so a later `var x = SOME_CONST` (registered
		// before the deferred checkPendingConstInits runs) infers the const's
		// type rather than dyn. Non-literal initializers still resolve in the
		// deferred pass.
		if typ.Kind == ir.TypeDyn && spec.Type == nil {
			if lt := literalConstType(spec.Default); lt != nil {
				typ = lt
			}
		}
		vars := make([]*ir.Var, 0, len(spec.Names))
		for _, name := range spec.Names {
			v := &ir.Var{AST: decl, Name: name, Type: typ, IsConst: true}
			c.pkg.Consts = append(c.pkg.Consts, v)
			c.bindVar(decl.Pos, v)
			vars = append(vars, v)
		}
		c.pendingConstInits = append(c.pendingConstInits, pendingConstInit{
			decl: decl, spec: spec, typ: typ, vars: vars,
		})
	}
}

// literalConstType returns the concrete type of a const initializer that is a
// plain literal, or nil when the initializer is absent or non-literal (in
// which case the type is resolved later by checkPendingConstInits).
func literalConstType(e ast.Expr) *ir.Type {
	lit, ok := e.(*ast.LiteralExpr)
	if !ok {
		return nil
	}
	switch lit.Kind {
	case ast.LiteralInt:
		return TypInt
	case ast.LiteralFloat:
		return TypFloat
	case ast.LiteralStringQuoted, ast.LiteralStringBackticked, ast.LiteralStringTrippleQuoted:
		return TypString
	case ast.LiteralBool:
		return TypBool
	}
	return nil
}

// checkPendingConstInits checks the value of every deferred top-level const now
// that all const shells, types, and funcs are registered. Const-ness is judged
// on the resolved IR via ir.IsConst (so a bare enum member or a forward const
// reference is accepted); nonConstRef is consulted only to phrase the error
// when the value is genuinely non-const.
func (c *checker) checkPendingConstInits() {
	for _, p := range c.pendingConstInits {
		if p.spec.Default == nil {
			continue
		}
		typ := p.typ
		initExpr := c.checkExprExpecting(p.spec.Default, typ)

		// nonConstRef is the primary const-ness gate (it recognizes pure-call
		// initializers during pass1, before purity analysis runs, and — now
		// that all const shells are registered — no longer misfires on forward
		// references). ir.IsConst on the resolved IR is an additional acceptor
		// for forms nonConstRef cannot judge from the AST, notably a bare enum
		// member resolved against the declared type.
		if name := c.nonConstRef(p.spec.Default); name != "" && !ir.IsConst(initExpr) {
			if !strings.HasPrefix(name, "<") {
				if _, declared := c.scope.Lookup(name); !declared {
					c.error(p.decl.Pos, "const initializer forward-references %q (declare it earlier)", name)
				} else {
					c.error(p.decl.Pos, "const initializer references non-const %q", name)
				}
			} else {
				c.error(p.decl.Pos, "const initializer references non-const %q", name)
			}
		}

		initType := exprType(initExpr)
		if typ.Kind != ir.TypeDyn && initType.Kind != ir.TypeDyn && !initType.IsAssignableTo(typ) {
			if adapted, ok := adaptLiteralZero(initExpr, typ); ok {
				initExpr = adapted
			} else {
				want, got := ir.Contrast(typ, initType)
				c.error(p.decl.Pos, "cannot initialize %s with %s", want, got)
			}
		}
		if typ.Kind != ir.TypeDyn {
			initExpr = wrapIfNeeded(initExpr, typ)
		}
		finalType := typ
		if c.requireValueType(initType, p.decl.Pos) {
			// Don't propagate void into an inferred const type.
		} else if typ.Kind == ir.TypeDyn {
			finalType = initType
		}
		for _, v := range p.vars {
			v.Init = initExpr
			v.Type = finalType
		}
	}
}

// nonConstRef walks an expression and returns the name of the first
// non-const identifier found, or "" if the expression is const-safe.
func (c *checker) nonConstRef(e ast.Expr) string {
	if e == nil {
		return ""
	}
	switch x := e.(type) {
	case *ast.LiteralExpr, *ast.UnitLiteral:
		return ""
	case *ast.IdentExpr:
		if sym, ok := c.scope.Lookup(x.Name); ok {
			if v, ok := sym.(*ir.Var); ok && v.IsConst {
				return ""
			}
			// Enum/struct types are fine as identifiers.
			if _, ok := sym.(*ir.EnumDef); ok {
				return ""
			}
			if _, ok := sym.(*ir.StructDef); ok {
				return ""
			}
		}
		return x.Name
	case *ast.BinaryExpr:
		if name := c.nonConstRef(x.Left); name != "" {
			return name
		}
		return c.nonConstRef(x.Right)
	case *ast.UnaryExpr:
		return c.nonConstRef(x.Operand)
	case *ast.ParenExpr:
		return c.nonConstRef(x.Inner)
	case *ast.ConstExpr:
		// Trust the const() assertion; the post-purity check emits the
		// precise diagnostic if the operand turns out to be non-const.
		return ""
	case *ast.TernaryExpr:
		if name := c.nonConstRef(x.Cond); name != "" {
			return name
		}
		if name := c.nonConstRef(x.Then); name != "" {
			return name
		}
		return c.nonConstRef(x.Else)
	case *ast.ListExpr:
		for _, el := range x.Elements {
			if name := c.nonConstRef(el); name != "" {
				return name
			}
		}
		return ""
	case *ast.SelectExpr:
		return c.nonConstRef(x.Operand)
	case *ast.InterpolationExpr:
		for _, part := range x.Parts {
			if name := c.nonConstRef(part); name != "" {
				return name
			}
		}
		return ""
	case *ast.StructExpr:
		for _, f := range x.Fields {
			if name := c.nonConstRef(f.Value); name != "" {
				return name
			}
		}
		return ""
	case *ast.CallExpr:
		return c.nonConstCallRef(x)
	}
	return ""
}

// isBuiltinTypeName reports whether name is a builtin type/conversion namespace
// (int, sized numerics, float, string, bool, duration, the string-repr structs,
// and the generic containers). Used by nonConstCallRef so a const initialized
// with any builtin cast — e.g. int32(5), float64(x), duration(1000) — or a
// type-namespace method — map.keys(m), string.length(s) — is recognized as
// const-safe.
//
// Neither half is a hand-maintained list: scalars come from the shared registry
// (ir/builtins.go), and the struct-backed types carry a #[builtin] mark on their
// stdlib declaration. Resolving the mark through scope also makes the check
// respect shadowing, so a user `struct color` is not a builtin conversion (D3).
func (c *checker) isBuiltinTypeName(name string) bool {
	if b, ok := ir.LookupBuiltinScalar(name); ok && b.Convertible {
		return true
	}
	sym, ok := c.scope.Lookup(name)
	if !ok {
		return false
	}
	sd, ok := sym.(*ir.StructDef)
	if !ok {
		return false
	}
	return sd.Builtin.IsStringRepr() || sd.Builtin.IsGeneric()
}

// nonConstCallRef checks whether a call expression is const-safe.

func (c *checker) nonConstCallRef(x *ast.CallExpr) string {
	checkArgs := func() string {
		for _, a := range x.Args.Args {
			if arg, ok := a.(ast.Arg); ok {
				if name := c.nonConstRef(arg.Value); name != "" {
					return name
				}
			}
		}
		return ""
	}

	switch fn := x.Func.(type) {
	case *ast.IdentExpr:
		// A builtin conversion like int32(5) or duration(1000) stays const when
		// its arguments are const — checkArgs recurses into them.
		if c.isBuiltinTypeName(fn.Name) {
			return checkArgs()
		}
	case *ast.SelectExpr:
		if ident, ok := fn.Operand.(*ast.IdentExpr); ok {
			// Type-namespace methods (e.g., string.length("hi"), map.keys(m)).
			if c.isBuiltinTypeName(ident.Name) {
				return checkArgs()
			}
			// Namespace function calls (e.g., docs.Pages()).
			if sym, ok := c.scope.Lookup(ident.Name); ok {
				if _, ok := sym.(*ir.Namespace); ok {
					return checkArgs()
				}
			}
		}
		// Method on const-safe receiver (e.g., "hello".length()).
		if name := c.nonConstRef(fn.Operand); name == "" {
			return checkArgs()
		}
	}
	return "<function call>"
}

func (c *checker) registerVars(decl *ast.VarDecl) {
	for _, spec := range decl.Specs {
		typ := c.resolveType(spec.Type)
		var initExpr ir.Expr
		// Type check initializer.
		if spec.Default != nil {
			initExpr = c.checkExprExpecting(spec.Default, typ)
			// Capturing a context into a var would freeze the value and miss
			// reactive updates. Reject here; read the context at each use site.
			if _, ok := initExpr.(*ir.ContextRead); ok {
				c.error(decl.Pos, "context value cannot be captured into a local var (read at use site instead)")
			}
			initType := exprType(initExpr)
			if typ.Kind != ir.TypeDyn && initType.Kind != ir.TypeDyn && !initType.IsAssignableTo(typ) {
				if adapted, ok := adaptLiteralZero(initExpr, typ); ok {
					initExpr = adapted
				} else {
					want, got := ir.Contrast(typ, initType)
					c.error(decl.Pos, "cannot initialize %s with %s", want, got)
				}
			}
			c.validateStringDomainLiteral(decl.Pos, typ, initExpr)
			if typ.Kind != ir.TypeDyn {
				initExpr = wrapIfNeeded(initExpr, typ)
			}
			// Infer type from init if not declared.
			if c.requireValueType(initType, decl.Pos) {
				// Don't propagate void into an inferred type.
			} else if typ.Kind == ir.TypeDyn {
				typ = initType
			}
		}
		for _, name := range spec.Names {
			if _, exists := c.scope.LookupLocal(name); exists {
				c.error(decl.Pos, "duplicate declaration of %q", name)
				continue
			}
			v := &ir.Var{
				AST:  decl,
				Name: name,
				Type: typ,
				Init: initExpr,
			}
			// Build event handlers.
			for i := range spec.Handlers {
				h := &spec.Handlers[i]
				handler := &ir.EventHandler{
					AST:  h,
					Name: h.Name,
					Func: &ir.Func{
						Params: c.buildParams(h.Params),
					},
				}
				v.Handlers = append(v.Handlers, handler)
			}
			c.pkg.Vars = append(c.pkg.Vars, v)
			c.bindVar(decl.Pos, v)
		}
	}
}

// checkComponentVars type-checks initializers for component-level vars
// that were pre-registered in pass1, and infers types from initializers.
func (c *checker) checkComponentVars(decl *ast.VarDecl, comp *ir.Component) {
	for _, spec := range decl.Specs {
		typ := c.resolveType(spec.Type)
		var initExpr ir.Expr
		if spec.Default != nil {
			initExpr = c.checkExprExpecting(spec.Default, typ)
			// Capturing a context into a var would freeze the value and miss
			// reactive updates. Reject here; read the context at each use site.
			if _, ok := initExpr.(*ir.ContextRead); ok {
				c.error(decl.Pos, "context value cannot be captured into a local var (read at use site instead)")
			}
			initType := exprType(initExpr)
			if typ.Kind != ir.TypeDyn && initType.Kind != ir.TypeDyn && !initType.IsAssignableTo(typ) {
				if adapted, ok := adaptLiteralZero(initExpr, typ); ok {
					initExpr = adapted
				} else {
					want, got := ir.Contrast(typ, initType)
					c.error(decl.Pos, "cannot initialize %s with %s", want, got)
				}
			}
			c.validateStringDomainLiteral(decl.Pos, typ, initExpr)
			if typ.Kind != ir.TypeDyn {
				initExpr = wrapIfNeeded(initExpr, typ)
			}
			if c.requireValueType(initType, decl.Pos) {
				// Don't propagate void into an inferred component var type.
			} else if typ.Kind == ir.TypeDyn {
				typ = initType
			}
		}
		// Update the pre-registered ir.Var's type and init.
		for _, name := range spec.Names {
			for _, v := range comp.Vars {
				if v.Name == name {
					v.Type = typ
					v.Init = initExpr
					break
				}
			}
		}
	}
}

// checkComponentConsts type-checks initializers for component-level consts
// that were pre-registered in pass1, and infers types from initializers.
func (c *checker) checkComponentConsts(decl *ast.ConstDecl, comp *ir.Component) {
	for _, spec := range decl.Specs {
		typ := c.resolveType(spec.Type)
		var initExpr ir.Expr
		if spec.Default != nil {
			if name := c.nonConstRef(spec.Default); name != "" {
				c.error(decl.Pos, "const initializer references non-const %q", name)
			}
			initExpr = c.checkExprExpecting(spec.Default, typ)
			initType := exprType(initExpr)
			if typ.Kind != ir.TypeDyn && initType.Kind != ir.TypeDyn && !initType.IsAssignableTo(typ) {
				if adapted, ok := adaptLiteralZero(initExpr, typ); ok {
					initExpr = adapted
				} else {
					want, got := ir.Contrast(typ, initType)
					c.error(decl.Pos, "cannot initialize %s with %s", want, got)
				}
			}
			if typ.Kind != ir.TypeDyn {
				initExpr = wrapIfNeeded(initExpr, typ)
			}
			if c.requireValueType(initType, decl.Pos) {
				// Don't propagate void into an inferred const type.
			} else if typ.Kind == ir.TypeDyn {
				typ = initType
			}
		}
		// Update the pre-registered ir.Var's type and init.
		for _, name := range spec.Names {
			for _, v := range comp.Vars {
				if v.Name == name {
					v.Type = typ
					v.Init = initExpr
					break
				}
			}
		}
	}
}

func (c *checker) registerFunc(f *ast.FuncDef) {
	fn := c.buildFunc(f)

	// Only free functions bind a file-scope name; a method's name lives under
	// its receiver, checked when it is attached there.
	claimed := true
	if fn.Receiver == "" {
		claimed = c.claimTopLevel(fn.Name, f.Pos, bindDecl, "")
	}

	if fn.Receiver != "" {
		// Attaching is a declaration: it fails on a member of that name
		// already there, unless this one shadows a standard-library member.
		if prev := c.declareMethod(f.Pos, fn); prev != nil {
			c.error(f.Pos, "duplicate declaration of %q on type %s", fn.Name, fn.Receiver)
			return
		}
		c.pkg.Funcs = append(c.pkg.Funcs, fn)
		return
	}

	c.pkg.Funcs = append(c.pkg.Funcs, fn)
	c.bindDeclared(claimed, fn)
}

// stdlibHint returns a suffix naming the import that would bring name into
// scope, for a name the file did not resolve but the standard library
// declares. Missing that one import is the most common way a file fails to
// check, and "unknown component \"vbox\"" on its own does not say so.
func (c *checker) stdlibHint(name string) string {
	// A macro is declared in lib/ but never registered as a function, so it is
	// never in scope: the only way to reach one is a `#[...]` mark. Saying so
	// beats "undefined", which is true but reads as a missing import.
	if uri := macroPackage(name); uri != "" {
		return fmt.Sprintf("; %s is a macro declared by sngl://%s — write it as a mark, not a call", name, uri)
	}
	if c.pkg == nil {
		return ""
	}
	// The hint is for user code. Loading the library to build one while the
	// library is itself loading would re-enter a package mid-load, which
	// libPkg reports as a cycle.
	if len(c.libLoading) > 0 {
		return ""
	}
	// Search every lib package, not just std: the shapes moved to sngl://draw,
	// and naming the wrong package is worse than saying nothing. Loading here
	// is on an error path only.
	// PublicPackages, not Packages: a hint names an import a program could
	// write, so the compiler's own tier and the per-target platform/language
	// packages are not candidates.
	for _, libName := range lib.PublicPackages() {
		if libName == "builtin" {
			continue // ambient; a miss here is not a missing import
		}
		pkg := c.libPkg(libName)
		if _, ok := pkg.Symbols.Root.LookupLocal(name); !ok {
			continue
		}
		path := "sngl://" + libName
		// Already imported under an alias: the name is reachable, just not bare.
		for _, imp := range c.pkg.Imports {
			if imp.Pkg == pkg {
				if imp.Alias == "." {
					return ""
				}
				return fmt.Sprintf("; %s declares it, reach it as %s.%s", imp.Path, imp.Alias, name)
			}
		}
		return fmt.Sprintf("; %s declares it, add import . %q", path, path)
	}
	return ""
}

// isLibraryNamespace reports whether name is in scope as a namespace bound to
// a package of the embedded library. Extension declarations (`component
// <ns>.X`) resolve their prefix this way rather than matching a fixed name, so
// the prefix is whatever alias the file imported the package under — and any
// library package can be extended, not only sngl://std. A platform needs to
// style `draw.canvas` as much as it needs to style `std.vbox`.
func (c *checker) isLibraryNamespace(name string) bool {
	sym, ok := c.scope.Lookup(name)
	if !ok {
		return false
	}
	ns, ok := sym.(*ir.Namespace)
	if !ok || ns.Pkg == nil {
		return false
	}
	for _, pkg := range c.libPkgs {
		if ns.Pkg == pkg {
			return true
		}
	}
	return false
}

func (c *checker) registerComponent(comp *ast.ComponentDecl) {
	// Component extensions: `component sngl.X { platform <name> { ... } }`.
	// A parens form with nothing in them declares no extension: android.sngl
	// writes `component sngl.X() { body }`, whose body the platform reads
	// itself rather than merging as an extension.
	bare := comp.HasParens && len(comp.Props.Props) == 0 && comp.ChildrenType == nil
	if dot := strings.IndexByte(comp.Name, '.'); dot > 0 && !bare {
		namespace := comp.Name[:dot]
		if !c.isLibraryNamespace(namespace) {
			c.error(comp.Pos, "extension namespace %q is not an imported library package; import it, e.g. import %s %q", namespace, namespace, "sngl://std")
			return
		}
		if len(comp.Props.Props) > 0 {
			pos := comp.Pos
			switch p := comp.Props.Props[0].(type) {
			case ast.Param:
				pos = p.Pos
			case ast.EventDecl:
				pos = p.Pos
			}
			c.error(pos, "component extension %q may not declare props (inherited from stdlib)", comp.Name)
			return
		}
		if comp.ChildrenType != nil {
			c.error(comp.Pos, "component extension %q may not declare children type (inherited from stdlib)", comp.Name)
			return
		}
		for _, s := range comp.Body.Stmts {
			switch s.(type) {
			case *ast.PlatformStmt, *ast.Comment:
				continue
			}
			pos := comp.Pos
			if sp := s.StmtPos(); sp != nil {
				pos = *sp
			}
			c.error(pos, "component extension %q body must contain only platform blocks", comp.Name)
			return
		}
		// Suppress normal registration — mergePlatformExtensions owns it.
		return
	}

	irComp := &ir.Component{
		AST:  comp,
		Name: comp.Name,
	}

	// Resolve props and events from PropList.
	for _, p := range comp.Props.Props {
		switch pd := p.(type) {
		case ast.Param:
			prop := &ir.Prop{
				Name:          pd.Name,
				Type:          c.resolveType(pd.Type),
				Bidirectional: pd.Bidirectional,
			}
			if prop.Type.Kind == ir.TypeDyn && pd.Default == nil {
				c.error(comp.Pos, "param %q must have a type hint or a default value", pd.Name)
			}
			// Default is checked later in checkComponentBody when scope is ready.
			irComp.Props = append(irComp.Props, prop)
		case ast.EventDecl:
			evt := &ir.EventDecl{
				Name: pd.Name,
				Type: c.resolveType(pd.Type),
			}
			irComp.Events = append(irComp.Events, evt)
		}
	}

	// Resolve children type.
	if comp.ChildrenType != nil {
		irComp.ChildrenType = c.resolveType(comp.ChildrenType)
	}

	nestedFuncs := c.collectComponentDecls(comp, irComp)

	c.pkg.Components = append(c.pkg.Components, irComp)
	c.bindDeclared(c.claimTopLevel(irComp.Name, comp.Pos, bindDecl, ""), irComp)

	irComp.Funcs = c.registerNestedMethods(irComp.Name, nil, nestedFuncs)
}

// collectComponentDecls walks a component body for nested declarations,
// hoisting struct/enum/unit decls to package scope (no target has a
// per-component type scope) and attaching vars and consts to irComp. The
// nested func defs are returned rather than registered, because the caller
// decides what receiver they get.
//
// checkComponentBody declares comp.Vars into the body scope, so a component
// whose body is checked must have been through here first.
func (c *checker) collectComponentDecls(comp *ast.ComponentDecl, irComp *ir.Component) []*ast.FuncDef {
	var nestedFuncs []*ast.FuncDef
	for _, stmt := range comp.Body.Stmts {
		switch s := ast.UnwrapStmt(stmt).(type) {
		case *ast.StructDef:
			c.registerStruct(s)
		case *ast.EnumDef:
			c.registerEnum(s)
		case *ast.UnitDef:
			c.registerUnit(s)
		case *ast.ConstDecl:
			for _, spec := range s.Specs {
				typ := c.resolveType(spec.Type)
				for _, name := range spec.Names {
					irComp.Vars = append(irComp.Vars, &ir.Var{AST: s, Name: name, Type: typ, IsConst: true})
				}
			}
		case *ast.VarDecl:
			for _, spec := range s.Specs {
				typ := c.resolveType(spec.Type)
				for _, name := range spec.Names {
					v := &ir.Var{AST: s, Name: name, Type: typ}
					for i := range spec.Handlers {
						h := &spec.Handlers[i]
						v.Handlers = append(v.Handlers, &ir.EventHandler{
							AST:  h,
							Name: h.Name,
							Func: &ir.Func{Params: c.buildParams(h.Params)},
						})
					}
					irComp.Vars = append(irComp.Vars, v)
				}
			}
		case *ast.FuncDef:
			nestedFuncs = append(nestedFuncs, s)
		}
	}
	return nestedFuncs
}

func (c *checker) registerRootVisualNode(vn *ast.VisualNode) {
	name := visualNodeTarget(vn)
	if c.isWindowNode(name) {
		w := c.buildWindow(vn)
		c.checkDuplicateWindowID(w, c.pkgWindowIDs)
		c.pkg.Windows = append(c.pkg.Windows, w)
		c.bindWindow(vn.Pos, w)
		return
	}
	if c.builtinNodeKind(name) == ast.BuiltinTimer {
		t := c.buildTimer(vn)
		c.pkg.Timers = append(c.pkg.Timers, t)
		return
	}
	switch name {
	// `output` stays a literal name. It parses as a visual node but is a build
	// directive with its own data structure, not a component — it is only not a
	// parser-level construct so that `output` need not be a keyword. There is
	// nothing in scope for it to resolve to.
	case "output":
		if !c.cfg.IsMain {
			c.error(vn.Pos, "output declarations only permitted in main file")
			return
		}
		c.buildOutputs(vn)
	default:
		c.error(vn.Pos, "unexpected root-level visual node %q", name)
	}
}

// builtinNodeComps indexes the components in comps by their #[builtin] node
// mark. Stdlib registration order is not significant, so the lookup is by tag
// rather than by position.
//
// Two declarations sharing a node kind is a stdlib authoring error, and it
// panics rather than resolving arbitrarily: comps is a map, so picking "the"
// component for a duplicated kind would depend on iteration order and the same
// source would compile differently run to run. Note that a duplicate mark could
// not be an alias even if we tolerated it — struct/component type identity is
// per-declaration (ir.Type.Equal compares Decl), so the mark classifies a
// declaration, it does not make two of them the same type.
func builtinNodeComps(comps map[string]ir.Symbol) map[ast.BuiltinKind]*ir.Component {
	out := map[ast.BuiltinKind]*ir.Component{}
	for _, sym := range comps {
		comp, ok := sym.(*ir.Component)
		if !ok || !comp.Builtin.IsNode() {
			continue
		}
		if prev, dup := out[comp.Builtin]; dup {
			panic(fmt.Sprintf("sngl: components %q and %q both carry #[builtin(%q)]",
				prev.Name, comp.Name, comp.Builtin))
		}
		out[comp.Builtin] = comp
	}
	return out
}

// builtinNodeKind resolves name, through the current scope, to the #[builtin]
// node kind it denotes — i.e. whether a visual node with this target is one of
// the compiler's own constructs (window/timer/slot/errorBoundary) rather than an
// ordinary node instance. Returns BuiltinNone for anything else.
//
// Going through the scope chain rather than comparing against literal names is
// what makes these built-ins shadowable (design doc D3): a user
// `component timer` resolves first and is treated as an ordinary component.
// Qualified targets (`sngl.timer`) are never built-in nodes, matching the
// bare-name-only behaviour this replaces.
func (c *checker) builtinNodeKind(name string) ast.BuiltinKind {
	sym, ok := c.resolveComponentSymbol(name)
	if !ok {
		return ast.BuiltinNone
	}
	comp, ok := sym.(*ir.Component)
	if !ok || !comp.Builtin.IsNode() {
		return ast.BuiltinNone
	}
	return comp.Builtin
}

// resolveComponentSymbol resolves a visual-node target — bare "Foo" or
// qualified "ns.Foo" — to the symbol it was declared as. A namespace's
// platform Resolve fallback is deliberately not consulted: it synthesises
// elements on demand, and a synthesised element never carries a #[builtin]
// mark, so consulting it could only ever produce a false negative at extra
// cost.
func (c *checker) resolveComponentSymbol(name string) (ir.Symbol, bool) {
	if name == "" {
		return nil, false
	}
	nsName, field, qualified := strings.Cut(name, ".")
	if !qualified {
		return c.scope.Lookup(name)
	}
	sym, ok := c.scope.Lookup(nsName)
	if !ok {
		return nil, false
	}
	ns, ok := sym.(*ir.Namespace)
	if !ok || ns.Pkg == nil {
		return nil, false
	}
	return ns.Pkg.Symbols.LookupComponent(field)
}

// isWindowNode reports whether name denotes the built-in window component
// specifically. Window is the only node kind that owns a lexical scope and
// hoists its own element ids, so a few sites care about it by name.
func (c *checker) isWindowNode(name string) bool {
	return c.builtinNodeKind(name) == ast.BuiltinWindow
}

// visualNodeTarget extracts the target name from a VisualNode.
// Returns "name" for bare identifiers and "pkg.Name" for qualified targets
// (e.g. html.div, docui.Sidebar).
func visualNodeTarget(vn *ast.VisualNode) string {
	if vn.Target == nil {
		return ""
	}
	switch t := vn.Target.(type) {
	case *ast.IdentExpr:
		return t.Name
	case *ast.SelectExpr:
		if id, ok := t.Operand.(*ast.IdentExpr); ok {
			return id.Name + "." + t.Field
		}
	}
	return ""
}

// buildOutputs validates and extracts output declarations from an output visual node.
// Supports flat form: output(lang="js", platform="html", stylesheet="...")
// and nested form: output { lang { platform(opts...) } }
func (c *checker) buildOutputs(vn *ast.VisualNode) {
	c.validateOutputArgs(vn)

	// Flat form: output node itself has lang/platform args.
	if c.outputHasLangPlatform(vn) {
		out := &ir.Output{AST: vn}
		for _, a := range vn.Args.Args {
			if arg, ok := a.(ast.Arg); ok && arg.Name != "" {
				switch arg.Name {
				case "lang":
					out.Lang = literalString(arg.Value)
				case "platform":
					out.Platform = literalString(arg.Value)
				}
			}
		}
		merged := c.mergedOptions(vn.Pos, out.Lang, out.Platform)
		out.Options = c.buildOptionsStructLit(vn.Pos, filterOptionArgs(vn.Args, "lang", "platform"), merged)
		c.pkg.Outputs = append(c.pkg.Outputs, out)
		return
	}

	// Nested form: output { lang { platform(opts...) } }
	for _, stmt := range vn.Block.Stmts {
		langNode, ok := stmt.(*ast.VisualNode)
		if !ok {
			c.error(*stmt.StmtPos(), "output block may only contain language targets")
			continue
		}
		c.validateOutputArgs(langNode)
		lang := visualNodeTarget(langNode)

		// Lang node with no block = bare lang (no platforms specified).
		if len(langNode.Block.Stmts) == 0 {
			continue
		}

		for _, langStmt := range langNode.Block.Stmts {
			out := c.buildPlatformOutput(langStmt, lang)
			if out != nil {
				c.pkg.Outputs = append(c.pkg.Outputs, out)
			}
		}
	}
}

// buildPlatformOutput extracts a platform Output from a statement inside a lang block.
// Handles both VisualNode (bare `bubbletea`) and CallStmt (`html(entry="app")`).
func (c *checker) buildPlatformOutput(stmt ast.Stmt, lang string) *ir.Output {
	switch s := stmt.(type) {
	case *ast.VisualNode:
		c.validateOutputArgs(s)
		platform := visualNodeTarget(s)
		if len(s.Block.Stmts) > 0 {
			c.error(s.Pos, "platform %q must not contain a body", platform)
		}
		out := &ir.Output{AST: s, Lang: lang, Platform: platform}
		merged := c.mergedOptions(s.Pos, lang, platform)
		out.Options = c.buildOptionsStructLit(s.Pos, s.Args, merged)
		return out
	case *ast.CallStmt:
		out := &ir.Output{Lang: lang}
		// Extract platform name from call target.
		if ident, ok := s.Call.Func.(*ast.IdentExpr); ok {
			out.Platform = ident.Name
		} else {
			c.error(s.Pos, "platform target must be a simple name")
			return nil
		}
		for _, a := range s.Call.Args.Args {
			if eh, ok := a.(ast.EventHandler); ok {
				c.error(s.Pos, "event handlers not permitted in output declarations")
				_ = eh
				continue
			}
			if arg, ok := a.(ast.Arg); ok && arg.Value != nil {
				if name := c.nonConstRef(arg.Value); name != "" {
					c.error(s.Pos, "output option %q must be a constant expression (references %q)", arg.Name, name)
				}
			}
		}
		merged := c.mergedOptions(s.Pos, lang, out.Platform)
		out.Options = c.buildOptionsStructLit(s.Pos, s.Call.Args, merged)
		return out
	default:
		c.error(*stmt.StmtPos(), "language block may only contain platform targets")
		return nil
	}
}

// filterOptionArgs returns args without entries whose Name is in the exclude
// set. Used for the flat output(lang=..., platform=..., opt=...) form so the
// discriminator args don't leak into the options struct lit.
func filterOptionArgs(args ast.ArgList, exclude ...string) ast.ArgList {
	excluded := make(map[string]bool, len(exclude))
	for _, n := range exclude {
		excluded[n] = true
	}
	out := ast.ArgList{IsMultiline: args.IsMultiline}
	for _, a := range args.Args {
		if arg, ok := a.(ast.Arg); ok && excluded[arg.Name] {
			continue
		}
		out.Args = append(out.Args, a)
	}
	return out
}

// validateOutputArgs checks that an output-level node has no event handlers
// and all option values are constant expressions.
func (c *checker) validateOutputArgs(vn *ast.VisualNode) {
	for _, a := range vn.Args.Args {
		switch a := a.(type) {
		case ast.EventHandler:
			c.error(vn.Pos, "event handlers not permitted in output declarations")
		case ast.Arg:
			if a.Value != nil {
				if name := c.nonConstRef(a.Value); name != "" {
					c.error(vn.Pos, "output option %q must be a constant expression (references %q)", a.Name, name)
				}
			}
		}
	}
}

// outputHasLangPlatform reports whether the output node uses flat form with explicit lang/platform args.
func (c *checker) outputHasLangPlatform(vn *ast.VisualNode) bool {
	for _, a := range vn.Args.Args {
		if arg, ok := a.(ast.Arg); ok && (arg.Name == "lang" || arg.Name == "platform") {
			return true
		}
	}
	return false
}

// pkgProvider is satisfied by both ir.Platform and ir.Language.
type pkgProvider interface {
	Description() string
	Resolve(identifier string) ir.Symbol
}

// lookupTarget finds a registered platform or language by name.
func (c *checker) lookupTarget(name string) pkgProvider {
	if c.cfg == nil {
		return nil
	}
	for _, p := range c.cfg.Platforms {
		if p.PlatformIdentifier() == name {
			return p
		}
	}
	for _, l := range c.cfg.Languages {
		if l.LanguageIdentifier() == name {
			return l
		}
	}
	return nil
}

// lookupOptions returns the Options struct for a platform or lang name.
// Returns nil if no target or no Options struct found.
func (c *checker) lookupOptions(name string) *ir.StructDef {
	if c.optionsCache != nil {
		if sd, ok := c.optionsCache[name]; ok {
			return sd
		}
	}
	t := c.lookupTarget(name)
	if t == nil {
		return nil
	}
	if c.optionsCache == nil {
		c.optionsCache = make(map[string]*ir.StructDef)
	}
	// Read the package source by lib path rather than asking the plugin: the
	// options schema is the same declarations sngl://platforms/<n> holds. Only
	// the Options struct is built — validating output() args must not drag a
	// whole platform package through the checker.
	docs := c.libDocs("platforms/" + name)
	if len(docs) == 0 {
		docs = c.libDocs("languages/" + name)
	}
	for _, doc := range docs {
		for _, stmt := range doc.Stmts {
			if sd, ok := stmt.(*ast.StructDef); ok && sd.Options {
				irSD := c.buildStructDef(sd)
				c.optionsCache[name] = irSD
				return irSD
			}
		}
	}
	c.optionsCache[name] = nil
	return nil
}

// lookupStdlibOptions returns the stdlib's top-level Options struct, or nil
// if the stdlib does not declare one.
//
// It reads sngl://std alone, not the whole embedded corpus: several lib
// packages declare an `Options`, and the corpus is ordered by sorted package
// path, so a scan of all of it would return whichever package sorts first
// rather than the stdlib's.
func (c *checker) lookupStdlibOptions() *ir.StructDef {
	if c.stdlibOptionsSet {
		return c.stdlibOptions
	}
	c.stdlibOptionsSet = true
	for _, doc := range PackageDocsFor("std") {
		for _, stmt := range doc.Stmts {
			if sd, ok := stmt.(*ast.StructDef); ok && sd.Options {
				c.stdlibOptions = c.buildStructDef(sd)
				return c.stdlibOptions
			}
		}
	}
	return nil
}

// mergedOptions returns the synthetic Options struct that unions stdlib,
// language, and platform Options for the given (lang, platform) target. Field
// collisions are allowed only when types match; mismatched-type collisions are
// reported once at the position pos and the offending field is dropped from
// the merged schema.
//
// Returns nil when the named lang/platform is not registered with the checker
// (e.g. a test driver running without targets) — callers must treat that as
// "skip validation" since we can't tell if an arg is valid.
//
// The synthetic struct is not registered in any scope — it's used purely for
// validating output() arg names and types.
func (c *checker) mergedOptions(pos ast.Pos, lang, platform string) *ir.StructDef {
	// If the user named a target that isn't registered, we have no schema for
	// its options. Returning nil tells the caller to skip validation rather
	// than reject options the platform itself would have accepted.
	if lang != "" && c.lookupTarget(lang) == nil {
		return nil
	}
	if platform != "" && c.lookupTarget(platform) == nil {
		return nil
	}

	key := lang + "|" + platform
	if c.mergedOptionsCache == nil {
		c.mergedOptionsCache = make(map[string]*ir.StructDef)
	}
	if sd, ok := c.mergedOptionsCache[key]; ok {
		return sd
	}

	merged := &ir.StructDef{Name: "Options"}
	add := func(source string, sd *ir.StructDef) {
		if sd == nil {
			return
		}
		for _, f := range sd.Fields {
			existing := findField(merged, f.Name)
			if existing == nil {
				merged.Fields = append(merged.Fields, f)
				continue
			}
			if !existing.Type.Equal(f.Type) {
				c.error(pos, "option %q declared with conflicting types: %s vs %s.%s", f.Name, existing.Type, source, f.Name)
			}
			// Same-type collision: keep the first-seen field.
		}
	}
	add("stdlib", c.lookupStdlibOptions())
	if lang != "" {
		add(lang, c.lookupOptions(lang))
	}
	if platform != "" {
		add(platform, c.lookupOptions(platform))
	}

	c.mergedOptionsCache[key] = merged
	return merged
}

func findField(sd *ir.StructDef, name string) *ir.StructField {
	for _, f := range sd.Fields {
		if f.Name == name {
			return f
		}
	}
	return nil
}

// pass1PlatformStmt registers declarations inside a platform block.
// Skipped when the target platform is known and doesn't match.
func (c *checker) pass1PlatformStmt(s *ast.PlatformStmt) {
	if c.cfg.Target != nil && c.cfg.Target.Platform != "" && c.cfg.Target.Platform != s.Platform {
		return
	}
	for _, stmt := range s.Body.Stmts {
		switch inner := ast.UnwrapStmt(stmt).(type) {
		case *ast.Import:
			c.registerImport(inner)
		case *ast.EnumDef:
			c.registerEnum(inner)
		case *ast.StructDef:
			c.registerStruct(inner)
		case *ast.UnitDef:
			c.registerUnit(inner)
		case *ast.ConstDecl:
			c.registerConsts(inner)
		case *ast.VarDecl:
			c.registerVars(inner)
		case *ast.FuncDef:
			c.registerFunc(inner)
		case *ast.ComponentDecl:
			c.registerComponent(inner)
		case *ast.VisualNode:
			c.registerRootVisualNode(inner)
		}
	}
}

// buildOptionsStructLit type-checks each named arg against the merged options
// schema and returns an *ir.StructLit suitable for storing on ir.Output.Options.
// Unknown option names are reported as diagnostics. Args that don't fit the
// arg.Name+arg.Value shape (event handlers, positional args) are silently
// skipped — those are checked elsewhere.
//
// When opts is nil (target not registered with the checker), validation is
// skipped and arg values are checked without an expected-type hint.
func (c *checker) buildOptionsStructLit(pos ast.Pos, args ast.ArgList, opts *ir.StructDef) *ir.StructLit {
	lit := &ir.StructLit{Def: opts}
	if opts != nil {
		lit.Type = &ir.Type{Kind: ir.TypeStruct, Decl: opts}
	}
	for _, a := range args.Args {
		arg, ok := a.(ast.Arg)
		if !ok || arg.Name == "" {
			continue
		}
		var field *ir.StructField
		if opts != nil {
			field = findField(opts, arg.Name)
			if field == nil {
				c.error(pos, "unknown option %q (available: %s)", arg.Name, optionFieldNames(opts))
				continue
			}
		}
		var value ir.Expr
		if arg.Value != nil {
			prev := c.expected
			if field != nil {
				c.expected = field.Type
			}
			value = c.checkExpr(arg.Value)
			c.expected = prev
			if field != nil && value != nil && value.ExprType() != nil && !typeAssignable(value.ExprType(), field.Type) {
				c.error(pos, "option %q: expected %s, got %s", arg.Name, field.Type, value.ExprType())
			}
		}
		lit.Fields = append(lit.Fields, ir.FieldInit{Name: arg.Name, Value: value})
	}
	return lit
}

// typeAssignable reports whether src is assignable to dst, allowing the same
// implicit conversions the rest of the checker permits at boundary positions
// (numeric widening, dyn pass-through, exact match).
func typeAssignable(src, dst *ir.Type) bool {
	if src == nil || dst == nil {
		return true
	}
	if src.Equal(dst) {
		return true
	}
	if src.Kind == ir.TypeDyn || dst.Kind == ir.TypeDyn {
		return true
	}
	return false
}

func optionFieldNames(sd *ir.StructDef) string {
	names := make([]string, len(sd.Fields))
	for i, f := range sd.Fields {
		names[i] = f.Name
	}
	return fmt.Sprintf("%v", names)
}

// checkDuplicateWindowID reports an error if w.Name is non-empty and another
// window with the same Name already exists in seen. Otherwise records w in
// seen and returns.
func (c *checker) checkDuplicateWindowID(w *ir.Window, seen map[string]bool) {
	if w == nil || w.Name == "" || seen == nil {
		return
	}
	if seen[w.Name] {
		c.error(w.AST.Pos, "duplicate window id %q", w.Name)
		return
	}
	seen[w.Name] = true
}

// resolvePositionalArgs maps positional args in an ArgList to named keys using
// the given positional order. Named args are included as-is. Event handlers
// are skipped. The caller handles EventHandler entries separately.
func resolvePositionalArgs(args ast.ArgList, order []string) map[string]ast.Expr {
	result := make(map[string]ast.Expr)
	positional := 0
	for _, a := range args.Args {
		arg, ok := a.(ast.Arg)
		if !ok {
			continue
		}
		if arg.Name == "" {
			if positional < len(order) && arg.Value != nil {
				result[order[positional]] = arg.Value
			}
			positional++
		} else if arg.Value != nil {
			result[arg.Name] = arg.Value
		}
	}
	return result
}

// buildWindow fills in the window a `window #id` node declares. When the id
// was hoisted by declareNodeIDs the shell it bound is the window's symbol
// already, so this sets its fields rather than binding a second symbol over
// the first — references made before the body is checked and after it resolve
// to the same declaration.
func (c *checker) buildWindow(vn *ast.VisualNode) *ir.Window {
	w := c.hoistedWindow(vn.ID)
	if w == nil {
		w = &ir.Window{Name: vn.ID, Typ: c.windowType}
	}
	w.AST = vn
	// URL template params like `{name}` in href become string vars on the
	// window, in scope for the href literal itself as well as the body.
	for _, name := range hrefPathParams(vn) {
		w.Vars = append(w.Vars, &ir.Var{Name: name, Type: TypString})
	}
	c.pushScope()
	defer c.popScope()
	for _, v := range w.Vars {
		c.declare(vn.Pos, v)
	}
	named := resolvePositionalArgs(vn.Args, []string{"title", "href", "favicon"})
	if e, ok := named["href"]; ok {
		w.Href = c.checkExpr(e)
	}
	if e, ok := named["title"]; ok {
		w.Title = c.checkExpr(e)
	}
	if e, ok := named["favicon"]; ok {
		w.Favicon = c.checkExpr(e)
	}
	for _, a := range vn.Args.Args {
		if eh, ok := a.(ast.EventHandler); ok && eh.Name == "error" {
			w.ErrorHandler = c.buildErrorHandler(&eh)
		}
	}
	return w
}

// buildErrorBoundary builds an ir.ErrorBoundary from an errorBoundary visual
// node. The @error handler is required and is type-checked with ErrorEvent
// defaulted on its parameter. Children are type-checked as a sub-block.
func (c *checker) buildErrorBoundary(vn *ast.VisualNode) *ir.ErrorBoundary {
	eb := &ir.ErrorBoundary{AST: vn}
	for _, a := range vn.Args.Args {
		eh, ok := a.(ast.EventHandler)
		if !ok || eh.Name != "error" {
			continue
		}
		eb.Handler = c.buildErrorHandler(&eh)
	}
	if eb.Handler == nil {
		c.error(vn.Pos, "errorBoundary requires an @error handler")
	}
	eb.Children = c.checkBlockIR(&vn.Block)
	return eb
}

func (c *checker) buildTimer(vn *ast.VisualNode) *ir.Timer {
	t := &ir.Timer{
		AST:     vn,
		Handler: &ir.Func{},
	}
	named := resolvePositionalArgs(vn.Args, []string{"interval", "enabled"})
	if e, ok := named["interval"]; ok {
		t.Interval = c.checkExpr(e)
	}
	if e, ok := named["enabled"]; ok {
		t.Enabled = c.checkExpr(e)
	}
	for _, a := range vn.Args.Args {
		if eh, ok := a.(ast.EventHandler); ok && eh.Name == "tick" {
			t.Handler = &ir.Func{
				Params: c.buildParams(eh.Params),
			}
			// Body is checked later in checkTimerBody.
			vn.Block = eh.Body
		}
	}
	return t
}

// literalString extracts the string value from a literal expression.
func literalString(e ast.Expr) string {
	if lit, ok := e.(*ast.LiteralExpr); ok {
		raw := lit.Raw
		// Strip quotes.
		if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
			raw = raw[1 : len(raw)-1]
		}
		return raw
	}
	return ""
}

// checkStructFieldDefaults fills in each struct field's default now that the
// scope holds everything a default may refer to. Declaration time installs a
// placeholder, because a default can name a constant declared further down;
// nothing replaced it, so `struct P { x int = 7 }` carried a default no
// consumer could read and `P{}` had no x at all.
func (c *checker) checkStructFieldDefaults() {
	for _, sd := range c.pkg.Structs {
		c.fillStructFieldDefaults(sd)
	}
}

// fillStructFieldDefaults checks each declared default against its field type
// and stores it, replacing the placeholder installed at declaration time.
func (c *checker) fillStructFieldDefaults(sd *ir.StructDef) {
	if sd == nil || sd.AST == nil {
		return
	}
	byName := make(map[string]*ir.StructField, len(sd.Fields))
	for _, f := range sd.Fields {
		byName[f.Name] = f
	}
	for _, f := range sd.AST.Fields() {
		if f.Default == nil {
			continue
		}
		for _, name := range f.Names {
			// By name rather than by position: one AST group declares several
			// names, and a rejected duplicate leaves the two lists a different
			// length, which silently gave a field its neighbour's default.
			if field, ok := byName[name]; ok {
				field.Default = c.checkExprExpecting(f.Default, field.Type)
			}
		}
	}
}

// --- pass2: type checking ---

func (c *checker) pass2() {
	// Pre-pass: check component nested-method bodies so their return types
	// are inferred before any top-level func body that calls them (test
	// funcs frequently invoke `c.foo()` on a component instance). The full
	// component body (visual nodes, initializer refinement) is still done
	// later via checkComponentBody — this pre-pass only resolves method
	// signatures and Block IRs.
	for _, comp := range c.pkg.Components {
		c.preCheckComponentMethods(comp)
	}

	c.checkStructFieldDefaults()

	// Check function bodies. Skip funcs whose body is checked inside
	// checkComponentBody — that includes (a) plain nested funcs and methods
	// on the surrounding component (Receiver == comp.Name) that need access
	// to component-local vars, and (b) funcs declared with no explicit
	// receiver but defined inside a component body (also in comp.Funcs with
	// Receiver == ""). Foreign-type methods nested inside a component body
	// (e.g. `func int.double(x int)` inside `component main`) ARE in
	// comp.Funcs too, but they don't need component scope; we check them
	// here so their return type is inferred BEFORE any top-level test func
	// (which may call them) is checked.
	compOwnedFuncs := map[*ir.Func]bool{}
	for _, comp := range c.pkg.Components {
		for _, fn := range comp.Funcs {
			if fn.Receiver == "" || fn.Receiver == comp.Name {
				compOwnedFuncs[fn] = true
			}
		}
	}
	for _, fn := range c.pkg.Funcs {
		if compOwnedFuncs[fn] {
			continue
		}
		c.checkFuncBody(fn)
	}

	// Check component bodies.
	for _, comp := range c.pkg.Components {
		c.checkComponentBody(comp)
	}

	// Check window bodies (skip those already checked in context, e.g., inside for-loops).
	for _, w := range c.pkg.Windows {
		if !w.Checked {
			c.checkWindowBody(w)
		}
	}

	// Check timer handler bodies (component timers are checked inside
	// checkComponentBody so they can see component vars in scope).
	for _, t := range c.pkg.Timers {
		c.checkTimerBody(t)
	}

	// Check top-level var handler bodies.
	c.checkVarHandlerBodies(c.pkg.Vars)
	// Component var handlers are checked inside checkComponentBody.

	// Purity + access analysis, over the checked IR with resolved symbols.
	// The var *set* is by pointer identity, so a local that shadows a package
	// var is correctly excluded (fixes the name-collision false positive).
	pkgVarSet := make(map[*ir.Var]struct{}, len(c.pkg.Vars))
	for _, v := range c.pkg.Vars {
		pkgVarSet[v] = struct{}{}
	}
	for _, fn := range c.pkg.Funcs {
		analyzeEffects(fn, pkgVarSet)
	}
	for _, comp := range c.pkg.Components {
		// Component methods read/write the component's own vars (referenced
		// bare, e.g. `name`), so purity and Reads/Writes must be computed
		// against a set that includes them. Using only package vars marks a
		// method like `func isLong() => name.length > 3` as PurityPure with
		// empty Reads — which lets the optimizer const-fold calls to it and
		// leaves reactivity unable to see its dep on `name`.
		varSet := make(map[*ir.Var]struct{}, len(pkgVarSet)+len(comp.Vars))
		maps.Copy(varSet, pkgVarSet)
		for _, v := range comp.Vars {
			varSet[v] = struct{}{}
		}
		for _, fn := range comp.Funcs {
			analyzeEffects(fn, varSet)
		}
	}

	// Transitive purity propagation. analyzePurity above only sees a
	// function's *direct* effects, so a function that merely calls an impure
	// one is left PurityPure — which the optimizer would then const-fold or
	// inline, silently discarding the transitive side effect. Propagate over
	// the user call graph to a fixed point (purity only increases, so this
	// converges), mirroring the stdlib pass's highestCalledPurity loop.
	allFuncs := make([]*ir.Func, 0, len(c.pkg.Funcs))
	allFuncs = append(allFuncs, c.pkg.Funcs...)
	for _, comp := range c.pkg.Components {
		allFuncs = append(allFuncs, comp.Funcs...)
	}
	for changed := true; changed; {
		changed = false
		for _, fn := range allFuncs {
			if fn.Foreign.Name != "" {
				continue // asserted by the mark; the body is only a description
			}
			if p := highestCalledPurity(fn); p > fn.Purity {
				fn.Purity = p
				changed = true
			}
		}
	}

	// Validate deferred const(expr) assertions now that function purities
	// are known.
	for _, a := range c.constAsserts {
		if !ir.IsConst(a.operand) {
			c.error(a.pos, "const() operand is not a constant expression")
		}
	}

	c.dropPlaceholderBodies()
}

// dropPlaceholderBodies discards the body of every intrinsic that did not
// claim it computes the right answer. The body is there to be type checked
// like any other — the mark does not excuse a declaration from that — but a
// backend is meant to replace it, and `return 0` compiles and runs and is
// wrong. Dropping it after checking means nothing downstream has to remember
// to ask: an evaluator with no body cannot answer, and codegen with no body
// and no emitter has nothing to fall through to.
func (c *checker) dropPlaceholderBodies() {
	drop := func(fn *ir.Func) {
		if fn != nil && fn.Intrinsic != "" && !fn.IntrinsicBodyUsable {
			fn.Block = nil
		}
	}
	// A method lives on the declaration it is attached to rather than in
	// pkg.Funcs, and the methods are where most of the marks are.
	seen := map[*ir.Package]bool{}
	var dropPkg func(pkg *ir.Package)
	dropPkg = func(pkg *ir.Package) {
		if pkg == nil || seen[pkg] {
			return
		}
		seen[pkg] = true
		// A receiver that names a namespace rather than a type has no
		// declaration to host its methods, so they live in the synthetic
		// package the namespace points at — html.frontend and html.backend
		// are reachable from nowhere else.
		if pkg.Symbols != nil && pkg.Symbols.Root != nil {
			for _, sym := range pkg.Symbols.Root.Symbols {
				if ns, ok := sym.(*ir.Namespace); ok {
					dropPkg(ns.Pkg)
				}
			}
		}
		for _, fn := range pkg.Funcs {
			drop(fn)
		}
		for _, sd := range pkg.Structs {
			for _, fn := range sd.Methods {
				drop(fn)
			}
		}
		for _, ed := range pkg.Enums {
			for _, fn := range ed.Methods {
				drop(fn)
			}
		}
		for _, ud := range pkg.Units {
			for _, fn := range ud.Methods {
				drop(fn)
			}
		}
		for _, comp := range pkg.Components {
			for _, fn := range comp.Funcs {
				drop(fn)
			}
			for _, fn := range comp.Methods {
				drop(fn)
			}
		}
	}
	dropPkg(c.pkg)
	for _, imp := range c.pkg.Imports {
		if imp != nil {
			dropPkg(imp.Pkg)
		}
	}
	// sngl://builtin is ambient and appears in nobody's import list, and its
	// methods carry the largest share of the marks.
	for _, pkg := range c.libPkgs {
		dropPkg(pkg)
	}
	dropPkg(c.builtinPkg)
	dropPkg(c.stdlibPkg)
}

func (c *checker) checkFuncBody(fn *ir.Func) {
	c.pushScope()
	defer c.popScope()

	// Declare params and fill in their checked IR defaults now that scope is ready.
	astParams := map[string]ast.Param{}
	if fn.AST != nil {
		for _, ap := range fn.AST.Params.Params {
			if ap.Default != nil {
				astParams[ap.Name] = ap
			}
		}
	}
	for _, p := range fn.Params {
		c.declare(funcDeclPos(fn), p)
		if ap, ok := astParams[p.Name]; ok {
			p.Default = c.checkExprExpecting(ap.Default, p.Type)
		}
	}

	prevReturn := c.returnType
	c.returnType = fn.Return
	defer func() { c.returnType = prevReturn }()

	prevTypeParams := c.typeParams
	c.typeParams = fn.TypeParams
	defer func() { c.typeParams = prevTypeParams }()

	if fn.AST != nil && fn.AST.Body != nil {
		body := fn.AST.Body
		bodyExpr := c.checkExpr(body)
		bodyType := exprType(bodyExpr)
		// Infer return type from expression body when there was no annotation.
		// An explicit `dyn` annotation is kept as-is.
		if fn.Return == nil {
			fn.Return = bodyType
		}
		// Expression-body return type check.
		if fn.Return != nil && fn.Return.Kind != ir.TypeDyn && bodyType.Kind != ir.TypeDyn && !bodyType.IsAssignableTo(fn.Return) {
			pos := *body.ExprPos()
			c.error(pos, "cannot return %s as %s", bodyType, fn.Return)
		}
		fn.Block = []ir.Stmt{&ir.Return{AST: &ast.ReturnStmt{Pos: *body.ExprPos(), Value: body}, Value: bodyExpr}}
	} else if fn.AST != nil && fn.AST.Block.IsDefined() {
		fn.Block = c.checkBlockIR(&fn.AST.Block)
		// A block-bodied func with a non-void return type must return on all
		// paths. (Expression bodies always return; void funcs need no return.)
		// An empty `{}` body is not exempt: nothing declares a signature
		// without a body any more, so exempting one would just let
		// `func f() int {}` compile.
		if fn.Return != nil && fn.Return.Kind != ir.TypeVoid && fn.Return.Kind != ir.TypeDyn &&
			!blockAlwaysReturns(fn.Block) && !lastStmtMayDiverge(fn.Block) {
			c.error(fn.AST.Pos, "missing return: %q must return %s on all paths", fn.Name, fn.Return)
		}
	}
}

// blockAlwaysReturns reports whether a statement block is guaranteed to return
// (or otherwise not fall off the end) on every path. Used for missing-return
// analysis on block-bodied funcs with a declared return type.
func blockAlwaysReturns(stmts []ir.Stmt) bool {
	if len(stmts) == 0 {
		return false
	}
	return stmtAlwaysReturns(stmts[len(stmts)-1])
}

func stmtAlwaysReturns(s ir.Stmt) bool {
	switch n := s.(type) {
	case *ir.Return:
		return true
	case *ir.If:
		// An if terminates only when it has an else and both arms terminate.
		return len(n.Else) > 0 && blockAlwaysReturns(n.Body) && blockAlwaysReturns(n.Else)
	case *ir.For:
		// A for terminates only when it has an else and both the body (which
		// returns before the first iteration completes) and the else (empty
		// case) terminate.
		return len(n.Else) > 0 && blockAlwaysReturns(n.Body) && blockAlwaysReturns(n.Else)
	default:
		return false
	}
}

// lastStmtMayDiverge reports whether a block's final statement has control flow
// the checker does not fully model and that may not fall through: a call (which
// may raise or never return), or a build-conditional / structural container.
// Missing-return is suppressed in these cases so a function that in fact always
// diverges or returns is not wrongly rejected.
func lastStmtMayDiverge(stmts []ir.Stmt) bool {
	if len(stmts) == 0 {
		return false
	}
	switch stmts[len(stmts)-1].(type) {
	case *ir.CallStmt, *ir.PlatformFilter, *ir.ErrorBoundary, *ir.SlotInst, *ir.ContextProvider, *ir.NodeInst:
		return true
	}
	return false
}

// preCheckComponentMethods runs an early pass over a component's nested
// methods (Receiver == comp.Name) to infer their return types BEFORE any
// top-level func body that may call them. The pre-pass discards its
// diagnostics — they may be spurious because component var initializers
// haven't been checked yet, so var types are Dyn here. The "real" check
// (with refined types and authoritative diagnostics) runs later inside
// checkComponentBody.
func (c *checker) preCheckComponentMethods(comp *ir.Component) {
	hasNested := false
	for _, fn := range comp.Funcs {
		if fn.Receiver == comp.Name {
			hasNested = true
			break
		}
	}
	if !hasNested {
		return
	}

	c.pushScope()
	defer c.popScope()

	prevComp := c.currentComponent
	c.currentComponent = comp
	defer func() { c.currentComponent = prevComp }()

	for _, p := range comp.Props {
		c.declare(compDeclPos(comp), propParam(p))
	}
	for _, v := range comp.Vars {
		c.declare(varPos(v), v)
	}
	for _, fn := range comp.Funcs {
		if fn.Receiver == "" {
			c.declare(funcDeclPos(fn), fn)
		}
	}

	// Snapshot diagnostics; discard whatever the pre-pass produces. The
	// authoritative method-body check runs again in checkComponentBody.
	diagMark := len(c.diags)
	for _, fn := range comp.Funcs {
		if fn.Receiver == comp.Name {
			c.checkFuncBody(fn)
		}
	}
	c.diags = c.diags[:diagMark]
}

// propParam returns the Param a prop is declared as inside its component's
// body, minting it on first use. The checker walks a component's bodies more
// than once (a pre-pass for method signatures, then the authoritative pass);
// reusing one symbol keeps every Ident in every pass pointing at the same
// declaration, which is what an evaluator keyed by declaration needs.
func propParam(p *ir.Prop) *ir.Param {
	if p.Sym == nil {
		p.Sym = &ir.Param{Name: p.Name}
	}
	p.Sym.Type = p.Type
	return p.Sym
}

func (c *checker) checkComponentBody(comp *ir.Component) {
	c.pushScope()
	defer c.popScope()

	prevComp := c.currentComponent
	c.currentComponent = comp
	defer func() { c.currentComponent = prevComp }()

	// Check prop defaults first (before declaring props as params in scope) so
	// that an unannotated prop's type can be inferred from its default and the
	// param entry we declare below picks up the inferred type.
	if comp.AST != nil {
		propIdx := 0
		for _, p := range comp.AST.Props.Props {
			if pd, ok := p.(ast.Param); ok {
				if propIdx < len(comp.Props) && pd.Default != nil {
					prop := comp.Props[propIdx]
					prop.Default = c.checkExprExpecting(pd.Default, prop.Type)
					initType := exprType(prop.Default)
					if prop.Type.Kind != ir.TypeDyn && initType.Kind != ir.TypeDyn && !initType.IsAssignableTo(prop.Type) {
						c.error(comp.AST.Pos, "default value type %s does not match param type %s", initType, prop.Type)
					}
					if pd.Type == nil && initType.Kind != ir.TypeDyn && initType.Kind != ir.TypeVoid {
						prop.Type = initType
					}
				}
				propIdx++
			}
		}
	}

	// Declare props as params now that any default-driven type inference has
	// finalized prop.Type.
	for _, p := range comp.Props {
		c.declare(compDeclPos(comp), propParam(p))
	}

	// Declare component-level vars and funcs.
	for _, v := range comp.Vars {
		c.declare(varPos(v), v)
	}
	for _, fn := range comp.Funcs {
		if fn.Receiver == "" {
			c.declare(funcDeclPos(fn), fn)
		} else {
			// Type-attached method registered on the symbol table so method
			// lookup at call sites finds it. Nested funcs on this component
			// (desugared with Receiver = comp.Name) are *not* declared in
			// scope by bare name; component-body references resolve through
			// the currentComponent-aware path in inferIdent / inferCall.
			if prev := c.declareMethod(compDeclPos(comp), fn); prev != nil {
				c.error(compDeclPos(comp), "duplicate declaration of %q on component %s", fn.Name, fn.Receiver)
			}
		}
	}

	// Declare named-node ids as component-scoped bindings so a bare reference
	// to a `#id`-tagged node resolves (this is also what lets lowered IR,
	// whose synthesized node handles are referenced by bare name, round-trip
	// through reparse + recheck). Node handles are opaque (dyn) and immutable.
	if comp.AST != nil {
		c.declareNodeIDs(&comp.AST.Body)
	}

	// Check var/const initializers first so types are inferred before function bodies.
	if comp.AST != nil && comp.AST.Body.IsDefined() {
		for _, stmt := range comp.AST.Body.Stmts {
			switch s := stmt.(type) {
			case *ast.ConstDecl:
				c.checkComponentConsts(s, comp)
			case *ast.VarDecl:
				c.checkComponentVars(s, comp)
			}
		}
	}

	// Check nested function bodies (vars are now fully typed). Component
	// nested methods (Receiver = comp.Name) were already pre-checked once
	// in pass2 with provisional var types; running them again here with
	// fully-typed vars produces the authoritative diagnostics. Reset
	// fn.Return so the pre-pass's possibly-Dyn inference doesn't shadow
	// the authoritative one.
	for _, fn := range comp.Funcs {
		// Foreign-type methods nested inside a component body (e.g.
		// `func int.double` inside `component main`) don't need component
		// scope and are checked by pass2's pkg.Funcs loop.
		if fn.Receiver != "" && fn.Receiver != comp.Name {
			continue
		}
		if fn.Receiver == comp.Name && fn.AST != nil && fn.AST.ReturnType == nil {
			fn.Return = nil
		}
		c.checkFuncBody(fn)
	}

	// Check var handler bodies within component scope so handlers can reference component vars.
	c.checkVarHandlerBodies(comp.Vars)

	// Check remaining component body statements.
	if comp.AST != nil && comp.AST.Body.IsDefined() {
		seenWindowIDs := map[string]bool{}
		for _, stmt := range comp.AST.Body.Stmts {
			switch ast.UnwrapStmt(stmt).(type) {
			case *ast.ConstDecl, *ast.VarDecl:
				continue // already checked above
			case *ast.FuncDef:
				continue // already checked above
			default:
				if s := c.checkStmt(stmt); s != nil {
					if w, ok := s.(*ir.Window); ok {
						c.checkDuplicateWindowID(w, seenWindowIDs)
					}
					comp.Body = append(comp.Body, s)
				}
			}
		}
	}

	// Check timer handler bodies inside the component scope so they can
	// reference component-level vars/funcs. Timers themselves were attached
	// to comp.Timers during the body pass above via the timer visual-node
	// special case.
	for _, t := range comp.Timers {
		c.checkTimerBody(t)
	}
}

func (c *checker) checkWindowBody(w *ir.Window) {
	c.pushScope()
	defer c.popScope()

	for _, v := range w.Vars {
		c.declare(varPos(v), v)
	}
	for _, fn := range w.Funcs {
		c.declare(funcDeclPos(fn), fn)
	}
	if w.AST != nil {
		c.declareNodeIDs(&w.AST.Block)
	}
	for _, fn := range w.Funcs {
		c.checkFuncBody(fn)
	}

	if w.AST != nil && w.AST.Block.IsDefined() {
		w.Body = c.checkBlockIR(&w.AST.Block)
	}
}

// declareNodeIDs declares every named visual node's #id within block as a
// component/window-scoped binding, so a bare reference to a `#id`-tagged node
// resolves. Node handles are opaque (dyn) and immutable; this is also the
// mechanism that lets lowered IR — whose synthesized node handles (`__nN`,
// `__root`) are referenced by bare name — round-trip through reparse + check.
func (c *checker) declareNodeIDs(block *ast.StmtBlock) {
	c.declareNodeIDsIn(block, false)
}

// declareNodeIDsIn hoists the node ids in block. inLoop marks a body that a
// `for` repeats: a window id there names the list of windows the loop
// produces, which collectForLoopWindowIDs binds, not a single window.
func (c *checker) declareNodeIDsIn(block *ast.StmtBlock, inLoop bool) {
	if block == nil || !block.IsDefined() {
		return
	}
	for _, s := range block.Stmts {
		c.declareNodeIDsStmt(s, inLoop)
	}
}

func (c *checker) declareNodeIDsStmt(s ast.Stmt, inLoop bool) {
	switch n := s.(type) {
	case *ast.VisualNode:
		isWindow := c.isWindowNode(visualNodeTarget(n))
		if isWindow && inLoop {
			// The loop hoists this id as a list of windows.
			return
		}
		c.declareNodeID(n.ID, isWindow)
		// Descend into the node's own children, but not into a nested
		// window — a window has its own scope and hoists its ids itself.
		if !isWindow {
			c.declareNodeIDsIn(&n.Block, inLoop)
		}
	case *ast.CallStmt:
		// `text #out(...)` / `button(@click)` parse as call statements but
		// carry an element-ref id semantically.
		if _, id, isElem := elementRefCallInfo(n.Call); isElem {
			c.declareNodeID(id, false)
		}
	case *ast.IfStmt:
		c.declareNodeIDsIn(&n.Body, inLoop)
		c.declareNodeIDsIn(&n.Else, inLoop)
	case *ast.ForStmt:
		c.declareNodeIDsIn(&n.Body, true)
		c.declareNodeIDsIn(&n.Else, true)
	case *ast.PlatformStmt:
		c.declareNodeIDsIn(&n.Body, inLoop)
	}
}

func (c *checker) declareNodeID(id string, isWindow bool) {
	if id == "" {
		return
	}
	// Skip if the name already resolves (a prop, var, func, or outer symbol);
	// node ids never shadow an existing binding.
	if _, ok := c.scope.Lookup(id); ok {
		return
	}
	// A window's id names the window itself, so bind the window here and let
	// buildWindow fill it in. Any other node id names a handle to a rendered
	// node, which has no declaration of its own.
	var sym ir.Symbol = &ir.Var{Name: id, Type: ir.TypDyn, IsConst: true}
	if isWindow {
		sym = &ir.Window{Name: id, Typ: c.windowType}
	}
	c.declare(ast.Pos{}, sym)
}

func (c *checker) hoistedWindow(id string) *ir.Window {
	if id == "" {
		return nil
	}
	sym, ok := c.scope.Lookup(id)
	if !ok {
		return nil
	}
	w, isWindow := sym.(*ir.Window)
	if !isWindow || w.Checked {
		return nil
	}
	return w
}

func (c *checker) bindWindow(pos ast.Pos, w *ir.Window) {
	if w.Name == "" {
		return
	}
	if prev, ok := c.scope.LookupLocal(w.Name); ok && prev == ir.Symbol(w) {
		return
	}
	c.declare(pos, w)
}

// hrefPathParams extracts URL template placeholders like {name} from a
// window's href. Both plain literals ("/{name}") and interpolation exprs
// (parser-lifted "/" + name) are handled. Returns the bare identifier name
// for each {x} placeholder.
func hrefPathParams(vn *ast.VisualNode) []string {
	if vn == nil {
		return nil
	}
	for _, a := range vn.Args.Args {
		arg, ok := a.(ast.Arg)
		if !ok || arg.Name != "href" {
			continue
		}
		switch v := arg.Value.(type) {
		case *ast.LiteralExpr:
			return extractBraceParams(strings.Trim(v.Raw, "\""))
		case *ast.InterpolationExpr:
			var out []string
			for _, part := range v.Parts {
				if id, ok := part.(*ast.IdentExpr); ok {
					out = append(out, id.Name)
				}
			}
			return out
		}
	}
	return nil
}

func extractBraceParams(s string) []string {
	var out []string
	for {
		i := strings.Index(s, "{")
		if i < 0 {
			break
		}
		j := strings.Index(s[i:], "}")
		if j < 0 {
			break
		}
		name := s[i+1 : i+j]
		if name != "" {
			out = append(out, name)
		}
		s = s[i+j+1:]
	}
	return out
}

func (c *checker) checkTimerBody(t *ir.Timer) {
	if t.AST == nil || !t.AST.Block.IsDefined() {
		return
	}
	c.pushScope()
	defer c.popScope()
	t.Handler.Block = c.checkBlockIR(&t.AST.Block)
}

// validateStringDomainLiteral checks whether a string literal is valid for a
// special type like color, date, email, etc.
func (c *checker) validateStringDomainLiteral(pos ast.Pos, typ *ir.Type, initExpr ir.Expr) {
	lit, ok := initExpr.(*ir.Literal)
	if !ok || lit.Type.Kind != ir.TypeString {
		return
	}
	val := lit.Raw

	// color/date/time/datetime are StructDef-backed; detect by name and apply
	// the same canonical-form validation that the kind-based types use below.
	switch {
	case ir.IsColorStruct(typ):
		if !isValidColor(val) {
			c.error(pos, "invalid color literal %q", val)
		}
		return
	case ir.IsDateStruct(typ):
		if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`).MatchString(val) {
			c.error(pos, "invalid date literal %q", val)
		}
		return
	case ir.IsTimeStruct(typ):
		if !regexp.MustCompile(`^\d{2}:\d{2}(:\d{2})?$`).MatchString(val) {
			c.error(pos, "invalid time literal %q", val)
		}
		return
	case ir.IsDateTimeStruct(typ):
		if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}`).MatchString(val) {
			c.error(pos, "invalid datetime literal %q", val)
		}
		return
	}

	switch typ.Kind {
	case ir.TypeColor:
		if !isValidColor(val) {
			c.error(pos, "invalid color literal %q", val)
		}
	}
}

func isValidColor(s string) bool {
	if len(s) == 0 {
		return false
	}
	if s[0] == '#' {
		hex := s[1:]
		if len(hex) != 3 && len(hex) != 6 && len(hex) != 8 {
			return false
		}
		for _, c := range hex {
			if !((c >= '0' && c <= '9') || (c >= 'a' && c <= 'f') || (c >= 'A' && c <= 'F')) {
				return false
			}
		}
		return true
	}
	return false
}

func (c *checker) checkVarHandlerBodies(vars []*ir.Var) {
	for _, v := range vars {
		for _, h := range v.Handlers {
			if h.AST == nil || !h.AST.Body.IsDefined() {
				continue
			}
			c.pushScope()
			for _, p := range h.Func.Params {
				c.declare(varPos(v), p)
			}
			h.Func.Block = c.checkBlockIR(&h.AST.Body)
			c.popScope()
		}
	}
}

// flattenDotImport lifts an imported package's exported declarations into the
// current scope, so `import . "p"` makes them available unqualified — the same
// shape loadStdlib gives the stdlib, reached by an explicit import instead.
//
// Imports are processed first in pass1, so anything the user declares afterwards
// lands in the same scope and overwrites the dot-imported binding of that name.
// That is what preserves override semantics: a user declaration shadows a
// dot-imported one exactly as it shadows a lifted stdlib one.
//
// Unexported names are skipped, matching qualified access (see rejectUnexported).
//
// Two dot imports lifting the same name is an error. Silently taking the last
// one would make which package a bare name refers to depend on import order.
func (c *checker) flattenDotImport(imp *ast.Import, irImport *ir.Import) {
	if irImport.Pkg == nil {
		// Macro-only or unresolved package — nothing to lift. Not an error: the
		// import may exist purely to enable a macro.
		return
	}
	pkg := irImport.Pkg
	c.markForeign(pkg)
	claim := func(name string) bool {
		return c.claimTopLevel(name, imp.Pos, bindDot, imp.Path)
	}
	// Methods need no lifting: they live on the receiver's declaration, so
	// lifting the type lifts its members with it.
	for _, sym := range pkg.Symbols.Root.Symbols {
		if !exported(sym) {
			continue
		}
		// Don't re-bind a namespace the imported package itself imported; dot
		// import lifts the package's own declarations, not its import graph.
		// The stdlib is the exception: `i18n`, `html` and `lower` are
		// namespaces it declares as part of its own surface.
		if _, isNS := sym.(*ir.Namespace); isNS {
			if pkg != c.stdlibPkg {
				continue
			}
			// Deliberately unclaimed. A lifted namespace names a package, and
			// a file may name that same package itself — `import
			// "sngl://internal/lower"` alongside the stdlib that already exposes
			// it. Both bindings mean the same package, so this is a restated
			// name rather than an ambiguous one. Only the stdlib lifts
			// namespaces, so no second dot import can disagree about one.
			c.bindLib(imp.Pos, c.scope, sym)
			continue
		}
		if !claim(sym.SymName()) {
			continue
		}
		c.bindLib(imp.Pos, c.scope, sym)
	}
}
