package checker

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"unicode"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

// ImportResolver loads all .sngl documents from a directory import path.
// The dir argument is the resolved absolute path of the directory to import.
type ImportResolver func(dir string) ([]*ast.Document, error)

// SchemeResolver resolves a scheme-based import URI (e.g., "go://pkg/path")
// into native declarations. The scheme is the URI scheme (e.g., "go"), uri is
// the full import path, and dir is the importing file's directory.
type SchemeResolver func(scheme, uri, dir string) (*ast.NativeDecls, error)

// DefaultResolver returns an ImportResolver that reads and parses all .sngl
// files from the given directory.
func DefaultResolver() ImportResolver {
	return func(dir string) ([]*ast.Document, error) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		var docs []*ast.Document
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".sngl") {
				continue
			}
			path := filepath.Join(dir, e.Name())
			f, err := os.Open(path)
			if err != nil {
				return nil, fmt.Errorf("%s: %w", e.Name(), err)
			}
			doc, err := parser.Parse(e.Name(), f)
			f.Close()
			if err != nil {
				return nil, fmt.Errorf("%s: %w", e.Name(), err)
			}
			docs = append(docs, doc)
		}
		return docs, nil
	}
}

// Check type-checks an SNGL document AST. It resolves types for all declarations,
// validates expressions against typed scopes, and checks visual nodes against
// component schemas. dir is the directory of the .sngl file, used to resolve
// relative import paths. resolve is an optional callback for directory imports;
// pass nil if directory imports are not supported.
func Check(doc *ast.Document, dir string, resolve ImportResolver, schemeResolve SchemeResolver, apis *APIConfig, isMain bool) error {
	registry, styleProps, stdlibUnits, stdlibFuncs, stdlibStructs, err := LoadStdlib()
	if err != nil {
		return fmt.Errorf("loading stdlib: %w", err)
	}

	unitTables := map[string]*ast.UnitTable{}
	for _, u := range stdlibUnits {
		unitTables[u.Name] = ast.BuildUnitTable(u)
	}
	// Include document-level unit definitions.
	for _, u := range doc.Units {
		unitTables[u.Name] = ast.BuildUnitTable(u)
	}

	c := &checker{
		registry:      registry,
		styleProps:    styleProps,
		unitTables:    unitTables,
		scope:         NewScope(nil),
		methods:       map[string]map[string]*methodInfo{},
		dir:           dir,
		resolve:       resolve,
		schemeResolve: schemeResolve,
		visited:       map[string]bool{},
		isMain:        isMain,
		namespaces:    map[string]*importNS{},
		apis:          apis,
	}

	// Inject stdlib functions into the document (prepend so user funcs can override)
	doc.Functions = append(stdlibFuncs, doc.Functions...)
	doc.Structs = append(stdlibStructs, doc.Structs...)

	if isMain && doc.App == nil && len(doc.Tests) == 0 {
		c.errorAt(ast.Pos{}, "missing app node")
		return c.joinErrors()
	}

	if !isMain {
		if doc.App != nil {
			c.errorAt(ast.Pos{}, "component main can only be defined in the main package")
		}
		if len(doc.Outputs) > 0 {
			c.errorAt(doc.Outputs[0].Pos, "output declarations can only appear in the main package")
		}
	}

	c.pass1(doc)
	c.pass2(doc)
	return c.joinErrors()
}

// CheckTests validates test blocks in a document. It resolves each test's
// target component and checks that the test body references valid variables.
func CheckTests(doc *ast.Document) []Diagnostic {
	var diags []Diagnostic
	for _, td := range doc.Tests {
		comp := findTestComponent(doc, td.Component)
		if comp == nil {
			diags = append(diags, Diagnostic{Pos: td.Pos, Msg: fmt.Sprintf("test targets unknown component %q", td.Component)})
			continue
		}
		diags = append(diags, checkTestBody(td, comp)...)
	}
	return diags
}

// findTestComponent locates the component definition for a test, handling "main".
func findTestComponent(doc *ast.Document, name string) *ast.Component {
	if name == "main" {
		return &ast.Component{
			Name:      "main",
			Data:      doc.Data,
			Computeds: doc.Computeds,
			Consts:    doc.Consts,
			Functions: doc.Functions,
		}
	}
	for _, c := range doc.Components {
		if c.Name == name {
			return c
		}
	}
	return nil
}

func checkTestBody(td *ast.TestDef, comp *ast.Component) []Diagnostic {
	known := map[string]bool{}
	for _, d := range comp.Data {
		known[d.Name] = true
	}
	for _, c := range comp.Computeds {
		known[c.Name] = true
	}
	for _, c := range comp.Consts {
		known[c.Name] = true
	}
	for _, p := range comp.Params {
		known[p.Name] = true
	}
	for _, fn := range comp.Functions {
		known[fn.Name] = true
	}
	known["assert"] = true
	known["tick"] = true

	var diags []Diagnostic
	for _, stmt := range td.Body {
		diags = append(diags, checkStmtRefs(stmt, known)...)
	}
	for _, sub := range td.Subtests {
		diags = append(diags, checkTestBody(sub, comp)...)
	}
	return diags
}

func checkStmtRefs(n ast.Node, known map[string]bool) []Diagnostic {
	// Shallow check: verify top-level identifiers in assignments and calls
	switch s := n.(type) {
	case *ast.AssignStmt:
		if ident, ok := s.Target.(*ast.IdentExpr); ok {
			if !known[ident.Name] {
				return []Diagnostic{{Msg: fmt.Sprintf("unknown variable %q", ident.Name)}}
			}
		}
	case *ast.CallExpr:
		if !known[s.Func] {
			return []Diagnostic{{Msg: fmt.Sprintf("unknown function %q", s.Func)}}
		}
	case *ast.CallStmt:
		if !known[s.Call.Func] {
			return []Diagnostic{{Msg: fmt.Sprintf("unknown function %q", s.Call.Func)}}
		}
	}
	return nil
}

// DynamicNSResolver resolves a name within a namespace dynamically.
// Called when a name is not found in the static API document.
type DynamicNSResolver func(namespace, name string) *ast.NativeDecls

// APIConfig provides platform/language API namespaces to the checker.
type APIConfig struct {
	Namespaces map[string]*ast.Document // lang/platform name → API doc
	DynamicNS  DynamicNSResolver        // optional dynamic fallback
}

// methodInfo describes a type-attached method (built-in or user-defined).
type methodInfo struct {
	ReturnType string
	IsBuiltin  bool
}

type checker struct {
	registry      SchemaRegistry
	styleProps    map[string]StylePropSchema
	unitTables    map[string]*ast.UnitTable
	scope         *Scope
	methods       map[string]map[string]*methodInfo // typeName -> methodName -> info
	components    []*ast.Component
	structs       []*ast.StructDef
	enums         []*ast.EnumDef
	constNames    map[string]bool // names declared as const (for untyped constant detection)
	dir           string
	resolve       ImportResolver
	schemeResolve SchemeResolver
	visited       map[string]bool // tracks visited import paths to detect cycles
	isMain        bool            // true for the entry-point package
	namespaces    map[string]*importNS
	apis          *APIConfig
	errs          []error
}

// importNS stores the exported declarations from an imported package.
type importNS struct {
	components []*ast.Component
	structs    []*ast.StructDef
	enums      []*ast.EnumDef
	data       []*ast.Data // extern funcs and vars from native imports
}

// lookupMethod checks the method registry for a type-attached method and returns its return type.
func (c *checker) lookupMethod(typeName, method string) (Type, bool) {
	if methods, ok := c.methods[typeName]; ok {
		if entry, ok := methods[method]; ok {
			if entry.ReturnType != "" {
				return TypeFromHint(entry.ReturnType), true
			}
			return Dyn, true
		}
	}
	return Dyn, false
}

// validateConstExpr checks that a const initializer only references literals, other consts,
// and pure function/method calls — not var fields.
func (c *checker) validateConstExpr(pos ast.Pos, n ast.Node, constNames map[string]bool) {
	switch e := n.(type) {
	case *ast.LiteralExpr:
		// ok
	case *ast.IdentExpr:
		if !constNames[e.Name] {
			c.errorAt(pos, "const initializer references non-const %q", e.Name)
		}
	case *ast.BinaryExpr:
		c.validateConstExpr(pos, e.Left, constNames)
		c.validateConstExpr(pos, e.Right, constNames)
	case *ast.UnaryExpr:
		c.validateConstExpr(pos, e.Operand, constNames)
	case *ast.TernaryExpr:
		c.validateConstExpr(pos, e.Cond, constNames)
		c.validateConstExpr(pos, e.Then, constNames)
		c.validateConstExpr(pos, e.Else, constNames)
	case *ast.CallExpr:
		for _, arg := range e.Args {
			c.validateConstExpr(pos, arg, constNames)
		}
	case *ast.MethodExpr:
		// Type-namespace calls (e.g., string.length("hi")) are fine
		if ident, ok := e.Receiver.(*ast.IdentExpr); ok {
			if _, _, isMethod := ast.SplitMethodName(ident.Name + "." + e.Method); isMethod {
				// Check if it looks like a type namespace
				switch ident.Name {
				case "int", "float", "string", "bool", "list", "color":
					// ok — type namespace
					for _, arg := range e.Args {
						c.validateConstExpr(pos, arg, constNames)
					}
					return
				}
			}
		}
		// Instance method — receiver must be const
		c.validateConstExpr(pos, e.Receiver, constNames)
		for _, arg := range e.Args {
			c.validateConstExpr(pos, arg, constNames)
		}
	case *ast.InterpolationExpr:
		for _, p := range e.Parts {
			c.validateConstExpr(pos, p, constNames)
		}
	case *ast.ListExpr:
		for _, el := range e.Elements {
			c.validateConstExpr(pos, el, constNames)
		}
	case *ast.StructExpr:
		for _, f := range e.Fields {
			c.validateConstExpr(pos, f.Value, constNames)
		}
	case *ast.SelectExpr:
		c.validateConstExpr(pos, e.Operand, constNames)
	case *ast.IndexExpr:
		c.validateConstExpr(pos, e.Operand, constNames)
		c.validateConstExpr(pos, e.Index, constNames)
	}
}

func (c *checker) errorAt(pos ast.Pos, format string, args ...any) {
	msg := fmt.Sprintf(format, args...)
	if pos.IsValid() {
		c.errs = append(c.errs, fmt.Errorf("%s: %s", pos, msg))
	} else {
		c.errs = append(c.errs, fmt.Errorf("%s", msg))
	}
}

func (c *checker) joinErrors() error {
	if len(c.errs) == 0 {
		return nil
	}
	msgs := make([]string, len(c.errs))
	for i, e := range c.errs {
		msgs[i] = e.Error()
	}
	return fmt.Errorf("%s", strings.Join(msgs, "\n"))
}

// pass1 resolves declarations: imports, enums, structs, binds, computeds, components, styles.
func (c *checker) pass1(doc *ast.Document) {
	// Imports
	for _, imp := range doc.Imports {
		// Scheme-based native imports (e.g., "go://pkg/path")
		if imp.Scheme != "" {
			if c.schemeResolve == nil {
				c.errorAt(imp.Pos, "import %q: scheme imports not supported in this context", imp.Path)
				continue
			}
			decls, err := c.schemeResolve(imp.Scheme, imp.Path, c.dir)
			if err != nil {
				c.errorAt(imp.Pos, "import %q: %v", imp.Path, err)
				continue
			}
			ns := &importNS{
				structs: decls.Structs,
				enums:   decls.Enums,
				data:    decls.Data,
			}
			c.namespaces[imp.Namespace] = ns
			c.structs = append(c.structs, decls.Structs...)
			c.enums = append(c.enums, decls.Enums...)
			// Register extern data in scope qualified by namespace
			for _, d := range decls.Data {
				hintType := Dyn
				if d.Init.TypeHint != "" {
					hintType = c.resolveTypeHint(imp.Pos, d.Init.TypeHint)
				}
				c.scope.Declare(imp.Namespace+"."+d.Name, hintType)
			}
			continue
		}

		// Directory imports load .sngl files
		if c.resolve == nil {
			c.errorAt(imp.Pos, "import %q: directory imports not supported in this context", imp.Path)
			continue
		}
		resolved := filepath.Join(c.dir, imp.Path)
		if c.visited[resolved] {
			c.errorAt(imp.Pos, "import %q: cycle detected", imp.Path)
			continue
		}
		c.visited[resolved] = true
		docs, err := c.resolve(resolved)
		if err != nil {
			c.errorAt(imp.Pos, "import %q: %v", imp.Path, err)
			continue
		}
		// Validate imported docs don't define compile targets.
		for _, d := range docs {
			if d.App != nil {
				c.errorAt(imp.Pos, "import %q: component main can only be defined in the main package", imp.Path)
			}
			if len(d.Outputs) > 0 {
				c.errorAt(imp.Pos, "import %q: output declarations can only appear in the main package", imp.Path)
			}
		}
		ns := &importNS{}
		for _, d := range docs {
			for _, s := range d.Structs {
				if isExported(s.Name) {
					ns.structs = append(ns.structs, s)
				}
			}
			for _, e := range d.Enums {
				if isExported(e.Name) {
					ns.enums = append(ns.enums, e)
				}
			}
			for _, comp := range d.Components {
				if !isExported(comp.Name) {
					continue
				}
				ns.components = append(ns.components, comp)
				qualName := imp.Namespace + "." + comp.Name
				schema := &ComponentSchema{
					Props:    make(map[string]PropSchema),
					Events:   map[string]string{},
					Children: ChildrenMany,
				}
				for _, p := range comp.Params {
					t := c.resolveParamType(p)
					schema.Props[p.Name] = PropSchema{Type: t}
				}
				c.registry[qualName] = schema
				doc.ImportedComponents = append(doc.ImportedComponents, comp)
			}
		}
		c.namespaces[imp.Namespace] = ns
	}

	// API namespaces from lang/platform providers
	if c.apis != nil {
		for nsName, apiDoc := range c.apis.Namespaces {
			ns := &importNS{
				components: apiDoc.Components,
				structs:    apiDoc.Structs,
				enums:      apiDoc.Enums,
				data:       apiDoc.Data,
			}
			c.namespaces[nsName] = ns
			c.structs = append(c.structs, apiDoc.Structs...)
			c.enums = append(c.enums, apiDoc.Enums...)
			for _, comp := range apiDoc.Components {
				qualName := nsName + "." + comp.Name
				schema := &ComponentSchema{
					Props:    make(map[string]PropSchema),
					Events:   map[string]string{},
					Children: ChildrenMany,
				}
				for _, p := range comp.Params {
					schema.Props[p.Name] = PropSchema{Type: c.resolveParamType(p)}
				}
				c.registry[qualName] = schema
				doc.ImportedComponents = append(doc.ImportedComponents, comp)
			}
			for _, d := range apiDoc.Data {
				hintType := Dyn
				if d.Init.TypeHint != "" {
					hintType = c.resolveTypeHint(ast.Pos{}, d.Init.TypeHint)
				}
				c.scope.Declare(nsName+"."+d.Name, hintType)
			}
		}
	}

	// Enums from document
	c.enums = append(c.enums, doc.Enums...)

	// Structs
	c.structs = append(c.structs, doc.Structs...)

	// Consts — must be compile-time constant expressions
	constNames := map[string]bool{}
	for _, cn := range doc.Consts {
		if cn.Init.SNGL != nil {
			c.validateConstExpr(cn.Pos, cn.Init.SNGL, constNames)
		}
		t := c.resolveExprType(cn.Pos, &cn.Init)
		c.scope.Declare(cn.Name, t)
		constNames[cn.Name] = true
	}
	c.constNames = constNames

	// Data fields
	for _, d := range doc.Data {
		if d.Extern && d.Trigger != "" {
			c.errorAt(d.Pos, "data %q: trigger on extern field is not allowed", d.Name)
		}
		// Validate type hint early so misspelled types are caught
		// even when SNGL is also set (e.g., var x stirng = "hello").
		var hintType Type
		if d.Init.TypeHint != "" {
			hintType = c.resolveTypeHint(d.Pos, d.Init.TypeHint)
		}
		if d.Extern || d.IsFunc {
			c.scope.Declare(d.Name, hintType)
			continue
		}
		c.validateEnumLiteral(d.Pos, &d.Init)
		c.validateSpecialLiteral(d.Pos, &d.Init)
		t := c.resolveExprType(d.Pos, &d.Init)
		if hintType != Dyn {
			t = hintType
		}
		c.scope.Declare(d.Name, t)
	}

	// Computeds
	for _, comp := range doc.Computeds {
		t := c.resolveExprType(comp.Pos, &comp.Expr)
		c.scope.Declare(comp.Name, t)
	}

	// Functions
	for _, fn := range doc.Functions {
		t := Dyn
		if fn.ReturnType != "" {
			t = c.resolveTypeHint(fn.Pos, fn.ReturnType)
		}
		c.scope.Declare(fn.Name, t)
		// Register type-attached methods
		if typeName, methodName, ok := ast.SplitMethodName(fn.Name); ok {
			if c.methods[typeName] == nil {
				c.methods[typeName] = map[string]*methodInfo{}
			}
			c.methods[typeName][methodName] = &methodInfo{ReturnType: fn.ReturnType}
		}
	}

	// User-defined components
	c.components = doc.Components
	for _, comp := range doc.Components {
		schema := &ComponentSchema{
			Props:    make(map[string]PropSchema),
			Events:   map[string]string{},
			Children: ChildrenMany,
		}
		for _, p := range comp.Params {
			t := c.resolveParamType(p)
			if p.Default.TypeHint != "" && p.Default.Literal != nil {
				litType := InferLiteralType(p.Default.Literal)
				if !isAssignable(litType, t) {
					c.errorAt(comp.Pos, "param %q: default value type %v does not match declared type %q", p.Name, litType, p.Default.TypeHint)
				}
			}
			c.validateEnumLiteral(comp.Pos, &p.Default)
			c.validateSpecialLiteral(comp.Pos, &p.Default)
			schema.Props[p.Name] = PropSchema{Type: t}
		}
		c.registry[comp.Name] = schema
	}

	// Styles
	for _, s := range doc.Styles {
		for prop, expr := range s.Props {
			if _, ok := c.styleProps[prop]; !ok {
				c.errorAt(s.Pos, "style %q: unknown style property %q", s.Name, prop)
				continue
			}
			c.resolveExprType(s.Pos, &expr)
			s.Props[prop] = expr
		}
	}

	// Timers
	for _, t := range doc.Timers {
		// Validate active var exists and is bool
		if varType, ok := c.scope.Lookup(t.Active); !ok {
			c.errorAt(t.Pos, "timer: unknown variable %q", t.Active)
		} else if varType != Bool {
			c.errorAt(t.Pos, "timer: active variable %q must be bool", t.Active)
		}
		// Validate body is a valid statement block
		if t.Body != nil && !isStatement(t.Body) {
			c.errorAt(t.Pos, "timer: body must contain statements")
		}
	}

	c.validateOutputOpts(doc)
}

// resolveParamType returns the type for a component param.
func (c *checker) resolveParamType(p *ast.Param) Type {
	if p.Default.TypeHint != "" {
		return c.resolveTypeHint(p.Pos, p.Default.TypeHint)
	}
	if p.Default.Literal != nil {
		return InferLiteralType(p.Default.Literal)
	}
	if p.Default.SNGL != nil {
		return c.inferNodeType(p.Default.SNGL)
	}
	c.errorAt(p.Pos, "param %q: must have a type hint or a default value", p.Name)
	return Dyn
}

// dynamicResolve attempts dynamic name resolution within a namespace,
// caching the result in the namespace for future lookups.
func (c *checker) dynamicResolve(nsName, name string) bool {
	if c.apis == nil || c.apis.DynamicNS == nil {
		return false
	}
	decls := c.apis.DynamicNS(nsName, name)
	if decls == nil {
		return false
	}
	ns := c.namespaces[nsName]
	if ns == nil {
		ns = &importNS{}
		c.namespaces[nsName] = ns
	}
	ns.structs = append(ns.structs, decls.Structs...)
	ns.enums = append(ns.enums, decls.Enums...)
	ns.data = append(ns.data, decls.Data...)
	c.structs = append(c.structs, decls.Structs...)
	c.enums = append(c.enums, decls.Enums...)
	for _, d := range decls.Data {
		hintType := Dyn
		if d.Init.TypeHint != "" {
			hintType = c.resolveTypeHint(ast.Pos{}, d.Init.TypeHint)
		}
		c.scope.Declare(nsName+"."+d.Name, hintType)
	}
	return true
}

// resolveTypeHint maps a type hint string to a Type, checking named enums
// and inline enum syntax before falling back to TypeFromHint.
func (c *checker) resolveTypeHint(pos ast.Pos, hint string) Type {
	// Qualified type: ns.Type
	if ns, local, ok := strings.Cut(hint, "."); ok {
		if ins, exists := c.namespaces[ns]; exists {
			for _, e := range ins.enums {
				if e.Name == local {
					return String
				}
			}
			for _, s := range ins.structs {
				if s.Name == local {
					return Struct
				}
			}
		}
		c.errorAt(pos, "unknown type %q", hint)
		return Dyn
	}
	// Named enum
	for _, e := range c.enums {
		if e.Name == hint {
			return String
		}
	}
	// Inline enum: enum:val1|val2|val3
	if strings.HasPrefix(hint, "enum:") {
		return String
	}
	// Named struct
	for _, s := range c.structs {
		if s.Name == hint {
			return Struct
		}
	}
	t := TypeFromHint(hint)
	if t == Dyn && !isKnownDynHint(hint) {
		c.errorAt(pos, "unknown type %q", hint)
	}
	return t
}

// resolveExprType determines the type of an expression.
func (c *checker) resolveExprType(pos ast.Pos, expr *ast.Expr) Type {
	if expr.SNGL != nil {
		return c.inferNodeType(expr.SNGL)
	}
	if expr.TypeHint != "" {
		return c.resolveTypeHint(pos, expr.TypeHint)
	}
	if expr.Literal != nil {
		return InferLiteralType(expr.Literal)
	}
	return Dyn
}

// inferNodeType walks a SNGL AST node and returns its inferred type.
func (c *checker) inferNodeType(n ast.Node) Type {
	switch e := n.(type) {
	case *ast.LiteralExpr:
		switch e.Kind {
		case ast.LiteralBool:
			return Bool
		case ast.LiteralInt:
			return Int
		case ast.LiteralFloat:
			return Float
		case ast.LiteralString:
			return String
		case ast.LiteralNull:
			return Dyn
		case ast.LiteralColor:
			return Color
		case ast.LiteralUnit:
			return String // units are string-like
		default:
			return Dyn
		}
	case *ast.IdentExpr:
		if t, ok := c.scope.Lookup(e.Name); ok {
			return t
		}
		// Built-in compile-time constants
		if e.Name == "PLATFORM" || e.Name == "LANGUAGE" {
			return String
		}
		return Dyn
	case *ast.BinaryExpr:
		switch e.Op {
		case ast.BinEq, ast.BinNeq, ast.BinLt, ast.BinLte, ast.BinGt, ast.BinGte,
			ast.BinAnd, ast.BinOr:
			return Bool
		default:
			left := c.inferNodeType(e.Left)
			right := c.inferNodeType(e.Right)
			if left == Float || right == Float {
				return Float
			}
			if left == Int && right == Int {
				return Int
			}
			if left == String && right == String && e.Op == ast.BinAdd {
				return String
			}
			return narrowNumeric(left, right)
		}
	case *ast.UnaryExpr:
		if e.Op == ast.UnaryNot {
			return Bool
		}
		return c.inferNodeType(e.Operand)
	case *ast.TernaryExpr:
		return c.inferNodeType(e.Then)
	case *ast.CallExpr:
		switch e.Func {
		case "string":
			return String
		case "int":
			return Int
		case "float":
			return Float
		default:
			if t, ok := c.scope.Lookup(e.Func); ok {
				return t
			}
			return Dyn
		}
	case *ast.MethodExpr:
		// Check if receiver is a namespace with extern functions (e.g., api.SaveTodo(item))
		if ident, ok := e.Receiver.(*ast.IdentExpr); ok {
			if ns, exists := c.namespaces[ident.Name]; exists {
				for _, d := range ns.data {
					if d.Name == e.Method && d.IsFunc && d.ReturnType != "" {
						return c.resolveTypeHint(ast.Pos{}, d.ReturnType)
					}
				}
				// Dynamic fallback: resolve unknown method in namespace
				if c.dynamicResolve(ident.Name, e.Method) {
					for _, d := range c.namespaces[ident.Name].data {
						if d.Name == e.Method && d.IsFunc && d.ReturnType != "" {
							return c.resolveTypeHint(ast.Pos{}, d.ReturnType)
						}
					}
					return Dyn
				}
			}
			// Check if receiver is a type name (e.g., int.sqrt(x))
			if t, ok := c.lookupMethod(ident.Name, e.Method); ok {
				return t
			}
		}
		// Check by inferred receiver type (e.g., x.sqrt())
		recvType := c.inferNodeType(e.Receiver)
		if t, ok := c.lookupMethod(recvType.String(), e.Method); ok {
			return t
		}
		return Dyn
	case *ast.SelectExpr:
		// Check if operand is a namespace with extern vars (e.g., api.Items)
		if ident, ok := e.Operand.(*ast.IdentExpr); ok {
			if ns, exists := c.namespaces[ident.Name]; exists {
				for _, d := range ns.data {
					if d.Name == e.Field && !d.IsFunc {
						if d.Init.TypeHint != "" {
							return c.resolveTypeHint(ast.Pos{}, d.Init.TypeHint)
						}
					}
				}
				// Dynamic fallback: resolve unknown field in namespace
				if c.dynamicResolve(ident.Name, e.Field) {
					for _, d := range c.namespaces[ident.Name].data {
						if d.Name == e.Field && !d.IsFunc {
							if d.Init.TypeHint != "" {
								return c.resolveTypeHint(ast.Pos{}, d.Init.TypeHint)
							}
						}
					}
					return Dyn
				}
			}
		}
		return Dyn
	case *ast.IndexExpr:
		return Dyn
	case *ast.ListExpr:
		return List
	case *ast.StructExpr:
		return Struct
	case *ast.InterpolationExpr:
		return String
	case *ast.ElementRefExpr:
		return Dyn
	default:
		return Dyn
	}
}

// validateEnumLiteral checks that a literal value is valid for an enum type hint.
func (c *checker) validateEnumLiteral(pos ast.Pos, expr *ast.Expr) {
	if expr.TypeHint == "" || expr.Literal == nil {
		return
	}
	s, ok := expr.Literal.(string)
	if !ok {
		return
	}

	var allowed []string

	// Named enum
	for _, e := range c.enums {
		if e.Name == expr.TypeHint {
			allowed = e.Values
			break
		}
	}

	// Inline enum: enum:val1|val2|val3
	if allowed == nil && strings.HasPrefix(expr.TypeHint, "enum:") {
		allowed = strings.Split(strings.TrimPrefix(expr.TypeHint, "enum:"), "|")
	}

	if allowed == nil {
		return
	}

	if slices.Contains(allowed, s) {
		return
	}
	c.errorAt(pos, "invalid enum value %q: expected one of %v", s, allowed)
}

// validateSpecialLiteral checks that a literal value is valid for a special type hint.
func (c *checker) validateSpecialLiteral(pos ast.Pos, expr *ast.Expr) {
	if expr.TypeHint == "" || expr.Literal == nil {
		return
	}
	switch expr.TypeHint {
	case "color", "date", "time", "dateTime", "duration",
		"url", "urlReference", "irl", "irlReference", "urlTemplate",
		"email", "uuid", "regex", "base64", "ipv4", "ipv6", "hostname",
		"country2", "country3", "currency":
		if err := validateSpecialLiteral(expr.TypeHint, expr.Literal); err != nil {
			c.errorAt(pos, "%v", err)
		}
	}
}

// validateOutputOpts checks output option keys against the platform's Opts struct.
func (c *checker) validateOutputOpts(doc *ast.Document) {
	if c.apis == nil {
		return
	}
	for _, out := range doc.Outputs {
		apiDoc, ok := c.apis.Namespaces[out.Platform]
		if !ok {
			continue
		}
		var opts *ast.StructDef
		for _, s := range apiDoc.Structs {
			if s.Name == "Opts" {
				opts = s
				break
			}
		}
		if opts == nil {
			continue
		}
		validFields := map[string]bool{}
		for _, f := range opts.Fields {
			validFields[f.Name] = true
		}
		for key := range out.Options {
			if !validFields[key] {
				c.errorAt(out.Pos, "unknown option %q for platform %q", key, out.Platform)
			}
		}
	}
}

// pass2 validates the visual tree.
func (c *checker) pass2(doc *ast.Document) {
	if doc.App != nil {
		for _, child := range doc.App.Children {
			c.checkVisualNode(child, c.scope)
		}
	}
	for _, comp := range doc.Components {
		compScope := NewScope(c.scope)
		for _, p := range comp.Params {
			compScope.Declare(p.Name, c.resolveParamType(p))
		}
		for _, child := range comp.Body {
			c.checkVisualNode(child, compScope)
		}
	}
}

func (c *checker) checkVisualNode(vn *ast.VisualNode, scope *Scope) {
	schema, ok := c.registry[vn.Component]
	if !ok {
		c.errorAt(vn.Pos, "unknown component %q", vn.Component)
		return
	}

	// Universal attributes
	if vn.Class != nil {
		c.checkExprType(vn.Pos, vn.Class, String, scope, "class")
	}
	if vn.Ref != nil {
		c.checkExprType(vn.Pos, vn.Ref, String, scope, "ref")
	}
	if vn.If != nil {
		c.checkExprType(vn.Pos, vn.If, Bool, scope, "if")
	}

	// For clause
	childScope := scope
	if vn.For != nil {
		c.resolveExprInScope(vn.Pos, &vn.For.Iterable, scope)
		childScope = NewScope(scope)
		childScope.Declare(vn.For.Variable, Dyn)
		if vn.For.IndexVar != "" {
			childScope.Declare(vn.For.IndexVar, Int)
		}
	}

	// Key
	if vn.Key != nil {
		c.checkExprType(vn.Pos, vn.Key, Dyn, childScope, "key")
	}

	// Props
	for name, expr := range vn.Props {
		ps, ok := schema.Props[name]
		if !ok {
			c.errorAt(vn.Pos, "%s: unknown property %q", vn.Component, name)
			continue
		}
		c.checkExprType(vn.Pos, &expr, ps.Type, childScope, vn.Component+"."+name)
		vn.Props[name] = expr
	}

	// Required params
	if comp := c.findComponent(vn.Component); comp != nil {
		for _, p := range comp.Params {
			if p.Required {
				if _, ok := vn.Props[p.Name]; !ok {
					c.errorAt(vn.Pos, "%s: required property %q not provided", vn.Component, p.Name)
				}
			}
		}
	}

	// Events
	for name, expr := range vn.Events {
		if _, ok := schema.Events[name]; !ok {
			c.errorAt(vn.Pos, "%s: unknown event %q", vn.Component, name)
			continue
		}
		if expr.SNGL != nil {
			if !isStatement(expr.SNGL) {
				c.errorAt(vn.Pos, "%s on:%s: handler must be a statement (assignment, toggle, or emit)", vn.Component, name)
			}
		}
		vn.Events[name] = expr
	}

	// Style attrs
	for name, expr := range vn.StyleAttrs {
		if _, ok := c.styleProps[name]; !ok {
			c.errorAt(vn.Pos, "%s: unknown style property %q", vn.Component, name)
			continue
		}
		c.resolveExprInScope(vn.Pos, &expr, scope)
		vn.StyleAttrs[name] = expr
	}

	// Style block
	for name, expr := range vn.StyleBlock {
		if _, ok := c.styleProps[name]; !ok {
			c.errorAt(vn.Pos, "%s: unknown style property %q", vn.Component, name)
			continue
		}
		c.resolveExprInScope(vn.Pos, &expr, scope)
		vn.StyleBlock[name] = expr
	}

	// Children policy
	switch schema.Children {
	case ChildrenNone:
		if len(vn.Children) > 0 {
			c.errorAt(vn.Pos, "%s: does not accept children", vn.Component)
		}
	case ChildrenOne:
		if len(vn.Children) != 1 {
			c.errorAt(vn.Pos, "%s: expects exactly one child, got %d", vn.Component, len(vn.Children))
		}
	}

	// Recurse
	for _, child := range vn.Children {
		c.checkVisualNode(child, childScope)
	}
}

// checkExprType resolves an expression's type and checks it matches the expected type.
// Numeric constant expressions are untyped: an int constant is assignable to float and vice versa.
func (c *checker) checkExprType(pos ast.Pos, expr *ast.Expr, expected Type, scope *Scope, context string) {
	got := c.resolveExprInScope(pos, expr, scope)
	if !isAssignable(got, expected) {
		if isNumeric(got) && isNumeric(expected) && c.isConstantExpr(expr) {
			return
		}
		c.errorAt(pos, "%s: expected %s, got %s", context, expected, got)
	}
}

// isConstantExpr reports whether expr is a compile-time constant expression
// (literals, const references, and pure operations on constants).
func (c *checker) isConstantExpr(expr *ast.Expr) bool {
	if expr.Literal != nil {
		return true
	}
	if expr.SNGL != nil {
		return c.isConstantNode(expr.SNGL)
	}
	return false
}

// isConstantNode reports whether an AST node is a compile-time constant.
func (c *checker) isConstantNode(n ast.Node) bool {
	switch e := n.(type) {
	case *ast.LiteralExpr:
		return true
	case *ast.IdentExpr:
		return c.constNames[e.Name]
	case *ast.BinaryExpr:
		return c.isConstantNode(e.Left) && c.isConstantNode(e.Right)
	case *ast.UnaryExpr:
		return c.isConstantNode(e.Operand)
	case *ast.TernaryExpr:
		return c.isConstantNode(e.Cond) && c.isConstantNode(e.Then) && c.isConstantNode(e.Else)
	case *ast.CallExpr:
		for _, arg := range e.Args {
			if !c.isConstantNode(arg) {
				return false
			}
		}
		return true
	case *ast.MethodExpr:
		if !c.isConstantNode(e.Receiver) {
			return false
		}
		for _, arg := range e.Args {
			if !c.isConstantNode(arg) {
				return false
			}
		}
		return true
	default:
		return false
	}
}

// resolveExprInScope infers a SNGL expression's type within a given scope.
func (c *checker) resolveExprInScope(pos ast.Pos, expr *ast.Expr, scope *Scope) Type {
	if expr.SNGL != nil {
		return c.inferNodeTypeInScope(expr.SNGL, scope)
	}
	if expr.TypeHint != "" {
		return c.resolveTypeHint(pos, expr.TypeHint)
	}
	if expr.Literal != nil {
		return InferLiteralType(expr.Literal)
	}
	return Dyn
}

// inferNodeTypeInScope walks a SNGL AST node and returns its inferred type,
// using the given scope for variable lookups.
func (c *checker) inferNodeTypeInScope(n ast.Node, scope *Scope) Type {
	switch e := n.(type) {
	case *ast.LiteralExpr:
		switch e.Kind {
		case ast.LiteralBool:
			return Bool
		case ast.LiteralInt:
			return Int
		case ast.LiteralFloat:
			return Float
		case ast.LiteralString:
			return String
		case ast.LiteralNull:
			return Dyn
		case ast.LiteralColor:
			return Color
		case ast.LiteralUnit:
			return String
		default:
			return Dyn
		}
	case *ast.IdentExpr:
		if t, ok := scope.Lookup(e.Name); ok {
			return t
		}
		if e.Name == "PLATFORM" || e.Name == "LANGUAGE" {
			return String
		}
		return Dyn
	case *ast.BinaryExpr:
		switch e.Op {
		case ast.BinEq, ast.BinNeq, ast.BinLt, ast.BinLte, ast.BinGt, ast.BinGte,
			ast.BinAnd, ast.BinOr:
			return Bool
		default:
			left := c.inferNodeTypeInScope(e.Left, scope)
			right := c.inferNodeTypeInScope(e.Right, scope)
			if left == Float || right == Float {
				return Float
			}
			if left == Int && right == Int {
				return Int
			}
			if left == String && right == String && e.Op == ast.BinAdd {
				return String
			}
			return narrowNumeric(left, right)
		}
	case *ast.UnaryExpr:
		if e.Op == ast.UnaryNot {
			return Bool
		}
		return c.inferNodeTypeInScope(e.Operand, scope)
	case *ast.TernaryExpr:
		return c.inferNodeTypeInScope(e.Then, scope)
	case *ast.CallExpr:
		switch e.Func {
		case "string":
			return String
		case "int":
			return Int
		case "float":
			return Float
		default:
			if t, ok := c.scope.Lookup(e.Func); ok {
				return t
			}
			return Dyn
		}
	case *ast.MethodExpr:
		// Check if receiver is a namespace with extern functions
		if ident, ok := e.Receiver.(*ast.IdentExpr); ok {
			if ns, exists := c.namespaces[ident.Name]; exists {
				for _, d := range ns.data {
					if d.Name == e.Method && d.IsFunc && d.ReturnType != "" {
						return c.resolveTypeHint(ast.Pos{}, d.ReturnType)
					}
				}
				if c.dynamicResolve(ident.Name, e.Method) {
					for _, d := range c.namespaces[ident.Name].data {
						if d.Name == e.Method && d.IsFunc && d.ReturnType != "" {
							return c.resolveTypeHint(ast.Pos{}, d.ReturnType)
						}
					}
					return Dyn
				}
			}
			// Check if receiver is a type name (e.g., int.sqrt(x))
			if t, ok := c.lookupMethod(ident.Name, e.Method); ok {
				return t
			}
		}
		// Check by inferred receiver type (e.g., x.sqrt())
		recvType := c.inferNodeTypeInScope(e.Receiver, scope)
		if t, ok := c.lookupMethod(recvType.String(), e.Method); ok {
			return t
		}
		return Dyn
	case *ast.SelectExpr:
		// Check if operand is a namespace with extern vars
		if ident, ok := e.Operand.(*ast.IdentExpr); ok {
			if ns, exists := c.namespaces[ident.Name]; exists {
				for _, d := range ns.data {
					if d.Name == e.Field && !d.IsFunc {
						if d.Init.TypeHint != "" {
							return c.resolveTypeHint(ast.Pos{}, d.Init.TypeHint)
						}
					}
				}
				if c.dynamicResolve(ident.Name, e.Field) {
					for _, d := range c.namespaces[ident.Name].data {
						if d.Name == e.Field && !d.IsFunc {
							if d.Init.TypeHint != "" {
								return c.resolveTypeHint(ast.Pos{}, d.Init.TypeHint)
							}
						}
					}
					return Dyn
				}
			}
		}
		return Dyn
	case *ast.IndexExpr:
		return Dyn
	case *ast.ListExpr:
		return List
	case *ast.StructExpr:
		return Struct
	case *ast.InterpolationExpr:
		return String
	case *ast.ElementRefExpr:
		return Dyn
	default:
		return Dyn
	}
}

// findComponent returns the AST component definition for a user-defined component, or nil.
func (c *checker) findComponent(name string) *ast.Component {
	if ns, local, ok := strings.Cut(name, "."); ok {
		if ins, exists := c.namespaces[ns]; exists {
			for _, comp := range ins.components {
				if comp.Name == local {
					return comp
				}
			}
		}
		return nil
	}
	for _, comp := range c.components {
		if comp.Name == name {
			return comp
		}
	}
	return nil
}

// isExported reports whether a name is exported (starts with uppercase).
func isExported(name string) bool {
	if name == "" {
		return false
	}
	return unicode.IsUpper(rune(name[0]))
}

// isStatement checks if an SNGL AST node represents a mutation statement.
func isStatement(n ast.Node) bool {
	switch e := n.(type) {
	case *ast.AssignStmt:
		return true
	case *ast.ToggleStmt:
		return true
	case *ast.EmitStmt:
		return true
	case *ast.CallStmt:
		return true
	case *ast.MethodExpr:
		// Namespaced function calls used as statements (e.g., api.SaveTodo(item))
		return true
	case *ast.StmtBlock:
		for _, s := range e.Stmts {
			if !isStatement(s) {
				return false
			}
		}
		return len(e.Stmts) > 0
	default:
		return false
	}
}
