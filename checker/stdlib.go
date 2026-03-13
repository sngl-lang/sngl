package checker

import (
	"embed"
	"fmt"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/parser"
	"github.com/google/cel-go/cel"
)

//go:embed stdlib/*.sngl.kdl
var stdlibFS embed.FS

// LoadStdlib parses the embedded stdlib .sngl.kdl files and returns the component
// schema registry and style property type map.
func LoadStdlib() (SchemaRegistry, map[string]*cel.Type, error) {
	registry := SchemaRegistry{}
	styleProps := map[string]*cel.Type{}

	entries, err := stdlibFS.ReadDir("stdlib")
	if err != nil {
		return nil, nil, fmt.Errorf("reading stdlib dir: %w", err)
	}

	for _, entry := range entries {
		f, err := stdlibFS.Open("stdlib/" + entry.Name())
		if err != nil {
			return nil, nil, fmt.Errorf("opening %s: %w", entry.Name(), err)
		}
		doc, err := parser.Parse(entry.Name(), f)
		f.Close()
		if err != nil {
			return nil, nil, fmt.Errorf("parsing %s: %w", entry.Name(), err)
		}

		for _, comp := range doc.Components {
			registry[comp.Name] = componentToSchema(comp)
		}
		for _, sd := range doc.StyleDefs {
			styleProps[sd.Name] = TypeHintToCelType(sd.TypeHint)
		}
	}
	return registry, styleProps, nil
}

func componentToSchema(comp *ast.Component) *ComponentSchema {
	schema := &ComponentSchema{
		Props:    make(map[string]PropSchema),
		Events:   make(map[string]string),
		Children: ChildrenMany, // default
	}
	for _, p := range comp.PropDecls {
		schema.Props[p.Name] = PropSchema{
			Type: TypeHintToCelType(p.TypeHint),
			Enum: p.Enum,
		}
	}
	for _, e := range comp.EventDecls {
		schema.Events[e.Name] = e.PayloadType
	}
	switch comp.ChildPolicy {
	case "none":
		schema.Children = ChildrenNone
	case "one":
		schema.Children = ChildrenOne
	case "many":
		schema.Children = ChildrenMany
	}
	return schema
}
