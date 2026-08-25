package draw

import (
	"errors"
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/expand"
)

// shapeKind is the tree sngl://draw's components form. #[draw.shape] is the
// public spelling of #[tree.kind("shape")]: the tree marks are internal to the
// compiler, so a user declaring a shape reaches them only through this one.
const shapeKind = "shape"

func init() {
	expand.RegisterPre("draw", "shape", nil, shapeHandler)
}

func shapeHandler(_ expand.Args, decl ast.Stmt) (ast.Stmt, error) {
	comp, ok := decl.(*ast.ComponentDecl)
	if !ok {
		return decl, errors.New("shape macro requires a component declaration")
	}
	// A painted shape has nothing to raise an event from. This is a rule about
	// drawing rather than about trees, so it is enforced here and not by the
	// tree marks.
	for _, p := range comp.Props.Props {
		if _, isEvent := p.(ast.EventDecl); isEvent {
			return decl, errors.New("shape components do not support event declarations")
		}
	}
	if comp.ChildrenType != nil {
		return decl, errors.New("shape components may only have shape children; remove the children type")
	}
	if err := comp.SetTreeKind(shapeKind); err != nil {
		return decl, fmt.Errorf("#[draw.shape]: %w", err)
	}
	return comp, nil
}
