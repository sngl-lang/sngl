package checker

import (
	"embed"
	"fmt"
	"strings"

	"git.duckfam.us/jonathan/sngl/ast"
	"git.duckfam.us/jonathan/sngl/internal/parser"
)

//go:embed stdlib/*.sngl
var stdlibFS embed.FS

// LoadStdlib parses the embedded stdlib files and returns the component
// schema registry, style property type map, unit definitions, and stdlib functions.
func LoadStdlib() (SchemaRegistry, map[string]StylePropSchema, []*ast.UnitDef, []*ast.FuncDef, []*ast.StructDef, error) {
	registry := SchemaRegistry{}
	styleProps := map[string]StylePropSchema{}
	var units []*ast.UnitDef
	var funcs []*ast.FuncDef
	var structs []*ast.StructDef

	entries, err := stdlibFS.ReadDir("stdlib")
	if err != nil {
		return nil, nil, nil, nil, nil, fmt.Errorf("reading stdlib dir: %w", err)
	}

	for _, entry := range entries {
		name := entry.Name()
		f, err := stdlibFS.Open("stdlib/" + name)
		if err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("opening %s: %w", name, err)
		}

		var doc *ast.Document
		doc, err = parser.Parse(name, f)
		f.Close()
		if err != nil {
			return nil, nil, nil, nil, nil, fmt.Errorf("parsing %s: %w", name, err)
		}

		for _, comp := range doc.Components {
			registry[comp.Name] = componentToSchema(comp, doc.Comments)
		}
		for _, sd := range doc.StyleDefs {
			styleProps[sd.Name] = StylePropSchema{
				Type: TypeFromHint(sd.TypeHint),
				Enum: sd.Enum,
			}
		}
		units = append(units, doc.Units...)
		structs = append(structs, doc.Structs...)
		for _, fn := range doc.Functions {
			fn.IsStdlib = true
			funcs = append(funcs, fn)
		}
	}
	return registry, styleProps, units, funcs, structs, nil
}

func componentToSchema(comp *ast.Component, comments []ast.Comment) *ComponentSchema {
	schema := &ComponentSchema{
		Props:    make(map[string]PropSchema),
		Events:   make(map[string]string),
		Children: ChildrenMany, // default
		Doc:      docForPos(comments, comp.Pos.Line),
	}
	for _, p := range comp.PropDecls {
		schema.Props[p.Name] = PropSchema{
			Type: TypeFromHint(p.TypeHint),
			Enum: p.Enum,
			Doc:  docForPos(comments, p.Pos.Line),
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

// docForPos returns the doc comment text for a declaration at the given line.
// It collects consecutive // comment lines immediately preceding the declaration.
func docForPos(comments []ast.Comment, line int) string {
	var docLines []string
	target := line - 1
	for i := len(comments) - 1; i >= 0; i-- {
		c := comments[i]
		if c.Pos.Line == target {
			text := strings.TrimPrefix(c.Text, "// ")
			text = strings.TrimPrefix(text, "//")
			text = strings.TrimSpace(text)
			if strings.HasPrefix(text, "---") {
				break
			}
			docLines = append([]string{text}, docLines...)
			target--
		} else if c.Pos.Line < target {
			break
		}
	}
	return strings.Join(docLines, " ")
}
