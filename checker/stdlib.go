package checker

import (
	"embed"
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/parser"
	"git.duckfam.us/jonathan/sngl/snglparser"
)

//go:embed stdlib/*.sngl.kdl stdlib/*.sngl
var stdlibFS embed.FS

// LoadStdlib parses the embedded stdlib files and returns the component
// schema registry, style property type map, and unit definitions.
func LoadStdlib() (SchemaRegistry, map[string]StylePropSchema, []*ast.UnitDef, error) {
	registry := SchemaRegistry{}
	styleProps := map[string]StylePropSchema{}
	var units []*ast.UnitDef

	entries, err := stdlibFS.ReadDir("stdlib")
	if err != nil {
		return nil, nil, nil, fmt.Errorf("reading stdlib dir: %w", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		f, err := stdlibFS.Open("stdlib/" + name)
		if err != nil {
			return nil, nil, nil, fmt.Errorf("opening %s: %w", name, err)
		}

		var doc *ast.Document
		if strings.HasSuffix(name, ".sngl.kdl") {
			doc, err = parser.Parse(name, f)
		} else if strings.HasSuffix(name, ".sngl") {
			doc, err = snglparser.Parse(name, f)
		} else {
			f.Close()
			continue
		}
		f.Close()
		if err != nil {
			return nil, nil, nil, fmt.Errorf("parsing %s: %w", name, err)
		}

		for _, comp := range doc.Components {
			registry[comp.Name] = componentToSchema(comp)
		}
		for _, sd := range doc.StyleDefs {
			styleProps[sd.Name] = StylePropSchema{
				Type: TypeHintToCelType(sd.TypeHint),
				Enum: sd.Enum,
			}
		}
		units = append(units, doc.Units...)
	}
	return registry, styleProps, units, nil
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
