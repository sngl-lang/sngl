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
	comp.IsShape = true
	return comp, nil
}
