package checker

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/snglparser"
)

// ImportResolver loads all .sngl documents from a directory import path.
// The dir argument is the resolved absolute path of the directory to import.
type ImportResolver func(dir string) ([]*ast.Document, error)

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
			doc, err := snglparser.Parse(e.Name(), f)
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
func Check(doc *ast.Document, dir string, resolve ImportResolver) error {
	registry, styleProps, stdlibUnits, err := LoadStdlib()
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
		registry:   registry,
		styleProps: styleProps,
		unitTables: unitTables,
		scope:      NewScope(nil),
		dir:        dir,
		resolve:    resolve,
		visited:    map[string]bool{},
	}

	if doc.App == nil && len(doc.Tests) == 0 {
		c.errorAt(ast.Pos{}, "missing app node")
		return c.joinErrors()
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

type checker struct {
	registry   SchemaRegistry
	styleProps map[string]StylePropSchema
	unitTables map[string]*ast.UnitTable
	scope      *Scope
	components []*ast.Component
	structs    []*ast.StructDef
	enums      []*ast.EnumDef
	dir        string
	resolve    ImportResolver
	visited    map[string]bool // tracks visited import paths to detect cycles
	errs       []error
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
	// Imports (directory imports load .sngl files)
	for _, imp := range doc.Imports {
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
		for _, d := range docs {
			c.structs = append(c.structs, d.Structs...)
			c.enums = append(c.enums, d.Enums...)
			for _, comp := range d.Components {
				c.components = append(c.components, comp)
				schema := &ComponentSchema{
					Props:    make(map[string]PropSchema),
					Events:   map[string]string{},
					Children: ChildrenMany,
				}
				for _, p := range comp.Params {
					t := c.resolveParamType(p)
					schema.Props[p.Name] = PropSchema{Type: t}
				}
				c.registry[comp.Name] = schema
			}
		}
	}

	// Enums from document
	c.enums = append(c.enums, doc.Enums...)

	// Structs
	c.structs = append(c.structs, doc.Structs...)

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

// resolveTypeHint maps a type hint string to a Type, checking named enums
// and inline enum syntax before falling back to TypeFromHint.
func (c *checker) resolveTypeHint(pos ast.Pos, hint string) Type {
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
		case "size":
			return Int
		default:
			if t, ok := c.scope.Lookup(e.Func); ok {
				return t
			}
			return Dyn
		}
	case *ast.MethodExpr:
		switch e.Method {
		case "contains", "startsWith", "endsWith":
			return Bool
		case "size":
			return Int
		}
		return Dyn
	case *ast.SelectExpr:
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
func (c *checker) checkExprType(pos ast.Pos, expr *ast.Expr, expected Type, scope *Scope, context string) {
	got := c.resolveExprInScope(pos, expr, scope)
	if !isAssignable(got, expected) {
		c.errorAt(pos, "%s: expected %s, got %s", context, expected, got)
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
		case "size":
			return Int
		default:
			if t, ok := c.scope.Lookup(e.Func); ok {
				return t
			}
			return Dyn
		}
	case *ast.MethodExpr:
		switch e.Method {
		case "contains", "startsWith", "endsWith":
			return Bool
		case "size":
			return Int
		}
		return Dyn
	case *ast.SelectExpr:
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
	for _, comp := range c.components {
		if comp.Name == name {
			return comp
		}
	}
	return nil
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
