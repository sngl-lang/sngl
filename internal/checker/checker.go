package checker

import (
	"fmt"
	"io/fs"
	"net"
	"net/mail"
	"net/url"
	"regexp"

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
}

// ImportResolver resolves import paths to parsed documents or native declarations.
type ImportResolver interface {
	// Resolve resolves a directory import to parsed AST documents.
	Resolve(fsys fs.FS, importPath string) ([]*ast.Document, error)

	// ResolveScheme resolves a scheme-based import (go://, git://, etc.).
	ResolveScheme(scheme, uri, dir string) (*ir.NativeImport, error)
}

// Check type-checks a parsed v2 AST Document and returns the IR Package.
// The Package is populated best-effort even when diagnostics are present.
func Check(doc *ast.Document, cfg *Config) (*ir.Package, []ir.Diagnostic) {
	c := newChecker(doc, cfg)
	c.pass1()
	c.pass2()
	c.pkg.Symbols = c.symtab
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

	// Current function return type (for return stmt checking).
	returnType *ir.Type

	// Expected type for the expression currently being checked.
	// When set to an enum type, bare enum member names resolve automatically.
	expected *ir.Type

	// Current component (for event validation).
	currentComponent *ir.Component

	// Cached Options structs from platform/language packages.
	optionsCache map[string]*ir.StructDef

	// Cached platform scopes built from Platform.Package() docs.
	platformScopeCache map[string]*ir.Scope
}

func newChecker(doc *ast.Document, cfg *Config) *checker {
	symtab := NewSymbolTable()
	c := &checker{
		doc:          doc,
		cfg:          cfg,
		pkg:          &ir.Package{},
		symtab:       symtab,
		scope:        symtab.Root,
		unitBySuffix: make(map[string]*ir.UnitDef),
		visited:      make(map[string]bool),
	}
	// Insert stdlib scope between base and Root so user declarations shadow stdlib.
	stdlibScope := NewScope(symtab.Root.Parent) // parent = baseScope
	c.scope = stdlibScope
	c.loadStdlib()
	symtab.Root.Parent = stdlibScope
	c.scope = symtab.Root

	// Inject all registered platform and language names as permissive namespaces
	// so raw element access (e.g., html.div) resolves without error.
	// The optimizer shakes off unused platform references; codegen fails if
	// an unresolvable platform element survives.
	for _, p := range cfg.Platforms {
		stdlibScope.Declare(&ir.Namespace{Name: p.PlatformIdentifier()})
	}
	for _, l := range cfg.Languages {
		stdlibScope.Declare(&ir.Namespace{Name: l.LanguageIdentifier()})
	}

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
		case *ast.Import:
			c.registerImport(s)
		case *ast.EnumDef, *ast.StructDef, *ast.UnitDef, *ast.ComponentDecl:
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
	scheme, uri := parseScheme(imp.Path)
	alias := imp.Alias
	if alias == "" {
		alias = namespaceFromPath(imp.Path)
	}

	irImport := &ir.Import{
		AST:   imp,
		Path:  imp.Path,
		Alias: alias,
	}

	if scheme == "internal" {
		// Built-in internal packages — no resolver needed.
		switch uri {
		case "stdlib":
			irImport.Pkg = c.buildIntrinsicsPkgFrom(ir.Intrinsics)
		case "alert":
			irImport.Pkg = c.buildIntrinsicsPkgFrom(ir.AlertIntrinsics)
		case "file":
			irImport.Pkg = c.buildIntrinsicsPkgFrom(ir.FileIntrinsics)
		default:
			c.error(imp.Pos, "unknown internal package: %q", uri)
		}
	} else if scheme != "" && c.cfg.Resolver != nil {
		// Scheme import (go://, git://, etc.)
		native, err := c.cfg.Resolver.ResolveScheme(scheme, uri, c.cfg.Dir)
		if err != nil {
			c.error(imp.Pos, "import %q: %v", imp.Path, err)
		}
		irImport.Native = native
		if native != nil {
			// Register native declarations under the namespace.
			nsPkg := &ir.Package{
				Structs: native.Structs,
				Enums:   native.Enums,
				Funcs:   native.Funcs,
				Vars:    native.Vars,
				Symbols: NewSymbolTable(),
			}
			for _, s := range native.Structs {
				nsPkg.Symbols.Types[s.Name] = s
			}
			for _, e := range native.Enums {
				nsPkg.Symbols.Types[e.Name] = e
			}
			irImport.Pkg = nsPkg
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
			for _, d := range docs {
				pkg, diags := Check(d, &Config{
					FS:        c.cfg.FS,
					Dir:       c.cfg.Dir,
					Resolver:  c.cfg.Resolver,
					Languages: c.cfg.Languages,
					Platforms: c.cfg.Platforms,
				})
				c.diags = append(c.diags, diags...)
				irImport.Pkg = pkg
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
	ns := &ir.Namespace{
		Name: alias,
		Pkg:  irImport.Pkg,
	}
	c.scope.Declare(ns)
}

func (c *checker) registerEnum(e *ast.EnumDef) {
	ed := c.buildEnumDef(e)
	c.pkg.Enums = append(c.pkg.Enums, ed)
	c.symtab.Types[ed.Name] = ed
	c.scope.Declare(ed)
}

func (c *checker) registerStruct(s *ast.StructDef) {
	sd := c.buildStructDef(s)
	c.pkg.Structs = append(c.pkg.Structs, sd)
	c.symtab.Types[sd.Name] = sd
	c.scope.Declare(sd)
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
				c.error(decl.Pos, "const initializer references non-const %q", name)
			}
			// Type check initializer.
			initExpr = c.checkExprExpecting(spec.Default, typ)
			initType := exprType(initExpr)
			if typ.Kind != ir.TypeDyn && initType.Kind != ir.TypeDyn && !initType.IsAssignableTo(typ) {
				c.error(decl.Pos, "cannot initialize %s with %s", typ, initType)
			}
			// Infer type from init if not declared.
			if typ.Kind == ir.TypeDyn {
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
			case "int", "float", "string", "bool", "list", "color":
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
			initType := exprType(initExpr)
			if typ.Kind != ir.TypeDyn && initType.Kind != ir.TypeDyn && !initType.IsAssignableTo(typ) {
				c.error(decl.Pos, "cannot initialize %s with %s", typ, initType)
			}
			c.validateStringDomainLiteral(decl.Pos, typ, initExpr)
			// Infer type from init if not declared.
			if typ.Kind == ir.TypeDyn {
				typ = initType
			}
		}
		for _, name := range spec.Names {
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
			initType := exprType(initExpr)
			if typ.Kind != ir.TypeDyn && initType.Kind != ir.TypeDyn && !initType.IsAssignableTo(typ) {
				c.error(decl.Pos, "cannot initialize %s with %s", typ, initType)
			}
			c.validateStringDomainLiteral(decl.Pos, typ, initExpr)
			if typ.Kind == ir.TypeDyn {
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
				c.error(decl.Pos, "cannot initialize %s with %s", typ, initType)
			}
			if typ.Kind == ir.TypeDyn {
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
	c.pkg.Funcs = append(c.pkg.Funcs, fn)

	if fn.Receiver != "" {
		// Type-attached method.
		c.symtab.RegisterMethod(fn.Receiver, fn)
	} else {
		c.scope.Declare(fn)
	}
}

func (c *checker) registerComponent(comp *ast.ComponentDecl) {
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

	// Walk component body for nested declarations.
	for _, stmt := range comp.Body.Stmts {
		switch s := stmt.(type) {
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
			fn := c.buildFunc(s)
			irComp.Funcs = append(irComp.Funcs, fn)
		}
	}

	c.pkg.Components = append(c.pkg.Components, irComp)
	c.symtab.Comps[irComp.Name] = irComp
	c.scope.Declare(irComp)
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
		c.pkg.Windows = append(c.pkg.Windows, w)
	case "timer":
		t := c.buildTimer(vn)
		c.pkg.Timers = append(c.pkg.Timers, t)
	default:
		c.error(vn.Pos, "unexpected root-level visual node %q", name)
	}
}

// visualNodeTarget extracts the target name from a VisualNode.
func visualNodeTarget(vn *ast.VisualNode) string {
	if vn.Target == nil {
		return ""
	}
	if id, ok := vn.Target.(*ast.IdentExpr); ok {
		return id.Name
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
		out := &ir.Output{
			AST:     vn,
			Options: make(map[string]string),
		}
		for _, a := range vn.Args.Args {
			if arg, ok := a.(ast.Arg); ok && arg.Name != "" {
				switch arg.Name {
				case "lang":
					out.Lang = literalString(arg.Value)
				case "platform":
					out.Platform = literalString(arg.Value)
				default:
					out.Options[arg.Name] = literalString(arg.Value)
				}
			}
		}
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
		out := &ir.Output{
			AST:      s,
			Lang:     lang,
			Platform: platform,
			Options:  make(map[string]string),
		}
		for _, a := range s.Args.Args {
			if arg, ok := a.(ast.Arg); ok && arg.Name != "" {
				out.Options[arg.Name] = literalString(arg.Value)
			}
		}
		if opts := c.lookupOptions(platform); opts != nil {
			c.validateOptionsAgainst(s.Pos, s.Args, opts)
		}
		return out
	case *ast.CallStmt:
		out := &ir.Output{
			Lang:    lang,
			Options: make(map[string]string),
		}
		// Extract platform name from call target.
		if ident, ok := s.Call.Func.(*ast.IdentExpr); ok {
			out.Platform = ident.Name
		} else {
			c.error(s.Pos, "platform target must be a simple name")
			return nil
		}
		// Extract and validate options from call args.
		for _, a := range s.Call.Args.Args {
			switch arg := a.(type) {
			case ast.EventHandler:
				c.error(s.Pos, "event handlers not permitted in output declarations")
			case ast.Arg:
				if arg.Value != nil {
					if name := c.nonConstRef(arg.Value); name != "" {
						c.error(s.Pos, "output option %q must be a constant expression (references %q)", arg.Name, name)
					}
				}
				if arg.Name != "" {
					out.Options[arg.Name] = literalString(arg.Value)
				}
			}
		}
		if opts := c.lookupOptions(out.Platform); opts != nil {
			c.validateOptionsAgainst(s.Pos, s.Call.Args, opts)
		}
		return out
	default:
		c.error(*stmt.StmtPos(), "language block may only contain platform targets")
		return nil
	}
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

// validateOptionsAgainst checks that all option names in the args are valid fields
// of the given Options struct.
func (c *checker) validateOptionsAgainst(pos ast.Pos, args ast.ArgList, opts *ir.StructDef) {
	for _, a := range args.Args {
		arg, ok := a.(ast.Arg)
		if !ok || arg.Name == "" {
			continue
		}
		found := false
		for _, f := range opts.Fields {
			if f.Name == arg.Name {
				found = true
				break
			}
		}
		if !found {
			c.error(pos, "unknown option %q (available: %s)", arg.Name, optionFieldNames(opts))
		}
	}
}

func optionFieldNames(sd *ir.StructDef) string {
	names := make([]string, len(sd.Fields))
	for i, f := range sd.Fields {
		names[i] = f.Name
	}
	return fmt.Sprintf("%v", names)
}

func (c *checker) buildWindow(vn *ast.VisualNode) *ir.Window {
	return &ir.Window{
		AST:  vn,
		Name: vn.ID,
	}
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
	// Check function bodies.
	for _, fn := range c.pkg.Funcs {
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

	// Check timer handler bodies.
	for _, t := range c.pkg.Timers {
		c.checkTimerBody(t)
	}
	for _, comp := range c.pkg.Components {
		for _, t := range comp.Timers {
			c.checkTimerBody(t)
		}
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
		for _, fn := range comp.Funcs {
			fn.Purity = analyzePurity(fn, vars)
			trackAccess(fn, vars)
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
		// Infer return type from expression body if not declared.
		if fn.Return.Kind == ir.TypeDyn && bodyType.Kind != ir.TypeDyn {
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

func (c *checker) checkComponentBody(comp *ir.Component) {
	c.pushScope()
	defer c.popScope()

	prevComp := c.currentComponent
	c.currentComponent = comp
	defer func() { c.currentComponent = prevComp }()

	// Declare props as params.
	for _, p := range comp.Props {
		c.scope.Declare(&ir.Param{
			Name: p.Name,
			Type: p.Type,
		})
	}

	// Check prop defaults now that scope is ready.
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
				}
				propIdx++
			}
		}
	}

	// Declare component-level vars and funcs.
	for _, v := range comp.Vars {
		c.scope.Declare(v)
	}
	for _, fn := range comp.Funcs {
		if fn.Receiver == "" {
			c.scope.Declare(fn)
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

	// Check nested function bodies (vars are now fully typed).
	for _, fn := range comp.Funcs {
		c.checkFuncBody(fn)
	}

	// Check var handler bodies within component scope so handlers can reference component vars.
	c.checkVarHandlerBodies(comp.Vars)

	// Check remaining component body statements.
	if comp.AST != nil && comp.AST.Body.IsDefined() {
		for _, stmt := range comp.AST.Body.Stmts {
			switch stmt.(type) {
			case *ast.ConstDecl, *ast.VarDecl:
				continue // already checked above
			case *ast.FuncDef:
				continue // already checked above
			default:
				if s := c.checkStmt(stmt); s != nil {
					comp.Body = append(comp.Body, s)
				}
			}
		}
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
