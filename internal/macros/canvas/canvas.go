package canvas

import (
	"errors"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/expand"
)

func init() {
	expand.RegisterPre("canvas", "shape", shapeHandler)
}

func shapeHandler(attr ast.MacroAttr, decl ast.Stmt) (ast.Stmt, error) {
	comp, ok := decl.(*ast.ComponentDecl)
	if !ok {
		return decl, errors.New("shape macro requires a component declaration")
	}
	for _, p := range comp.Props.Props {
		if _, isEvent := p.(ast.EventDecl); isEvent {
			return decl, errors.New("shape components do not support event declarations")
		}
	}

	// Rewrite ChildrenType to list<shape>.
	listShape := &ast.NamedType{
		Name:     "list",
		TypeArgs: []ast.TypeExpr{&ast.NamedType{Name: "shape"}},
	}
	switch {
	case comp.ChildrenType == nil:
		comp.ChildrenType = listShape
	default:
		named, isNamed := comp.ChildrenType.(*ast.NamedType)
		if isNamed && named.Name == "list" && len(named.TypeArgs) > 0 {
			inner, ok := named.TypeArgs[0].(*ast.NamedType)
			if ok && inner.Name == "shape" {
				// already list<shape> — leave unchanged
			} else if ok && inner.Name == "component" {
				comp.ChildrenType = listShape
			} else {
				return decl, errors.New("shape components may only have list<shape> children")
			}
		} else {
			return decl, errors.New("shape components may only have list<shape> children")
		}
	}

	comp.IsShape = true
	return comp, nil
}
