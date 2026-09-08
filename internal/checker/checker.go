package checker

import (
	"fmt"
	"io/fs"
	"maps"
	"path"
	"regexp"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/imports"
	"git.duckfam.us/jonathan/sngl/ir"
	"git.duckfam.us/jonathan/sngl/lib"
)

type Config struct {
	FS  fs.FS  // filesystem for resolving relative imports
	Dir string // OS directory for scheme imports
	// PkgPath is where the package being checked sits relative to FS's root,
	// so that a relative import it writes is rebased onto it before the
	// resolver reads it. Empty for the package the caller named.
	PkgPath   string
	IsMain    bool           // whether this is the program's own package, not a dependency
	Resolver  ImportResolver // import resolver (nil = no imports)
	Languages []ir.Language  // registered languages
	Platforms []ir.Platform  // registered platforms
	// Targets are the compile targets this check is for. A target's library
	// package is loaded as though the document had written
	// `import _ "sngl:platform/<it>"`, so its overrides are checked and its
	// failures are this build's. Empty is not "check nothing": the document's
	// own `output` blocks name targets too, and an explicit import of one
	// names it as well.
	Targets []ir.StaticTarget
	// Replaces maps local import paths to replacement URLs, supplied by an
	// outer (main) package. Entries here override any `=>` mapping declared
	// in the package being checked.
	Replaces map[string]string
	// LibSources substitutes the source of a library package, keyed by lib
	// path ("platform/teststub"). It exists for the in-test platform stubs,
	// which register a plugin with no lib/ directory behind it; production
	// callers leave it nil. A substitution replaces the package, where the
	// source a target provides (ProvidedDocs) adds to it.
	LibSources map[string][]*ast.Document
	// libs is the library-package cache this check shares with the nested
	// checks its imports start. Unexported: it is the compiler's own
	// bookkeeping, and a cache built against other platforms would hand this
	// check their declarations.
	libs *libCache
	// visited is the stack of directory packages currently being checked,
	// shared with the nested checks so that a package importing itself back is
	// reported. Two files of one package importing the same sibling is not a
	// cycle, which is why entries are removed once the package is checked.
	visited map[string]bool
	// dirPkgs memoizes the directory packages this build has checked, keyed by
	// their path under FS. Checking one twice would give the build two
	// declarations of each of its types, and type identity is
	// per-declaration -- which reads as "cannot pass Op as Op" at the call
	// site where the two meet.
	dirPkgs map[string]*ir.Package
}

// visitedStack is the import stack a nested check inherits, or a fresh one for
// the package the caller named.
func (cfg *Config) visitedStack() map[string]bool {
	if cfg != nil && cfg.visited != nil {
		return cfg.visited
	}
	return map[string]bool{}
}

// dirPkgCache is the checked-package cache a nested check inherits, or a fresh
// one for the package the caller named.
func (cfg *Config) dirPkgCache() map[string]*ir.Package {
	if cfg != nil && cfg.dirPkgs != nil {
		return cfg.dirPkgs
	}
	return map[string]*ir.Package{}
}

// libCache holds the library packages one build has loaded. It is shared with
// the nested checks an import starts, so a declaration of sngl:ui is the
// same *ir.Component in every package of the build — a platform extension is
// attached to that one declaration, and type identity is per-declaration.
type libCache struct {
	pkgs    map[string]*ir.Package
	loading map[string]bool
	// extended records which target packages' `component sngl.X` overrides
	// have been merged into these packages, keyed by lib path. Sharing the
	// packages means sharing the components the bodies attach to, so merging
	// the same one twice would report every override as a duplicate platform
	// block.
	extended map[string]bool
	// roles records the declaration each #[builtin] kind named in library
	// source. A mark binds its declaration to the checker that registered it,
	// but the packages are shared: a nested check reaches them from the cache
	// with nothing to register, so it takes the same references from here.
	roles map[ir.BuiltinKind]ir.Symbol
	// macroSigs records the macro declarations whose parameter types have
	// been resolved; see resolveMacroSig.
	macroSigs map[*ir.Func]bool
	// Shared for the same reason `extended` is: the packages are shared, so a
	// later check must re-bind the same symbol rather than a second one, which
	// Declare would rightly call a redeclaration.
}

// libCache returns the library-package cache a check runs against: the one an
// outer check handed down, or a new one.
func (cfg *Config) libCache() *libCache {
	if cfg != nil && cfg.libs != nil {
		return cfg.libs
	}
	return &libCache{pkgs: map[string]*ir.Package{}, loading: map[string]bool{}, extended: map[string]bool{}, roles: map[ir.BuiltinKind]ir.Symbol{}, macroSigs: map[*ir.Func]bool{}}
}

type ImportResolver interface {
	// Resolve resolves a directory import to parsed AST documents.
	Resolve(fsys fs.FS, importPath string) ([]*ast.Document, error)

	// ResolveScheme resolves a scheme-based import to native (language-level)
	// declarations (e.g. go:pkg/path).
	ResolveScheme(scheme, uri, dir string) (*ir.NativeImport, error)

	// ResolveSchemeFS resolves a scheme-based import to a set of SNGL .sngl
	// documents plus the filesystem they were read from. Used by FS schemes
	// like git:// that vend remote SNGL packages. Returns (nil, nil, nil) when
	// the scheme is not FS-registered — callers should then try ResolveScheme.
	// The returned subFS is rooted at the package's cache directory so that
	// transitive imports inside the package resolve against it.
	ResolveSchemeFS(scheme, uri, dir string) (docs []*ast.Document, subFS fs.FS, err error)
}

// Check type-checks one parsed document. A document whose statements came from
// several files is a package of those files -- which is what the CLI hands in,
// having merged a directory into one document -- so it is split back apart and
// checked as one.
func Check(doc *ast.Document, cfg *Config) (*ir.Package, []ir.Diagnostic) {
	return CheckPackage(splitByFile(doc), cfg)
}

// splitByFile groups a document's statements by the file they were parsed
// from, preserving order. A statement with no position joins the group being
// built, so a synthesized declaration stays with its neighbours.
func splitByFile(doc *ast.Document) []*ast.Document {
	var out []*ast.Document
	cur, curFile := (*ast.Document)(nil), ""
	for _, stmt := range doc.Stmts {
		file := ""
		if p := stmt.StmtPos(); p != nil {
			file = p.File
		}
		if cur == nil || (file != "" && file != curFile) {
			cur = &ast.Document{BlankLines: doc.BlankLines}
			out = append(out, cur)
			curFile = file
		}
		cur.Stmts = append(cur.Stmts, stmt)
	}
	if len(out) <= 1 {
		// One file: hand back the document itself, so callers holding it (the
		// mark scope, the formatter) keep the identity they parsed.
		return []*ast.Document{doc}
	}
	return out
}

// CheckPackage type-checks a package's documents together.
//
// A package is one declaration set however many files it is written in, so a
// type annotated in one file may name a type declared in a sibling: pass1
// registers every document's declarations before pass2 resolves any of them.
// Checking the files one at a time instead resolved each without its siblings
// in scope, which reached users as "unknown type" on a name the package
// plainly declares.
//
// Imports stay the file's own: the alias in `import draw "sngl:ui/draw"` binds
// where it is written and nowhere else.
func CheckPackage(docs []*ast.Document, cfg *Config) (*ir.Package, []ir.Diagnostic) {
	c := newChecker(docs, cfg)
	c.pass1()
	// A program's own overrides resolve after pass1: an override may be
	// written above the declaration it overrides, and may name a component
	// this file declares further down.
	c.collectUserOverrides()
	c.collectFuncOverrides()
	// Body-check pending stdlib platform extensions after user pass1 so that
	// user-declared symbols are in scope when the platform body resolves
	// identifiers. Collection (the AST walk that enumerates pending bodies)
	// happened in newChecker; checkPendingExtensions populates each stdlib
	// Component's PlatformOverrides map.
	c.checkPendingExtensions()
	c.pass2()
	// After pass2: an override body is checked against the base's signature,
	// which pass2 has finished resolving, and may name anything the program
	// declares.
	c.checkPendingFuncOverrides()
	c.verifyRefCoercions(c.pkg)
	c.analyzeErrors()
	c.analyzeAsync()
	analyzePointsTo(c.pkg)
	c.analyzeAsyncWithPointsTo()
	c.checkAsyncRules()
	c.pkg.Symbols = c.symtab
	ir.Normalize(c.pkg)
	return c.pkg, c.diags
}

type checker struct {
	// docs is the package being checked: every file of it, registered as one
	// declaration set. doc is whichever one is being registered or checked
	// right now.
	doc  *ast.Document
	docs []*ast.Document
	cfg  *Config

	// fileScopes holds one scope per file, carrying that file's imports and
	// nothing else. It sits between the package scope and the stdlib, so a
	// declaration still shadows a dot-imported name while an alias one file
	// binds stays invisible to its siblings. `enterFile` swaps which one the
	// package scope's parent is; `importScope` is where an import binds.
	fileScopes map[*ast.Document]*ir.Scope
	// fileScopesByName resolves a declaration back to its file in pass2, where
	// the work is driven by the package's declarations rather than by its
	// files.
	fileScopesByName map[string]*ir.Scope
	// curFileScope is the file whose imports are in scope, or nil outside any
	// file (loading a library, checking synthesized IR).
	curFileScope *ir.Scope
	// fileTopLevel keeps each file's `topLevel` map across the several passes
	// pass1 makes over the package.
	fileTopLevel map[*ast.Document]map[string]topLevelBinding

	pkg    *ir.Package
	diags  []ir.Diagnostic
	scope  *ir.Scope
	symtab *ir.SymbolTable

	// refCoercions are the call sites that handed a plain T to a ref<T>
	// parameter; see verifyRefCoercions for why the callee is held to its half
	// of the bargain only once every body has been checked.
	refCoercions []refCoercion

	// Unit suffix reverse lookup.

	// bodyTypeDecls is where each body-local type name was first declared, per
	// package it lands in. Interim, for the #198 collision error only.
	bodyTypeDecls map[*ir.Package]map[string]ast.Pos

	// bodyComps is every component declared inside a body, in registration
	// order. Interim, for the #198 collision report only.
	bodyComps []*ir.Component

	// bodyChecked is the components whose body pass2 has already walked. A
	// body may register another as it is checked, so the walk is resumable.
	bodyChecked map[*ir.Component]bool

	// bodyOwner is the body a body-local component was declared in, so its own
	// body can be checked with its siblings in scope.
	bodyOwner map[*ir.Component]*ir.Component

	// inferring is the stack of functions whose return type is being inferred,
	// so a body that calls itself is reported rather than recurring.
	inferring map[*ir.Func]bool

	// Import cycle detection: the stack of directory packages being checked.
	visited map[string]bool
	// dirPkgs is the build's checked-package cache, shared with every nested
	// check so that one directory yields one set of declarations.
	dirPkgs map[string]*ir.Package

	// foreign marks declarations that arrived from another package, so their
	// unexported members stay private to it.
	foreign map[ir.Symbol]bool

	// topLevel records every name bound at file scope and how it got there,
	// so two bindings of one name are reported instead of silently resolving
	// by declaration order. Reset per file: an alias is one file's.
	topLevel map[string]topLevelBinding

	// pkgDecls is where each name this package declares was declared, since a
	// declaration is package-wide while topLevel is per file. Per declaration
	// set: enterPackage clears it for a library loading mid-pass1 and puts the
	// program's back.
	pkgDecls map[string]ast.Pos

	// Effective replace map for this package: outer overrides layered over
	// this package's own `import "p" => "url"` declarations. Populated at the
	// start of pass1 before any import is resolved.
	replaces map[string]string

	// targets is what this check is building for: what the caller named, or --
	// when it named nothing -- what this document's own `output` blocks
	// declare. Resolved once because three things read it and they have to
	// agree: which target packages load, which `platform` blocks are checked,
	// and what a nested check inherits. An imported document declares no
	// `output` of its own, so it can only inherit.
	targets []ir.StaticTarget

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
	// funcDepth is non-zero while a function or handler body is being checked.
	// A slot insertion renders where it is written, so one reaching a func body
	// has nowhere to project and is reported rather than built. It is also
	// what says a statement is *not* in a view body, which is where a loop
	// that iterates nothing -- a condition or a forever loop -- is refused.
	funcDepth int
	// loopDepth is the number of `for` bodies enclosing the statement being
	// checked, and what `break` and `continue` require one of. It resets at
	// every imperative-body boundary (enterFuncBody): a lambda written inside
	// a loop body cannot escape the loop it was written in.
	loopDepth int

	// Tracks window #id collisions at package scope.
	pkgWindowIDs map[string]bool

	outputDecl *ast.VisualNode

	// pendingPkgBody holds the statements written at the package's top level,
	// collected in pass1 and checked in pass2. They cannot be checked where
	// they are found: they read vars and instantiate components that pass1 is
	// still registering.
	pendingPkgBody []ast.Stmt

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
	// `import <alias> "sngl:ui"` and flattened by the dot form.
	stdlibPkg *ir.Package
	// libs memoizes loaded sngl:<name> packages and guards against a cycle
	// among them. Shared with the checks this one's imports start.
	libs     *libCache
	libDepth int
	// markAliases and markDotPkgs resolve the package a #[...] mark names,
	// for the documents whose declarations are being registered. A lib load
	// swaps them for its own (setMarkScope): an alias binds a macro package
	// the way it binds any other imported name.
	markAliases map[string]imports.ImportRef
	markDotPkgs []string
	// libLoadPkg is the library package currently loading, and the owner of
	// any import registered while it does. nil outside a lib load.
	libLoadPkg *ir.Package
	// libImportScope is where an import registered while a library package
	// loads binds its namespace, and where a dot import lifts into. It sits
	// above the package's own root, so what the package imports is reachable
	// while it loads without becoming part of what it exports.
	libImportScope *ir.Scope
	// libPkgName is the URI of the lib package currently being loaded, stamped
	// onto every declaration it builds as that declaration's identity (see
	// ir.StructDef.Pkg). Saved and restored around each load, because a lib
	// package's import of another nests one load inside the other.
	libPkgName string
	// macroStruct is sngl:internal/ir's `Macro`, the return type that makes a
	// declared function a macro. Held as the declaration rather than the name
	// because type identity is per-declaration.
	macroStruct *ir.StructDef

	// anonStructs interns one synthesized declaration per package and
	// canonical anonymous struct signature; see internAnonStruct. anonNames
	// is the names already taken in each, since two signatures can spell one.
	anonStructs map[*ir.Package]map[string]*ir.StructDef
	anonNames   map[*ir.Package]map[string]bool

	// builtinPkg is sngl:builtin, registered ambiently into every file.
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

	// The target-identity types. Held so PLATFORM and LANGUAGE can be typed
	// and so a target's synthesized identity const has a type to carry; the
	// declarations are in lib/builtin and are shadowable like any other.
	platformType *ir.StructDef
	languageType *ir.StructDef
	// userOverrides and userFuncOverrides are the program's own
	// `X[target]` declarations, merged after pass1 by collectUserOverrides.
	userOverrides        []*ast.ComponentDecl
	userFuncOverrides    []*ast.FuncDef
	pendingFuncOverrides []pendingFuncOverride
	// pendingConstruct is the #[construct] marks written in this package,
	// waiting for a type complete enough to judge. See checkConstructProps.
	pendingConstruct []constructMark

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
	// stdComp.PlatformOverrides[platformName] = checkedIRBody mapping.
	pendingExtensions []pendingExtension

	// providedCache memoizes providedDocs for the life of this checker; see
	// the comment there for why the scope is exactly one check.
	providedCache map[string][]*ast.Document

	// Flow narrowing of option<T> after a null test. narrowed holds the facts
	// current at the statement being checked, narrowUsed says which of them a
	// read actually went through, and narrowChecks defers the soundness
	// question to after the purity fixed point. noNarrow suspends the rewrite
	// where an lvalue is being checked. See narrow.go.
	narrowed     map[narrowKey]narrowFact
	narrowUsed   map[narrowKey]bool
	narrowChecks []narrowCheck
	noNarrow     int

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

func newChecker(docs []*ast.Document, cfg *Config) *checker {
	symtab := NewSymbolTable()
	c := &checker{
		doc:          firstDoc(docs),
		docs:         docs,
		cfg:          cfg,
		pkg:          &ir.Package{LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{}, AddressedVars: map[*ir.Var]bool{}},
		symtab:       symtab,
		scope:        symtab.Root,
		visited:      cfg.visitedStack(),
		dirPkgs:      cfg.dirPkgCache(),
		pkgWindowIDs: make(map[string]bool),
		libs:         cfg.libCache(),
	}
	// Allocated before the library loads, because those now run the same
	// pass1 a program's package does, and pass1 enters a file per document.
	c.fileScopes = make(map[*ast.Document]*ir.Scope, len(docs))
	c.fileScopesByName = make(map[string]*ir.Scope, len(docs))
	c.fileTopLevel = make(map[*ast.Document]map[string]topLevelBinding, len(docs))
	// Set before the library loads, which swap in their own: a lib load
	// restores what it found, and what it finds must be the program's.
	// Insert stdlib scope between base and Root so user declarations shadow stdlib.
	stdlibScope := NewScope(symtab.Root.Parent) // parent = baseScope
	c.scope = stdlibScope
	c.builtinPkg, c.stdlibPkg = c.loadStdlib()
	// A library package the cache already held registered nothing here, so the
	// references its marks bound are taken from the cache instead.
	for kind, sym := range c.libs.roles {
		c.bindBuiltinRole(kind, sym)
	}
	c.stdlibScope = stdlibScope
	if c.windowComp == nil {
		// The stdlib is embedded and compiler-controlled; a missing window
		// declaration would silently turn every `window #id` into "unexpected
		// root-level visual node". Fail loudly, as parseStdlibDocs does.
		panic("sngl: embedded stdlib declares no #[builtin(\"window\")] component")
	}
	c.windowType = c.windowComp.SymType()
	c.typeTargetConsts()
	symtab.Root.Parent = stdlibScope
	c.scope = symtab.Root

	// One import scope per file, spliced between the package scope and the
	// stdlib. Declarations keep going into Root, so nothing about how a
	// declaration is registered changes; what changes is which imports Root's
	// parent chain reaches, and enterFile is what swaps that. A document with
	// no scope here -- a library's, loaded through this same checker -- binds
	// its imports where importScope says a library's belong.
	for _, d := range docs {
		fs := NewScope(stdlibScope)
		c.fileScopes[d] = fs
		if name := docFileName(d); name != "" {
			c.fileScopesByName[name] = fs
		}
	}

	// Inject all registered platform and language names as namespaces so a
	// target's own declarations are reachable unqualified by name (html.div).
	// The namespace is ambient for every platform. The standard library may
	// also declare a namespace of the same name (lib/std/html.sngl declares
	// `html` for the html.frontend/html.backend placement directives); the
	// target's package goes behind that one rather than shadowing it.
	// A target's package is reached by importing it, like any other. It used
	// to be bound here for every registered target, so `html.div` resolved in
	// a file that imported nothing -- and a misspelled tag reached the element
	// wildcard of a package the program never named. The package's own source
	// binds the namespace while it loads (loadStdlibPackage); an override's
	// target index binds it for the merge (mergeTargetExtensions).
	_ = stdlibScope

	// Splice the build target's extension bodies into the stdlib components
	// they target. AST splicing happens here so that user pass1/pass2 see
	// body-bearing stdlib components; IR body checking runs from Check() after
	// user pass1, so a body can reference a user-declared symbol.
	//
	// Building for a target is an `import _ "sngl:platform/<it>"` nobody
	// wrote, so only the target's package loads here. A program that imports
	// one itself gets the same treatment where the import is checked, which is
	// how it asks to be held to a platform's rules without naming any of its
	// declarations.
	c.targets = c.resolvedTargets()
	for _, pkgName := range c.targetPackages() {
		c.mergeTargetExtensions(pkgName)
	}

	return c
}

// docFileName is the file a document was parsed from, taken from the first
// statement that carries a position. A synthesized document has none, and
// nothing in pass2 needs to find its way back to one.
func docFileName(d *ast.Document) string {
	for _, stmt := range d.Stmts {
		if p := stmt.StmtPos(); p != nil && p.File != "" {
			return p.File
		}
	}
	return ""
}

// saveFile captures which file is current and returns the restore. A library
// package now runs the same pass1 a program's does, and pass1 enters a file
// per document -- so a lib load that happened while the program was mid-file
// left the program with no file current, and the imports registered after it
// bound into the package scope where every sibling could see them.
func (c *checker) saveFile() func() {
	doc, scope := c.doc, c.curFileScope
	return func() {
		c.doc, c.curFileScope = doc, scope
		if c.curFileScope != nil {
			c.symtab.Root.Parent = c.curFileScope
		}
	}
}

// enterFile makes d the current file: its imports come into scope, and
// `c.doc` is what the per-file registration loops read.
func (c *checker) enterFile(d *ast.Document) {
	c.doc = d
	c.curFileScope = c.fileScopes[d]
	if c.curFileScope != nil {
		c.symtab.Root.Parent = c.curFileScope
	}
	// Allocated rather than left nil so that every later pass over this file
	// adds to the same map: claimTopLevel allocates lazily, and a lazily
	// allocated one would be dropped when the next pass resumed the file.
	c.topLevel = map[string]topLevelBinding{}
	c.fileTopLevel[d] = c.topLevel
}

// resumeFile re-enters a file that enterFile has already run over, restoring
// the names it bound. pass1 makes several passes over the package and each one
// has to see what the ones before it claimed in this file -- a declaration
// clashing with an alias is reported where the declaration is registered,
// which is a later pass than the import.
func (c *checker) resumeFile(d *ast.Document) {
	c.doc = d
	c.curFileScope = c.fileScopes[d]
	if c.curFileScope != nil {
		c.symtab.Root.Parent = c.curFileScope
	}
	c.topLevel = c.fileTopLevel[d]
}

// enterFileOf makes the file that pos came from current, so that a body
// checked in pass2 -- which is driven by the package's declarations, not by
// its files -- resolves through the imports of the file it was written in.
// A position naming no file leaves the scope alone: synthesized IR has no
// imports of its own to reach.
func (c *checker) enterFileOf(pos ast.Pos) {
	if pos.File == "" {
		return
	}
	if fs, ok := c.fileScopesByName[pos.File]; ok {
		c.curFileScope = fs
		c.symtab.Root.Parent = fs
	}
}

// symType is a symbol's type, with an inferred return type resolved first.
// Every place the checker turns a resolved symbol into a type goes through
// here, because until a `func f() => expr` has had its body checked its return
// type is nil, and nil reads as void.
func (c *checker) symType(sym ir.Symbol) *ir.Type {
	if fn, ok := sym.(*ir.Func); ok {
		c.ensureReturnType(fn)
	}
	if sym == nil {
		return nil
	}
	return sym.SymType()
}

// ensureReturnType checks fn's body when its return type is still waiting on
// it. pass2 checks bodies in declaration order, so a call written above the
// declaration -- or in whichever file of the package the directory happened to
// be read first -- saw a function with no return type yet and typed the call
// void. Order is not supposed to matter, and across files there is no order to
// appeal to.
//
// Only an expression body infers: a block-bodied function with no annotation
// returns nothing, by declaration rather than by inference.
func (c *checker) ensureReturnType(fn *ir.Func) {
	if fn == nil || fn.Return != nil {
		return
	}
	if fn.AST == nil || fn.AST.Body == nil {
		return
	}
	if c.inferring[fn] {
		// `func f() => f()` has no type to arrive at. Reported here rather
		// than left to recur, and typed dyn so the rest of the file checks.
		c.error(funcDeclPos(fn), "cannot infer the return type of %q from a body that calls itself; annotate it", fn.Name)
		fn.Return = TypDyn
		return
	}
	if c.inferring == nil {
		c.inferring = map[*ir.Func]bool{}
	}
	c.inferring[fn] = true
	defer delete(c.inferring, fn)

	// A top-level body resolves in package scope, not in whatever body was
	// being checked when the call to it was reached.
	savedScope, savedComp := c.scope, c.currentComponent
	c.scope, c.currentComponent = c.symtab.Root, nil
	defer func() { c.scope, c.currentComponent = savedScope, savedComp }()

	// Only the return type is wanted here. pass2 checks this body again in its
	// own order — that pass is the authoritative one, and it is where the
	// diagnostics belong, so anything said here is dropped rather than said
	// twice.
	mark := len(c.diags)
	c.checkFuncBody(fn)
	c.diags = c.diags[:mark]
}

// fileOf brings the imports of the file pos names into scope and returns the
// undo. pass2 is driven by the package's declarations rather than by its
// files, so each body says which file it was written in:
//
//	defer c.fileOf(compDeclPos(comp))()
//
// A position naming no file changes nothing, which is what synthesized IR and
// every single-file package want.
func (c *checker) fileOf(pos ast.Pos) func() {
	savedScope := c.curFileScope
	savedParent := c.symtab.Root.Parent
	c.enterFileOf(pos)
	return func() {
		c.curFileScope = savedScope
		c.symtab.Root.Parent = savedParent
	}
}

// declare binds sym in the current scope, reporting a name already bound there
// instead of letting the later binding silently win. A reserved name is
// reported and then bound anyway, so one error does not become a
// name-not-found at every use.
func (c *checker) declare(pos ast.Pos, sym ir.Symbol) {
	c.rejectReservedName(pos, sym.SymName())
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

func (c *checker) pushScope() {
	c.scope = NewScope(c.scope)
}

func (c *checker) popScope() {
	c.scope = c.scope.Parent
}

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

// claimTopLevel records name as bound and reports a conflict with an existing
// binding. Returns false when the caller should skip binding.
//
// The scope a claim is held to is the scope the binding has: an import binds
// into one file, a declaration into the whole package. So two claims are
// compared in `topLevel` (per file) or in `pkgDecls` (per package) accordingly.
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

// claimPackage reports a second declaration of name in this package, whichever
// file that one is in, and returns false when the caller should bind nothing.
// It only reads pkgDecls; the name is reserved by claimTopLevel once the
// file-scope claim has also succeeded, so a declaration that binds nothing
// reserves nothing.
func (c *checker) claimPackage(name string, pos ast.Pos) bool {
	prev, dup := c.pkgDecls[name]
	if !dup {
		return true
	}
	where := "package"
	if prev.File == pos.File {
		where = "file"
	}
	c.error(pos, "%q redeclared in this %s (previous declaration at %s)", name, where, prev)
	return false
}

func (c *checker) claimTopLevel(name string, pos ast.Pos, kind topLevelKind, path string) bool {
	if name == "" || name == "_" {
		return true
	}
	if c.rejectReservedName(pos, name) {
		return false
	}
	// Package before file, so two declarations in one file keep the
	// redeclaration wording rather than the file-scope conflict's.
	if kind == bindDecl && !c.claimPackage(name, pos) {
		return false
	}
	if !c.claimFile(name, pos, kind, path) {
		return false
	}
	if kind == bindDecl {
		if c.pkgDecls == nil {
			c.pkgDecls = map[string]ast.Pos{}
		}
		c.pkgDecls[name] = pos
	}
	return true
}

// claimFile records name as bound at file scope and reports a conflict with an
// existing binding there.
func (c *checker) claimFile(name string, pos ast.Pos, kind topLevelKind, path string) bool {
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

// firstDoc is the document `doc` starts on: the file whose imports are in
// scope before any pass has entered one. A checker built for no documents at
// all (a native value, a library load) has none.
func firstDoc(docs []*ast.Document) *ast.Document {
	if len(docs) == 0 {
		return nil
	}
	return docs[0]
}

// stmts is every top-level statement of the package, in file order. A package
// is one declaration set, so the file a declaration sits in does not affect
// what it can name.
func (c *checker) stmts() []ast.Stmt {
	if len(c.docs) == 1 {
		return c.docs[0].Stmts
	}
	var out []ast.Stmt
	for _, d := range c.docs {
		out = append(out, d.Stmts...)
	}
	return out
}

// enterPackage switches the checker to docs and returns the restore.
//
// A lib package loads lazily -- the first import that names it, or the error
// path that searches every package for a "did you forget an import?" hint --
// so the load fires in the middle of the program's own pass1. It then runs
// that same pass1, which opens by clearing the per-package registration state
// below. Restoring only c.docs left the rest cleared for the remainder of the
// program's pass1:
//
//   - topLevel is the file-scope claim table, so every name claimed before the
//     load was forgotten and a later declaration colliding with one went
//     unreported. A component whose prop names an unknown type is enough to
//     trigger it, because the hint search loads every public package.
//   - replaces is the import-replace map, collected once at the top of pass1
//     for the whole package. Cleared mid-import-loop, a later
//     `import "p" => "url"` resolves without its replacement.
//   - pendingPkgBody accumulates the program's top-level visual nodes across
//     pass1 rather than being reset by it, so the loaded package would append
//     its own to the program's.
//
// The rule is that anything pass1 touches is this package's, whether it resets
// it or builds it up. TestEnterPackageRestoresWhatPass1Resets enforces exactly
// that, and is what caught pendingPkgBody: the field and the guard arrived on
// separate branches, so neither failed until they met on main.
//
// Anything pass1 resets belongs here. The two are one function because the
// bug is precisely that they were not.
func (c *checker) enterPackage(docs []*ast.Document) func() {
	savedDocs, savedTopLevel := c.docs, c.topLevel
	savedReplaces, savedPending := c.replaces, c.pendingPkgBody
	savedPkgDecls := c.pkgDecls
	restoreFile := c.saveFile()
	c.docs = docs
	// A library package is its own declaration set: a name the program already
	// declared must not read as a redeclaration inside lib/, or the reverse.
	c.pkgDecls = nil
	// Cleared rather than merely saved: pass1 accumulates into this one, so
	// left in place the loaded package would append its own top-level body to
	// the program's. No lib package writes one today, which is the only reason
	// that is a latent leak rather than a live one.
	c.pendingPkgBody = nil
	return func() {
		restoreFile()
		c.docs, c.topLevel = savedDocs, savedTopLevel
		c.replaces, c.pendingPkgBody = savedReplaces, savedPending
		c.pkgDecls = savedPkgDecls
	}
}

// pass1 registers every declaration in the package. Each stage runs across all
// files before the next begins, which is what makes a forward reference work
// between two files as it already did within one: every type name exists
// before any struct body is resolved, and every type and component exists
// before any const, var or func signature names one.
//
// `topLevel` is reset per file rather than per package: an alias is one file's
// business. Two files declaring one name is `pkgDecls`, which is not.
func (c *checker) pass1() {
	// Collect replace map for the whole package before any import is
	// resolved, so declaration order of `import "p" => "url"` relative to bare
	// `import "p"` does not matter -- across files as well as within one.
	// Outer (cfg.Replaces) wins over this package's own.
	c.replaces = map[string]string{}
	for _, stmt := range c.stmts() {
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
	for _, d := range c.docs {
		c.enterFile(d)
		for _, stmt := range d.Stmts {
			if imp, ok := stmt.(*ast.Import); ok {
				c.registerImport(imp)
			}
		}
		c.fileTopLevel[d] = c.topLevel
	}

	// Pre-register type declarations so they're visible for forward references
	// (test functions referencing later types, a struct field or component prop
	// naming a type declared later, mutually recursive structs). Register every
	// type NAME first — structs as field-less shells — then resolve struct
	// fields in a sub-pass once all shells exist. Components are registered
	// after the shells because their prop/children types may name any of them.
	type pendingShell struct {
		doc *ast.Document
		sd  *ir.StructDef
	}
	type pendingComp struct {
		doc  *ast.Document
		decl *ast.ComponentDecl
	}
	var structShells []pendingShell
	var pendingComponents []pendingComp
	for _, d := range c.docs {
		c.resumeFile(d)
		for _, stmt := range d.Stmts {
			switch s := stmt.(type) {
			case *ast.StructDef:
				structShells = append(structShells, pendingShell{d, c.registerStructShell(s)})
			case *ast.EnumDef:
				c.registerEnum(s)
			case *ast.UnitDef:
				c.registerUnit(s)
			case *ast.ComponentDecl:
				pendingComponents = append(pendingComponents, pendingComp{d, s})
			}
		}
	}
	for _, p := range pendingComponents {
		c.resumeFile(p.doc)
		c.registerComponent(p.decl)
	}
	for _, p := range structShells {
		c.resumeFile(p.doc)
		c.resolveStructBody(p.sd)
	}
	// Every shell is filled, so a struct a #[construct] prop names can now be
	// walked field by field.
	c.checkConstructProps()

	for _, d := range c.docs {
		c.resumeFile(d)
		for _, stmt := range d.Stmts {
			switch s := stmt.(type) {
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
			case *ast.CallStmt:
				// A context declaration is a call statement by syntax and a
				// declaration by meaning, so it is recognised before anything
				// else. Everything left is the package's body: `counter()` -- a
				// component instantiated with no block -- parses as a call, and is
				// the composition a top-level body is usually made of.
				if c.isContextDeclCallStmt(s) {
					c.registerRootContextDecl(s)
				} else if vn := c.outputCallStmt(s); vn != nil {
					c.registerOutput(vn)
				} else {
					c.pendingPkgBody = append(c.pendingPkgBody, s)
				}
			case *ast.DisabledDecl:
			case *ast.Comment:
			default:
				// IfStmt, ForStmt at top level are checked in pass2.
			}
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

	// sngl:platform/<n> and sngl:language/<n> load through libPkg like
	// any other embedded package. The registered plugin is still consulted for
	// the one thing the lib tree cannot say: whether the target exists at all
	// here.
	platName, isPlatform := "", false
	langName, isLanguage := "", false
	if scheme == "sngl" {
		platName, isPlatform = strings.CutPrefix(uri, "platform/")
		langName, isLanguage = strings.CutPrefix(uri, "language/")
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
			// A platform need not ship declarations (`none` does not); the
			// namespace is still bound.
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
			if c.hasLibPkg(uri) {
				irImport.Pkg = c.libPkg(uri)
			}
		}
	} else if scheme == "sngl" {
		// The standard library. A scheme keeps it from colliding with a local
		// package directory of any name — the collision a reserved bare path
		// like "std" would reintroduce.
		// sngl:internal/<name> is the compiler's own tier, importable only
		// from library source.
		internal := strings.HasPrefix(uri, "internal/")
		if internal && !c.inLibSource() {
			c.error(imp.Pos, "%q is internal to the compiler and cannot be imported", target)
			return
		}
		// Every library package is a directory under lib/, macro-only ones
		// included: a package declares its macros the way it declares
		// anything else, so the layout is the whole answer to whether one
		// exists. hasLibPkg rather than HasPackage: Config.LibSources
		// substitutes the source of an embedded package, and a substituted one
		// has to resolve.
		if !c.hasLibPkg(uri) {
			if internal {
				c.error(imp.Pos, "unknown internal package %q", uri)
			} else {
				c.error(imp.Pos, "unknown stdlib package %q (have: %s)", uri, strings.Join(c.importablePackages(), ", "))
			}
			return
		}
		if uri == "builtin" {
			c.error(imp.Pos, "sngl:builtin is always in scope; remove the import")
			return
		}
		irImport.Pkg = c.libPkg(uri)
	} else if scheme != "" && c.cfg.Resolver != nil {
		// Scheme import. Try FS-backed schemes first (git://, http://, …) so
		// remote SNGL packages resolve to .sngl docs; fall back to native
		// scheme importers (go:, ts://, …) for language sources.
		docs, subFS, err := c.cfg.Resolver.ResolveSchemeFS(scheme, uri, c.cfg.Dir)
		if err != nil {
			c.error(imp.Pos, "import %q: %v", imp.Path, err)
		} else if len(docs) > 0 {
			// One package, however many files it arrived as -- the same rule
			// a directory import follows.
			pkg, diags := CheckPackage(docs, &Config{
				FS:         subFS,
				Dir:        c.cfg.Dir,
				Resolver:   c.cfg.Resolver,
				Languages:  c.cfg.Languages,
				Platforms:  c.cfg.Platforms,
				Replaces:   c.replaces,
				Targets:    c.targets,
				LibSources: c.cfg.LibSources,
				libs:       c.libs,
			})
			c.diags = append(c.diags, diags...)
			exported := &ir.Package{Symbols: NewSymbolTable(), LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{}, AddressedVars: map[*ir.Var]bool{}}
			c.mergePkgInto(exported, pkg)
			irImport.Pkg = exported
		} else {
			native, err := c.cfg.Resolver.ResolveScheme(scheme, uri, c.cfg.Dir)
			if err != nil {
				c.error(imp.Pos, "import %q: %v", imp.Path, err)
			}
			irImport.Native = native
			if native != nil {
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
		// A `./` or `../` import is relative to the file that wrote it, so it
		// is rebased onto the importing package's own path before the resolver
		// sees it. Without that, `../calc` inside an imported `keypad/`
		// resolved against the root and looked for a sibling of the program.
		// A bare path stays as written -- that is what a replace target is,
		// and it names a package rather than a neighbour.
		dirPath := uri
		if strings.HasPrefix(uri, "./") || strings.HasPrefix(uri, "../") {
			dirPath = path.Join(c.cfg.PkgPath, uri)
		}
		// Cycle detection is a stack, not a memory: the same package imported
		// by two files of one package is a package imported twice, and only a
		// package that imports itself back is a cycle.
		switch {
		case c.visited[dirPath]:
			c.error(imp.Pos, "import cycle detected: %q", imp.Path)
		case c.dirPkgs[dirPath] != nil:
			irImport.Pkg = c.dirPkgs[dirPath]
		default:
			c.visited[dirPath] = true
			docs, err := c.cfg.Resolver.Resolve(c.cfg.FS, dirPath)
			if err != nil {
				c.error(imp.Pos, "import %q: %v", imp.Path, err)
			}
			defer delete(c.visited, dirPath)
			if len(docs) > 0 {
				// The package's files together, not one at a time: a
				// declaration in one may be named by an annotation in another,
				// and checking them separately left each without its siblings
				// in scope.
				//
				// PkgPath is what a relative import inside it resolves
				// against, and visited/dirPkgs are shared so a sibling
				// importing the same package is neither a cycle nor a second
				// copy of its types.
				pkg, diags := CheckPackage(docs, &Config{
					FS:         c.cfg.FS,
					Dir:        c.cfg.Dir,
					PkgPath:    dirPath,
					Resolver:   c.cfg.Resolver,
					visited:    c.visited,
					dirPkgs:    c.dirPkgs,
					Languages:  c.cfg.Languages,
					Platforms:  c.cfg.Platforms,
					Replaces:   c.replaces,
					Targets:    c.targets,
					LibSources: c.cfg.LibSources,
					libs:       c.libs,
				})
				c.diags = append(c.diags, diags...)
				// mergePkgInto builds the view an importer sees: the package's
				// own declarations, and not the names its own dot imports
				// lifted into its scope. A package does not re-export what it
				// imported, so the checked package's root scope is not that
				// view.
				exported := &ir.Package{Symbols: NewSymbolTable(), LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{}, AddressedVars: map[*ir.Var]bool{}}
				c.mergePkgInto(exported, pkg)
				irImport.Pkg = exported
				// The view is memoized, not the checked package: two importers
				// of one directory must see one set of declarations, or a
				// value from one cannot be passed to the other.
				c.dirPkgs[dirPath] = exported
			}
		}
	}

	owner := c.declPkg()
	owner.Imports = append(owner.Imports, irImport)

	// A segmented tree reaches this package if any package it imports declares
	// one of its nodes: inlining will bring the nodes here, and the pass that
	// lowers them runs on this package.
	if irImport.Pkg != nil {
		for kind := range irImport.Pkg.TreeKinds {
			owner.NoteTreeKind(kind)
		}
	}

	// Check for component main in imported library packages. The package's
	// own root only: a lib package's root parents whatever scope was current
	// when it loaded, so a lookup that walks the chain finds the importing
	// file's own main and blames the import for it.
	if irImport.Pkg != nil && irImport.Pkg.Symbols != nil && irImport.Pkg.Symbols.Root != nil {
		if sym, ok := irImport.Pkg.Symbols.Root.LookupDeclaredLocal("main"); ok {
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
	// If the new namespace is inert (nil pkg) and a namespace with the same
	// alias already exists in scope with a non-nil package (e.g. the
	// predeclared "i18n" stdlib namespace), skip re-declaration so the
	// existing, richer namespace stays accessible. This prevents `import "i18n"`
	// from shadowing the predeclared i18n namespace with a no-op nil-pkg entry.
	ns := &ir.Namespace{
		Name: alias,
		Pkg:  irImport.Pkg,
	}
	if ns.Pkg == nil {
		if existing, ok := c.scope.Lookup(alias); ok {
			if existingNS, ok := existing.(*ir.Namespace); ok && existingNS.Pkg != nil {
				return // keep the existing richer namespace; don't shadow it
			}
		}
	}
	c.markForeign(irImport.Pkg)
	claimed := c.claimTopLevel(alias, imp.Pos, bindAlias, imp.Path)
	// One name, two packages: `html` is sngl:ui's namespace for the
	// placement directives and the html platform's for its elements, and a
	// file that dot-imports std and imports the platform means both. The
	// imported package goes behind the one already in scope, so a name std
	// declares wins and the platform's -- including its wildcard -- is reached
	// after.
	//
	// Only once the alias is this file's to bind: two imports claiming one
	// alias is still two meanings for a name, which claimTopLevel reports.
	if claimed && ns.Pkg != nil && ns.Pkg.Symbols != nil {
		if existing, ok := c.scope.Lookup(alias); ok {
			if existingNS, isNS := existing.(*ir.Namespace); isNS &&
				existingNS.Pkg != nil && existingNS.Pkg != ns.Pkg && existingNS.Pkg.Symbols != nil {
				existingNS.Pkg.Symbols.Root.Parent = ns.Pkg.Symbols.Root
				return
			}
		}
	}
	c.bindImport(claimed, ns)
}

// declPkg is the package a declaration being registered belongs to: the
// library package while one loads, and the program's own otherwise. Every
// registrar appends through this rather than naming c.pkg, which is what lets
// one set of them serve both -- a `sngl:` package and a user package differ in
// where their declarations land, not in how they are built.
//
// Imports go the same way, and for the reason this started as: appending a
// loading library's imports to the program's package would put
// `import std "sngl:ui"` in the IR of every program that reaches i18n.
//
// Contexts are the exception and stay on c.pkg. A context is program-global by
// design -- the `#locale` sngl:i18n declares has to reach the program's own
// codegen, which reads c.pkg.Contexts -- so it is discovered alongside the
// user's rather than kept with the package that wrote it.
func (c *checker) declPkg() *ir.Package {
	if c.libLoadPkg != nil {
		return c.libLoadPkg
	}
	return c.pkg
}

func (c *checker) mergePkgInto(dst, src *ir.Package) {
	if src == nil {
		return
	}
	dst.Structs = append(dst.Structs, src.Structs...)
	dst.Enums = append(dst.Enums, src.Enums...)
	dst.Units = append(dst.Units, src.Units...)
	for kind := range src.TreeKinds {
		dst.NoteTreeKind(kind)
	}
	dst.Funcs = append(dst.Funcs, src.Funcs...)
	dst.Components = append(dst.Components, src.Components...)
	dst.Vars = append(dst.Vars, src.Vars...)
	dst.Consts = append(dst.Consts, src.Consts...)
	dst.Imports = append(dst.Imports, src.Imports...)
	for _, sd := range src.Structs {
		// Nobody named it, so it is not part of what the package exports.
		if sd.Anon {
			continue
		}
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
	c.applyMarks(e, ed)
	c.declPkg().Enums = append(c.declPkg().Enums, ed)
	c.bindDeclared(claimed, ed)
	c.registerNestedMethods(ed.Name, nil, e.Funcs())
}

// registerStructShell registers a struct's name and type parameters without
// resolving its fields, so the type is visible for forward and mutually
// recursive references. resolveStructBody fills in the fields (and nested
// methods) in a later pass1 sub-pass, once every type shell exists.
func (c *checker) registerStructShell(s *ast.StructDef) *ir.StructDef {
	claimed := c.claimTopLevel(s.Name, s.Pos, bindDecl, "")
	sd := &ir.StructDef{AST: s, Name: s.Name, TypeParams: typeParamShells(s.TypeParams), Pkg: c.libPkgName}
	c.applyMarks(s, sd)
	c.declPkg().Structs = append(c.declPkg().Structs, sd)
	c.bindDeclared(claimed, sd)
	c.publishBuiltinStruct(sd)
	return sd
}

// publishBuiltinStruct hands a marked declaration to the phases that read it
// where they have no scope to resolve a name in. Both cases key on the mark
// the declaration carries, so neither is a property of the tier it sits in.
func (c *checker) publishBuiltinStruct(sd *ir.StructDef) {
	// Macro carries no #[builtin] kind: a kind names the IR construct a
	// declaration dispatches to, and this one dispatches to none. It is found
	// by name within the compiler's own package, which no program can import.
	if c.libPkgName == "sngl:"+irPkg && sd.Name == macroTypeName {
		c.macroStruct = sd
	}
	// The canonical datetime type, for the foreign-type importers that
	// synthesize one without a scope of their own.
	if sd.Builtin == ir.BuiltinDateTime {
		ir.RegisterDateTimeStruct(sd.SymType())
	}
	ir.RegisterGenericBuiltin(sd)
}

func (c *checker) resolveStructBody(sd *ir.StructDef) {
	defer pushTypeParams(c, sd.AST.TypeParams)()
	sd.Fields = c.resolveStructFields(sd.AST)
	// A default is a type reference, so it waits for the same every-shell-exists
	// condition the fields do.
	sd.TypeParams = c.resolveTypeParams(sd.AST.TypeParams)
	c.registerNestedMethods(sd.Name, sd.AST.TypeParams, sd.AST.Funcs())
}

// claimBodyType reports a body-local type name something else in this package
// already declared -- another body, or a top-level declaration. Both names are
// correctly scoped and stay so; the collision is that both declarations land in
// one ir.Package and every backend emits a type declaration straight from it,
// so the host would get two of that name. #198 renames per body and removes
// this.
//
// Which of two bodies is called second follows registration order, and so for a
// multi-file package follows document order; the message names both positions
// for that reason.
func (c *checker) claimBodyType(name string, pos ast.Pos) {
	if name == "" || name == "_" {
		return
	}
	// Two in one body is one scope's duplicate, which c.declare reports in its
	// own words; recording it here would report the interim message instead and
	// re-point the name at the second of the two.
	if _, local := c.scope.LookupDeclaredLocal(name); local {
		return
	}
	pkg := c.declPkg()
	if pkg == nil {
		return
	}
	if c.bodyTypeDecls == nil {
		c.bodyTypeDecls = map[*ir.Package]map[string]ast.Pos{}
	}
	seen := c.bodyTypeDecls[pkg]
	if seen == nil {
		seen = map[string]ast.Pos{}
		c.bodyTypeDecls[pkg] = seen
	}
	if prev, dup := seen[name]; dup {
		c.reportBodyTypeCollision(name, pos, prev, "another body of this package")
		return
	}
	// A top-level declaration of the name is the same collision: it lands in
	// the same slice, and a body-local type correctly shadows it in scope, so
	// nothing else reports the pair.
	if prev, dup := c.pkgDecls[name]; dup {
		c.reportBodyTypeCollision(name, pos, prev, "this package")
		return
	}
	seen[name] = pos
}

func (c *checker) reportBodyTypeCollision(name string, pos, prev ast.Pos, where string) {
	c.error(pos, "%q is declared in %s; a body-local type is not yet renamed per body, so the two would emit one host type (see #198) (previous declaration at %s)", name, where, prev)
}

// registerBodyDecl registers one declaration written inside a body: the name
// binds in the scope collectComponentDecls pushed rather than at file scope,
// and nothing else about the declaration differs. Returns the symbol pass2
// rebinds, or nil for a statement that is not one of these four kinds.
func (c *checker) registerBodyDecl(stmt ast.Stmt) ir.Symbol {
	switch s := stmt.(type) {
	case *ast.ComponentDecl:
		// No claimBodyType: a component collides only inside a recursion
		// cycle, which reportBodyComponentCollisions asks after pass2.
		return c.registerComponentDecl(s, true)
	case *ast.StructDef:
		sd := c.buildStructDef(s)
		c.applyMarks(s, sd)
		c.declPkg().Structs = append(c.declPkg().Structs, sd)
		c.claimBodyType(s.Name, s.Pos)
		c.declare(s.Pos, sd)
		c.registerNestedMethods(sd.Name, s.TypeParams, s.Funcs())
		return sd
	case *ast.EnumDef:
		ed := c.buildEnumDef(s)
		c.applyMarks(s, ed)
		c.declPkg().Enums = append(c.declPkg().Enums, ed)
		c.claimBodyType(s.Name, s.Pos)
		c.declare(s.Pos, ed)
		c.registerNestedMethods(ed.Name, nil, s.Funcs())
		return ed
	case *ast.UnitDef:
		ud := c.buildUnitDef(s)
		c.applyMarks(s, ud)
		c.declPkg().Units = append(c.declPkg().Units, ud)
		c.claimBodyType(s.Name, s.Pos)
		c.declare(s.Pos, ud)
		return ud
	}
	return nil
}

func (c *checker) registerUnit(u *ast.UnitDef) {
	claimed := c.claimTopLevel(u.Name, u.Pos, bindDecl, "")
	ud := c.buildUnitDef(u)
	c.applyMarks(u, ud)
	c.declPkg().Units = append(c.declPkg().Units, ud)
	c.bindDeclared(claimed, ud)
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
			c.declPkg().Consts = append(c.declPkg().Consts, v)
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
		// An un-annotated const backed by a literal gets its concrete type on
		// the shell immediately, so a later `var x = SOME_CONST` (registered
		// before the deferred checkPendingConstInits runs) infers the const's
		// type rather than dyn. Non-literal initializers still resolve in the
		// deferred pass.
		var typ *ir.Type
		switch {
		case spec.Type != nil:
			typ = c.resolveType(spec.Type)
		default:
			typ = literalConstType(spec.Default)
			if typ == nil {
				typ = dynDeferred("const type, resolved by checkPendingConstInits")
			}
		}
		vars := make([]*ir.Var, 0, len(spec.Names))
		for _, name := range spec.Names {
			v := &ir.Var{AST: decl, Name: name, Type: typ, IsConst: true}
			c.applyMarks(decl, v)
			c.declPkg().Consts = append(c.declPkg().Consts, v)
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
		// A mark that supplied both the type and the value has already said
		// what this const is -- a target identity is a string in the source
		// and a target type in the IR, which is the one thing this pass cannot
		// reconcile for it.
		if len(p.vars) > 0 && p.vars[0].Synthesized && p.vars[0].Init != nil {
			continue
		}
		// Deferred out of the per-file loop, so each initializer says which
		// file it was written in rather than inheriting the last one's
		// imports.
		restore := c.fileOf(p.decl.Pos)
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
		restore()
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
	// A var declares no marked form, so this reports the mark rather than
	// applying one; the declaration still registers.
	c.applyMarks(decl, nil)
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
			// Infer type from init if not declared.
			if c.requireValueType(initType, decl.Pos) {
				// Don't propagate void into an inferred type.
			} else if typ.Kind == ir.TypeDyn {
				typ = initType
			}
		} else if sd, ok := structDeclOf(typ); ok {
			// A struct var with no initializer is that struct's zero value,
			// which is its fields' defaults -- not an empty struct. Built here
			// so every consumer sees what a written `Counter{}` already gives
			// them: left empty, `var c Counter` read back undefined on the web
			// and its declared defaults everywhere else.
			if fields := withFieldDefaults(sd, nil); len(fields) > 0 {
				initExpr = &ir.StructLit{Type: typ, Def: sd, Fields: fields}
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
			for i := range spec.Handlers {
				h := &spec.Handlers[i]
				handler := &ir.EventHandler{
					AST:  h,
					Name: h.Name,
					Func: &ir.Func{},
				}
				v.Handlers = append(v.Handlers, handler)
			}
			c.declPkg().Vars = append(c.declPkg().Vars, v)
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
		} else if sd, ok := structDeclOf(typ); ok {
			// See registerVars: a struct var with no initializer is that
			// struct's zero value, which is its fields' defaults.
			if fields := withFieldDefaults(sd, nil); len(fields) > 0 {
				initExpr = &ir.StructLit{Type: typ, Def: sd, Fields: fields}
			}
		}
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

func (c *checker) registerFunc(f *ast.FuncDef) *ir.Func {
	// An override implements one declaration for one target and says which on
	// its own name, exactly as a component override does. It declares nothing
	// of its own, so it registers nothing here.
	if f.Target != nil {
		if c.inLibSource() {
			// As with a component override: a library package's belongs to the
			// target machinery, which resolves it in that package's own scope.
			return nil
		}
		c.userFuncOverrides = append(c.userFuncOverrides, f)
		return nil
	}
	fn := c.buildFunc(f)

	// A macro declaration is not a function -- it is where a `#[...]` mark's
	// documentation and argument list are written, and a Go handler is what
	// runs -- but it is bound like one, because a mark resolves through the
	// scope like every other name. It used to be kept off the scope and looked
	// up in a table built by re-scanning the imports, which is why a package
	// could not use a macro it declared: its own declarations were never in
	// that table, though they were always in scope.
	//
	// The two reasons it was kept out are answered rather than avoided. A
	// program calling one is refused where a call is checked, by its type: a
	// macro answers ir.Macro, which nothing constructs and no value holds. And
	// a dot import of a package that dot-imports the mark's package does not
	// lift it on, because no package re-exports what it imported -- an importer
	// sees the export view mergePkgInto builds of a package's own declarations.
	if c.isMacroSig(fn.Return) {
		c.declPkg().Macros = append(c.declPkg().Macros, fn)
	}
	c.applyMarks(f, fn)

	// Library source is not body-checked, so two things it would otherwise
	// infer are stated here. A signature with no return annotation is dyn
	// rather than void. And purity, which the analysis never runs for, starts
	// pure so the optimizer can fold int.min and its like; anything reaching
	// outside the program has it overridden afterwards.
	if c.inLibSource() {
		fn.Stdlib = true
		if fn.Return == nil && f.Body != nil {
			fn.Return = dynFallback("library function %q has a body and no return annotation", fn.Name)
		}
		if fn.Purity == ir.PurityUnknown {
			fn.Purity = ir.PurityPure
		}
	}

	// After the block above: the return type and purity it settles are part of
	// what gets published. A mistyped id is not an error here -- there is no
	// list for it to be absent from -- but RequireIntrinsicFallback catches one
	// when a backend has neither an emitter nor a `usable` body.
	if fn.Intrinsic != "" {
		c.publishIntrinsic(fn)
	}

	// Only free functions bind a file-scope name; a method's name lives under
	// its receiver, checked when it is attached there.
	claimed := true
	if fn.Receiver == "" {
		claimed = c.claimTopLevel(fn.Name, f.Pos, bindDecl, "")
	}

	if fn.Receiver != "" {
		// Attaching is a declaration: it fails on a member of that name
		// already there, unless this one shadows a standard-library member.
		// A receiver naming a namespace rather than a type (i18n) has no
		// declaration to host it, and declareMethod says so.
		if prev := c.declareMethod(f.Pos, fn); prev != nil {
			c.error(f.Pos, "duplicate declaration of %q on type %s", fn.Name, fn.Receiver)
			return nil
		}
		c.declPkg().Funcs = append(c.declPkg().Funcs, fn)
		return fn
	}

	c.declPkg().Funcs = append(c.declPkg().Funcs, fn)
	c.bindDeclared(claimed, fn)
	return fn
}

// stdlibHint returns a suffix naming the import that would bring name into
// scope, for a name the file did not resolve but the standard library
// declares. Missing that one import is the most common way a file fails to
// check, and "unknown component \"vbox\"" on its own does not say so.
func (c *checker) stdlibHint(name string) string {
	return c.stdlibHintFor(name, hintAny)
}

// hintRole is what the unresolved name was being read as, so the hint can
// prefer a package declaring that rather than the first package binding the
// name in any role at all. `time` is the case that forced this: sngl:i18n has
// a `time` formatter and sorts before sngl:time, so a missing import of the
// type was reported against the wrong package.
type hintRole int

const (
	hintAny hintRole = iota
	hintType
	hintComponent
)

// declaresAs reports whether pkg binds name in the role want.
func declaresAs(pkg *ir.Package, name string, want hintRole) bool {
	sym, ok := pkg.Symbols.Root.LookupLocal(name)
	if !ok {
		return false
	}
	switch want {
	case hintComponent:
		_, is := sym.(*ir.Component)
		return is
	case hintType:
		switch sym.(type) {
		case *ir.StructDef, *ir.EnumDef, *ir.UnitDef:
			return true
		}
		return false
	}
	return true
}

func (c *checker) stdlibHintFor(name string, want hintRole) string {
	// A macro is declared in lib/ but never registered as a function, so it is
	// never in scope: the only way to reach one is a `#[...]` mark. Saying so
	// beats "undefined", which is true but reads as a missing import.
	if uri := macroPackage(name); uri != "" {
		return fmt.Sprintf("; %s is a macro declared by sngl:%s — write it as a mark, not a call", name, uri)
	}
	if c.pkg == nil {
		return ""
	}
	// The hint is for user code. Loading the library to build one while the
	// library is itself loading would re-enter a package mid-load, which
	// libPkg reports as a cycle.
	if len(c.libs.loading) > 0 {
		return ""
	}
	// Search every lib package, not just std: the shapes moved to sngl:ui/draw,
	// and naming the wrong package is worse than saying nothing. Loading here
	// is on an error path only.
	// PublicPackages, not Packages: a hint names an import a program could
	// write, so the compiler's own tier and the per-target platform/language
	// packages are not candidates.
	// A package binding the name in the role asked for wins; one binding it in
	// some other role is the fallback, since it is still better than silence.
	var fallback string
	for _, libName := range lib.PublicPackages() {
		if libName == "builtin" {
			continue // ambient; a miss here is not a missing import
		}
		pkg := c.libPkg(libName)
		if _, ok := pkg.Symbols.Root.LookupLocal(name); !ok {
			continue
		}
		if want != hintAny && !declaresAs(pkg, name, want) {
			if fallback == "" {
				fallback = c.hintFor(name, libName, pkg)
			}
			continue
		}
		return c.hintFor(name, libName, pkg)
	}
	return fallback
}

// hintFor phrases the hint for a name this package declares, saying how to
// reach it rather than only which package has it.
func (c *checker) hintFor(name, libName string, pkg *ir.Package) string {
	{
		path := "sngl:" + libName
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
}

// isLibraryNamespace reports whether name is in scope as a namespace bound to
// a package of the embedded library. Extension declarations (`component
// <ns>.X`) resolve their prefix this way rather than matching a fixed name, so
// the prefix is whatever alias the file imported the package under — and any
// library package can be extended, not only sngl:ui. A platform needs to
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
	for _, pkg := range c.libs.pkgs {
		if ns.Pkg == pkg {
			return true
		}
	}
	return false
}

// claimComponentAPI holds a component's members to one name each: props,
// events, slots, vars, methods and the element ids its body declares are one
// namespace, because `c.<name>` and the body's own scope read from it together.
//
// A slot is in the namespace but is not a member: it is placed in the body
// rather than read off the component, so nothing resolves `c.<slot>`. It still
// claims its name, since a slot is resolved as a tag from anywhere in the body.
//
// An id declared inside a *descendant* component is deliberately absent. It is
// reachable from here as list<host>, but the parent did not declare it -- were
// it a claim, renaming a var in a child would break its parent, and a library
// component's internal ids would land in every caller's namespace.
func (c *checker) claimComponentAPI(decl *ast.ComponentDecl, comp *ir.Component) {
	type claimed struct {
		kind string
		pos  ast.Pos
	}
	seen := make(map[string]claimed, len(comp.Props)+len(comp.Events)+len(comp.Slots))
	claim := func(name, kind string, pos ast.Pos) {
		if name == "" {
			return
		}
		if prev, dup := seen[name]; dup {
			article := "a"
			if strings.ContainsRune("aeiou", rune(prev.kind[0])) {
				article = "an"
			}
			c.error(pos, "%s %q on component %s: name is already declared as %s %s (at %s)",
				kind, name, comp.Name, article, prev.kind, prev.pos)
			return
		}
		seen[name] = claimed{kind, pos}
	}
	for _, p := range decl.Props.Props {
		switch pd := p.(type) {
		case ast.Param:
			claim(pd.Name, "prop", pd.Pos)
		case ast.EventDecl:
			// The `@` is declaration syntax, not part of the name, so an event
			// competes with everything else on the bare identifier.
			claim(pd.Name, "event", pd.Pos)
		case ast.SlotDecl:
			claim(pd.Name, "slot", pd.Pos)
		}
	}
	for _, v := range comp.Vars {
		pos := decl.Pos
		if v.AST != nil {
			if p := v.AST.StmtPos(); p != nil {
				pos = *p
			}
		}
		kind := "var"
		if v.IsConst {
			kind = "const"
		}
		claim(v.Name, kind, pos)
	}
	for _, fn := range comp.Funcs {
		pos := decl.Pos
		if fn.AST != nil {
			pos = fn.AST.Pos
		}
		claim(fn.Name, "func", pos)
	}
	for _, ref := range collectElementRefIDs(decl.Body.Stmts) {
		claim(ref.name, "element id", ref.pos)
	}
}

type elementRef struct {
	name string
	pos  ast.Pos
}

// collectElementRefIDs returns every element id declared in a component body,
// in source order. It is findHostComponentAST's walk widened from one name to
// all of them -- ids hide inside if/for branches and inside a slot's block, and
// the `Comp() #id` call form declares one as much as `Comp #id { }` does. It
// does not descend into the components the body instantiates: those ids are
// their own declarations'.
func collectElementRefIDs(stmts []ast.Stmt) []elementRef {
	var out []elementRef
	var walk func(stmts []ast.Stmt)
	walk = func(stmts []ast.Stmt) {
		for _, s := range stmts {
			switch n := s.(type) {
			case *ast.VisualNode:
				if n.ID != "" {
					out = append(out, elementRef{n.ID, n.Pos})
				}
				walk(n.Block.Stmts)
			case *ast.CallStmt:
				if _, id, isElem := elementRefCallInfo(n.Call); isElem && id != "" {
					out = append(out, elementRef{id, n.Pos})
				}
			case *ast.SlotNode:
				walk(n.Block.Stmts)
			case *ast.IfStmt:
				walk(n.Body.Stmts)
				walk(n.Else.Stmts)
			case *ast.ForStmt:
				walk(n.Body.Stmts)
				walk(n.Else.Stmts)
			}
		}
	}
	walk(stmts)
	return out
}

// buildSlotDecl resolves one slot declaration, for either registration path.
func (c *checker) buildSlotDecl(pd ast.SlotDecl) *ir.SlotDecl {
	slot := &ir.SlotDecl{Name: pd.Name}
	if pd.Type != nil {
		slot.Content, slot.Card = c.resolveSlotContent(pd.Type)
	}
	if pd.Name == ir.DefaultSlot && len(pd.Params) > 0 {
		// Its content is written as ordinary children, which have no binding
		// site, so there is nowhere to collect a parameter.
		c.error(pd.Pos, "the default slot takes no parameters: its content is written as ordinary children")
	}
	for _, t := range pd.Params {
		slot.Params = append(slot.Params, c.resolveType(t))
	}
	return slot
}

func (c *checker) registerComponent(comp *ast.ComponentDecl) {
	c.registerComponentDecl(comp, false)
}

// registerComponentDecl builds and registers one component declaration.
func (c *checker) registerComponentDecl(comp *ast.ComponentDecl, bodyLocal bool) *ir.Component {
	// An override implements one declaration for one target, and says which
	// target on its own name: `component ui.button[platform] { ... }`.
	// It is not a declaration of its own, so it registers nothing here --
	// collectUserOverrides merges it into the declaration it names, after
	// pass1 has registered everything it might name.
	//
	// A parens form with nothing in them is neither: android.sngl writes
	// `component ui.X() { body }`, whose body the platform reads itself.
	bare := comp.HasParens && len(comp.Props.Props) == 0 && comp.ChildrenType == nil
	// An override merges into a declaration registered elsewhere in the
	// package, so there is nothing body-scoped for one to land on.
	if bodyLocal && (comp.Target != nil || strings.IndexByte(comp.Name, '.') > 0) {
		c.error(comp.Pos, "an override may only be written at the root of a file")
		return nil
	}
	if comp.Target != nil && !bare {
		if c.inLibSource() {
			// A library package's overrides belong to mergeTargetExtensions,
			// which resolves each one in that package's own scope -- where the
			// target's namespace is bound. Collecting them here would resolve
			// `[android.platform]` later and elsewhere, with no `android` in
			// scope to resolve it against.
			return nil
		}
		c.userOverrides = append(c.userOverrides, comp)
		return nil
	}
	if dot := strings.IndexByte(comp.Name, '.'); dot > 0 && !bare {
		namespace := comp.Name[:dot]
		if !c.isLibraryNamespace(namespace) {
			c.error(comp.Pos, "extension namespace %q is not an imported library package; import it, e.g. import %s %q", namespace, namespace, "sngl:ui")
			return nil
		}
		// Naming another package's declaration is only meaningful as an
		// override of it, and an override names its target.
		c.error(comp.Pos, "override %q must name the target it implements, e.g. component %s[html.platform]", comp.Name, comp.Name)
		return nil
	}

	// The parameters are in scope for what the declaration writes after its
	// name: a prop's type, an event's payload, a slot's content. The scope is
	// closed again before the component is bound and its body collected --
	// those declare into the package, and a name declared while this scope is
	// open would go away with it.
	popTypeParams := pushTypeParams(c, comp.TypeParams)

	irComp := &ir.Component{
		AST:        comp,
		Name:       comp.Name,
		Stdlib:     c.inLibSource(),
		Pkg:        c.libPkgName,
		TypeParams: c.resolveTypeParams(comp.TypeParams),
	}
	c.applyMarks(comp, irComp)

	for _, p := range comp.Props.Props {
		switch pd := p.(type) {
		case ast.Param:
			prop := &ir.Prop{
				Name:          pd.Name,
				Type:          c.resolveType(pd.Type),
				Bidirectional: pd.Bidirectional,
			}
			c.applyParamMarks(pd, prop)
			// A prop with neither an annotation nor a default says nothing
			// about what it takes. `dyn` written out is an annotation: it says
			// the prop takes anything, which is what `context(default dyn)`
			// means and what a caller reads at the call site.
			if prop.Type.Kind == ir.TypeDyn && pd.Type == nil && pd.Default == nil {
				c.error(comp.Pos, "param %q must have a type hint or a default value", pd.Name)
			}
			// Default is checked later in checkComponentBody when scope is
			// ready. A library component has no body to check, so its default
			// is recorded as a typed placeholder: what reads it downstream asks
			// whether the prop has one, not what it is.
			if pd.Default != nil && c.inLibSource() {
				prop.Default = &ir.Literal{Type: prop.Type}
			}
			irComp.Props = append(irComp.Props, prop)
		case ast.EventDecl:
			evt := &ir.EventDecl{
				Name: pd.Name,
				Type: c.resolveType(pd.Type),
			}
			c.applyEventMarks(pd, evt)
			irComp.Events = append(irComp.Events, evt)
		case ast.SlotDecl:
			irComp.Slots = append(irComp.Slots, c.buildSlotDecl(pd))
		}
	}

	c.finishWildcardMarks(comp.Pos, irComp)
	if comp.ChildrenType != nil {
		irComp.ChildrenType = c.resolveType(comp.ChildrenType)
	}
	c.finishTreeMarks(comp, irComp, c.declPkg())
	c.finishDefaultSlot(irComp)
	popTypeParams()

	c.pushScope()
	bodyTypeScope := c.scope
	nestedFuncs := c.collectComponentDecls(comp, irComp)
	c.popScope()

	c.declPkg().Components = append(c.declPkg().Components, irComp)
	if bodyLocal {
		c.bodyComps = append(c.bodyComps, irComp)
		c.declare(comp.Pos, irComp)
	} else {
		c.bindDeclared(c.claimTopLevel(irComp.Name, comp.Pos, bindDecl, ""), irComp)
	}

	// The component's type parameters travel with its methods, which is what
	// puts them in scope for a signature and a body resolved from here: this
	// is past popTypeParams, and the pop cannot move -- a name declared while
	// a type-parameter scope is open goes away with it.
	// buildFunc pushes what it is handed, so handing it the parameters is the
	// same fix collectComponentDecls makes for a body `var`.
	c.scope = bodyTypeScope
	irComp.Funcs = c.registerNestedMethods(irComp.Name, comp.TypeParams, nestedFuncs)
	c.popScope()
	c.claimComponentAPI(comp, irComp)
	return irComp
}

// collectComponentDecls walks a component body for nested declarations,
// recording struct/enum/unit/component decls on irComp.BodyDecls and attaching
// vars and consts to irComp. The nested func defs are returned rather than
// registered, because the caller decides what receiver they get.
//
// checkComponentBody declares comp.Vars into the body scope, so a component
// whose body is checked must have been through here first.
func (c *checker) collectComponentDecls(comp *ast.ComponentDecl, irComp *ir.Component) []*ast.FuncDef {
	var nestedFuncs []*ast.FuncDef
	for _, stmt := range comp.Body.Stmts {
		switch s := stmt.(type) {
		case *ast.StructDef, *ast.EnumDef, *ast.UnitDef, *ast.ComponentDecl:
			if sym := c.registerBodyDecl(s); sym != nil {
				irComp.BodyDecls = append(irComp.BodyDecls, sym)
				c.noteBodyOwner(irComp, sym)
			}
		case *ast.ConstDecl, *ast.VarDecl:
			// A state declaration's annotation may name the component's type
			// parameters -- `var last T` is most of what a generic component
			// is for -- so they are in scope for the resolve. Pushed here
			// rather than around the whole loop because the other three cases
			// register into the package, and a name declared while a
			// type-parameter scope is open goes away with it.
			popTypeParams := pushTypeParams(c, comp.TypeParams)
			irComp.Vars = append(irComp.Vars, c.collectComponentVarDecl(stmt)...)
			popTypeParams()
		case *ast.FuncDef:
			nestedFuncs = append(nestedFuncs, s)
		}
	}
	return nestedFuncs
}

// collectComponentVarDecl builds the pre-registered ir.Vars for one component
// body var/const declaration. Split out of collectComponentDecls because a
// platform extension body is collected on its own (checkPendingExtensions),
// where only the state declarations travel with the body — the struct/enum/unit
// hoisting and nested funcs of a full component body do not.
//
// Returns nil for any other statement.
func (c *checker) collectComponentVarDecl(stmt ast.Stmt) []*ir.Var {
	var out []*ir.Var
	switch s := stmt.(type) {
	case *ast.ConstDecl:
		for _, spec := range s.Specs {
			typ := c.resolveType(spec.Type)
			for _, name := range spec.Names {
				v := &ir.Var{AST: s, Name: name, Type: typ, IsConst: true}
				c.applyMarks(s, v)
				out = append(out, v)
			}
		}
	case *ast.VarDecl:
		for _, spec := range s.Specs {
			typ := c.resolveType(spec.Type)
			if sd := treeStruct(typ); sd != nil {
				c.error(s.Pos, "%s names a tree, which has no values", sd.Name)
				typ = TypDyn
			}
			for _, name := range spec.Names {
				v := &ir.Var{AST: s, Name: name, Type: typ}
				// A mark means the same thing wherever the declaration sits.
				// Skipping this is how a macro written on a component-local
				// var did nothing and said nothing, while the same mark on the
				// same form at top level was an error.
				c.applyMarks(s, v)
				for i := range spec.Handlers {
					h := &spec.Handlers[i]
					v.Handlers = append(v.Handlers, &ir.EventHandler{
						AST:  h,
						Name: h.Name,
						Func: &ir.Func{},
					})
				}
				out = append(out, v)
			}
		}
	}
	return out
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
	switch kind, _ := c.builtinNode(name); kind {
	case ir.BuiltinOutput:
		c.registerOutput(vn)
	default:
		// An ordinary visual node at the top level is the package's own body:
		// what the program renders, with the package's vars as its state. Held
		// until pass2, because it reads declarations pass1 is still making.
		c.pendingPkgBody = append(c.pendingPkgBody, ast.Stmt(vn))
	}
}

func (c *checker) outputCallStmt(s *ast.CallStmt) *ast.VisualNode {
	id, ok := s.Call.Func.(*ast.IdentExpr)
	if !ok || s.Call.ID != "" || c.builtinNodeKind(id.Name) != ir.BuiltinOutput {
		return nil
	}
	return &ast.VisualNode{Pos: id.Pos, Target: id, Args: s.Call.Args}
}

func (c *checker) registerOutput(vn *ast.VisualNode) {
	switch {
	case c.inLibSource():
		c.error(vn.Pos, "%s cannot declare output: the build directive is the program's", c.libPkgName)
	case !c.cfg.IsMain:
		c.error(vn.Pos, "output declarations only permitted in the program's own package")
	case c.outputDecl != nil:
		c.error(vn.Pos, "output is already declared for this package at %s", c.outputDecl.Pos)
	default:
		c.outputDecl = vn
		c.buildOutputs(vn)
	}
}

// checkPackageBody checks the visual nodes written at the package's top level
// into c.pkg.Body.
//
// The package is a state owner like a component or a window (ir.Owners): its
// vars are the state this body reads, and they are already bound at file scope
// by pass1, so unlike checkComponentBody and checkWindowBody there is nothing
// to declare here but the node ids.
func (c *checker) checkPackageBody() {
	if len(c.pendingPkgBody) == 0 {
		return
	}
	c.pushScope()
	defer c.popScope()
	for _, st := range c.pendingPkgBody {
		c.declareNodeIDsStmt(st, false)
	}
	for _, st := range c.pendingPkgBody {
		if checked := c.checkStmt(st); checked != nil {
			c.pkg.Body = append(c.pkg.Body, checked)
		}
	}
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
func (c *checker) builtinNodeKind(name string) ir.BuiltinKind {
	kind, _ := c.builtinNode(name)
	return kind
}

// builtinNode is builtinNodeKind plus the declaration the kind was read off.
//
// A built-in node's props are that declaration's, so the builder that
// hand-picks the ones it cares about needs it to say which names exist at all.
func (c *checker) builtinNode(name string) (ir.BuiltinKind, *ir.Component) {
	sym, ok := c.resolveComponentSymbol(name)
	if !ok {
		return ir.BuiltinNone, nil
	}
	comp, ok := sym.(*ir.Component)
	if !ok || !(comp.Builtin.IsNode() || comp.Builtin.IsDirective()) {
		return ir.BuiltinNone, nil
	}
	return comp.Builtin, comp
}

// resolveComponentSymbol resolves a visual-node target — bare "Foo" or
// qualified "ns.Foo" — to the symbol it was declared as.
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
	return ns.Pkg.Symbols.LookupRootComponent(field)
}

// isWindowNode reports whether name denotes the built-in window component
// specifically. Window is the only node kind that owns a lexical scope and
// hoists its own element ids, so a few sites care about it by name.
func (c *checker) isWindowNode(name string) bool {
	return c.builtinNodeKind(name) == ir.BuiltinWindow
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
}

// importablePackages names every package this check could import: the public
// lib/ tiers, plus the package each configured target serves for itself. A
// target's package is not under lib/, so a list read from there alone would
// omit every one of them from the hint.
func (c *checker) importablePackages() []string {
	out := lib.PublicPackages()
	if c.cfg != nil {
		for _, p := range c.cfg.Platforms {
			out = append(out, "platform/"+p.PlatformIdentifier())
		}
		for _, l := range c.cfg.Languages {
			out = append(out, "language/"+l.LanguageIdentifier())
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

func (c *checker) lookupTarget(name string) pkgProvider {
	if t := c.lookupTargetIn(name, ir.BuiltinPlatform); t != nil {
		return t
	}
	return c.lookupTargetIn(name, ir.BuiltinLanguage)
}

// lookupTargetIn finds a registered target of one tier by name.
//
// The tier is not decoration. The two share a namespace -- nothing stops a
// language and a platform from both being called `go` -- so a search that
// falls through from one to the other answers a question nobody asked: it made
// hasLibPkg("platform/go") true for a language, which loaded golang.sngl a
// second time under a name it does not have and left a macro it declares
// reporting whichever load resolved it.
func (c *checker) lookupTargetIn(name string, kind ir.BuiltinKind) pkgProvider {
	if c.cfg == nil {
		return nil
	}
	if kind == ir.BuiltinLanguage {
		for _, l := range c.cfg.Languages {
			if l.LanguageIdentifier() == name {
				return l
			}
		}
		return nil
	}
	for _, p := range c.cfg.Platforms {
		if p.PlatformIdentifier() == name {
			return p
		}
	}
	return nil
}

// lookupOptions returns the Options struct a target of this tier declares, or
// nil when there is no such target or it declares none.
//
// The tier comes from the caller because the caller has it -- an output block
// names a language and a platform, and which of the two `go` is is not
// something to rediscover from the name. Guessing it is what let a language be
// asked for its platform package.
func (c *checker) lookupOptions(name string, kind ir.BuiltinKind) *ir.StructDef {
	uri := targetTierMember(kind) + "/" + name
	if c.optionsCache != nil {
		if sd, ok := c.optionsCache[uri]; ok {
			return sd
		}
	}
	if c.lookupTargetIn(name, kind) == nil {
		return nil
	}
	if c.optionsCache == nil {
		c.optionsCache = make(map[string]*ir.StructDef)
	}
	// Read the options schema off the loaded package rather than asking the
	// plugin: the schema is the #[options]-marked declaration that package
	// holds, and the mark is only on the IR.
	if c.hasLibPkg(uri) {
		for _, sd := range c.libPkg(uri).Structs {
			if sd.Options {
				c.optionsCache[uri] = sd
				return sd
			}
		}
	}
	c.optionsCache[uri] = nil
	return nil
}

// lookupStdlibOptions returns the stdlib's top-level Options struct, or nil
// if the stdlib does not declare one.
//
// It reads sngl:app alone, not the whole embedded corpus: several lib
// packages declare an `Options`, and the corpus is ordered by sorted package
// path, so a scan of all of it would return whichever package sorts first
// rather than the stdlib's.
func (c *checker) lookupStdlibOptions() *ir.StructDef {
	if c.stdlibOptionsSet {
		return c.stdlibOptions
	}
	c.stdlibOptionsSet = true
	for _, sd := range c.libPkg(optionsPkg).Structs {
		if sd.Options {
			c.stdlibOptions = sd
			return c.stdlibOptions
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
	if lang != "" && c.lookupTargetIn(lang, ir.BuiltinLanguage) == nil {
		return nil
	}
	if platform != "" && c.lookupTargetIn(platform, ir.BuiltinPlatform) == nil {
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
		add(lang, c.lookupOptions(lang, ir.BuiltinLanguage))
	}
	if platform != "" {
		add(platform, c.lookupOptions(platform, ir.BuiltinPlatform))
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
// windowPropOrder is the positional order of a window's props: the order the
// declaration writes them in. Duplicating it in Go was a second place for it
// to be wrong.
func windowPropOrder(comp *ir.Component) []string {
	if comp == nil {
		return []string{"title", "href", "favicon"}
	}
	out := make([]string, 0, len(comp.Props))
	for _, p := range comp.Props {
		if p != nil && p.Wildcard == "" {
			out = append(out, p.Name)
		}
	}
	return out
}

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
	// Checked against the declaration like any other component's node. A
	// window took whatever it was given: `window(width=320)` named a prop the
	// #[builtin("window")] component does not declare, and nothing said so --
	// so it read as a prop gtk4 ignored rather than one nobody declared.
	c.validateVisualNodeProps(vn, c.windowComp)
	named := resolvePositionalArgs(vn.Args, windowPropOrder(c.windowComp))
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
func (c *checker) buildErrorBoundary(vn *ast.VisualNode, comp *ir.Component) *ir.ErrorBoundary {
	eb := &ir.ErrorBoundary{AST: vn}
	c.validateVisualNodeProps(vn, comp)
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

func literalString(e ast.Expr) string {
	if lit, ok := e.(*ast.LiteralExpr); ok {
		if v, ok := lit.StringValue(); ok {
			return v
		}
		return lit.Raw
	}
	return ""
}

// checkStructFieldDefaults fills in each struct field's default now that the
// scope holds everything a default may refer to. Declaration time installs a
// placeholder, because a default can name a constant declared further down;
// nothing replaced it, so `struct P { x int = 7 }` carried a default no
// consumer could read and `P{}` had no x at all.
func (c *checker) checkStructFieldDefaults() {
	for _, sd := range c.declPkg().Structs {
		c.fillStructFieldDefaults(sd)
	}
}

// fillStructFieldDefaults checks each declared default against its field type
// and stores it, replacing the placeholder installed at declaration time.
func (c *checker) fillStructFieldDefaults(sd *ir.StructDef) {
	if sd == nil || sd.AST == nil {
		return
	}
	defer c.fileOf(sd.AST.Pos)()
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

	c.checkComponentBodies()

	// Check window bodies (skip those already checked in context, e.g., inside for-loops).
	for _, w := range c.pkg.Windows {
		if !w.Checked {
			c.checkWindowBody(w)
		}
	}
	// A window body may declare one too.
	c.checkComponentBodies()
	c.reportBodyComponentCollisions()
	c.reportBodyComponentCapture()

	c.checkPackageBody()

	// Check timer handler bodies (component timers are checked inside
	// checkComponentBody so they can see component vars in scope).
	for _, t := range c.pkg.Timers {
		c.checkTimerBody(t)
	}

	c.checkVarHandlerBodies(c.pkg.Vars)
	// Component var handlers are checked inside checkComponentBody.

	// Purity + access analysis, over the checked IR with resolved symbols.
	// The var *set* is by pointer identity, so a local that shadows a package
	// var is correctly excluded (fixes the name-collision false positive).
	pkgVarSet := make(map[*ir.Var]struct{}, len(c.pkg.Vars))
	for _, v := range c.pkg.Vars {
		pkgVarSet[v] = struct{}{}
	}
	// Every reactive var in the package, which is what a callee could reach.
	narrowVarSet := make(map[*ir.Var]struct{}, len(pkgVarSet))
	maps.Copy(narrowVarSet, pkgVarSet)
	for _, comp := range c.pkg.Components {
		for _, v := range comp.Vars {
			narrowVarSet[v] = struct{}{}
		}
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

	// After the fixpoint, because the rule reads what a handler writes through
	// the functions it calls and those sets are only complete now.
	c.checkEffectSelfRekey()

	// Every body is checked, so every instance is built and every type
	// parameter a call site pinned can be followed to what it pinned it to.
	c.checkRebuildKeys()

	// Same moment, same reason: a narrowing is only sound if nothing in the
	// branch could have written the value, and what a call writes is not
	// settled until the fixed point above has run.
	c.validateNarrowings(narrowVarSet)

	// Validate deferred const(expr) assertions now that function purities
	// are known.
	for _, a := range c.constAsserts {
		if !ir.IsConst(a.operand) {
			c.error(a.pos, "const() operand is not a constant expression")
		}
	}

}

// enterFuncBody marks the start of an imperative body -- a function, an event
// handler, a timer or var handler -- and returns the restore.
//
// It is the one place loopDepth resets. A `break` inside a lambda written in a
// loop body acts on a loop in that lambda, not on the one the lambda sits
// inside: the lambda's body runs later, or not at all, and by then the loop it
// was written in may be over.
func (c *checker) enterFuncBody() func() {
	c.funcDepth++
	savedLoops := c.loopDepth
	c.loopDepth = 0
	restoreNarrow := c.clearNarrowings()
	return func() {
		c.funcDepth--
		c.loopDepth = savedLoops
		restoreNarrow()
	}
}

func (c *checker) checkFuncBody(fn *ir.Func) {
	defer c.fileOf(funcDeclPos(fn))()
	c.pushScope()
	defer c.popScope()
	defer c.enterFuncBody()()

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

	defer pushTypeParams(c, fn.RecvTypeParams, fn.TypeParams)()

	// A method on a generic receiver may carry no receiver parameter -- saying
	// `this` instead -- so bind it for the expression bodies that delegate to
	// an intrinsic, `func list<T>.push(item T) => stdlib.ListPush(this, item)`.
	// A concrete receiver names itself a parameter and needs none, and so does
	// a nested method, whose `this` is synthesized into its parameter list.
	// Only an expression body: the bodyless generic stubs never say `this`, and
	// resolving the receiver here would trip the map-key comparability check
	// on an abstract key type.
	if fn.AST != nil && fn.AST.Body != nil && fn.Receiver != "" && len(fn.RecvTypeParams) > 0 &&
		!slices.ContainsFunc(fn.Params, func(p *ir.Param) bool { return p.Name == ir.ReceiverParam }) {
		if thisType := c.resolveType(synthRecvTypeExpr(funcDeclPos(fn), fn.Receiver, fn.RecvTypeParams)); thisType != nil {
			recv := &ir.Param{Name: ir.ReceiverParam, Type: thisType, Receiver: true}
			c.declare(funcDeclPos(fn), recv)
			// Kept on the declaration as well as in the scope: the body names
			// it, so whoever replaces the body's parameters has to know which
			// symbol the receiver is.
			fn.RecvParam = recv
		}
	}

	if fn.AST != nil && fn.AST.Body != nil {
		body := fn.AST.Body
		bodyExpr := c.checkExpr(body)
		if bodyExpr == nil {
			return
		}
		bodyType := exprType(bodyExpr)
		// Infer return type from expression body when there was no annotation.
		// An explicit `dyn` annotation is kept as-is -- except in library
		// source, where an unannotated signature was given dyn at
		// registration rather than left nil, so the annotation and the
		// absence of one are the same thing by the time this runs. Restricted
		// to primitives: the `color` struct and the `color` primitive share a
		// name, so inferring `color.rgb(...) => color{...}` as the struct
		// makes it collide with the primitive in callers.
		if fn.Return == nil {
			fn.Return = bodyType
		} else if c.inLibSource() && fn.Return.Kind == ir.TypeDyn &&
			bodyType != nil && isPrimitiveTypeKind(bodyType.Kind) {
			fn.Return = bodyType
		}
		if fn.Return != nil && fn.Return.Kind != ir.TypeDyn && bodyType.Kind != ir.TypeDyn && !bodyType.IsAssignableTo(fn.Return) {
			pos := *body.ExprPos()
			c.error(pos, "cannot return %s as %s", bodyType, fn.Return)
		}
		fn.Block = []ir.Stmt{&ir.Return{AST: &ast.ReturnStmt{Pos: *body.ExprPos(), Value: body}, Value: bodyExpr}}
	} else if fn.AST != nil && fn.AST.Block.IsDefined() {
		fn.Block = c.checkBlockIR(&fn.AST.Block)
		// A block-bodied func with a non-void return type must return on all
		// paths, an empty `{}` body included.
		if fn.Return != nil && fn.Return.Kind != ir.TypeVoid && fn.Return.Kind != ir.TypeDyn &&
			!blockAlwaysReturns(fn.Block) && !lastStmtMayDiverge(fn.Block) {
			c.error(fn.AST.Pos, "missing return: %q must return %s on all paths", fn.Name, fn.Return)
		}
		// A native names an identifier that already exists, so a body written
		// beside it is emitted by nobody and read by nobody: every call is
		// routed to the foreign name instead. Both native marks tried to say
		// this themselves and could not -- a mark runs while the declaration is
		// registered, which is before any body is checked, so the block they
		// tested was always empty. Here it is not.
		if fn.Foreign.Name != "" && !fn.Foreign.Marked {
			c.error(fn.AST.Pos, "%q has a body and names %s, which already exists: the body would be emitted by nobody and read by nobody",
				fn.Name, fn.Foreign.Name)
		}
	} else if fn.AST != nil {
		// No body at all: a signature. Something else has to supply the answer,
		// and this is where that is required rather than assumed.
		if !c.bodySuppliedElsewhere(fn) {
			c.error(fn.AST.Pos, "%q has no body: give it one, or say where the answer comes from — #[intrinsic], #[foreign], or a per-target override",
				fn.Name)
		}
	}
}

// bodySuppliedElsewhere reports whether a declaration with no written body gets
// one from somewhere the checker can name.
//
// Three answers, and each is a declaration rather than a convention: an
// #[intrinsic] id every backend must implement, a #[foreign] correspondence
// whose body would only ever have described what it names, and a per-target
// override carrying the body for the target a build picks.
func (c *checker) bodySuppliedElsewhere(fn *ir.Func) bool {
	if fn == nil {
		return false
	}
	// A macro is the fourth: the declaration is where a mark's arguments and
	// documentation are written, and the compiler's implementation of that mark
	// is what runs. There has never been a body worth writing.
	// A name is what says the answer comes from outside, not a path: a
	// JavaScript global has no module to name, and #[js.native("setInterval")]
	// is the whole of what there is to say about it.
	return fn.Intrinsic != "" || fn.Foreign.Path != "" || fn.Foreign.Name != "" || c.isMacroSig(fn.Return) ||
		len(fn.PlatformOverrides) > 0 || len(fn.LanguageOverrides) > 0
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
		// A loop with no condition never falls out of the bottom, so the only
		// way past it is a `break` -- and without one, the statement after it
		// is unreachable and the function returns from inside the loop. This
		// is what makes `for { … return … }` a complete function body, which
		// is the whole of how such a loop ends.
		breaks, continues := loopEscapes(n.Body)
		if n.Iter == nil {
			return !breaks
		}
		// Any other for terminates only when it has an else and both the body
		// (which returns before the first iteration completes) and the else
		// (empty case) terminate -- and only when no escape can carry control
		// past the pair of them. A `break` leaves the loop outright. A
		// `continue` is subtler: it ends the iteration rather than the loop,
		// so the loop can simply run out afterwards, and the else does not
		// run then either, because the body did.
		return len(n.Else) > 0 && !breaks && !continues &&
			blockAlwaysReturns(n.Body) && blockAlwaysReturns(n.Else)
	default:
		return false
	}
}

// loopEscapes reports which escapes in stmts act on the loop this block is the
// body of: whether it can `break` out, and whether it can `continue` to the
// next iteration.
//
// The two are separated because they end different things, and a caller
// reasoning about whether control can get *past* the loop cares about that. A
// break does that on its own. A continue does it only in a loop that can run
// out -- which is every loop except the one with no condition, where a
// continue is another turn of a loop that never ends by itself.
//
// A nested loop's body is not searched: an escape written there acts on that
// loop. Its else is, because the else runs when the inner body never did,
// which is after the inner loop is over and still inside this one.
func loopEscapes(stmts []ir.Stmt) (breaks, continues bool) {
	for _, s := range stmts {
		switch n := s.(type) {
		case *ir.Break:
			breaks = true
		case *ir.Continue:
			continues = true
		case *ir.If:
			for _, block := range [][]ir.Stmt{n.Body, n.Else} {
				b, c := loopEscapes(block)
				breaks, continues = breaks || b, continues || c
			}
		case *ir.For:
			b, c := loopEscapes(n.Else)
			breaks, continues = breaks || b, continues || c
		}
	}
	return breaks, continues
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
	case *ir.CallStmt, *ir.ErrorBoundary, *ir.SlotInst, *ir.ContextProvider, *ir.NodeInst:
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
	defer c.fileOf(compDeclPos(comp))()
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
	c.declareEnclosingBody(comp)
	c.declareBodyDecls(comp)
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

// noteBodyOwner records that owner's body declared sym, for the kinds that
// need to know: a component's own body is checked with its siblings in scope.
func (c *checker) noteBodyOwner(owner *ir.Component, sym ir.Symbol) {
	nested, isComp := sym.(*ir.Component)
	if !isComp {
		return
	}
	if c.bodyOwner == nil {
		c.bodyOwner = map[*ir.Component]*ir.Component{}
	}
	c.bodyOwner[nested] = owner
}

// checkComponentBodies checks every component body in the package, including
// one registered mid-walk -- a range would snapshot pkg.Components' length.
func (c *checker) checkComponentBodies() {
	for i := 0; i < len(c.pkg.Components); i++ {
		c.checkBodyOnce(c.pkg.Components[i])
	}
}

// checkBodyOnce checks comp's body, and the body that declared it first.
//
// The order matters for capture: an unannotated `var count = 0` gets its type
// from the owner's own body check, and pass1 registers a nested declaration
// ahead of its owner -- so read in package order, a nested body saw the
// captured var as Dyn and an interpolation of it emitted an unconverted
// operand.
func (c *checker) checkBodyOnce(comp *ir.Component) {
	if c.bodyChecked[comp] {
		return
	}
	if c.bodyChecked == nil {
		c.bodyChecked = map[*ir.Component]bool{}
	}
	c.bodyChecked[comp] = true
	if owner := c.bodyOwner[comp]; owner != nil {
		c.checkBodyOnce(owner)
	}
	c.checkComponentBody(comp)
}

// declareBodyDecls rebinds what comp's body declares: a scope cannot span the
// two passes, so pass2 declares them again from the symbols.
func (c *checker) declareBodyDecls(comp *ir.Component) {
	for _, sym := range comp.BodyDecls {
		c.declare(declPos(sym), sym)
	}
}

// declareEnclosingBody declares, into comp's body scope, what the body comp
// was written in declares -- its own body decls, props, vars and plain funcs,
// outermost owner first so a nearer declaration shadows a farther one.
//
// A nested component reads the owner's state directly rather than through a
// synthesized prop, which is what makes a write from inside it a write to the
// owner's var; passNoInlineComponents splices the nested body into the owner's
// before the owner is inlined anywhere, so the reference survives the
// per-instance rename. No-op for a component nobody's body declared.
//
// The funcs loop mirrors checkComponentBody's and so covers only the ones with
// no receiver -- a func in a nested block, lifted here. A `func` at the top of
// the owner's body is a method and resolves through currentComponent, which is
// the nested component by the time this scope is open.
func (c *checker) declareEnclosingBody(comp *ir.Component) {
	owner := c.bodyOwner[comp]
	if owner == nil {
		return
	}
	c.declareEnclosingBody(owner)
	c.declareBodyDecls(owner)
	for _, p := range owner.Props {
		c.declare(compDeclPos(owner), propParam(p))
	}
	for _, v := range owner.Vars {
		c.declare(varPos(v), v)
	}
	for _, fn := range owner.Funcs {
		if fn.Receiver == "" {
			c.declare(funcDeclPos(fn), fn)
		}
	}
}

func (c *checker) checkComponentBody(comp *ir.Component) {
	defer c.fileOf(compDeclPos(comp))()
	c.pushScope()
	defer c.popScope()

	prevComp := c.currentComponent
	c.currentComponent = comp
	defer func() { c.currentComponent = prevComp }()

	// A body-local component sees the body it was written in: its sibling
	// declarations, and its props, vars and funcs (#202).
	c.declareEnclosingBody(comp)

	// The body may name the component's type parameters, and a prop default is
	// checked against what they stand for here. A call site binds them from the
	// props it supplies; a declaration has only the parameters' own defaults,
	// which is what declTypeBindings collects.
	//
	// The claim holds for what this pass reads. It did not hold for a state
	// declaration, whose annotation is resolved back in pass1 by
	// collectComponentDecls -- outside this scope, and until that pass pushed
	// one of its own, `var last T` was "unknown type" in the body of the
	// declaration that introduces T.
	if len(comp.TypeParams) > 0 {
		defer pushTypeParams(c, comp.TypeParams)()
	}
	declBindings := declTypeBindings(comp)

	// Check prop defaults first (before declaring props as params in scope) so
	// that an unannotated prop's type can be inferred from its default and the
	// param entry we declare below picks up the inferred type.
	if comp.AST != nil {
		propIdx := 0
		for _, p := range comp.AST.Props.Props {
			if pd, ok := p.(ast.Param); ok {
				if propIdx < len(comp.Props) && pd.Default != nil {
					prop := comp.Props[propIdx]
					// A default states a value of what the prop takes here,
					// which for `on T = 0` under `<T = int>` is an int. A
					// parameter with no default of its own leaves the prop type
					// standing, and then the declaration says nothing the
					// default could disagree with.
					want := prop.Type.Substitute(declBindings)
					prop.Default = c.checkExprExpecting(pd.Default, want)
					initType := exprType(prop.Default)
					if want.Kind != ir.TypeDyn && initType.Kind != ir.TypeDyn &&
						!mentionsTypeParam(want) && !initType.IsAssignableTo(want) {
						c.error(comp.AST.Pos, "default value type %s does not match param type %s", initType, want)
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

	c.declareBodyDecls(comp)

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

	if comp.AST != nil && comp.AST.Body.IsDefined() {
		seenWindowIDs := map[string]bool{}
		for _, stmt := range comp.AST.Body.Stmts {
			switch stmt.(type) {
			case *ast.ConstDecl, *ast.VarDecl:
				continue // already checked above
			case *ast.FuncDef:
				continue // already checked above
			case *ast.StructDef, *ast.EnumDef, *ast.UnitDef, *ast.ComponentDecl:
				continue // registered in pass1, rebound above
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
	if w.AST != nil {
		defer c.fileOf(w.AST.Pos)()
	}
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
		target := visualNodeTarget(n)
		isWindow := c.isWindowNode(target)
		if isWindow && inLoop {
			// The loop hoists this id as a list of windows.
			return
		}
		c.declareNodeID(n.ID, target, isWindow)
		// Descend into the node's own children, but not into a nested
		// window — a window has its own scope and hoists its ids itself.
		if !isWindow {
			c.declareNodeIDsIn(&n.Block, inLoop)
		}
	case *ast.CallStmt:
		// `text #out(...)` / `button(@click)` parse as call statements but
		// carry an element-ref id semantically.
		if target, id, isElem := elementRefCallInfo(n.Call); isElem {
			c.declareNodeID(id, target, false)
		}
	case *ast.IfStmt:
		c.declareNodeIDsIn(&n.Body, inLoop)
		c.declareNodeIDsIn(&n.Else, inLoop)
	case *ast.ForStmt:
		c.declareNodeIDsIn(&n.Body, true)
		c.declareNodeIDsIn(&n.Else, true)
	}
}

// declareNodeID binds one node id. target names the component the node
// instantiates.
func (c *checker) declareNodeID(id, target string, isWindow bool) {
	if id == "" {
		return
	}
	// Skip if the name already resolves (a prop, var, func, or outer symbol);
	// node ids never shadow an existing binding.
	if _, ok := c.scope.Lookup(id); ok {
		return
	}
	// A component's methods are registered under the component as receiver, not
	// in scope by bare name: inferIdent reaches them only when the scope lookup
	// misses. So `button #bump` beside `func bump()` would shadow the method.
	if c.currentComponent != nil {
		if _, ok := c.lookupBodyMethod(id); ok {
			return
		}
	}
	// A window's id names the window itself, so bind the window here and let
	// buildWindow fill it in.
	var sym ir.Symbol = &ir.Var{Name: id, Type: c.nodeHandleType(target), IsConst: true}
	if isWindow {
		sym = &ir.Window{Name: id, Typ: c.windowType}
	}
	c.declare(ast.Pos{}, sym)
}

// nodeHandleType is what a handle to a rendered instance of target reads at --
// the same answer inferSelect reaches through findHostComponentAST, arrived at
// directly because the hoisting pass already has the name.
func (c *checker) nodeHandleType(target string) *ir.Type {
	if comp := c.componentNamed(target); comp != nil {
		if t := comp.SymType(); t != nil {
			return t
		}
	}
	return dynFallback("node id names an instance of %q, which resolves to no component", target)
}

// componentNamed resolves a visual node's target, bare or `pkg.Name`.
func (c *checker) componentNamed(target string) *ir.Component {
	if target == "" {
		return nil
	}
	pkg, name, qualified := strings.Cut(target, ".")
	if !qualified {
		if sym, ok := c.scope.Lookup(target); ok {
			comp, _ := sym.(*ir.Component)
			return comp
		}
		return nil
	}
	sym, ok := c.scope.Lookup(pkg)
	if !ok {
		return nil
	}
	ns, ok := sym.(*ir.Namespace)
	if !ok || ns.Pkg == nil {
		return nil
	}
	member, ok := ns.Pkg.Symbols.LookupMember(name)
	if !ok {
		return nil
	}
	comp, _ := member.(*ir.Component)
	return comp
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
			href, _ := v.StringValue()
			return extractBraceParams(href)
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
	defer c.fileOf(t.AST.Pos)()
	c.pushScope()
	defer c.popScope()
	defer c.enterFuncBody()()
	t.Handler.Block = c.checkBlockIR(&t.AST.Block)
}

// validateStringDomainLiteral checks whether a string literal is valid for a
// special type like color, date, email, etc.
func (c *checker) validateStringDomainLiteral(pos ast.Pos, typ *ir.Type, initExpr ir.Expr) {
	lit, ok := initExpr.(*ir.Literal)
	if !ok || lit.Type.Kind != ir.TypeString {
		return
	}
	val := lit.Value

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

	// A colour is validated where it is built, in lowerHexLiteral: what
	// reaches here is already a color StructLit, never a literal of a colour
	// kind.
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
			restore := c.fileOf(varPos(v))
			h.Func.Params = c.bindParams(h.AST.Params, v.Type, "@"+h.Name, "the assignment")
			c.pushScope()
			for _, p := range h.Func.Params {
				c.declare(varPos(v), p)
			}
			restoreBody := c.enterFuncBody()
			h.Func.Block = c.checkBlockIR(&h.AST.Body)
			restoreBody()
			c.popScope()
			restore()
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
	dst := c.importScope()
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
			// "sngl:internal/lower"` alongside the stdlib that already exposes
			// it. Both bindings mean the same package, so this is a restated
			// name rather than an ambiguous one. Only the stdlib lifts
			// namespaces, so no second dot import can disagree about one.
			c.bindLifted(imp.Pos, dst, sym)
			continue
		}
		if !claim(sym.SymName()) {
			continue
		}
		c.bindLifted(imp.Pos, dst, sym)
	}
}

// finishDefaultSlot derives the children contract from the `_` slot.
func (c *checker) finishDefaultSlot(comp *ir.Component) {
	slot := findSlot(comp, ir.DefaultSlot)
	if slot == nil {
		return
	}
	comp.ChildrenType = childrenTypeFor(slot)
}
