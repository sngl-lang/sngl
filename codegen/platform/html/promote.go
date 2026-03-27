package html

import "git.duckfam.us/jonathan/sngl/ast"

// PromoteComponent creates a Document with the named component's fields
// promoted to top-level, suitable for HTML codegen.
func PromoteComponent(doc *ast.Document, name string) *ast.Document {
	comp := doc.FindComponent(name)
	if comp == nil || len(comp.Body) == 0 {
		return nil
	}

	promoted := &ast.Document{
		Structs:    doc.Structs,
		Enums:      doc.Enums,
		Components: doc.Components,
		App:        &ast.App{Children: comp.Body},
	}

	promoted.Data = append(promoted.Data, comp.Data...)
	promoted.Computeds = append(promoted.Computeds, comp.Computeds...)
	promoted.Consts = append(promoted.Consts, comp.Consts...)

	for _, p := range comp.Params {
		promoted.Data = append(promoted.Data, &ast.Data{
			Name: p.Name,
			Init: p.Default,
		})
	}

	return promoted
}
