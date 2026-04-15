package checker

import (
	"fmt"
	"io/fs"

	"git.duckfam.us/jonathan/sngl/ast"
)

// Target provides type information for a registered language or platform.
type Target interface {
	Identifier() string
	Package() []*ast.Document         // parsed .sngl API docs (includes Options struct)
	Resolve(identifier string) Symbol // dynamic identifiers (e.g., html.div); nil if unknown
}

// Language is a Target for a registered language translator.
type Language interface{ Target }

// Platform is a Target for a registered platform generator.
type Platform interface {
	Target
	IsLanguageSupported(Language) bool
}

// StaticTarget identifies the compile target by name.
type StaticTarget struct {
	Platform string
	Language string
}

// Config holds checker configuration.
type Config struct {
	FS        fs.FS          // filesystem for resolving relative imports
	Dir       string         // OS directory for scheme imports
	IsMain    bool           // whether output declarations are allowed
	Resolver  ImportResolver // import resolver (nil = no imports)
	Languages []Language     // registered languages
	Platforms []Platform     // registered platforms
	Target    *StaticTarget  // current compile target (nil = check all)
}

// ImportResolver resolves import paths to parsed documents or native declarations.
type ImportResolver interface {
	// Resolve resolves a directory import to parsed AST documents.
	Resolve(fsys fs.FS, importPath string) ([]*ast.Document, error)

	// ResolveScheme resolves a scheme-based import (go://, git://, etc.).
	ResolveScheme(scheme, uri, dir string) (*NativeImport, error)
}

// Check type-checks a parsed v2 AST Document and returns the IR Package.
// The Package is populated best-effort even when diagnostics are present.
func Check(doc *ast.Document, cfg *Config) (*Package, []Diagnostic) {
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

	pkg    *Package
	diags  []Diagnostic
	scope  *Scope
	symtab *SymbolTable

	// Type resolution context.
	typeParams []string // active generic type params (set during function checking)

	// Unit suffix reverse lookup.
	unitBySuffix map[string]*UnitDef

	// Import cycle detection.
	visited map[string]bool

	// Current function return type (for return stmt checking).
	returnType *Type

	// Expected type for the expression currently being checked.
	// When set to an enum type, bare enum member names resolve automatically.
	expected *Type

	// Current component (for event validation).
	currentComponent *Component

	// Cached Options structs from platform/language packages.
	optionsCache map[string]*StructDef

	// Cached platform scopes built from Platform.Package() docs.
	platformScopeCache map[string]*Scope
}

func newChecker(doc *ast.Document, cfg *Config) *checker {
	symtab := NewSymbolTable()
	c := &checker{
		doc:          doc,
		cfg:          cfg,
		pkg:          &Package{TypeMap: make(map[ast.Expr]*Type)},
		symtab:       symtab,
		scope:        symtab.Root,
		unitBySuffix: make(map[string]*UnitDef),
		visited:      make(map[string]bool),
	}
	// Insert stdlib scope between base and Root so user declarations shadow stdlib.
	stdlibScope := NewScope(symtab.Root.parent) // parent = baseScope
	c.scope = stdlibScope
	stdlibPkg := c.loadStdlib()
	c.pkg.Stdlib = stdlibPkg
	symtab.Root.parent = stdlibScope
	c.scope = symtab.Root

	// Inject all registered platform and language names as permissive namespaces
	// so raw element access (e.g., html.div) resolves without error.
	// The optimizer shakes off unused platform references; codegen fails if
	// an unresolvable platform element survives.
	for _, p := range cfg.Platforms {
		stdlibScope.Declare(&Namespace{Name: p.Identifier()})
	}
	for _, l := range cfg.Languages {
		stdlibScope.Declare(&Namespace{Name: l.Identifier()})
	}

	return c
}

func (c *checker) error(pos ast.Pos, format string, args ...any) {
	c.diags = append(c.diags, Diagnostic{
		Pos:      pos,
		Msg:      fmt.Sprintf(format, args...),
		Severity: Error,
	})
}

func (c *checker) warn(pos ast.Pos, format string, args ...any) {
	c.diags = append(c.diags, Diagnostic{
		Pos:      pos,
		Msg:      fmt.Sprintf(format, args...),
		Severity: Warning,
	})
}

// pushScope creates a child scope and makes it current.
func (c *checker) pushScope() {
	c.scope = NewScope(c.scope)
}

// popScope restores the parent scope.
func (c *checker) popScope() {
	c.scope = c.scope.parent
}

// --- pass1: declaration registration ---

func (c *checker) pass1() {
	for _, stmt := range c.doc.Stmts {
		switch s := stmt.(type) {
		case *ast.Import:
			c.registerImport(s)
		case *ast.EnumDef:
			c.registerEnum(s)
		case *ast.StructDef:
			c.registerStruct(s)
		case *ast.UnitDef:
			c.registerUnit(s)
		case *ast.ConstDecl:
			c.registerConsts(s)
		case *ast.VarDecl:
			c.registerVars(s)
		case *ast.FuncDef:
			c.registerFunc(s)
		case *ast.ComponentDecl:
			c.registerComponent(s)
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

	irImport := &Import{
		AST:   imp,
		Alias: alias,
		Pos:   imp.Pos,
	}

	if scheme != "" && c.cfg.Resolver != nil {
		// Scheme import (go://, git://, etc.)
		native, err := c.cfg.Resolver.ResolveScheme(scheme, uri, c.cfg.Dir)
		if err != nil {
			c.error(imp.Pos, "import %q: %v", imp.Path, err)
		}
		irImport.Native = native
		if native != nil {
			// Register native declarations under the namespace.
			nsPkg := &Package{
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

	// Declare namespace in scope.
	ns := &Namespace{
		Name: alias,
		Pkg:  irImport.Pkg,
		Pos:  imp.Pos,
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
	// Populate reverse suffix lookup.
	for _, s := range ud.Suffixes {
		c.unitBySuffix[s.Name] = ud
	}
}

func (c *checker) registerConsts(decl *ast.ConstDecl) {
	for _, spec := range decl.Specs {
		typ := c.resolveType(spec.Type)
		// Validate const initializer only references consts/literals.
		if spec.Default != nil {
			if name := c.nonConstRef(spec.Default); name != "" {
				c.error(decl.Pos, "const initializer references non-const %q", name)
			}
			// Type check initializer.
			initType := c.checkExprExpecting(spec.Default, typ)
			if typ.Kind != TypeDyn && initType.Kind != TypeDyn && !initType.IsAssignableTo(typ) {
				c.error(decl.Pos, "cannot initialize %s with %s", typ, initType)
			}
			// Infer type from init if not declared.
			if typ.Kind == TypeDyn {
				typ = initType
			}
		}
		for _, name := range spec.Names {
			v := &Var{
				AST:     decl,
				Name:    name,
				Type:    typ,
				IsConst: true,
				Pos:     decl.Pos,
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
			if v, ok := sym.(*Var); ok && v.IsConst {
				return ""
			}
			// Enum/struct types are fine as identifiers.
			if _, ok := sym.(*EnumDef); ok {
				return ""
			}
			if _, ok := sym.(*StructDef); ok {
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
				if _, ok := sym.(*Namespace); ok {
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
		// Type check initializer.
		if spec.Default != nil {
			initType := c.checkExprExpecting(spec.Default, typ)
			if typ.Kind != TypeDyn && initType.Kind != TypeDyn && !initType.IsAssignableTo(typ) {
				c.error(decl.Pos, "cannot initialize %s with %s", typ, initType)
			}
			// Infer type from init if not declared.
			if typ.Kind == TypeDyn {
				typ = initType
			}
		}
		for _, name := range spec.Names {
			v := &Var{
				AST:  decl,
				Name: name,
				Type: typ,
				Pos:  decl.Pos,
			}
			// Build event handlers.
			for i := range spec.Handlers {
				h := &spec.Handlers[i]
				handler := &EventHandler{
					AST:  h,
					Name: h.Name,
					Func: &Func{
						Params:   c.buildParams(h.Params),
						ASTBlock: &h.Body,
						Pos:      h.Pos,
					},
				}
				v.Handlers = append(v.Handlers, handler)
			}
			c.pkg.Vars = append(c.pkg.Vars, v)
			c.scope.Declare(v)
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
	irComp := &Component{
		AST:  comp,
		Name: comp.Name,
		Body: &comp.Body,
		Pos:  comp.Pos,
	}

	// Resolve props and events from PropList.
	for _, p := range comp.Props.Props {
		switch pd := p.(type) {
		case ast.Param:
			prop := &Prop{
				Name:          pd.Name,
				Type:          c.resolveType(pd.Type),
				Default:       pd.Default,
				Bidirectional: pd.Bidirectional,
			}
			irComp.Props = append(irComp.Props, prop)
		case ast.EventDecl:
			evt := &EventDecl{
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
					v := &Var{
						AST:     s,
						Name:    name,
						Type:    typ,
						IsConst: true,
						Pos:     s.Pos,
					}
					irComp.Vars = append(irComp.Vars, v)
				}
			}
		case *ast.VarDecl:
			for _, spec := range s.Specs {
				typ := c.resolveType(spec.Type)
				for _, name := range spec.Names {
					v := &Var{
						AST:  s,
						Name: name,
						Type: typ,
						Pos:  s.Pos,
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
		out := &Output{
			AST:     vn,
			Options: make(map[string]string),
			Pos:     vn.Pos,
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
func (c *checker) buildPlatformOutput(stmt ast.Stmt, lang string) *Output {
	switch s := stmt.(type) {
	case *ast.VisualNode:
		c.validateOutputArgs(s)
		platform := visualNodeTarget(s)
		if len(s.Block.Stmts) > 0 {
			c.error(s.Pos, "platform %q must not contain a body", platform)
		}
		out := &Output{
			AST:      s,
			Lang:     lang,
			Platform: platform,
			Options:  make(map[string]string),
			Pos:      s.Pos,
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
		out := &Output{
			Lang:    lang,
			Options: make(map[string]string),
			Pos:     s.Pos,
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
		switch a.(type) {
		case ast.EventHandler:
			c.error(vn.Pos, "event handlers not permitted in output declarations")
		case ast.Arg:
			arg := a.(ast.Arg)
			if arg.Value != nil {
				if name := c.nonConstRef(arg.Value); name != "" {
					c.error(vn.Pos, "output option %q must be a constant expression (references %q)", arg.Name, name)
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

// lookupTarget finds a registered platform or language by name.
func (c *checker) lookupTarget(name string) Target {
	if c.cfg == nil {
		return nil
	}
	for _, p := range c.cfg.Platforms {
		if p.Identifier() == name {
			return p
		}
	}
	for _, l := range c.cfg.Languages {
		if l.Identifier() == name {
			return l
		}
	}
	return nil
}

// lookupOptions returns the Options struct for a platform or lang name.
// Returns nil if no target or no Options struct found.
func (c *checker) lookupOptions(name string) *StructDef {
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
		c.optionsCache = make(map[string]*StructDef)
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
func (c *checker) validateOptionsAgainst(pos ast.Pos, args ast.ArgList, opts *StructDef) {
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

func optionFieldNames(sd *StructDef) string {
	names := make([]string, len(sd.Fields))
	for i, f := range sd.Fields {
		names[i] = f.Name
	}
	return fmt.Sprintf("%v", names)
}

func (c *checker) buildWindow(vn *ast.VisualNode) *Window {
	w := &Window{
		AST:  vn,
		Name: vn.ID, // window #name
		Body: &vn.Block,
		Pos:  vn.Pos,
	}
	// Extract name from args if ID not set.
	if w.Name == "" {
		for _, arg := range vn.Args.Args {
			if a, ok := arg.(ast.Arg); ok && a.Name == "" {
				w.Name = literalString(a.Value)
				break
			}
		}
	}
	return w
}

func (c *checker) buildTimer(vn *ast.VisualNode) *Timer {
	return &Timer{
		AST: vn,
		Handler: &Func{
			ASTBlock: &vn.Block,
			Pos:      vn.Pos,
		},
		Pos: vn.Pos,
	}
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

func (c *checker) collectVarMap() map[string]*Var {
	vars := make(map[string]*Var)
	for _, v := range c.pkg.Vars {
		vars[v.Name] = v
	}
	return vars
}

func (c *checker) checkFuncBody(fn *Func) {
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

	if fn.Body != nil {
		bodyType := c.checkExpr(fn.Body)
		// Infer return type from expression body if not declared.
		if fn.Return.Kind == TypeDyn && bodyType.Kind != TypeDyn {
			fn.Return = bodyType
		}
		// Expression-body return type check.
		if fn.Return != nil && fn.Return.Kind != TypeDyn && bodyType.Kind != TypeDyn && !bodyType.IsAssignableTo(fn.Return) {
			c.error(fn.Pos, "cannot return %s as %s", bodyType, fn.Return)
		}
	}
	if fn.ASTBlock != nil {
		fn.Block = c.checkBlockIR(fn.ASTBlock)
	}
}

func (c *checker) checkComponentBody(comp *Component) {
	c.pushScope()
	defer c.popScope()

	prevComp := c.currentComponent
	c.currentComponent = comp
	defer func() { c.currentComponent = prevComp }()

	// Declare props as params.
	for _, p := range comp.Props {
		c.scope.Declare(&Param{
			Name: p.Name,
			Type: p.Type,
			Pos:  comp.Pos,
		})
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

	// Check nested function bodies.
	for _, fn := range comp.Funcs {
		c.checkFuncBody(fn)
	}

	// Check component body statements.
	if comp.Body != nil {
		comp.IRBody = c.checkBlockIR(comp.Body)
	}
}

func (c *checker) checkWindowBody(w *Window) {
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

	if w.Body != nil {
		w.IRBody = c.checkBlockIR(w.Body)
	}
}
