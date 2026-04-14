package checker

import (
	"fmt"
	"io/fs"

	"git.duckfam.us/jonathan/sngl/ast"
)

// Config holds checker configuration.
type Config struct {
	FS       fs.FS          // filesystem for resolving relative imports
	Dir      string         // OS directory for scheme imports
	IsMain   bool           // whether output declarations are allowed
	Resolver ImportResolver // import resolver (nil = no imports)
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

	// Current component (for event validation).
	currentComponent *Component
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
					FS:       c.cfg.FS,
					Dir:      c.cfg.Dir,
					Resolver: c.cfg.Resolver,
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
			initType := c.checkExpr(spec.Default)
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
	case *ast.CallExpr:
		// Function calls in const context reference non-const values.
		if ident, ok := x.Func.(*ast.IdentExpr); ok {
			// Builtin conversions are const-safe.
			switch ident.Name {
			case "int", "float", "string", "bool":
				for _, a := range x.Args.Args {
					if arg, ok := a.(ast.Arg); ok {
						if name := c.nonConstRef(arg.Value); name != "" {
							return name
						}
					}
				}
				return ""
			}
		}
		return "<function call>"
	}
	return ""
}

func (c *checker) registerVars(decl *ast.VarDecl) {
	for _, spec := range decl.Specs {
		typ := c.resolveType(spec.Type)
		// Type check initializer.
		if spec.Default != nil {
			initType := c.checkExpr(spec.Default)
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
						Params: c.buildParams(h.Params),
						Block:  &h.Body,
						Pos:    h.Pos,
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
		out := c.buildOutput(vn)
		c.pkg.Outputs = append(c.pkg.Outputs, out)
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

func (c *checker) buildOutput(vn *ast.VisualNode) *Output {
	out := &Output{
		AST:     vn,
		Options: make(map[string]string),
		Pos:     vn.Pos,
	}
	// Extract props from args.
	for _, arg := range vn.Args.Args {
		if a, ok := arg.(ast.Arg); ok && a.Name != "" {
			switch a.Name {
			case "lang":
				out.Lang = literalString(a.Value)
			case "platform":
				out.Platform = literalString(a.Value)
			default:
				out.Options[a.Name] = literalString(a.Value)
			}
		}
	}
	// Also check block body for nested platform/lang nodes.
	// Output blocks can contain nested visual nodes for platform { lang { options } }.
	for _, stmt := range vn.Block.Stmts {
		if child, ok := stmt.(*ast.VisualNode); ok {
			platform := visualNodeTarget(child)
			for _, childStmt := range child.Block.Stmts {
				if langNode, ok := childStmt.(*ast.VisualNode); ok {
					lang := visualNodeTarget(langNode)
					nested := &Output{
						AST:      langNode,
						Platform: platform,
						Lang:     lang,
						Options:  make(map[string]string),
						Pos:      langNode.Pos,
					}
					for _, a := range langNode.Args.Args {
						if arg, ok := a.(ast.Arg); ok && arg.Name != "" {
							nested.Options[arg.Name] = literalString(arg.Value)
						}
					}
					c.pkg.Outputs = append(c.pkg.Outputs, nested)
				}
			}
		}
	}
	return out
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
			Block: &vn.Block,
			Pos:   vn.Pos,
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

	// Check window bodies.
	for _, w := range c.pkg.Windows {
		c.checkWindowBody(w)
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
	if fn.Block != nil {
		c.checkBlock(fn.Block)
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
		c.checkBlock(comp.Body)
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
		c.checkBlock(w.Body)
	}
}
