package checker

import (
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/ir"
)

// extractBindings processes `:prop=target` args on a VisualNode.
// For each arg whose Name starts with ":":
//   - The ":" is stripped from the prop name in-place.
//   - If the target is an assignable lvalue, a PropBinding is returned.
//
// No event handlers are synthesized here. The lowering phase is responsible
// for translating PropBindings into @event+handler pairs for each platform.
func (c *checker) extractBindings(comp *ir.Component, props []ir.Arg, handlers []ir.EventHandler) ([]ir.Arg, []ir.EventHandler, []ir.PropBinding) {
	var bindings []ir.PropBinding
	for i := range props {
		p := &props[i]
		if !strings.HasPrefix(p.Name, ":") {
			continue
		}
		propName := p.Name[1:]
		p.Name = propName

		if p.Value == nil || !isAssignableTarget(p.Value) {
			c.error(p.NamePos, "cannot bind :%s: a binding writes the new value back, so its target must be assignable — a variable, a field, or an element", propName)
			continue
		}
		if decl := declaredProp(comp, propName); decl != nil && !decl.Bidirectional {
			c.error(p.NamePos, "cannot bind :%s: %s declares %s as a one-way prop; write it as `:%s` in the declaration to let a caller bind it", propName, comp.Name, propName, propName)
			continue
		}
		bindings = append(bindings, ir.PropBinding{
			PropName: propName,
			NamePos:  p.NamePos,
			Target:   p.Value,
		})
	}
	return props, handlers, bindings
}

// declaredProp returns the prop a `:name` binding targets, or nil when the
// component declares none under that name — a wildcard prop collected it, or
// the name is unknown and validateVisualNodeProps has already reported it.
func declaredProp(comp *ir.Component, name string) *ir.Prop {
	if comp == nil {
		return nil
	}
	for _, p := range comp.Props {
		if p.Name == name && p.Wildcard == "" {
			return p
		}
	}
	return nil
}

// isAssignableTarget reports whether e is a valid lvalue for a prop binding:
// a plain identifier, field access, index access, or deref of a loop ref.
func isAssignableTarget(e ir.Expr) bool {
	switch x := e.(type) {
	case *ir.Ident:
		return x != nil
	case *ir.Select:
		return x != nil && isAssignableTarget(x.Operand)
	case *ir.Index:
		return x != nil && isAssignableTarget(x.Operand)
	case *ir.Unary:
		return x != nil && x.Op == ast.UnaryDeref && isAssignableTarget(x.Operand)
	}
	return false
}
