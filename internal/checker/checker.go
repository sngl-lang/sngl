package checker

import (
	"fmt"
	"io/fs"
	"maps"
	"net"
	"net/mail"
	"net/url"
	"regexp"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
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

	// Effective replace map for this package: outer overrides layered over
	// this package's own `import "p" => "url"` declarations. Populated at the
	// start of pass1 before any import is resolved.
	replaces map[string]string

	// Current function return type (for return stmt checking).
	returnType *ir.Type

	// Expected type for the expression currently being checked.
	// When set to an enum type, bare enum member names resolve automatically.
	expected *ir.Type

	// Current component (for event validation).
	currentComponent *ir.Component

	// Tracks window #id collisions at package scope.
	pkgWindowIDs map[string]bool

	// userMethods tracks methods registered from user source (not stdlib),
	// keyed by receiver+method name. Used to detect duplicates within the
	// user's pass1 without conflicting with stdlib methods that the user
	// may legitimately override.
	userMethods map[string]bool

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

	// Stdlib Window struct type, used to type window symbols so `home.href`
	// resolves through the regular struct-field machinery.
	windowType *ir.Type

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
}

// constAssertion captures a const(expr) use site and the IR expression to
// validate once purity analysis has populated func purities.
type constAssertion struct {
	pos     ast.Pos
	operand ir.Expr
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
		userMethods:  make(map[string]bool),
	}
	// Insert stdlib scope between base and Root so user declarations shadow stdlib.
	stdlibScope := NewScope(symtab.Root.Parent) // parent = baseScope
	c.scope = stdlibScope
	c.loadStdlib()
	if sd, ok := c.symtab.Types["Window"].(*ir.StructDef); ok {
		c.windowType = sd.SymType()
	}
	symtab.Root.Parent = stdlibScope
	c.scope = symtab.Root

	// Inject all registered platform and language names as namespaces with
	// Resolve fallback so raw element access (e.g., html.div) works.
	for _, p := range cfg.Platforms {
		stdlibScope.Declare(&ir.Namespace{
			Name:    p.PlatformIdentifier(),
			Resolve: p.Resolve,
		})
	}
	for _, l := range cfg.Languages {
		stdlibScope.Declare(&ir.Namespace{
			Name:    l.LanguageIdentifier(),
			Resolve: l.Resolve,
		})
	}

	// Splice platform extension bodies into the stdlib components they target.
	// AST splicing happens here so that user pass1/pass2 see body-bearing
	// stdlib components; IR body checking is run from Check() after user
	// pass1 (so user-declared symbols are visible if a body references them).
	c.mergePlatformExtensions()

	return c
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

func (c *checker) pass1() {
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
		if imp, ok := stmt.(*ast.Import); ok {
			c.registerImport(imp)
		}
	}

	// Pre-register type declarations so they're visible for forward references
	// (e.g., test functions that reference types defined later in the file).
	for _, stmt := range c.doc.Stmts {
		switch s := stmt.(type) {
		case *ast.EnumDef:
			c.registerEnum(s)
		case *ast.StructDef:
			c.registerStruct(s)
		case *ast.UnitDef:
			c.registerUnit(s)
		case *ast.ComponentDecl:
			c.registerComponent(s)
		}
	}

	for _, stmt := range c.doc.Stmts {
		switch s := stmt.(type) {
		case *ast.Import, *ast.EnumDef, *ast.StructDef, *ast.UnitDef, *ast.ComponentDecl:
			continue // already registered above
		case *ast.ConstDecl:
			c.registerConsts(s)
		case *ast.VarDecl:
			c.registerVars(s)
		case *ast.FuncDef:
			c.registerFunc(s)
		case *ast.VisualNode:
			c.registerRootVisualNode(s)
		case *ast.PlatformStmt:
			c.pass1PlatformStmt(s)
		case *ast.CallStmt:
			if isContextDeclCallStmt(s) {
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

	if scheme == "internal" {
		// Built-in internal packages — no resolver needed.
		switch uri {
		case "stdlib":
			irImport.Pkg = c.buildIntrinsicsPkgFrom(ir.Intrinsics)
		case "alert":
			irImport.Pkg = c.buildIntrinsicsPkgFrom(ir.AlertIntrinsics)
		case "file":
			irImport.Pkg = c.buildIntrinsicsPkgFrom(ir.FileIntrinsics)
		case "intl":
			irImport.Pkg = c.buildIntrinsicsPkgFrom(ir.I18nIntrinsics)
		case "lower":
			irImport.Pkg = c.buildIntrinsicsPkgFrom(ir.LowerIntrinsics)
		default:
			c.error(imp.Pos, "unknown internal package: %q", uri)
		}
	} else if scheme == "platform" {
		// Platform package import: import "platform://html"
		var target ir.Platform
		for _, p := range c.cfg.Platforms {
			if p.PlatformIdentifier() == uri {
				target = p
				break
			}
		}
		if target == nil {
			c.error(imp.Pos, "unknown platform %q", uri)
		} else {
			irImport.Pkg = c.buildPkgFromDocs(target.Package())
			nsResolve = target.Resolve
		}
	} else if scheme == "language" {
		// Language package import: import "language://js"
		var target ir.Language
		for _, l := range c.cfg.Languages {
			if l.LanguageIdentifier() == uri {
				target = l
				break
			}
		}
		if target == nil {
			c.error(imp.Pos, "unknown language %q", uri)
		} else {
			irImport.Pkg = c.buildPkgFromDocs(target.Package())
			nsResolve = target.Resolve
		}
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
				mergePkgInto(merged, pkg)
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
					nsPkg.Symbols.Types[s.Name] = s
				}
				for _, e := range native.Enums {
					nsPkg.Symbols.Types[e.Name] = e
				}
				for _, f := range native.Funcs {
					nsPkg.Symbols.Root.Declare(f)
				}
				for _, v := range native.Vars {
					nsPkg.Symbols.Root.Declare(v)
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
					mergePkgInto(merged, pkg)
				}
				irImport.Pkg = merged
			}
		}
	}

	c.pkg.Imports = append(c.pkg.Imports, irImport)

	// Check for component main in imported library packages.
	if irImport.Pkg != nil {
		if _, hasMain := irImport.Pkg.Symbols.LookupComponent("main"); hasMain {
			c.error(imp.Pos, "component main can only be defined in the main package")
		}
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
	c.scope.Declare(ns)
}

// buildPkgFromDocs type-checks a set of .sngl documents (typically from a
// platform or language Package()) and returns a merged ir.Package.
func (c *checker) buildPkgFromDocs(docs []*ast.Document) *ir.Package {
	if len(docs) == 0 {
		return nil
	}
	merged := &ir.Package{Symbols: NewSymbolTable(), LiftedCaptures: map[*ir.Func]map[ir.Symbol]string{}, AddressedVars: map[*ir.Var]bool{}}
	for _, doc := range docs {
		pkg, _ := Check(doc, &Config{
			Languages: c.cfg.Languages,
			Platforms: c.cfg.Platforms,
		})
		mergePkgInto(merged, pkg)
	}
	return merged
}

// mergePkgInto merges all declarations from src into dst, registering symbols.
func mergePkgInto(dst, src *ir.Package) {
	if src == nil {
		return
	}
	dst.Structs = append(dst.Structs, src.Structs...)
	dst.Enums = append(dst.Enums, src.Enums...)
	dst.Units = append(dst.Units, src.Units...)
	dst.Funcs = append(dst.Funcs, src.Funcs...)
	dst.Components = append(dst.Components, src.Components...)
	dst.Vars = append(dst.Vars, src.Vars...)
	dst.Consts = append(dst.Consts, src.Consts...)
	dst.Imports = append(dst.Imports, src.Imports...)
	for _, sd := range src.Structs {
		dst.Symbols.Root.Declare(sd)
		dst.Symbols.Types[sd.Name] = sd
	}
	for _, ed := range src.Enums {
		dst.Symbols.Root.Declare(ed)
		dst.Symbols.Types[ed.Name] = ed
	}
	for _, ud := range src.Units {
		dst.Symbols.Root.Declare(ud)
		dst.Symbols.Types[ud.Name] = ud
	}
	for _, fn := range src.Funcs {
		dst.Symbols.Root.Declare(fn)
	}
	for _, comp := range src.Components {
		dst.Symbols.Root.Declare(comp)
		dst.Symbols.Comps[comp.Name] = comp
	}
	for _, v := range src.Vars {
		dst.Symbols.Root.Declare(v)
	}
	for _, v := range src.Consts {
		dst.Symbols.Root.Declare(v)
	}
}

func (c *checker) registerEnum(e *ast.EnumDef) {
	ed := c.buildEnumDef(e)
	c.pkg.Enums = append(c.pkg.Enums, ed)
	c.symtab.Types[ed.Name] = ed
	c.scope.Declare(ed)
	c.registerNestedMethods(ed.Name, nil, e.Funcs())
}

func (c *checker) registerStruct(s *ast.StructDef) {
	sd := c.buildStructDef(s)
	c.pkg.Structs = append(c.pkg.Structs, sd)
	c.symtab.Types[sd.Name] = sd
	c.scope.Declare(sd)
	c.registerNestedMethods(sd.Name, sd.TypeParams, s.Funcs())
}

func (c *checker) registerUnit(u *ast.UnitDef) {
	ud := c.buildUnitDef(u)
	c.pkg.Units = append(c.pkg.Units, ud)
	c.symtab.Types[ud.Name] = ud
	c.scope.Declare(ud)
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
					c.error(decl.Pos, "cannot initialize %s with %s", typ, initType)
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
			c.scope.Declare(v)
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
		// Builtin constants are fine.
		switch x.Name {
		case "true", "false", "null", "PLATFORM", "LANGUAGE":
			return ""
		}
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
		switch fn.Name {
		case "int", "float", "string", "bool":
			return checkArgs()
		}
	case *ast.SelectExpr:
		if ident, ok := fn.Operand.(*ast.IdentExpr); ok {
			// Type-namespace methods (e.g., string.length("hi")).
			switch ident.Name {
			case "int", "float", "string", "bool", "list", "color", "ref":
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
					c.error(decl.Pos, "cannot initialize %s with %s", typ, initType)
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
			c.scope.Declare(v)
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
					c.error(decl.Pos, "cannot initialize %s with %s", typ, initType)
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
					c.error(decl.Pos, "cannot initialize %s with %s", typ, initType)
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

	if fn.Receiver != "" {
		// Reject duplicate method against another user-registered method
		// (nested or top-level). Stdlib methods may be overridden by user.
		key := fn.Receiver + "." + fn.Name
		if c.userMethods[key] {
			c.error(f.Pos, "duplicate declaration of %q on type %s", fn.Name, fn.Receiver)
			return
		}
		c.userMethods[key] = true
	}

	c.pkg.Funcs = append(c.pkg.Funcs, fn)

	if fn.Receiver != "" {
		// Type-attached method.
		c.symtab.RegisterMethod(fn.Receiver, fn)
	} else {
		c.scope.Declare(fn)
	}
}

func (c *checker) registerComponent(comp *ast.ComponentDecl) {
	// Component extensions: `component sngl.X { platform <name> { ... } }`.
	// Validate qualified names. Tolerate the legacy `component sngl.X() { body }`
	// form (parens, no props, no children type) used in html.sngl and
	// bubbletea.sngl until those files are rewritten in Phase C.
	legacyForm := comp.HasParens && len(comp.Props.Props) == 0 && comp.ChildrenType == nil
	if dot := strings.IndexByte(comp.Name, '.'); dot > 0 && !legacyForm {
		namespace := comp.Name[:dot]
		if namespace != "sngl" {
			c.error(comp.Pos, "extension namespace %q not supported (only \"sngl\" is valid)", namespace)
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
		// Suppress normal registration — extension merge (Phase B) handles it.
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

	// Walk component body for nested declarations. Struct/enum/unit
	// decls inside a component body are hoisted to package scope at the
	// IR level (Go and other targets have no per-component type scope).
	var nestedFuncs []*ast.FuncDef
	for _, stmt := range comp.Body.Stmts {
		switch s := stmt.(type) {
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
					v := &ir.Var{
						AST:     s,
						Name:    name,
						Type:    typ,
						IsConst: true,
					}
					irComp.Vars = append(irComp.Vars, v)
				}
			}
		case *ast.VarDecl:
			for _, spec := range s.Specs {
				typ := c.resolveType(spec.Type)
				for _, name := range spec.Names {
					v := &ir.Var{
						AST:  s,
						Name: name,
						Type: typ,
					}
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
					irComp.Vars = append(irComp.Vars, v)
				}
			}
		case *ast.FuncDef:
			nestedFuncs = append(nestedFuncs, s)
		}
	}

	c.pkg.Components = append(c.pkg.Components, irComp)
	c.symtab.Comps[irComp.Name] = irComp
	c.scope.Declare(irComp)

	irComp.Funcs = c.registerNestedMethods(irComp.Name, nil, nestedFuncs)
}

func (c *checker) registerRootVisualNode(vn *ast.VisualNode) {
	name := visualNodeTarget(vn)
	switch name {
	case "output":
		if !c.cfg.IsMain {
			c.error(vn.Pos, "output declarations only permitted in main file")
			return
		}
		c.buildOutputs(vn)
	case "window":
		w := c.buildWindow(vn)
		c.checkDuplicateWindowID(w, c.pkgWindowIDs)
		c.pkg.Windows = append(c.pkg.Windows, w)
		if w.Name != "" {
			c.scope.Declare(w)
		}
	case "timer":
		t := c.buildTimer(vn)
		c.pkg.Timers = append(c.pkg.Timers, t)
	default:
		c.error(vn.Pos, "unexpected root-level visual node %q", name)
	}
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
	Package() []*ast.Document
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
	for _, doc := range t.Package() {
		for _, stmt := range doc.Stmts {
			if sd, ok := stmt.(*ast.StructDef); ok && sd.Name == "Options" {
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
func (c *checker) lookupStdlibOptions() *ir.StructDef {
	if c.stdlibOptionsSet {
		return c.stdlibOptions
	}
	c.stdlibOptionsSet = true
	for _, doc := range parseStdlibDocs() {
		for _, stmt := range doc.Stmts {
			if sd, ok := stmt.(*ast.StructDef); ok && sd.Name == "Options" {
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
		switch inner := stmt.(type) {
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
	if src.Kind == ir.TypeInt && dst.Kind == ir.TypeFloat {
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

func (c *checker) buildWindow(vn *ast.VisualNode) *ir.Window {
	w := &ir.Window{AST: vn, Name: vn.ID, Typ: c.windowType}
	// URL template params like `{name}` in href become string vars on the
	// window, in scope for the href literal itself as well as the body.
	for _, name := range hrefPathParams(vn) {
		w.Vars = append(w.Vars, &ir.Var{Name: name, Type: TypString})
	}
	c.pushScope()
	defer c.popScope()
	for _, v := range w.Vars {
		c.scope.Declare(v)
	}
	for _, a := range vn.Args.Args {
		switch arg := a.(type) {
		case ast.Arg:
			switch arg.Name {
			case "href":
				w.Href = c.checkExpr(arg.Value)
			case "title":
				w.Title = c.checkExpr(arg.Value)
			case "favicon":
				w.Favicon = c.checkExpr(arg.Value)
			}
		case ast.EventHandler:
			if arg.Name == "error" {
				w.ErrorHandler = c.buildErrorHandler(&arg)
			}
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
	for _, a := range vn.Args.Args {
		switch arg := a.(type) {
		case ast.Arg:
			switch arg.Name {
			case "interval":
				t.Interval = c.checkExpr(arg.Value)
			case "enabled":
				t.Enabled = c.checkExpr(arg.Value)
			}
		case ast.EventHandler:
			if arg.Name == "tick" {
				t.Handler = &ir.Func{
					Params: c.buildParams(arg.Params),
				}
				// Body is checked later in checkTimerBody.
				vn.Block = arg.Body
			}
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

	// Purity analysis.
	vars := c.collectVarMap()
	for _, fn := range c.pkg.Funcs {
		fn.Purity = analyzePurity(fn, vars)
		trackAccess(fn, vars)
	}
	for _, comp := range c.pkg.Components {
		// Component methods read/write the component's own vars (referenced
		// bare, e.g. `name`), so purity and Reads/Writes must be computed
		// against a scope that includes them. Using only package vars marks a
		// method like `func isLong() => name.length > 3` as PurityPure with
		// empty Reads — which lets the optimizer const-fold calls to it and
		// leaves reactivity unable to see its dep on `name`.
		compVars := make(map[string]*ir.Var, len(vars)+len(comp.Vars))
		maps.Copy(compVars, vars)
		for _, v := range comp.Vars {
			compVars[v.Name] = v
		}
		for _, fn := range comp.Funcs {
			fn.Purity = analyzePurity(fn, compVars)
			trackAccess(fn, compVars)
		}
	}

	// Validate deferred const(expr) assertions now that function purities
	// are known.
	for _, a := range c.constAsserts {
		if !ir.IsConst(a.operand) {
			c.error(a.pos, "const() operand is not a constant expression")
		}
	}
}

func (c *checker) collectVarMap() map[string]*ir.Var {
	vars := make(map[string]*ir.Var)
	for _, v := range c.pkg.Vars {
		vars[v.Name] = v
	}
	return vars
}

func (c *checker) checkFuncBody(fn *ir.Func) {
	c.pushScope()
	defer c.popScope()

	// Declare params.
	for _, p := range fn.Params {
		c.scope.Declare(p)
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
	}
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
		c.scope.Declare(&ir.Param{Name: p.Name, Type: p.Type})
	}
	for _, v := range comp.Vars {
		c.scope.Declare(v)
	}
	for _, fn := range comp.Funcs {
		if fn.Receiver == "" {
			c.scope.Declare(fn)
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
		c.scope.Declare(&ir.Param{
			Name: p.Name,
			Type: p.Type,
		})
	}

	// Declare component-level vars and funcs.
	for _, v := range comp.Vars {
		c.scope.Declare(v)
	}
	for _, fn := range comp.Funcs {
		if fn.Receiver == "" {
			c.scope.Declare(fn)
		} else {
			// Type-attached method registered on the symbol table so method
			// lookup at call sites finds it. Nested funcs on this component
			// (desugared with Receiver = comp.Name) are *not* declared in
			// scope by bare name; component-body references resolve through
			// the currentComponent-aware path in inferIdent / inferCall.
			c.symtab.RegisterMethod(fn.Receiver, fn)
		}
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
			switch stmt.(type) {
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
		c.scope.Declare(v)
	}
	for _, fn := range w.Funcs {
		c.scope.Declare(fn)
	}
	for _, fn := range w.Funcs {
		c.checkFuncBody(fn)
	}

	if w.AST != nil && w.AST.Block.IsDefined() {
		w.Body = c.checkBlockIR(&w.AST.Block)
	}
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

	// Color is StructDef-backed; treat it as a string-domain target here so
	// the same string-literal validation applies.
	if ir.IsColorStruct(typ) {
		if !isValidColor(val) {
			c.error(pos, "invalid color literal %q", val)
		}
		return
	}

	switch typ.Kind {
	case ir.TypeColor:
		if !isValidColor(val) {
			c.error(pos, "invalid color literal %q", val)
		}
	case ir.TypeDate:
		if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`).MatchString(val) {
			c.error(pos, "invalid date literal %q", val)
		}
	case ir.TypeTime:
		if !regexp.MustCompile(`^\d{2}:\d{2}(:\d{2})?$`).MatchString(val) {
			c.error(pos, "invalid time literal %q", val)
		}
	case ir.TypeDateTime:
		if !regexp.MustCompile(`^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}`).MatchString(val) {
			c.error(pos, "invalid dateTime literal %q", val)
		}
	case ir.TypeDuration:
		if !regexp.MustCompile(`^P`).MatchString(val) {
			c.error(pos, "invalid duration literal %q", val)
		}
	case ir.TypeURL:
		if u, err := url.Parse(val); err != nil || u.Scheme == "" {
			c.error(pos, "invalid url literal %q", val)
		}
	case ir.TypeEmail:
		if _, err := mail.ParseAddress(val); err != nil {
			c.error(pos, "invalid email literal %q", val)
		}
	case ir.TypeUUID:
		if !regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`).MatchString(val) {
			c.error(pos, "invalid uuid literal %q", val)
		}
	case ir.TypeIPV4:
		if ip := net.ParseIP(val); ip == nil || ip.To4() == nil {
			c.error(pos, "invalid ipv4 literal %q", val)
		}
	case ir.TypeIPV6:
		if ip := net.ParseIP(val); ip == nil || ip.To4() != nil {
			c.error(pos, "invalid ipv6 literal %q", val)
		}
	case ir.TypeHostname:
		if !regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9\-]*[a-zA-Z0-9])?(\.[a-zA-Z0-9]([a-zA-Z0-9\-]*[a-zA-Z0-9])?)*$`).MatchString(val) {
			c.error(pos, "invalid hostname literal %q", val)
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
				c.scope.Declare(p)
			}
			h.Func.Block = c.checkBlockIR(&h.AST.Body)
			c.popScope()
		}
	}
}
