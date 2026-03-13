package checker

import (
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"github.com/google/cel-go/cel"
)

// Check type-checks an SNGL document AST. It resolves types for all declarations,
// validates CEL expressions against typed scopes, and checks visual nodes against
// component schemas.
func Check(doc *ast.Document) error {
	c := &checker{
		registry: NewStandardRegistry(),
		scope:    NewScope(nil),
	}
	c.pass1(doc)
	c.pass2(doc)
	return c.joinErrors()
}

type checker struct {
	registry   SchemaRegistry
	scope      *Scope
	components []*ast.Component
	structs    []*ast.StructDef
	errs       []error
}

func (c *checker) errorf(format string, args ...any) {
	c.errs = append(c.errs, fmt.Errorf(format, args...))
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

// pass1 resolves declarations: structs, binds, computeds, components, styles.
func (c *checker) pass1(doc *ast.Document) {
	// Structs
	c.structs = doc.Structs

	// Binds
	for _, b := range doc.Binds {
		t := c.resolveExprType(&b.Init)
		c.scope.Declare(b.Name, t)
	}

	// Computeds
	for _, comp := range doc.Computeds {
		t := c.resolveExprType(&comp.Expr)
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
				t = TypeHintToCelType(p.Default.TypeHint)
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
			if _, ok := StylePropertyTypes[prop]; !ok {
				c.errorf("style %q: unknown style property %q", s.Name, prop)
				continue
			}
			c.resolveExprType(&expr)
			s.Props[prop] = expr
		}
	}
}

// resolveExprType determines the type of an expression, type-checking CEL if needed.
func (c *checker) resolveExprType(expr *ast.Expr) *cel.Type {
	if expr.CEL != "" {
		t, err := checkExpr(c.scope, expr, c.structs...)
		if err != nil {
			c.errorf("%v", err)
			return cel.DynType
		}
		return t
	}
	if expr.TypeHint != "" {
		return TypeHintToCelType(expr.TypeHint)
	}
	if expr.Literal != nil {
		return InferLiteralType(expr.Literal)
	}
	return cel.DynType
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
				t = TypeHintToCelType(p.Default.TypeHint)
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
		c.errorf("unknown component %q", vn.Component)
		return
	}

	// Universal attributes
	if vn.ID != nil {
		c.checkExprType(vn.ID, cel.StringType, scope, "id")
	}
	if vn.Key != nil {
		c.checkExprType(vn.Key, cel.StringType, scope, "key")
	}
	if vn.Class != nil {
		c.checkExprType(vn.Class, cel.StringType, scope, "class")
	}
	if vn.Ref != nil {
		c.checkExprType(vn.Ref, cel.StringType, scope, "ref")
	}
	if vn.If != nil {
		c.checkExprType(vn.If, cel.BoolType, scope, "if")
	}

	// For clause
	childScope := scope
	if vn.For != nil {
		c.resolveExprInScope(&vn.For.Iterable, scope)
		childScope = NewScope(scope)
		childScope.Declare(vn.For.Variable, cel.DynType)
	}

	// Props (use childScope so for-loop variables are visible)
	for name, expr := range vn.Props {
		ps, ok := schema.Props[name]
		if !ok {
			c.errorf("%s: unknown property %q", vn.Component, name)
			continue
		}
		c.checkExprType(&expr, ps.Type, childScope, vn.Component+"."+name)
		vn.Props[name] = expr
	}

	// Required params (user-defined components)
	if comp := c.findComponent(vn.Component); comp != nil {
		for _, p := range comp.Params {
			if p.Required {
				if _, ok := vn.Props[p.Name]; !ok {
					c.errorf("%s: required property %q not provided", vn.Component, p.Name)
				}
			}
		}
	}

	// Events
	for name, expr := range vn.Events {
		if _, ok := schema.Events[name]; !ok {
			c.errorf("%s: unknown event %q", vn.Component, name)
			continue
		}
		eventScope := NewScope(childScope)
		eventScope.Declare("event", cel.DynType)
		t, err := checkExpr(eventScope, &expr, c.structs...)
		if err != nil {
			c.errorf("%s on:%s: %v", vn.Component, name, err)
		} else if !isMutationType(t) {
			c.errorf("%s on:%s: handler must return Mutation or list(Mutation), got %s", vn.Component, name, t)
		}
		vn.Events[name] = expr
	}

	// Style attrs
	for name, expr := range vn.StyleAttrs {
		if _, ok := StylePropertyTypes[name]; !ok {
			c.errorf("%s: unknown style property %q", vn.Component, name)
			continue
		}
		c.resolveExprInScope(&expr, scope)
		vn.StyleAttrs[name] = expr
	}

	// Style block
	for name, expr := range vn.StyleBlock {
		if _, ok := StylePropertyTypes[name]; !ok {
			c.errorf("%s: unknown style property %q", vn.Component, name)
			continue
		}
		c.resolveExprInScope(&expr, scope)
		vn.StyleBlock[name] = expr
	}

	// Children policy
	switch schema.Children {
	case ChildrenNone:
		if len(vn.Children) > 0 {
			c.errorf("%s: does not accept children", vn.Component)
		}
	case ChildrenOne:
		if len(vn.Children) != 1 {
			c.errorf("%s: expects exactly one child, got %d", vn.Component, len(vn.Children))
		}
	}

	// Recurse
	for _, child := range vn.Children {
		c.checkVisualNode(child, childScope)
	}
}

// checkExprType resolves an expression's type and checks it matches the expected type.
func (c *checker) checkExprType(expr *ast.Expr, expected *cel.Type, scope *Scope, context string) {
	got := c.resolveExprInScope(expr, scope)
	if expected != cel.DynType && got != cel.DynType && !got.IsEquivalentType(expected) {
		c.errorf("%s: expected %s, got %s", context, expected, got)
	}
}

// resolveExprInScope type-checks a CEL expression or infers a literal's type within a given scope.
func (c *checker) resolveExprInScope(expr *ast.Expr, scope *Scope) *cel.Type {
	if expr.CEL != "" {
		t, err := checkExpr(scope, expr, c.structs...)
		if err != nil {
			c.errorf("%v", err)
			return cel.DynType
		}
		return t
	}
	if expr.TypeHint != "" {
		return TypeHintToCelType(expr.TypeHint)
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
