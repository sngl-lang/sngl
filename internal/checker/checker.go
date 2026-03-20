package checker

import (
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/snglparser"
	"github.com/google/cel-go/cel"
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
// validates CEL expressions against typed scopes, and checks visual nodes against
// component schemas. dir is the directory of the .sngl file, used to resolve
// relative import paths. resolve is an optional callback for directory imports;
// pass nil if directory imports are not supported.
func Check(doc *ast.Document, dir string, resolve ImportResolver) error {
	registry, styleProps, _, err := LoadStdlib()
	if err != nil {
		return fmt.Errorf("loading stdlib: %w", err)
	}

	c := &checker{
		registry:   registry,
		styleProps: styleProps,
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
	}
	return nil
}

type checker struct {
	registry   SchemaRegistry
	styleProps map[string]StylePropSchema
	scope      *Scope
	components []*ast.Component
	structs    []*ast.StructDef
	enums      []*ast.EnumDef
	protoDescs []any // *descriptorpb.FileDescriptorProto for cel.TypeDescs
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

// pass1 resolves declarations: proto imports, enums, structs, binds, computeds, components, styles.
func (c *checker) pass1(doc *ast.Document) {
	// Imports
	for _, imp := range doc.Imports {
		if strings.HasSuffix(imp.Path, ".proto") {
			result, err := ParseProtoFile(filepath.Join(c.dir, imp.Path))
			if err != nil {
				c.errorAt(imp.Pos, "import %q: %v", imp.Path, err)
				continue
			}
			c.structs = append(c.structs, result.Structs...)
			c.enums = append(c.enums, result.Enums...)
			c.protoDescs = append(c.protoDescs, result.FileDesc)
		} else {
			// Directory import: load all .sngl files from the directory
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
					// Register imported component schema
					schema := &ComponentSchema{
						Props:    make(map[string]PropSchema),
						Events:   map[string]string{},
						Children: ChildrenMany,
					}
					for _, p := range comp.Params {
						var t *cel.Type
						if p.Default.TypeHint != "" {
							t = c.resolveTypeHint(p.Default.TypeHint)
						} else if p.Default.Literal != nil {
							t = InferLiteralType(p.Default.Literal)
						} else {
							t = cel.DynType
						}
						schema.Props[p.Name] = PropSchema{Type: t}
					}
					c.registry[comp.Name] = schema
				}
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
		if d.Extern || d.IsFunc {
			// Extern fields/funcs: declare in scope with resolved type
			t := c.resolveTypeHint(d.Init.TypeHint)
			c.scope.Declare(d.Name, t)
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

	// User-defined components
	c.components = doc.Components
	for _, comp := range doc.Components {
		schema := &ComponentSchema{
			Props:    make(map[string]PropSchema),
			Events:   map[string]string{},
			Children: ChildrenMany,
		}
		for _, p := range comp.Params {
			var t *cel.Type
			if p.Default.TypeHint != "" {
				t = c.resolveTypeHint(p.Default.TypeHint)
			} else if p.Default.Literal != nil {
				t = InferLiteralType(p.Default.Literal)
			} else {
				t = cel.DynType
			}
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

// resolveTypeHint maps a type hint string to a CEL type, checking named enums
// and inline enum syntax before falling back to TypeHintToCelType.
func (c *checker) resolveTypeHint(hint string) *cel.Type {
	// Named enum
	for _, e := range c.enums {
		if e.Name == hint {
			return cel.StringType
		}
	}
	// Inline enum: enum:val1|val2|val3
	if strings.HasPrefix(hint, "enum:") {
		return cel.StringType
	}
	return TypeHintToCelType(hint)
}

// resolveExprType determines the type of an expression, type-checking CEL if needed.
func (c *checker) resolveExprType(pos ast.Pos, expr *ast.Expr) *cel.Type {
	if expr.CEL != "" {
		t, err := checkExpr(c.scope, expr, c.structs, c.protoDescs)
		if err != nil {
			c.errorAt(pos, "%v", err)
			return cel.DynType
		}
		return t
	}
	if expr.TypeHint != "" {
		return c.resolveTypeHint(expr.TypeHint)
	}
	if expr.Literal != nil {
		return InferLiteralType(expr.Literal)
	}
	return cel.DynType
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
		// Create scope with component params declared
		compScope := NewScope(c.scope)
		for _, p := range comp.Params {
			var t *cel.Type
			if p.Default.TypeHint != "" {
				t = c.resolveTypeHint(p.Default.TypeHint)
			} else if p.Default.Literal != nil {
				t = InferLiteralType(p.Default.Literal)
			} else {
				t = cel.DynType
			}
			compScope.Declare(p.Name, t)
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
		c.checkExprType(vn.Pos, vn.Class, cel.StringType, scope, "class")
	}
	if vn.Ref != nil {
		c.checkExprType(vn.Pos, vn.Ref, cel.StringType, scope, "ref")
	}
	if vn.If != nil {
		c.checkExprType(vn.Pos, vn.If, cel.BoolType, scope, "if")
	}

	// For clause
	childScope := scope
	if vn.For != nil {
		c.resolveExprInScope(vn.Pos, &vn.For.Iterable, scope)
		childScope = NewScope(scope)
		childScope.Declare(vn.For.Variable, cel.DynType)
		if vn.For.IndexVar != "" {
			childScope.Declare(vn.For.IndexVar, cel.IntType)
		}
	}

	// Key is checked in childScope so it can reference for-loop variables
	if vn.Key != nil {
		c.checkExprType(vn.Pos, vn.Key, cel.DynType, childScope, "key")
	}

	// Props (use childScope so for-loop variables are visible)
	for name, expr := range vn.Props {
		ps, ok := schema.Props[name]
		if !ok {
			c.errorAt(vn.Pos, "%s: unknown property %q", vn.Component, name)
			continue
		}
		c.checkExprType(vn.Pos, &expr, ps.Type, childScope, vn.Component+"."+name)
		vn.Props[name] = expr
	}

	// Required params (user-defined components)
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
		eventScope := NewScope(childScope)
		eventScope.Declare("event", cel.DynType)
		if expr.CEL != "" {
			t, err := checkExpr(eventScope, &expr, c.structs, c.protoDescs)
			if err != nil {
				c.errorAt(vn.Pos, "%s on:%s: %v", vn.Component, name, err)
			} else if !isMutationType(t) {
				c.errorAt(vn.Pos, "%s on:%s: handler must return Mutation or list(Mutation), got %s", vn.Component, name, t)
			}
		} else if expr.SNGL != nil {
			if !isMutationNode(expr.SNGL) {
				c.errorAt(vn.Pos, "%s on:%s: handler must return Mutation or list(Mutation)", vn.Component, name)
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
func (c *checker) checkExprType(pos ast.Pos, expr *ast.Expr, expected *cel.Type, scope *Scope, context string) {
	got := c.resolveExprInScope(pos, expr, scope)
	if !isAssignable(got, expected) {
		c.errorAt(pos, "%s: expected %s, got %s", context, expected, got)
	}
}

// resolveExprInScope type-checks a CEL expression or infers a literal's type within a given scope.
func (c *checker) resolveExprInScope(pos ast.Pos, expr *ast.Expr, scope *Scope) *cel.Type {
	if expr.CEL != "" {
		t, err := checkExpr(scope, expr, c.structs, c.protoDescs)
		if err != nil {
			c.errorAt(pos, "%v", err)
			return cel.DynType
		}
		return t
	}
	if expr.TypeHint != "" {
		return c.resolveTypeHint(expr.TypeHint)
	}
	if expr.Literal != nil {
		return InferLiteralType(expr.Literal)
	}
	return cel.DynType
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

// isMutationNode checks if an SNGL AST node represents a mutation statement.
func isMutationNode(n ast.Node) bool {
	switch e := n.(type) {
	case *ast.AssignStmt:
		return true
	case *ast.ToggleStmt:
		return true
	case *ast.EmitStmt:
		return true
	case *ast.StmtBlock:
		for _, s := range e.Stmts {
			if !isMutationNode(s) {
				return false
			}
		}
		return len(e.Stmts) > 0
	default:
		return false
	}
}

// isMutationType checks if a type is Mutation or list(Mutation).
func isMutationType(t *cel.Type) bool {
	if t.IsEquivalentType(MutationType) {
		return true
	}
	if t.IsEquivalentType(cel.ListType(MutationType)) {
		return true
	}
	return false
}
